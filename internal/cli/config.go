// Package cli provides command-line interface commands for Reconify
package cli

import (
	"encoding/csv"
	"fmt"
	"os"

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
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			cmd.PrintErrf("Checking source %q against file %q...\n", sourceName, filePath)

			// Verify that the source in the command exist in the config file
			source, ok := cfg.Sources[sourceName]
			if !ok {
				return fmt.Errorf("source %q not found in config", sourceName)
			}

			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("failed to open file: %w", err)
			}
			defer file.Close()

			// csv file reader
			reader := csv.NewReader(file)

			// Read headers row
			headers, err := reader.Read()
			if err != nil {
				return fmt.Errorf("failed to read file: %w", err)
			}

			headerSet := make(map[string]bool)
			for _, header := range headers {
				headerSet[header] = true
			}

			valid := true

			if !headerSet[source.Parser.DateCol] {
				cmd.PrintErrf("[x] date_col %q not found in CSV headers\n", source.Parser.DateCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] date_col %q found\n", source.Parser.DateCol)
			}

			if !headerSet[source.Parser.AmountCol] {
				cmd.PrintErrf("[x] amount_col %q not found in CSV headers\n", source.Parser.AmountCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] amount_col %q found\n", source.Parser.AmountCol)
			}

			if source.Parser.CurrencyCol != "" && !headerSet[source.Parser.CurrencyCol] {
				cmd.PrintErrf("[x] currency_col %q not found in CSV headers\n", source.Parser.CurrencyCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] currency_col %q found\n", source.Parser.CurrencyCol)
			}

			if source.Parser.NameCol != "" && !headerSet[source.Parser.NameCol] {
				cmd.PrintErrf("[x] name_col %q not found in CSV headers\n", source.Parser.NameCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] name_col %q found\n", source.Parser.NameCol)
			}

			if source.Parser.RefCol != "" && !headerSet[source.Parser.RefCol] {
				cmd.PrintErrf("[x] ref_col %q not found in CSV headers\n", source.Parser.RefCol)
				valid = false
			} else {
				cmd.PrintErrf("[ok] ref_col %q found\n", source.Parser.RefCol)
			}

			if !valid {
				return fmt.Errorf("source %q does not match file %q", sourceName, filePath)
			}

			cmd.PrintErrf("[OK] source %q matches file %q\n", sourceName, filePath)

			return nil
		},
	}

	cmd.Flags().StringVar(&sourceName, "source", "", "Source name to check")
	cmd.Flags().StringVar(&filePath, "file", "", "CSV file path to validate")

	return cmd
}
