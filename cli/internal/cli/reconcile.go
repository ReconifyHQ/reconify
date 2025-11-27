package cli

import (
	"fmt"

	"github.com/reconify/reconify/internal/config"
	"github.com/spf13/cobra"
)

func newReconcileCmd() *cobra.Command {
	var pairName string
	var outputPath string

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Run a reconciliation between two sources",
		Long: `Execute a reconciliation between two configured sources.
This command reads CSV files, normalizes them, and matches transactions
according to the configured rules.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if pairName == "" {
				return fmt.Errorf("--pair is required")
			}

			cfgPath := getConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Validate config
			if errs := cfg.Validate(); len(errs) > 0 {
				return fmt.Errorf("config validation failed: %v", errs[0])
			}

			// TODO: Implement reconciliation logic
			fmt.Fprintf(cmd.OutOrStdout(), "Reconciling pair: %s\n", pairName)
			fmt.Fprintf(cmd.OutOrStdout(), "Output: %s\n", outputPath)
			fmt.Fprintf(cmd.OutOrStdout(), "⚠️  Reconciliation not yet implemented\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&pairName, "pair", "", "Pair name to reconcile (required)")
	cmd.Flags().StringVarP(&outputPath, "out", "o", "-", "Output file path (use '-' for stdout)")

	return cmd
}
