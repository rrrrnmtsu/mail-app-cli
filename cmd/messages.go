package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/intelligrit/mail-app-cli/pkg/cache"
	"github.com/intelligrit/mail-app-cli/pkg/mail"
	"github.com/spf13/cobra"
)

const messageCacheTTL = 5 * time.Minute

var (
	msgAccount       string
	msgMailbox       string
	msgLimit         int
	msgOffset        int
	msgUnread        bool
	msgFlaggedFilter bool
	msgWithContent   bool
	msgRead          bool
	msgFlaggedSet    bool
	msgSince         string
	msgNoCache       bool
	msgForceRefresh  bool
	msgJunkSet       bool
	msgReplyAll      bool
	msgBody          string
	msgTo            []string
	msgCc            []string
	msgSignature     string
	msgFlagColor     int
	msgAttachments   []string
)

// sanitizeCacheKey replaces non-alphanumeric chars so the key is safe as a filename component.
func sanitizeCacheKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()
}

// invalidateMailboxCache removes all message-list cache entries for the given mailbox.
// Call this after any mutation so subsequent list commands see fresh data.
func invalidateMailboxCache(account, mailbox string) {
	if c, err := cache.New(); err == nil {
		prefix := fmt.Sprintf("msgs-%s-%s-", sanitizeCacheKey(account), sanitizeCacheKey(mailbox))
		c.DeletePrefix(prefix)
	}
}

var messagesCmd = &cobra.Command{
	Use:   "messages",
	Short: "Manage Mail.app messages",
	Long:  `View and manage email messages in Mail.app.`,
}

var messagesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List messages",
	Long:  `List messages from a specific mailbox. Output is JSON format. Use jq for pretty printing: mail-app-cli messages list -a Account -m INBOX | jq`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		// Build a cache key that encodes all query parameters so different queries
		// get separate cache entries.
		cacheKey := fmt.Sprintf("msgs-%s-%s-%d-%d-%v-%v-%s-%v",
			sanitizeCacheKey(msgAccount),
			sanitizeCacheKey(msgMailbox),
			msgLimit, msgOffset,
			msgUnread, msgFlaggedFilter,
			sanitizeCacheKey(msgSince),
			msgWithContent,
		)

		// Try cache first (skip if content requested — content is per-user and typically large)
		if !msgNoCache && !msgForceRefresh {
			c, err := cache.New()
			if err == nil {
				c.SetTTL(messageCacheTTL)
				var cached []mail.Message
				found, err := c.Get(cacheKey, &cached)
				if err == nil && found {
					output, err := json.MarshalIndent(cached, "", "  ")
					if err != nil {
						return fmt.Errorf("failed to marshal messages: %w", err)
					}
					fmt.Println(string(output))
					return nil
				}
			}
		}

		client := mail.NewClient()
		messages, err := client.GetMessagesJSON(msgAccount, msgMailbox, msgLimit, msgOffset, msgUnread, msgFlaggedFilter, msgWithContent, msgSince)
		if err != nil {
			return fmt.Errorf("failed to get messages: %w", err)
		}

		// Populate cache (always write unless --no-cache)
		if !msgNoCache {
			if c, err := cache.New(); err == nil {
				c.SetTTL(messageCacheTTL)
				c.Set(cacheKey, messages)
			}
		}

		output, err := json.MarshalIndent(messages, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal messages: %w", err)
		}

		fmt.Println(string(output))
		return nil
	},
}

var messagesShowCmd = &cobra.Command{
	Use:   "show [message-id]",
	Short: "Show message details",
	Long:  `Show full details of a specific message. Output is JSON format.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		message, err := client.GetMessageDetailsJSON(msgAccount, msgMailbox, messageID)
		if err != nil {
			return fmt.Errorf("failed to get message: %w", err)
		}

		output, err := json.MarshalIndent(message, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal message: %w", err)
		}

		fmt.Println(string(output))
		return nil
	},
}

var messagesMarkCmd = &cobra.Command{
	Use:   "mark [message-id]",
	Short: "Mark message as read/unread",
	Long:  `Mark a message as read or unread.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		err := client.MarkMessageAsRead(msgAccount, msgMailbox, messageID, msgRead)
		if err != nil {
			return fmt.Errorf("failed to mark message: %w", err)
		}
		invalidateMailboxCache(msgAccount, msgMailbox)

		status := "unread"
		if msgRead {
			status = "read"
		}
		fmt.Printf("Message marked as %s\n", status)
		return nil
	},
}

