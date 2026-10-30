package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/intelligrit/mail-app-cli/pkg/mail"
	"github.com/spf13/cobra"
)

var (
	rulesEnabledSet bool
	ruleName        string
	ruleFrom        string
	ruleSubject     string
	ruleTo          string
	ruleMoveTo      string
	ruleMarkRead    bool
	ruleFlagColor   int
	ruleDeleteMsg   bool
)

var rulesCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an enabled rule",
	Long: `Create an enabled Mail.app rule. Conditions are "contains" matches and all
of them must match; at least one condition and one action are required.
Examples:
  mail-app-cli rules create --name "Newsletters" --from news@example.com --move-to "On My Mac/Newsletters" --mark-read
  mail-app-cli rules create --name "Boss" --from boss@example.com --flag-color 0`,
	RunE: func(cmd *cobra.Command, args []string) error {
		spec := mail.RuleSpec{
			Name: ruleName, From: ruleFrom, Subject: ruleSubject, To: ruleTo,
			MoveTo: ruleMoveTo, MarkRead: ruleMarkRead, FlagColor: -1, DeleteMessage: ruleDeleteMsg,
		}
		if cmd.Flags().Changed("flag-color") {
			spec.FlagColor = ruleFlagColor
		}
		if err := spec.Validate(); err != nil {
			return err
		}
		rule, err := mail.NewClient().CreateRule(spec)
		if err != nil {
			return fmt.Errorf("failed to create rule: %w", err)
		}
		output, err := json.MarshalIndent(rule, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal rule: %w", err)
		}
		fmt.Println(string(output))
		return nil
	},
}

var rulesDeleteCmd = &cobra.Command{
	Use:   "delete [rule-name]",
	Short: "Delete a rule",
	Long:  `Delete the rule with exactly this name. Refuses if several rules share the name.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := mail.NewClient().DeleteRule(args[0]); err != nil {
			return fmt.Errorf("failed to delete rule: %w", err)
		}
		fmt.Printf("Rule %q deleted\n", args[0])
		return nil
	},
}

var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Manage Mail.app message rules",
	Long:  `View, create, delete and toggle your Mail.app message rules.`,
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
	rulesCmd.AddCommand(rulesCreateCmd)
	rulesCmd.AddCommand(rulesDeleteCmd)

	rulesCreateCmd.Flags().StringVar(&ruleName, "name", "", "Rule name (required)")
	rulesCreateCmd.Flags().StringVar(&ruleFrom, "from", "", "Condition: From contains TEXT")
	rulesCreateCmd.Flags().StringVar(&ruleSubject, "subject", "", "Condition: Subject contains TEXT")
	rulesCreateCmd.Flags().StringVar(&ruleTo, "to", "", "Condition: To contains TEXT")
	rulesCreateCmd.Flags().StringVar(&ruleMoveTo, "move-to", "", `Action: move to "ACCOUNT/MAILBOX" (account may be "On My Mac")`)
	rulesCreateCmd.Flags().BoolVar(&ruleMarkRead, "mark-read", false, "Action: mark as read")
	rulesCreateCmd.Flags().IntVar(&ruleFlagColor, "flag-color", 0, "Action: flag with colour 0-6 (0=red 1=orange 2=yellow 3=green 4=blue 5=purple 6=gray)")
	rulesCreateCmd.Flags().BoolVar(&ruleDeleteMsg, "delete-message", false, "Action: delete the message")

	rulesSetEnabledCmd.Flags().BoolVar(&rulesEnabledSet, "enabled", true, "Enable the rule (default) or use --enabled=false to disable")
}
