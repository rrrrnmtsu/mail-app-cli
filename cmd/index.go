package cmd

import (
	"fmt"

	"github.com/intelligrit/mail-app-cli/pkg/mail"
	"github.com/spf13/cobra"
)

var indexCheckLimit int

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Fast listings from Mail's own database",
	Long: `Message listings can be read straight from Mail.app's Envelope Index
instead of AppleScript (milliseconds instead of seconds). This needs Full Disk
Access for the terminal and is only enabled after "index check" passes on this
macOS build. Set ` + mail.DisableIndexEnv + `=1 to turn it off.`,
}

var indexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which path listings use",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(mail.IndexStatus())
		return nil
	},
}

var indexCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Compare the index with AppleScript and enable it if they agree",
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := mail.NewClient().CheckIndex(indexCheckLimit)
		if err != nil {
			return err
		}
		fmt.Printf("index: %s\nmacOS build: %s\n", res.Path, res.Build)
		for _, mb := range res.Mailboxes {
			switch {
			case mb.Skipped != "":
				fmt.Printf("  SKIP %s / %s: %s\n", mb.Account, mb.Mailbox, mb.Skipped)
			case len(mb.Mismatches) == 0:
				fmt.Printf("  OK   %s / %s (%d messages)\n", mb.Account, mb.Mailbox, mb.Compared)
			default:
				fmt.Printf("  NG   %s / %s (%d messages, %d mismatches)\n", mb.Account, mb.Mailbox, mb.Compared, len(mb.Mismatches))
				for i, m := range mb.Mismatches {
					if i == 5 {
						fmt.Printf("       … %d more\n", len(mb.Mismatches)-5)
						break
					}
					fmt.Printf("       %s\n", m)
				}
			}
		}
		if !res.OK {
			return fmt.Errorf("index does not match AppleScript; listings stay on AppleScript")
		}
		fmt.Printf("enabled (marker: %s)\n", res.Marker)
		return nil
	},
}

func init() {
	indexCheckCmd.Flags().IntVarP(&indexCheckLimit, "limit", "l", 30, "Messages to compare per mailbox")
	indexCmd.AddCommand(indexStatusCmd)
	indexCmd.AddCommand(indexCheckCmd)
	rootCmd.AddCommand(indexCmd)
}
