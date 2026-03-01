package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/reconify/reconify/config"
	"github.com/reconify/reconify/engine"
	"github.com/spf13/cobra"
)

func newParseCmd() *cobra.Command {
	var sourceName string
	var filePath string

	cmd := &cobra.Command{
		Use:   "parse",
		Short: "Parse a CSV file according to source configuration",
		Long: `Parse a CSV file using a configured source parser.
This is useful for testing and debugging parser configurations.
Outputs one JSON transaction per line (NDJSON) to stdout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
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

			if errs := cfg.Validate(); len(errs) > 0 {
				return fmt.Errorf("config validation failed: %v", errs[0])
			}

			source, ok := cfg.Sources[sourceName]
			if !ok {
				return fmt.Errorf("source %q not found in config (available: %v)", sourceName, sourceNames(cfg.Sources))
			}

			// Resolve file: use --file directly, or fall back to source glob pattern
			resolvedPath := filePath
			if _, statErr := os.Stat(resolvedPath); os.IsNotExist(statErr) {
				matches, globErr := filepath.Glob(source.FilePattern)
				if globErr != nil || len(matches) == 0 {
					return fmt.Errorf("file %q not found", filePath)
				}
				resolvedPath = matches[0]
			}

			txns, err := engine.ParseCSV(sourceName, resolvedPath, source.Parser)
			if err != nil {
				return fmt.Errorf("parse failed: %w", err)
			}

			enc := json.NewEncoder(os.Stdout)
			for _, tx := range txns {
				if encErr := enc.Encode(tx); encErr != nil {
					return fmt.Errorf("encode: %w", encErr)
				}
			}

			cmd.PrintErrf("Parsed %d transactions from %q\n", len(txns), resolvedPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceName, "source", "", "Source name to use for parsing (required)")
	cmd.Flags().StringVar(&filePath, "file", "", "CSV file path to parse (required)")

	return cmd
}

func sourceNames(sources map[string]config.Source) []string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	return names
}
