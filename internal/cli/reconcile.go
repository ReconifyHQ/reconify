package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/reconify/reconify/config"
	"github.com/reconify/reconify/engine"
	"github.com/spf13/cobra"
)

func newReconcileCmd() *cobra.Command {
	var pairName string
	var outputPath string
	var leftFile string
	var rightFile string
	var format string
	var maxTokenBuffer int
	var auditMode bool
	var deterministic bool

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Run a reconciliation between two sources",
		Long: `Execute a reconciliation between two configured sources.
Reads CSV files, normalizes them, and matches transactions according to
configured rules. Outputs results in the requested format.

Formats:
  json         (default) Indented JSON object; buffers full result in memory.
               For files >500k rows, prefer json-stream, ndjson, or csv.
  json-stream  Streaming JSON object; same structure as json but encodes
               each event immediately. Lower GC pressure for large files.
               Note: output is invalid JSON if the process is interrupted.
  ndjson       One tagged JSON line per event; O(1) memory; crash-safe.
  csv          Fixed-schema CSV; O(1) memory.
  table        Aligned ASCII table; buffers all rows in memory.`,
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

			// Open output destination
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

			// All formats route through ReconcileStreaming.
			// The caller creates the index and passes it in — ReconcileStreaming
			// never assumes a specific RightIndex implementation.
			w, err := engine.NewResultWriter(format, out)
			if err != nil {
				return err
			}

			// Propagate pair/source metadata to writers that support it.
			if sw, ok := w.(interface {
				SetMeta(pairName, leftSource, rightSource string)
			}); ok {
				sw.SetMeta(pairName, pair.Left, pair.Right)
			}

			// Audit mode: hash both input files and embed run provenance in output.
			if auditMode {
				runStart := time.Now().UTC()
				info, err := engine.BuildRunInfo(cliVersion, leftPath, rightPath, pair, runStart)
				if err != nil {
					return fmt.Errorf("audit: %w", err)
				}
				if setter, ok := w.(engine.RunInfoSetter); ok {
					if err := setter.SetRunInfo(info); err != nil {
						return fmt.Errorf("audit: set run info: %w", err)
					}
				}
			}

			// Deterministic mode: stable output ordering for diff-based audit trails.
			if deterministic {
				if sd, ok := w.(interface{ SetDeterministic(bool) }); ok {
					sd.SetDeterministic(true)
				} else {
					fmt.Fprintf(os.Stderr,
						"warning: --deterministic has no effect for --format=%q; use --format=json\n",
						format)
				}
			}

			idx := engine.NewMemoryIndex()
			defer idx.Close()

			if err := engine.ReconcileStreaming(
				context.Background(),
				pairName,
				pair.Left,
				pair.Right,
				leftPath,
				rightPath,
				leftSrc.Parser,
				rightSrc.Parser,
				pair,
				idx,
				w,
				maxTokenBuffer,
			); err != nil {
				return fmt.Errorf("reconciliation failed: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&pairName, "pair", "", "Pair name to reconcile (required)")
	cmd.Flags().StringVarP(&outputPath, "out", "o", "-", "Output file path (use '-' for stdout)")
	cmd.Flags().StringVar(&leftFile, "left-file", "", "Explicit path to left source CSV file")
	cmd.Flags().StringVar(&rightFile, "right-file", "", "Explicit path to right source CSV file")
	cmd.Flags().StringVar(&format, "format", "json",
		`Output format: json (default), json-stream, ndjson, csv, table`)
	cmd.Flags().IntVar(&maxTokenBuffer, "max-token-buffer", 100_000,
		"Advisory row limit for token-mode unmatched buffer (0 = unlimited)")
	cmd.Flags().BoolVar(&auditMode, "audit", false,
		"Embed run provenance in output: SHA-256 file hashes, timestamp, tool version, pair config snapshot")
	cmd.Flags().BoolVar(&deterministic, "deterministic", false,
		"Sort output sections for stable diff-based audit trails (json format only; adds sort overhead)")

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
