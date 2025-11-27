package cli

import (
	"fmt"

	"github.com/reconify/reconify/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration management commands",
		Long:  "Commands for validating and checking configuration files",
	}

	cmd.AddCommand(newConfigValidateCmd())
	cmd.AddCommand(newConfigCheckSourceCmd())

	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file structure",
		Long: `Validate the structure and syntax of a reconify configuration file.
This checks that all required fields are present and have valid values.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath := getConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if errs := cfg.Validate(); len(errs) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "❌ %s is invalid:\n", cfgPath)
				for _, err := range errs {
					fmt.Fprintf(cmd.ErrOrStderr(), "  - %v\n", err)
				}
				return fmt.Errorf("validation failed")
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✅ %s is valid\n", cfgPath)
			return nil
		},
	}
}

func newConfigCheckSourceCmd() *cobra.Command {
	var sourceName string
	var filePath string

	cmd := &cobra.Command{
		Use:   "check-source",
		Short: "Check if a CSV file matches a source configuration",
		Long: `Check if a CSV file's structure matches the expected configuration for a source.
This validates that required columns exist and that sample data can be parsed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceName == "" {
				return fmt.Errorf("--source is required")
			}
			if filePath == "" {
				return fmt.Errorf("--file is required")
			}

			cfgPath := getConfigPath()
			_, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// TODO: Implement source checking logic
			fmt.Fprintf(cmd.OutOrStdout(), "Checking source %q against file %q...\n", sourceName, filePath)
			fmt.Fprintf(cmd.OutOrStdout(), "⚠️  Source checking not yet implemented\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&sourceName, "source", "", "Source name to check")
	cmd.Flags().StringVar(&filePath, "file", "", "CSV file path to validate")

	return cmd
}

