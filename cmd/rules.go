package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/intelligrit/mail-app-cli/pkg/mail"
	"github.com/spf13/cobra"
)

var rulesEnabledSet bool

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Manage Mail.app message rules",
	Long:  `View and toggle your Mail.app message rules.`,
}

var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all rules",
	Long:  `List all Mail.app message rules. Output is JSON format. Use jq for pretty printing: mail-app-cli rules list | jq`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := mail.NewClient()
		rules, err := client.GetRulesJSON()
		if err != nil {
			return fmt.Errorf("failed to get rules: %w", err)
		}

		output, err := json.MarshalIndent(rules, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal rules: %w", err)
		}

		fmt.Println(string(output))
		return nil
	},
}

var rulesSetEnabledCmd = &cobra.Command{
	Use:   "set-enabled [rule-name]",
	Short: "Enable or disable a rule",
	Long:  `Enable or disable a rule by name. Use --enabled=false to disable.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		client := mail.NewClient()
		err := client.SetRuleEnabled(name, rulesEnabledSet)
		if err != nil {
			return fmt.Errorf("failed to update rule: %w", err)
		}

		status := "disabled"
		if rulesEnabledSet {
			status = "enabled"
		}
		fmt.Printf("Rule %q %s\n", name, status)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rulesCmd)
	rulesCmd.AddCommand(rulesListCmd)
	rulesCmd.AddCommand(rulesSetEnabledCmd)

	rulesSetEnabledCmd.Flags().BoolVar(&rulesEnabledSet, "enabled", true, "Enable the rule (default) or use --enabled=false to disable")
}
