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
	var failIfUnmatched bool

	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Run a reconciliation between two sources",
		Long: `Execute a reconciliation between two configured sources.
Reads configured input files, normalizes them, and matches transactions according to
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
				return configErr("--pair is required")
			}

			cfgPath := getConfigPath()
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return configErrf("failed to load config: %v", err)
			}
			if errs := cfg.Validate(); len(errs) > 0 {
				return configErrf("config validation failed: %v", errs[0])
			}
			cfgAbs, err := filepath.Abs(cfgPath)
			if err != nil {
				return fmt.Errorf("resolve config path: %w", err)
			}
			configDir := filepath.Dir(cfgAbs)

			pair, ok := cfg.Pairs[pairName]
			if !ok {
				return configErrf("pair %q not found in config", pairName)
			}

			leftSrc, ok := cfg.Sources[pair.Left]
			if !ok {
				return configErrf("left source %q not found in config", pair.Left)
			}

			// Counterparts() resolves either pair.Right (single counterpart, the
			// historical config shape) or pair.Rights (1-N sources). Config
			// validation already guarantees exactly one of the two is set and that
			// every name exists in cfg.Sources.
			counterparts := pair.Counterparts()
			if len(counterparts) == 0 {
				return configErrf("pair %q has no right or rights configured", pairName)
			}

			// Resolve left file path: explicit flag overrides the glob pattern.
			leftPath, err := resolveFile(leftFile, leftSrc.FilePattern, configDir)
			if err != nil {
				return configErrf("left source: %v", err)
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
			rightLabel := strings.Join(counterparts, ",")
			if sw, ok := w.(interface {
				SetMeta(pairName, leftSource, rightSource string)
			}); ok {
				sw.SetMeta(pairName, pair.Left, rightLabel)
			}

			// Audit mode: hash both input files and embed run provenance in output.
			// Not yet supported for multi-counterpart (rights) pairs — BuildRunInfo's
			// envelope assumes a single right file/source. auditInfo is populated
			// below, inside the single-counterpart branch, once rightPath is known.
			if auditMode && len(counterparts) > 1 {
				return configErr("--audit is not yet supported for multi-counterpart (rights) pairs")
			}
			var auditInfo engine.RunInfo

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

			// When --fail-if-unmatched is set, wrap the writer to capture the final
			// summary. All optional-interface setup (SetMeta, SetDeterministic) has
			// already been applied to the inner writer above; audit's SetRunInfo is
			// applied in the single-counterpart branch below, also before wrapping.
			var sc *summaryCapture
			if failIfUnmatched {
				sc = &summaryCapture{inner: w}
				w = sc
			}

			jobStart := time.Now()

			// Grouped passes need complete groups in memory before they can compare
			// summed amounts and group dates — this breaks the streaming memory contract.
			// Route to the batch path when the pair pipeline contains one.
			if containsBatchOnlyGroupedPass(pair.Passes) {
				if auditMode {
					return fmt.Errorf("--audit is not yet supported with grouped passes")
				}
				leftTxns, parseErr := engine.Parse(pair.Left, leftPath, leftSrc.Parser)
				if parseErr != nil {
					return fmt.Errorf("parse left source: %w", parseErr)
				}
				var batchResult *engine.Result
				if len(counterparts) == 1 {
					rightSrc, ok := cfg.Sources[counterparts[0]]
					if !ok {
						return configErrf("right source %q not found in config", counterparts[0])
					}
					rightPath, rErr := resolveFile(rightFile, rightSrc.FilePattern, configDir)
					if rErr != nil {
						return configErrf("right source: %v", rErr)
					}
					rightTxns, rParseErr := engine.Parse(counterparts[0], rightPath, rightSrc.Parser)
					if rParseErr != nil {
						return fmt.Errorf("parse right source: %w", rParseErr)
					}
					batchResult, parseErr = engine.Reconcile(pairName, pair.Left, counterparts[0], leftTxns, rightTxns, pair,
						engine.ReconcileOptions{
							LeftPolicy:  leftSrc.Parser.ResolvedDuplicatePolicy(),
							RightPolicy: rightSrc.Parser.ResolvedDuplicatePolicy(),
						})
					if parseErr != nil {
						return fmt.Errorf("reconciliation failed: %w", parseErr)
					}
				} else {
					if rightFile != "" {
						return configErr("--right-file is not supported with multiple counterparts (rights); each counterpart resolves its file via its own source's file_pattern")
					}
					cps := make([]engine.CounterpartInput, 0, len(counterparts))
					for _, name := range counterparts {
						src, ok := cfg.Sources[name]
						if !ok {
							return configErrf("right source %q not found in config", name)
						}
						cpPath, cpErr := resolveFile("", src.FilePattern, configDir)
						if cpErr != nil {
							return configErrf("counterpart %q: %v", name, cpErr)
						}
						cpTxns, cpParseErr := engine.Parse(name, cpPath, src.Parser)
						if cpParseErr != nil {
							return fmt.Errorf("parse counterpart %q: %w", name, cpParseErr)
						}
						cps = append(cps, engine.CounterpartInput{
							SourceName:   name,
							Transactions: cpTxns,
							ParserCfg:    src.Parser,
						})
					}
					batchResult, parseErr = engine.ReconcileMultiSource(pairName, pair.Left, leftTxns, cps, pair,
						engine.ReconcileOptions{LeftPolicy: leftSrc.Parser.ResolvedDuplicatePolicy()})
					if parseErr != nil {
						return fmt.Errorf("reconciliation failed: %w", parseErr)
					}
				}
				if drainErr := drainResultToWriter(w, batchResult); drainErr != nil {
					return drainErr
				}
				if commitErr := output.Commit(); commitErr != nil {
					return commitErr
				}
				if progress {
					fmt.Fprintf(os.Stderr, "progress: reconcile done pair=%s elapsed=%s\n", pairName, time.Since(jobStart).Round(time.Second))
				}
				if failIfUnmatched && (batchResult.Summary.UnmatchedLeft+batchResult.Summary.UnmatchedRight) > 0 {
					return &Error{Code: ErrCodeUnmatched, ErrCode: "unmatched", Msg: "reconciliation has unmatched rows"}
				}
				return nil
			}

			if len(counterparts) == 1 {
				// Single-counterpart path: byte-identical to pre-1-N-source behavior.
				// Never touches the multi-source code path below.
				rightSrc, ok := cfg.Sources[counterparts[0]]
				if !ok {
					return configErrf("right source %q not found in config", counterparts[0])
				}
				rightPath, err := resolveFile(rightFile, rightSrc.FilePattern, configDir)
				if err != nil {
					return configErrf("right source: %v", err)
				}

				if auditMode {
					// Check the inner writer directly: when summaryCapture wraps w, the
					// optional RunInfoSetter interface is no longer visible on w itself.
					auditTarget := w
					if sc != nil {
						auditTarget = sc.inner
					}
					setter, ok := auditTarget.(engine.RunInfoSetter)
					if !ok {
						return configErrf("--audit is only supported for --format=json, json-stream, or ndjson (got %q)", format)
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
						return configErrf("right source %q not found in config", name)
					}
					path, err := resolveFile("", src.FilePattern, configDir)
					if err != nil {
						return configErrf("counterpart %q: %v", name, err)
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
			if failIfUnmatched && sc != nil &&
				(sc.captured.UnmatchedLeft+sc.captured.UnmatchedRight) > 0 {
				return &Error{Code: ErrCodeUnmatched, ErrCode: "unmatched", Msg: "reconciliation has unmatched rows"}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&pairName, "pair", "", "Pair name to reconcile (required)")
	cmd.Flags().StringVarP(&outputPath, "out", "o", "-", "Output file path (use '-' for stdout)")
	cmd.Flags().StringVar(&leftFile, "left-file", "", "Explicit path to left source input file")
	cmd.Flags().StringVar(&rightFile, "right-file", "", "Explicit path to right source input file")
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
	cmd.Flags().BoolVar(&failIfUnmatched, "fail-if-unmatched", false,
		"Exit with code 3 if reconciliation completes with any unmatched rows on either side")

	return cmd
}

// summaryCapture wraps a ResultWriter and captures the final Summary written by
// WriteSummary. All optional interface setup (SetMeta, SetDeterministic,
// RunInfoSetter) must be applied to the inner writer before wrapping.
//
// GroupedEventWriter and SourceBreakdownWriter are forwarded to the inner
// writer when it supports them, so grouped events and per-source summaries
// are preserved when --fail-if-unmatched is combined with those formats.
type summaryCapture struct {
	inner    engine.ResultWriter
	captured engine.Summary
}

func (s *summaryCapture) WriteMatch(p engine.MatchedPair) error {
	return s.inner.WriteMatch(p)
}
func (s *summaryCapture) WriteAmountDiff(p engine.AmountDiffPair) error {
	return s.inner.WriteAmountDiff(p)
}
func (s *summaryCapture) WriteTimingDiff(p engine.TimingDiffPair) error {
	return s.inner.WriteTimingDiff(p)
}
func (s *summaryCapture) WriteUnmatched(tx engine.Transaction, side string) error {
	return s.inner.WriteUnmatched(tx, side)
}
func (s *summaryCapture) WriteDuplicate(g engine.DuplicateGroup) error {
	return s.inner.WriteDuplicate(g)
}
func (s *summaryCapture) WriteSummary(sum engine.Summary) error {
	s.captured = sum
	return s.inner.WriteSummary(sum)
}
func (s *summaryCapture) Flush() error { return s.inner.Flush() }

// WriteSourceSummary forwards to the inner writer when it implements SourceBreakdownWriter.
func (s *summaryCapture) WriteSourceSummary(sourceName string, sum engine.Summary) error {
	if sbw, ok := s.inner.(engine.SourceBreakdownWriter); ok {
		return sbw.WriteSourceSummary(sourceName, sum)
	}
	return nil
}

// GroupedEventWriter forwarding — delegates to inner when supported.
func (s *summaryCapture) WriteGroupedMatch(p engine.GroupedMatchedPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteGroupedMatch(p)
	}
	return nil
}
func (s *summaryCapture) WriteGroupedAmountDiff(p engine.GroupedAmountDiffPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteGroupedAmountDiff(p)
	}
	return nil
}
func (s *summaryCapture) WriteGroupedTimingDiff(p engine.GroupedTimingDiffPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteGroupedTimingDiff(p)
	}
	return nil
}
func (s *summaryCapture) WriteAmbiguousGroup(p engine.AmbiguousGroupPair) error {
	if gw, ok := s.inner.(engine.GroupedEventWriter); ok {
		return gw.WriteAmbiguousGroup(p)
	}
	return nil
}

// ManyToManyEventWriter forwarding — delegates to inner when supported.
func (s *summaryCapture) WriteManyToManyMatch(p engine.ManyToManyMatchedPair) error {
	if mw, ok := s.inner.(engine.ManyToManyEventWriter); ok {
		return mw.WriteManyToManyMatch(p)
	}
	return nil
}
func (s *summaryCapture) WriteManyToManyAmountDiff(p engine.ManyToManyAmountDiffPair) error {
	if mw, ok := s.inner.(engine.ManyToManyEventWriter); ok {
		return mw.WriteManyToManyAmountDiff(p)
	}
	return nil
}
func (s *summaryCapture) WriteManyToManyTimingDiff(p engine.ManyToManyTimingDiffPair) error {
	if mw, ok := s.inner.(engine.ManyToManyEventWriter); ok {
		return mw.WriteManyToManyTimingDiff(p)
	}
	return nil
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
	f, err := os.Open(path) // #nosec G304 -- path was created by this process for verified audit output.
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

// containsBatchOnlyGroupedPass reports whether any pass requires grouped batch matching.
// engine.containsPass is unexported, so the CLI keeps its own copy.
func containsBatchOnlyGroupedPass(passes []config.PassConfig) bool {
	for _, p := range passes {
		if p.Type == config.PassTypeOneToMany || p.Type == config.PassTypeManyToMany {
			return true
		}
	}
	return false
}

// drainResultToWriter replays all events from a batch Result through a ResultWriter.
// Writers that implement GroupedEventWriter or ManyToManyEventWriter receive grouped
// events; for writers that do not (csv, table), a single warning is printed to stderr
// and those events are skipped — all standard events are still emitted.
// ambiguous_groups require manual reconciliation and are never silently dropped from
// the Summary that is always written last.
func drainResultToWriter(w engine.ResultWriter, res *engine.Result) error {
	for _, mp := range res.Matched {
		if err := w.WriteMatch(mp); err != nil {
			return err
		}
	}
	for _, ap := range res.AmountDiff {
		if err := w.WriteAmountDiff(ap); err != nil {
			return err
		}
	}
	for _, tp := range res.TimingDiff {
		if err := w.WriteTimingDiff(tp); err != nil {
			return err
		}
	}
	for _, tx := range res.UnmatchedLeft {
		if err := w.WriteUnmatched(tx, "left"); err != nil {
			return err
		}
	}
	for _, tx := range res.UnmatchedRight {
		if err := w.WriteUnmatched(tx, "right"); err != nil {
			return err
		}
	}
	for _, dg := range res.Duplicates {
		if err := w.WriteDuplicate(dg); err != nil {
			return err
		}
	}
	hasGrouped := len(res.GroupedMatched)+len(res.GroupedAmountDiff)+
		len(res.GroupedTimingDiff)+len(res.AmbiguousGroups) > 0
	if gw, ok := w.(engine.GroupedEventWriter); ok {
		for _, gm := range res.GroupedMatched {
			if err := gw.WriteGroupedMatch(gm); err != nil {
				return err
			}
		}
		for _, gd := range res.GroupedAmountDiff {
			if err := gw.WriteGroupedAmountDiff(gd); err != nil {
				return err
			}
		}
		for _, gt := range res.GroupedTimingDiff {
			if err := gw.WriteGroupedTimingDiff(gt); err != nil {
				return err
			}
		}
		for _, ag := range res.AmbiguousGroups {
			if err := gw.WriteAmbiguousGroup(ag); err != nil {
				return err
			}
		}
	} else if hasGrouped {
		fmt.Fprintln(os.Stderr,
			"warning: current --format does not support grouped or ambiguous match events; "+
				"use --format=json or --format=ndjson to capture all one_to_many output")
	}
	hasManyToMany := len(res.ManyToManyMatched)+len(res.ManyToManyAmountDiff)+
		len(res.ManyToManyTimingDiff) > 0
	if mw, ok := w.(engine.ManyToManyEventWriter); ok {
		for _, mm := range res.ManyToManyMatched {
			if err := mw.WriteManyToManyMatch(mm); err != nil {
				return err
			}
		}
		for _, md := range res.ManyToManyAmountDiff {
			if err := mw.WriteManyToManyAmountDiff(md); err != nil {
				return err
			}
		}
		for _, mt := range res.ManyToManyTimingDiff {
			if err := mw.WriteManyToManyTimingDiff(mt); err != nil {
				return err
			}
		}
	} else if hasManyToMany {
		fmt.Fprintln(os.Stderr,
			"warning: current --format does not support many_to_many match events; "+
				"use --format=json or --format=ndjson to capture all many_to_many output")
	}
	// Emit warnings to stderr — mirrors what the streaming path does via cc.Warnings().
	for _, warning := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}
	if sbw, ok := w.(engine.SourceBreakdownWriter); ok {
		for name, summary := range res.BySource {
			if err := sbw.WriteSourceSummary(name, summary); err != nil {
				return err
			}
		}
	}
	if err := w.WriteSummary(res.Summary); err != nil {
		return err
	}
	return w.Flush()
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