var messagesFlagCmd = &cobra.Command{
	Use:   "flag [message-id]",
	Short: "Flag or unflag a message",
	Long: `Set or unset the flagged status of a message.
With --color N the message is flagged with Mail's flag colour N:
0=red 1=orange 2=yellow 3=green 4=blue 5=purple 6=gray.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		if cmd.Flags().Changed("color") {
			if err := validateFlagColorArgs(msgFlaggedSet, msgFlagColor); err != nil {
				return err
			}
			if err := client.SetFlagColor(msgAccount, msgMailbox, messageID, msgFlagColor); err != nil {
				return fmt.Errorf("failed to flag message: %w", err)
			}
			invalidateMailboxCache(msgAccount, msgMailbox)
			fmt.Printf("Message flagged (%s)\n", mail.FlagColorNames[msgFlagColor])
			return nil
		}
		err := client.FlagMessage(msgAccount, msgMailbox, messageID, msgFlaggedSet)
		if err != nil {
			return fmt.Errorf("failed to flag message: %w", err)
		}
		invalidateMailboxCache(msgAccount, msgMailbox)

		status := "unflagged"
		if msgFlaggedSet {
			status = "flagged"
		}
		fmt.Printf("Message %s\n", status)
		return nil
	},
}

var messagesDeleteCmd = &cobra.Command{
	Use:   "delete [message-id]",
	Short: "Delete a message",
	Long:  `Move a message to the trash.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		err := client.DeleteMessage(msgAccount, msgMailbox, messageID)
		if err != nil {
			return fmt.Errorf("failed to delete message: %w", err)
		}
		invalidateMailboxCache(msgAccount, msgMailbox)

		fmt.Println("Message deleted")
		return nil
	},
}

var messagesArchiveCmd = &cobra.Command{
	Use:   "archive [message-id]",
	Short: "Archive a message",
	Long:  `Move a message to the Archive mailbox.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		err := client.ArchiveMessage(msgAccount, msgMailbox, messageID)
		if err != nil {
			return fmt.Errorf("failed to archive message: %w", err)
		}
		invalidateMailboxCache(msgAccount, msgMailbox)
		// Also invalidate the archive mailbox (provider-dependent name)
		invalidateMailboxCache(msgAccount, "Archive")
		invalidateMailboxCache(msgAccount, "All Mail")

		fmt.Println("Message archived")
		return nil
	},
}

var messagesMoveCmd = &cobra.Command{
	Use:   "move [message-id] [target-mailbox]",
	Short: "Move a message to another mailbox",
	Long:  `Move a message to a different mailbox.`,
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		targetMailbox := args[1]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		err := client.MoveMessage(msgAccount, msgMailbox, messageID, targetMailbox)
		if err != nil {
			return fmt.Errorf("failed to move message: %w", err)
		}
		invalidateMailboxCache(msgAccount, msgMailbox)
		invalidateMailboxCache(msgAccount, targetMailbox)

		fmt.Printf("Message moved to %s\n", targetMailbox)
		return nil
	},
}

var messagesReplyCmd = &cobra.Command{
	Use:   "reply [message-id]",
	Short: "Reply to a message",
	Long: `Creates and sends a reply using Mail.app's own reply command, so the
outgoing message carries Mail.app's native subject/recipient/thread
handling instead of being composed as an unrelated new message.
Mail.app does not reliably pre-fill the quoted original text via
scripting, so pass the full body (including any quote) via --body.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		attachments, err := mail.ResolveAttachmentPaths(msgAttachments)
		if err != nil {
			return err
		}
		err = client.ReplyMessage(msgAccount, msgMailbox, messageID, msgReplyAll, msgBody, msgSignature, attachments)
		if err != nil {
			return fmt.Errorf("failed to reply: %w", err)
		}
		fmt.Println("Reply sent")
		return nil
	},
}

var messagesForwardCmd = &cobra.Command{
	Use:   "forward [message-id]",
	Short: "Forward a message",
	Long: `Creates and sends a forward using Mail.app's own forward command.
Recipients are not inherited from the original message and must be
supplied via --to (and optionally --cc).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}
		if len(msgTo) == 0 {
			return fmt.Errorf("at least one --to recipient is required")
		}

		client := mail.NewClient()
		attachments, err := mail.ResolveAttachmentPaths(msgAttachments)
		if err != nil {
			return err
		}
		err = client.ForwardMessage(msgAccount, msgMailbox, messageID, msgTo, msgCc, msgBody, msgSignature, attachments)
		if err != nil {
			return fmt.Errorf("failed to forward: %w", err)
		}
		fmt.Println("Forward sent")
		return nil
	},
}

var messagesRedirectCmd = &cobra.Command{
	Use:   "redirect [message-id]",
	Short: "Redirect a message unchanged",
	Long: `Resends a message verbatim (unchanged subject and body, original
sender preserved) via Mail.app's redirect command — the scriptable
equivalent of the GUI's "Redirect...".`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}
		if len(msgTo) == 0 {
			return fmt.Errorf("at least one --to recipient is required")
		}

		client := mail.NewClient()
		err := client.RedirectMessage(msgAccount, msgMailbox, messageID, msgTo)
		if err != nil {
			return fmt.Errorf("failed to redirect: %w", err)
		}
		fmt.Println("Redirected")
		return nil
	},
}

