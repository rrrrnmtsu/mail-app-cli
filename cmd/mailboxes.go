package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/intelligrit/mail-app-cli/pkg/cache"
	"github.com/intelligrit/mail-app-cli/pkg/mail"
	"github.com/spf13/cobra"
)

var (
	mailboxAccount      string
	mailboxNoCache      bool
	mailboxForceRefresh bool
	mailboxName         string
	mailboxParent       string
	mailboxNewName      string
	mailboxForce        bool
)

// invalidateMailboxListCache drops cached mailbox listings after the tree
// changes, so `mailboxes list` does not keep showing the old tree.
func invalidateMailboxListCache() {
	if c, err := cache.New(); err == nil {
		c.DeletePrefix("mailboxes")
	}
}

func printMailboxInfo(info *mail.MailboxInfo) error {
	output, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mailbox: %w", err)
	}
	fmt.Println(string(output))
	return nil
}

var mailboxesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a mailbox",
	Long: `Create a mailbox in an account, or in the local store with -a "On My Mac"
(aliases: "このMac内", "local"). --parent takes a mailbox name or a
"Parent/Child" path. Prints the created mailbox as JSON.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if mailboxAccount == "" || mailboxName == "" {
			return fmt.Errorf("both --account and --name are required")
		}
		info, err := mail.NewClient().CreateMailbox(mailboxAccount, mailboxName, mailboxParent)
		if err != nil {
			return fmt.Errorf("failed to create mailbox: %w", err)
		}
		invalidateMailboxListCache()
		return printMailboxInfo(info)
	},
}

var mailboxesRenameCmd = &cobra.Command{
	Use:   "rename",
	Short: "Rename a mailbox",
	Long: `Rename a mailbox in place (it stays under the same parent). --name takes a
mailbox name or a "Parent/Child" path; --to is the new leaf name.
Prints the renamed mailbox as JSON.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if mailboxAccount == "" || mailboxName == "" || mailboxNewName == "" {
			return fmt.Errorf("--account, --name and --to are required")
		}
		info, err := mail.NewClient().RenameMailbox(mailboxAccount, mailboxName, mailboxNewName)
		if err != nil {
			return fmt.Errorf("failed to rename mailbox: %w", err)
		}
		invalidateMailboxListCache()
		invalidateMailboxCache(mailboxAccount, mailboxName)
		return printMailboxInfo(info)
	},
}

var mailboxesDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a mailbox",
	Long: `Delete a mailbox. Deleting a mailbox in Mail.app deletes the messages in it,
so a mailbox that still contains messages or child mailboxes is refused
unless --force is given. System mailboxes (INBOX, Sent, Drafts, Trash,
Junk, All Mail, ...) are always refused. Prints the deleted mailbox as JSON.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if mailboxAccount == "" || mailboxName == "" {
			return fmt.Errorf("both --account and --name are required")
		}
		info, err := mail.NewClient().DeleteMailbox(mailboxAccount, mailboxName, mailboxForce)
		if err != nil {
			return fmt.Errorf("failed to delete mailbox: %w", err)
		}
		invalidateMailboxListCache()
		invalidateMailboxCache(mailboxAccount, mailboxName)
		return printMailboxInfo(info)
	},
}

var mailboxesCmd = &cobra.Command{
	Use:   "mailboxes",
	Short: "Manage Mail.app mailboxes",
	Long:  `View and manage your Mail.app mailboxes.`,
}

var mailboxesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all mailboxes",
	Long:  `List all mailboxes across all accounts or for a specific account. Output is JSON format. Use jq for pretty printing: mail-app-cli mailboxes list | jq`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var mailboxes []mail.Mailbox

		// Determine cache key based on whether account is specified
		cacheKey := "mailboxes"
		if mailboxAccount != "" {
			cacheKey = fmt.Sprintf("mailboxes-%s", mailboxAccount)
		}

		// Try to get from cache if not disabled
		if !mailboxNoCache && !mailboxForceRefresh {
			c, err := cache.New()
			if err == nil {
				found, err := c.Get(cacheKey, &mailboxes)
				if err == nil && found {
					output, err := json.MarshalIndent(mailboxes, "", "  ")
					if err != nil {
						return fmt.Errorf("failed to marshal mailboxes: %w", err)
					}
					fmt.Println(string(output))
					return nil
				}
			}
		}

		// Get from Mail.app
		client := mail.NewClient()
		mailboxes, err := client.GetMailboxesJSON(mailboxAccount)
		if err != nil {
			return fmt.Errorf("failed to get mailboxes: %w", err)
		}

		// Save to cache if not disabled
		if !mailboxNoCache {
			c, err := cache.New()
			if err == nil {
				c.Set(cacheKey, mailboxes)
			}
		}

		output, err := json.MarshalIndent(mailboxes, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal mailboxes: %w", err)
		}

		fmt.Println(string(output))
		return nil
	},
}

func init() {
	mailboxesCmd.AddCommand(mailboxesListCmd)
	mailboxesListCmd.Flags().StringVarP(&mailboxAccount, "account", "a", "", "Filter by account name")
	mailboxesListCmd.Flags().BoolVar(&mailboxNoCache, "no-cache", false, "Bypass cache and fetch fresh data")
	mailboxesListCmd.Flags().BoolVar(&mailboxForceRefresh, "force-refresh", false, "Force refresh cache with fresh data")

	mailboxesCmd.AddCommand(mailboxesCreateCmd, mailboxesRenameCmd, mailboxesDeleteCmd)
	for _, c := range []*cobra.Command{mailboxesCreateCmd, mailboxesRenameCmd, mailboxesDeleteCmd} {
		c.Flags().StringVarP(&mailboxAccount, "account", "a", "", `Account name, or "On My Mac" for local mailboxes (required)`)
		c.Flags().StringVar(&mailboxName, "name", "", "Mailbox name (required)")
	}
	mailboxesCreateCmd.Flags().StringVar(&mailboxParent, "parent", "", "Parent mailbox name or path to create under")
	mailboxesRenameCmd.Flags().StringVar(&mailboxNewName, "to", "", "New mailbox name (required)")
	mailboxesDeleteCmd.Flags().BoolVar(&mailboxForce, "force", false, "Delete even if the mailbox still contains messages or child mailboxes (deletes them)")
}
