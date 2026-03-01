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

func newReconcileCmd() *cobra.Command {
	var pairName string
	var outputPath string
	var leftFile string
	var rightFile string

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Run a reconciliation between two sources",
		Long: `Execute a reconciliation between two configured sources.
This command reads CSV files, normalizes them, and matches transactions
according to the configured rules. Outputs JSON to stdout or a file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			if pairName == "" {
				return fmt.Errorf("--pair is required")
			}

			cfgPath := getConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}
			if errs := cfg.Validate(); len(errs) > 0 {
				return fmt.Errorf("config validation failed: %v", errs[0])
			}

			pair, ok := cfg.Pairs[pairName]
			if !ok {
				return fmt.Errorf("pair %q not found in config", pairName)
			}

			leftSrc, ok := cfg.Sources[pair.Left]
			if !ok {
				return fmt.Errorf("left source %q not found in config", pair.Left)
			}
			rightSrc, ok := cfg.Sources[pair.Right]
			if !ok {
				return fmt.Errorf("right source %q not found in config", pair.Right)
			}

			// Resolve file paths: explicit flags override glob patterns
			leftPath, err := resolveFile(leftFile, leftSrc.FilePattern)
			if err != nil {
				return fmt.Errorf("left source: %w", err)
			}
			rightPath, err := resolveFile(rightFile, rightSrc.FilePattern)
			if err != nil {
				return fmt.Errorf("right source: %w", err)
			}

			// Parse both sources
			leftTxns, err := engine.ParseCSV(pair.Left, leftPath, leftSrc.Parser)
			if err != nil {
				return fmt.Errorf("parse left source: %w", err)
			}
			rightTxns, err := engine.ParseCSV(pair.Right, rightPath, rightSrc.Parser)
			if err != nil {
				return fmt.Errorf("parse right source: %w", err)
			}

			// Run reconciliation
			result, err := engine.Reconcile(pairName, pair.Left, pair.Right, leftTxns, rightTxns, pair)
			if err != nil {
				return fmt.Errorf("reconciliation failed: %w", err)
			}

			// Write output
			var out *os.File
			if outputPath == "-" || outputPath == "" {
				out = os.Stdout
			} else {
				out, err = os.Create(outputPath)
				if err != nil {
					return fmt.Errorf("create output file: %w", err)
				}
				defer out.Close()
			}

			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			if err := enc.Encode(result); err != nil {
				return fmt.Errorf("encode result: %w", err)
			}

			cmd.PrintErrf(
				"Reconciled %q: %d matched, %d unmatched (%.1f%% match rate)\n",
				pairName,
				result.Summary.MatchedCount,
				result.Summary.UnmatchedLeft+result.Summary.UnmatchedRight,
				result.Summary.MatchRatePct,
			)

			return nil
		},
	}

	cmd.Flags().StringVar(&pairName, "pair", "", "Pair name to reconcile (required)")
	cmd.Flags().StringVarP(&outputPath, "out", "o", "-", "Output file path (use '-' for stdout)")
	cmd.Flags().StringVar(&leftFile, "left-file", "", "Explicit path to left source CSV file")
	cmd.Flags().StringVar(&rightFile, "right-file", "", "Explicit path to right source CSV file")

	return cmd
}

// resolveFile returns an explicit path if provided, otherwise resolves the first glob match.
func resolveFile(explicit, pattern string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("file %q not found", explicit)
		}
		return explicit, nil
	}
	if pattern == "" {
		return "", fmt.Errorf("no file specified and no file_pattern configured")
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", pattern, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no files match pattern %q", pattern)
	}
	return matches[0], nil
}