var messagesMarkJunkCmd = &cobra.Command{
	Use:   "mark-junk [message-id]",
	Short: "Mark or unmark a message as junk",
	Long: `Sets Mail.app's own junk-mail-status flag on a message (not just a
mailbox move), so it participates in whatever junk-filter training
Mail.app does internally.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		err := client.SetJunkStatus(msgAccount, msgMailbox, messageID, msgJunkSet)
		if err != nil {
			return fmt.Errorf("failed to set junk status: %w", err)
		}

		status := "unmarked as junk"
		if msgJunkSet {
			status = "marked as junk"
		}
		fmt.Printf("Message %s\n", status)
		return nil
	},
}

var messagesOpenCmd = &cobra.Command{
	Use:   "open [message-id]",
	Short: "Open a message in Mail.app",
	Long: `Brings Mail.app to the front with the message open in its own window.
This activates Mail.app and takes focus.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		messageID := args[0]
		if msgAccount == "" || msgMailbox == "" {
			return fmt.Errorf("both --account and --mailbox are required")
		}

		client := mail.NewClient()
		if err := client.OpenMessage(msgAccount, msgMailbox, messageID); err != nil {
			return fmt.Errorf("failed to open message: %w", err)
		}
		fmt.Println("Message opened in Mail.app")
		return nil
	},
}

// validateFlagColorArgs rejects --color combined with --flagged=false and
// out-of-range colours.
func validateFlagColorArgs(flagged bool, color int) error {
	if !flagged {
		return fmt.Errorf("--color cannot be combined with --flagged=false")
	}
	return mail.ValidateFlagColor(color)
}

// newUnifiedCmd returns a cobra.Command for a unified mailbox view.
// mailboxType must match one of the types understood by GetUnifiedMessagesJSON.
func newUnifiedCmd(use, short, mailboxType string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := mail.NewClient()
			messages, err := client.GetUnifiedMessagesJSON(mailboxType, msgLimit, msgOffset, msgWithContent)
			if err != nil {
				return fmt.Errorf("failed to get %s messages: %w", mailboxType, err)
			}

			output, err := json.MarshalIndent(messages, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal messages: %w", err)
			}

			fmt.Println(string(output))
			return nil
		},
	}
}

var messagesInboxCmd = newUnifiedCmd(
	"inbox",
	"List inbox messages across all accounts",
	"inbox",
)

var messagesUnreadCmd = newUnifiedCmd(
	"unread",
	"List unread messages across all accounts",
	"unread",
)

var messagesSentCmd = newUnifiedCmd(
	"sent",
	"List sent messages across all accounts",
	"sent",
)

var messagesDraftsCmd = newUnifiedCmd(
	"drafts",
	"List draft messages across all accounts",
	"drafts",
)

var messagesFlaggedCmd = newUnifiedCmd(
	"flagged",
	"List flagged messages across all accounts",
	"flagged",
)

var messagesTrashCmd = newUnifiedCmd(
	"trash",
	"List trash messages across all accounts",
	"trash",
)

var messagesJunkCmd = newUnifiedCmd(
	"junk",
	"List junk/spam messages across all accounts",
	"junk",
)

