package cli

import (
	"context"
	"fmt"
	"io"
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
  json-stream  Streaming JSON object; encodes each event to bytes immediately,
               releasing Go structs early. Lower GC pressure than json, but
               JSON bytes still accumulate; not O(1) memory. For O(1) memory,
               use ndjson or csv.
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
			cfgAbs, err := filepath.Abs(cfgPath)
			if err != nil {
				return fmt.Errorf("resolve config path: %w", err)
			}
			configDir := filepath.Dir(cfgAbs)

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
			leftPath, err := resolveFile(leftFile, leftSrc.FilePattern, configDir)
			if err != nil {
				return fmt.Errorf("left source: %w", err)
			}
			rightPath, err := resolveFile(rightFile, rightSrc.FilePattern, configDir)
			if err != nil {
				return fmt.Errorf("right source: %w", err)
			}

			output, err := openReconcileOutput(outputPath, auditMode)
			if err != nil {
				return err
			}
			defer output.Cleanup()

			// All formats route through ReconcileStreaming.
			// The caller creates the index and passes it in — ReconcileStreaming
			// never assumes a specific RightIndex implementation.
			w, err := engine.NewResultWriter(format, output.File)
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
			var auditInfo engine.RunInfo
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
				auditInfo = info
				if err := setter.SetRunInfo(info); err != nil {
					return fmt.Errorf("audit: set run info: %w", err)
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
				if auditMode && auditFixedTimestamp == "" {
					fmt.Fprintln(os.Stderr,
						"warning: --deterministic with --audit still varies run_info.timestamp/run_id; set --audit-fixed-timestamp for byte-identical reruns")
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
			jobStart := time.Now()
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
						pair.Right,
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
					pair.Right,
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
			if auditMode {
				if err := engine.VerifyAuditFiles(auditInfo); err != nil {
					return fmt.Errorf("audit: %w", err)
				}
			}
			if err := output.Commit(); err != nil {
				return err
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

type reconcileOutput struct {
	File         *os.File
	finalPath    string
	tempPath     string
	copyToStdout bool
	directStdout bool
	closed       bool
	committed    bool
}

func openReconcileOutput(outputPath string, auditMode bool) (*reconcileOutput, error) {
	if outputPath == "-" || outputPath == "" {
		if !auditMode {
			return &reconcileOutput{File: os.Stdout, directStdout: true, committed: true}, nil
		}
		tmp, err := os.CreateTemp("", "reconify-audit-output-*")
		if err != nil {
			return nil, fmt.Errorf("create temporary audit output: %w", err)
		}
		return &reconcileOutput{File: tmp, tempPath: tmp.Name(), copyToStdout: true}, nil
	}

	dir := filepath.Dir(outputPath)
	base := filepath.Base(outputPath)
	if fi, err := os.Lstat(outputPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("output path %q is a symlink; refusing to follow it (remove the symlink first)", outputPath)
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("output path %q exists and is not a regular file", outputPath)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect output path %q: %w", outputPath, err)
	}

	tmp, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary output file: %w", err)
	}
	return &reconcileOutput{File: tmp, finalPath: outputPath, tempPath: tmp.Name()}, nil
}

func (o *reconcileOutput) Commit() error {
	if o.committed {
		return nil
	}
	if err := o.close(); err != nil {
		return err
	}

	if o.copyToStdout {
		if err := copyFileToStdout(o.tempPath); err != nil {
			return err
		}
		o.committed = true
		if err := os.Remove(o.tempPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove temporary audit output: %w", err)
		}
		o.tempPath = ""
		return nil
	}

	if err := ensureOutputPathIsReplaceable(o.finalPath); err != nil {
		return err
	}
	if err := replaceOutputFile(o.tempPath, o.finalPath); err != nil {
		return err
	}
	o.committed = true
	return nil
}

func (o *reconcileOutput) Cleanup() {
	if o == nil || o.directStdout {
		return
	}
	if err := o.close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: close output file: %v\n", err)
	}
	if o.tempPath != "" && !o.committed {
		if err := os.Remove(o.tempPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: remove temporary output file: %v\n", err)
		}
	}
}

func (o *reconcileOutput) close() error {
	if o.closed || o.directStdout {
		return nil
	}
	o.closed = true
	if err := o.File.Close(); err != nil {
		return fmt.Errorf("close output file: %w", err)
	}
	return nil
}

func copyFileToStdout(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open verified audit output: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "warning: close verified audit output: %v\n", closeErr)
		}
	}()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return fmt.Errorf("write verified audit output to stdout: %w", err)
	}
	return nil
}

func ensureOutputPathIsReplaceable(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect output path %q: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path %q is a symlink; refusing to follow it (remove the symlink first)", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("output path %q exists and is not a regular file", path)
	}
	return nil
}

func replaceOutputFile(tempPath, finalPath string) error {
	if err := os.Rename(tempPath, finalPath); err == nil {
		return nil
	} else if err := replaceExistingOutputFile(tempPath, finalPath, err); err != nil {
		return err
	}
	return nil
}

func replaceExistingOutputFile(tempPath, finalPath string, renameErr error) error {
	fi, err := os.Lstat(finalPath)
	if err != nil {
		return fmt.Errorf("replace output file: %w", renameErr)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output path %q is a symlink; refusing to follow it (remove the symlink first)", finalPath)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("output path %q exists and is not a regular file", finalPath)
	}
	if err := os.Remove(finalPath); err != nil {
		return fmt.Errorf("remove existing output file: %w", err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("replace output file: %w", err)
	}
	return nil
}

// resolveFile returns an explicit path if provided, otherwise resolves the first glob match.
func resolveFile(explicit, pattern, configDir string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("file %q not found", explicit)
		}
		return explicit, nil
	}
	if pattern == "" {
		return "", fmt.Errorf("no file specified and no file_pattern configured")
	}
	resolvedPattern := pattern
	if !filepath.IsAbs(resolvedPattern) {
		resolvedPattern = filepath.Join(configDir, resolvedPattern)
	}
	matches, err := filepath.Glob(resolvedPattern)
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", resolvedPattern, err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no files match pattern %q", resolvedPattern)
	}
	if filepath.IsAbs(pattern) {
		return matches[0], nil
	}
	for _, match := range matches {
		inside, err := pathWithinDir(configDir, match)
		if err != nil {
			return "", err
		}
		if inside {
			return match, nil
		}
	}
	return "", fmt.Errorf("file_pattern %q resolves outside config directory %q", pattern, configDir)
}

func pathWithinDir(dir, path string) (bool, error) {
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return false, fmt.Errorf("resolve config directory: %w", err)
	}
	dirAbs, err = filepath.EvalSymlinks(dirAbs)
	if err != nil {
		return false, fmt.Errorf("resolve config directory symlinks: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve matched file: %w", err)
	}
	pathAbs, err = filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return false, fmt.Errorf("resolve matched file symlinks: %w", err)
	}
	rel, err := filepath.Rel(dirAbs, pathAbs)
	if err != nil {
		return false, fmt.Errorf("compare matched file to config directory: %w", err)
	}
	return rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !startsWithParent(rel)), nil
}

func startsWithParent(path string) bool {
	return path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator))
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
