package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/intelligrit/mail-app-cli/pkg/mail"
	"github.com/spf13/cobra"
)

var signaturesCmd = &cobra.Command{
	Use:   "signatures",
	Short: "View Mail.app signatures",
	Long:  `List your configured Mail.app signatures. Read-only — Mail.app's scripting dictionary does not allow creating or editing signature content.`,
}

var signaturesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all signatures",
	Long:  `List all Mail.app signatures. Output is JSON format. Names are the values accepted by --signature on send/reply/forward.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := mail.NewClient()
		sigs, err := client.GetSignaturesJSON()
		if err != nil {
			return fmt.Errorf("failed to get signatures: %w", err)
		}

		output, err := json.MarshalIndent(sigs, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal signatures: %w", err)
		}

		fmt.Println(string(output))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(signaturesCmd)
	signaturesCmd.AddCommand(signaturesListCmd)
}
