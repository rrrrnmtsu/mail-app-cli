package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/intelligrit/mail-app-cli/pkg/mail"
	"github.com/spf13/cobra"
)

var (
	sendAccount     string
	sendTo          []string
	sendCc          []string
	sendBcc         []string
	sendSubject     string
	sendBody        string
	sendAttachments []string
	sendSignature   string
	sendDraft       bool
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send an email message",
	Long: `Send an email message through Mail.app.
Examples:
  mail-app-cli send --account "Gmail" --to user@example.com --subject "Hello" --body "Message content"
  mail-app-cli send -a "Gmail" -t user@example.com -t another@example.com --subject "Multi recipient" --body "Content"
  mail-app-cli send -a "Gmail" -t user@example.com -s "With attachments" --body "See attached" --attach ~/file.pdf --attach ~/image.png
  mail-app-cli send -a "Gmail" -t user@example.com -s "Later" --body "Draft text" --draft`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if sendAccount == "" {
			return fmt.Errorf("--account is required")
		}
		if len(sendTo) == 0 {
			return fmt.Errorf("at least one --to recipient is required")
		}
		if sendSubject == "" {
			return fmt.Errorf("--subject is required")
		}

		client := mail.NewClient()
		if sendDraft {
			attachments, err := mail.ResolveAttachmentPaths(sendAttachments)
			if err != nil {
				return err
			}
			draft, err := client.SaveDraft(mail.OutgoingMessage{
				Account: sendAccount, Subject: sendSubject, Body: sendBody,
				To: sendTo, Cc: sendCc, Bcc: sendBcc,
				Attachments: attachments, Signature: sendSignature,
			})
			if err != nil {
				return fmt.Errorf("failed to save draft: %w", err)
			}
			output, err := json.MarshalIndent(draft, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal draft: %w", err)
			}
			fmt.Println(string(output))
			fmt.Fprintf(os.Stderr, "Draft saved to %s/%s\n", draft.Account, draft.Mailbox)
			return nil
		}
		err := client.SendMessage(sendAccount, sendSubject, sendBody, sendTo, sendCc, sendBcc, sendAttachments, sendSignature)
		if err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}

		if len(sendAttachments) > 0 {
			fmt.Fprintf(os.Stderr, "Message sent to %s with %d attachment(s)\n", strings.Join(sendTo, ", "), len(sendAttachments))
		} else {
			fmt.Fprintf(os.Stderr, "Message sent to: %s\n", strings.Join(sendTo, ", "))
		}
		return nil
	},
}

func init() {
	sendCmd.Flags().StringVarP(&sendAccount, "account", "a", "", "Account to send from (required)")
	sendCmd.Flags().StringSliceVarP(&sendTo, "to", "t", []string{}, "To recipients (can be specified multiple times)")
	sendCmd.Flags().StringSliceVarP(&sendCc, "cc", "c", []string{}, "Cc recipients (can be specified multiple times)")
	sendCmd.Flags().StringSliceVarP(&sendBcc, "bcc", "b", []string{}, "Bcc recipients (can be specified multiple times)")
	sendCmd.Flags().StringVarP(&sendSubject, "subject", "s", "", "Email subject (required)")
	sendCmd.Flags().StringVarP(&sendBody, "body", "", "", "Email body content")
	sendCmd.Flags().StringSliceVar(&sendAttachments, "attach", []string{}, "File paths to attach (can be specified multiple times)")
	sendCmd.Flags().BoolVar(&sendDraft, "draft", false, "Save to the account's Drafts mailbox instead of sending (prints the draft as JSON)")
	sendCmd.Flags().StringVar(&sendSignature, "signature", "", "Name of an existing Mail.app signature to attach (see: mail-app-cli signatures list)")

	sendCmd.MarkFlagRequired("account")
	sendCmd.MarkFlagRequired("to")
	sendCmd.MarkFlagRequired("subject")
}
