package cli

import (
	"fmt"

	"github.com/reconify/reconify/internal/config"
	"github.com/spf13/cobra"
)

func newParseCmd() *cobra.Command {
	var sourceName string
	var filePath string

	cmd := &cobra.Command{
		Use:   "parse",
		Short: "Parse a CSV file according to source configuration",
		Long: `Parse a CSV file using a configured source parser.
This is useful for testing and debugging parser configurations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceName == "" {
				return fmt.Errorf("--source is required")
			}
			if filePath == "" {
				return fmt.Errorf("--file is required")
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

			// TODO: Implement parsing logic
			fmt.Fprintf(cmd.OutOrStdout(), "Parsing source %q from file %q...\n", sourceName, filePath)
			fmt.Fprintf(cmd.OutOrStdout(), "⚠️  Parsing not yet implemented\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&sourceName, "source", "", "Source name to use for parsing (required)")
	cmd.Flags().StringVar(&filePath, "file", "", "CSV file path to parse (required)")

	return cmd
}

