// Package cli provides command-line interface commands for Reconify
package cli

import (
	"fmt"

	"github.com/reconify/reconify/config"
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
			_ = args // avoid unused variable warning
			cfgPath := getConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if errs := cfg.Validate(); len(errs) > 0 {
				cmd.PrintErrf("❌ %s is invalid:\n", cfgPath)
				for _, err := range errs {
					cmd.PrintErrf("  - %v\n", err)
				}
				return fmt.Errorf("validation failed")
			}

			cmd.PrintErrf("✅ %s is valid\n", cfgPath)
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
			_ = args // avoid unused variable warning
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
			cmd.PrintErrf("Checking source %q against file %q...\n", sourceName, filePath)
			cmd.PrintErrf("⚠️  Source checking not yet implemented\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&sourceName, "source", "", "Source name to check")
	cmd.Flags().StringVar(&filePath, "file", "", "CSV file path to validate")

	return cmd
}
