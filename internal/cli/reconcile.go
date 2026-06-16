package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine"
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
	var auditFixedTimestamp string
	var deterministic bool
	var progress bool
	var progressEvery int

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

			// Counterparts() resolves either pair.Right (single counterpart, the
			// historical config shape) or pair.Rights (1-N sources). Config
			// validation already guarantees exactly one of the two is set and that
			// every name exists in cfg.Sources.
			counterparts := pair.Counterparts()
			if len(counterparts) == 0 {
				return fmt.Errorf("pair %q has no right or rights configured", pairName)
			}

			// Resolve left file path: explicit flag overrides the glob pattern.
			leftPath, err := resolveFile(leftFile, leftSrc.FilePattern)
			if err != nil {
				return fmt.Errorf("left source: %w", err)
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
				defer func() {
					if closeErr := out.Close(); closeErr != nil {
						fmt.Fprintf(os.Stderr, "warning: close output file: %v\n", closeErr)
					}
				}()
			}

			// All formats route through ReconcileStreaming.
			// The caller creates the index and passes it in — ReconcileStreaming
			// never assumes a specific RightIndex implementation.
			w, err := engine.NewResultWriter(format, out)
			if err != nil {
				return err
			}

			// Propagate pair/source metadata to writers that support it.
			rightLabel := strings.Join(counterparts, ",")
			if sw, ok := w.(interface {
				SetMeta(pairName, leftSource, rightSource string)
			}); ok {
				sw.SetMeta(pairName, pair.Left, rightLabel)
			}

			// Audit mode: hash both input files and embed run provenance in output.
			// Not yet supported for multi-counterpart (rights) pairs — BuildRunInfo's
			// envelope assumes a single right file/source.
			if auditMode && len(counterparts) > 1 {
				return fmt.Errorf("--audit is not yet supported for multi-counterpart (rights) pairs")
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
				if auditMode && auditFixedTimestamp == "" {
					fmt.Fprintln(os.Stderr,
						"warning: --deterministic with --audit still varies run_info.timestamp/run_id; set --audit-fixed-timestamp for byte-identical reruns")
				}
			}

			jobStart := time.Now()

			if len(counterparts) == 1 {
				// Single-counterpart path: byte-identical to pre-1-N-source behavior.
				// Never touches the multi-source code path below.
				rightSrc, ok := cfg.Sources[counterparts[0]]
				if !ok {
					return fmt.Errorf("right source %q not found in config", counterparts[0])
				}
				rightPath, err := resolveFile(rightFile, rightSrc.FilePattern)
				if err != nil {
					return fmt.Errorf("right source: %w", err)
				}

				if auditMode {
					setter, ok := w.(engine.RunInfoSetter)
					if !ok {
						return fmt.Errorf("--audit is only supported for --format=json, json-stream, or ndjson (got %q)", format)
					}
					runStart := time.Now().UTC()
					if auditFixedTimestamp != "" {
						parsed, err := time.Parse(time.RFC3339Nano, auditFixedTimestamp)
						if err != nil {
							return fmt.Errorf("--audit-fixed-timestamp must be RFC3339 or RFC3339Nano: %w", err)
						}
						runStart = parsed.UTC()
					}
					info, err := engine.BuildRunInfo(cliVersion, leftPath, rightPath, pair, runStart)
					if err != nil {
						return fmt.Errorf("audit: %w", err)
					}
					if err := setter.SetRunInfo(info); err != nil {
						return fmt.Errorf("audit: set run info: %w", err)
					}
				}

				idx, backendLabel, err := newRightIndex(cfg.Index, rightPath)
				if err != nil {
					return fmt.Errorf("init right index: %w", err)
				}
				defer func() {
					if closeErr := idx.Close(); closeErr != nil {
						fmt.Fprintf(os.Stderr, "warning: close index backend: %v\n", closeErr)
					}
				}()
				if progress {
					fmt.Fprintf(os.Stderr, "progress: index backend=%s\n", backendLabel)
				}

				progressFn := func(e engine.ProgressEvent) {
					elapsed := e.Elapsed.Round(time.Second)
					rate := 0.0
					if e.Elapsed > 0 {
						rate = float64(e.Rows) / e.Elapsed.Seconds()
					}
					if e.Done {
						fmt.Fprintf(os.Stderr, "progress: %s done rows=%d elapsed=%s avg_rate=%.0f rows/s\n", e.Phase, e.Rows, elapsed, rate)
						return
					}
					fmt.Fprintf(os.Stderr, "progress: %s rows=%d elapsed=%s rate=%.0f rows/s\n", e.Phase, e.Rows, elapsed, rate)
				}

				run := func() error {
					if progress {
						return engine.ReconcileStreamingWithProgress(
							context.Background(),
							pairName,
							pair.Left,
							counterparts[0],
							leftPath,
							rightPath,
							leftSrc.Parser,
							rightSrc.Parser,
							pair,
							idx,
							w,
							maxTokenBuffer,
							progressFn,
							progressEvery,
						)
					}
					return engine.ReconcileStreaming(
						context.Background(),
						pairName,
						pair.Left,
						counterparts[0],
						leftPath,
						rightPath,
						leftSrc.Parser,
						rightSrc.Parser,
						pair,
						idx,
						w,
						maxTokenBuffer,
					)
				}

				if err := run(); err != nil {
					return fmt.Errorf("reconciliation failed: %w", err)
				}
			} else {
				// Multi-counterpart (1-N source) path: each counterpart resolves its
				// file via its own source's file_pattern; --right-file (a single
				// explicit override) does not apply here.
				if rightFile != "" {
					return fmt.Errorf("--right-file is not supported with multiple counterparts (rights); each counterpart resolves its file via its own source's file_pattern")
				}
				if progress {
					fmt.Fprintln(os.Stderr, "warning: --progress is not yet supported for multi-counterpart (rights) pairs; ignoring")
				}

				cps := make([]engine.CounterpartStream, 0, len(counterparts))
				var indexes []engine.RightIndex
				defer func() {
					for _, idx := range indexes {
						if closeErr := idx.Close(); closeErr != nil {
							fmt.Fprintf(os.Stderr, "warning: close index backend: %v\n", closeErr)
						}
					}
				}()

				for _, name := range counterparts {
					src, ok := cfg.Sources[name]
					if !ok {
						return fmt.Errorf("right source %q not found in config", name)
					}
					path, err := resolveFile("", src.FilePattern)
					if err != nil {
						return fmt.Errorf("counterpart %q: %w", name, err)
					}
					idx, _, err := newRightIndex(cfg.Index, path)
					if err != nil {
						return fmt.Errorf("init index for counterpart %q: %w", name, err)
					}
					indexes = append(indexes, idx)
					cps = append(cps, engine.CounterpartStream{
						SourceName: name,
						RightPath:  path,
						RightCfg:   src.Parser,
						Index:      idx,
					})
				}

				if err := engine.ReconcileStreamingMultiSource(
					context.Background(),
					pairName,
					pair.Left,
					leftPath,
					leftSrc.Parser,
					cps,
					pair,
					w,
					maxTokenBuffer,
				); err != nil {
					return fmt.Errorf("reconciliation failed: %w", err)
				}
			}

			if progress {
				fmt.Fprintf(os.Stderr, "progress: reconcile done pair=%s elapsed=%s\n", pairName, time.Since(jobStart).Round(time.Second))
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
	cmd.Flags().StringVar(&auditFixedTimestamp, "audit-fixed-timestamp", "",
		"Optional RFC3339/RFC3339Nano timestamp to freeze run_info timestamp/run_id (use with --audit for byte-identical reruns)")
	cmd.Flags().BoolVar(&deterministic, "deterministic", false,
		"Sort output sections for stable diff-based audit trails (json format only; adds sort overhead)")
	cmd.Flags().BoolVar(&progress, "progress", false,
		"Log progress to stderr while processing large files")
	cmd.Flags().IntVar(&progressEvery, "progress-every", 1_000_000,
		"Progress log interval in rows (used with --progress)")

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

const defaultAutoMaxRightFileMB int64 = 2048

func newRightIndex(indexCfg config.IndexCfg, rightPath string) (engine.RightIndex, string, error) {
	backend := indexCfg.Backend
	if backend == "" {
		backend = "memory"
	}

	switch backend {
	case "memory":
		return engine.NewMemoryIndex(), "memory", nil
	case "disk":
		idx, err := engine.NewDiskIndex(indexCfg.SpillDir)
		if err != nil {
			return nil, "", err
		}
		if indexCfg.SpillDir == "" {
			return idx, "disk(tempdir)", nil
		}
		return idx, fmt.Sprintf("disk(spill_dir=%s)", indexCfg.SpillDir), nil
	case "auto":
		thresholdMB := indexCfg.AutoMaxRightFileMB
		if thresholdMB <= 0 {
			thresholdMB = defaultAutoMaxRightFileMB
		}
		st, err := os.Stat(rightPath)
		if err != nil {
			return nil, "", fmt.Errorf("stat right file for auto backend: %w", err)
		}
		rightMB := st.Size() / (1024 * 1024)
		if rightMB > thresholdMB {
			idx, err := engine.NewDiskIndex(indexCfg.SpillDir)
			if err != nil {
				return nil, "", err
			}
			return idx, fmt.Sprintf("disk(auto right_file_mb=%d threshold_mb=%d)", rightMB, thresholdMB), nil
		}
		return engine.NewMemoryIndex(), fmt.Sprintf("memory(auto right_file_mb=%d threshold_mb=%d)", rightMB, thresholdMB), nil
	default:
		// Guarded by config validation, but keep a safe runtime fallback.
		return nil, "", fmt.Errorf("unsupported index backend %q", backend)
	}
}