func init() {
	messagesCmd.AddCommand(messagesListCmd)
	messagesCmd.AddCommand(messagesShowCmd)
	messagesCmd.AddCommand(messagesMarkCmd)
	messagesCmd.AddCommand(messagesFlagCmd)
	messagesCmd.AddCommand(messagesDeleteCmd)
	messagesCmd.AddCommand(messagesArchiveCmd)
	messagesCmd.AddCommand(messagesMoveCmd)
	messagesCmd.AddCommand(messagesReplyCmd)
	messagesCmd.AddCommand(messagesForwardCmd)
	messagesCmd.AddCommand(messagesRedirectCmd)
	messagesCmd.AddCommand(messagesMarkJunkCmd)
	messagesCmd.AddCommand(messagesOpenCmd)
	// Unified view subcommands
	messagesCmd.AddCommand(messagesInboxCmd)
	messagesCmd.AddCommand(messagesUnreadCmd)
	messagesCmd.AddCommand(messagesSentCmd)
	messagesCmd.AddCommand(messagesDraftsCmd)
	messagesCmd.AddCommand(messagesFlaggedCmd)
	messagesCmd.AddCommand(messagesTrashCmd)
	messagesCmd.AddCommand(messagesJunkCmd)

	// Common flags for all message commands
	messagesCmd.PersistentFlags().StringVarP(&msgAccount, "account", "a", "", "Account name (required)")
	messagesCmd.PersistentFlags().StringVarP(&msgMailbox, "mailbox", "m", "", "Mailbox name (required)")

	// List-specific flags
	messagesListCmd.Flags().IntVarP(&msgLimit, "limit", "l", 25, "Maximum number of messages to display")
	messagesListCmd.Flags().IntVarP(&msgOffset, "offset", "o", 0, "Number of messages to skip (for pagination)")
	messagesListCmd.Flags().BoolVarP(&msgUnread, "unread", "u", false, "Show only unread messages")
	messagesListCmd.Flags().BoolVarP(&msgFlaggedFilter, "flagged", "f", false, "Show only flagged messages")
	messagesListCmd.Flags().BoolVar(&msgWithContent, "with-content", false, "Include message content (slower but better for accessibility)")
	messagesListCmd.Flags().StringVarP(&msgSince, "since", "s", "", "Show messages since date (format: YYYY-MM-DD or YYYY-MM-DD HH:MM:SS)")
	messagesListCmd.Flags().BoolVar(&msgNoCache, "no-cache", false, "Bypass cache and fetch fresh data")
	messagesListCmd.Flags().BoolVar(&msgForceRefresh, "force-refresh", false, "Force refresh cache with fresh data")

	// Mark-specific flags
	messagesMarkCmd.Flags().BoolVarP(&msgRead, "read", "r", true, "Mark as read (default) or use --read=false for unread")

	// Flag-specific flags
	messagesFlagCmd.Flags().BoolVarP(&msgFlaggedSet, "flagged", "f", true, "Flag message (default) or use --flagged=false to unflag")
	messagesFlagCmd.Flags().IntVar(&msgFlagColor, "color", 0, "Flag colour 0-6 (0=red 1=orange 2=yellow 3=green 4=blue 5=purple 6=gray)")

	// Reply/forward/redirect-specific flags
	messagesReplyCmd.Flags().BoolVar(&msgReplyAll, "all", false, "Reply to all recipients instead of just the sender")
	messagesReplyCmd.Flags().StringVar(&msgBody, "body", "", "Body text to send (Mail.app does not reliably auto-quote the original via scripting)")
	messagesReplyCmd.Flags().StringVar(&msgSignature, "signature", "", "Name of an existing Mail.app signature to attach")
	messagesReplyCmd.Flags().StringSliceVar(&msgAttachments, "attach", []string{}, "File paths to attach (can be specified multiple times)")

	messagesForwardCmd.Flags().StringSliceVarP(&msgTo, "to", "t", []string{}, "To recipients (can be specified multiple times, required)")
	messagesForwardCmd.Flags().StringSliceVarP(&msgCc, "cc", "c", []string{}, "Cc recipients (can be specified multiple times)")
	messagesForwardCmd.Flags().StringVar(&msgBody, "body", "", "Body text to prepend")
	messagesForwardCmd.Flags().StringVar(&msgSignature, "signature", "", "Name of an existing Mail.app signature to attach")
	messagesForwardCmd.Flags().StringSliceVar(&msgAttachments, "attach", []string{}, "File paths to attach (can be specified multiple times)")

	messagesRedirectCmd.Flags().StringSliceVarP(&msgTo, "to", "t", []string{}, "To recipients (can be specified multiple times, required)")

	// Mark-junk-specific flags
	messagesMarkJunkCmd.Flags().BoolVar(&msgJunkSet, "junk", true, "Mark as junk (default) or use --junk=false to unmark")

	// Unified view flags (shared across all unified subcommands)
	for _, cmd := range []*cobra.Command{
		messagesInboxCmd, messagesUnreadCmd, messagesSentCmd,
		messagesDraftsCmd, messagesFlaggedCmd, messagesTrashCmd, messagesJunkCmd,
	} {
		cmd.Flags().IntVarP(&msgLimit, "limit", "l", 25, "Maximum number of messages to return")
		cmd.Flags().IntVarP(&msgOffset, "offset", "o", 0, "Number of messages to skip (pagination)")
		cmd.Flags().BoolVar(&msgWithContent, "with-content", false, "Include message content")
	}
}
