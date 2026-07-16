package cli

import (
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
	var partitionWorkers int
	var partitionQueueCapacity int
	var partitionMaxChunkMB int64
	var auditMode bool
	var auditFixedTimestamp string
	var deterministic bool
	var progress bool
	var progressEvery int
	var heartbeatEvery string
	var progressOut string
	var failIfUnmatched bool
	var resultModeFlag string

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
			ctx := cmd.Context()
			if pairName == "" {
				return configErr("--pair is required")
			}
			if progressEvery <= 0 {
				return configErr("--progress-every must be greater than zero")
			}
			if partitionWorkers < 0 {
				return configErr("--partition-workers must be >= 0 (0 = serial)")
			}
			if partitionQueueCapacity < 0 {
				return configErr("--partition-queue-capacity must be >= 0 (0 = derived from workers)")
			}
			if partitionMaxChunkMB < 0 || partitionMaxChunkMB > 1<<40 {
				return configErr("--partition-max-chunk-mb must be between 0 and 1099511627776")
			}
			partitionMaxChunkBytes := partitionMaxChunkMB * (1 << 20)
			switch config.ResultMode(resultModeFlag) {
			case "", config.ResultModeAll, config.ResultModeExceptionsOnly, config.ResultModeSummaryOnly:
				// valid
			default:
				return configErrf("--result-mode: must be one of [all, exceptions_only, summary_only] (got %q)", resultModeFlag)
			}
			heartbeatInterval, err := time.ParseDuration(heartbeatEvery)
			if err != nil || heartbeatInterval <= 0 {
				return configErr("--heartbeat-every must be a positive duration (for example 30s)")
			}
			telemetryEnabled := progress || progressOut != ""

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
			rightPaths := make(map[string]string, len(counterparts))
			for _, name := range counterparts {
				src, ok := cfg.Sources[name]
				if !ok {
					return configErrf("right source %q not found in config", name)
				}
				explicitRight := ""
				if len(counterparts) == 1 {
					explicitRight = rightFile
				} else if rightFile != "" {
					return configErr("--right-file is not supported with multiple counterparts (rights); each counterpart resolves its file via its own source's file_pattern")
				}
				path, err := resolveFile(explicitRight, src.FilePattern, configDir)
				if err != nil {
					return configErrf("right source %q: %v", name, err)
				}
				rightPaths[name] = path
			}
			if progressOut != "" {
				inputs := []string{cfgAbs, leftPath}
				for _, name := range counterparts {
					inputs = append(inputs, rightPaths[name])
				}
				if err := validateProgressOutput(progressOut, outputPath, inputs...); err != nil {
					return configErr(err.Error())
				}
			}

			output, err := openReconcileOutput(outputPath, auditMode)
			if err != nil {
				return err
			}
			defer output.Cleanup()

			telemetry, closeTelemetry, err := openTelemetry(progress, progressOut, progressEvery, heartbeatInterval)
			if err != nil {
				return err
			}
			defer closeTelemetry()
			if !telemetryEnabled {
				telemetry = engine.TelemetryOptions{}
			} else {
				telemetry.RunID = engine.NewTelemetryRunID()
			}

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

			// Resolve effective result mode: explicit CLI flag > pair config > "all".
			effectiveMode := config.ResultMode(resultModeFlag)
			if effectiveMode == "" {
				effectiveMode = pair.ResultMode
			}
			// Always wrap: adds ResultMode/RunID to Summary and filters events by mode.
			w = engine.WrapWithResultModeAndWarnings(w, effectiveMode, telemetry.RunID, stderrWarningObserver{})

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
			var multiDecisions []indexSelectionDecision
			if len(counterparts) > 1 {
				multiDecisions, err = chooseMultiIndexBackends(cfg.Index, leftPath, leftSrc.Parser, counterparts, rightPaths, cfg.Sources, pair)
				if err != nil {
					return fmt.Errorf("select index backends: %w", err)
				}
				allPartitioned := len(multiDecisions) == len(counterparts)
				for _, decision := range multiDecisions {
					if !decision.Partitioned {
						allPartitioned = false
						break
					}
				}
				if allPartitioned {
					if err := reportIndexSelection(w, multiDecisions[0].Selection); err != nil {
						return err
					}
					partitionedCounterparts := make([]engine.PartitionedCounterpartInput, 0, len(counterparts))
					for _, name := range counterparts {
						src := cfg.Sources[name]
						partitionedCounterparts = append(partitionedCounterparts, engine.PartitionedCounterpartInput{
							SourceName: name,
							RightPath:  rightPaths[name],
							ParserCfg:  src.Parser,
						})
					}
					options := engine.PartitionedOptions{
						MaxTokenBuffer: maxTokenBuffer,
						Partitions:     multiDecisions[0].Selection.PartitionCount,
						SpillDir:       cfg.Index.SpillDir,
						Workers:        partitionWorkers,
						QueueCapacity:  partitionQueueCapacity,
						MaxChunkBytes:  partitionMaxChunkBytes,
					}
					var runErr error
					if telemetryEnabled {
						runErr = engine.ReconcilePartitionedMultiSourceWithOptionsAndTelemetry(ctx, pairName, pair.Left, leftPath, leftSrc.Parser, partitionedCounterparts, pair, w, options, telemetry)
					} else {
						runErr = engine.ReconcilePartitionedMultiSourceWithOptions(ctx, pairName, pair.Left, leftPath, leftSrc.Parser, partitionedCounterparts, pair, w, options)
					}
					if runErr != nil {
						return fmt.Errorf("reconciliation failed: %w", runErr)
					}
					if err := output.Commit(); err != nil {
						return err
					}
					if progress {
						fmt.Fprintf(os.Stderr, "progress: reconcile done pair=%s elapsed=%s\n", pairName, time.Since(jobStart).Round(time.Second))
					}
					if failIfUnmatched && sc != nil && (sc.captured.UnmatchedLeft+sc.captured.UnmatchedRight) > 0 {
						return &Error{Code: ErrCodeUnmatched, ErrCode: "unmatched", Msg: "reconciliation has unmatched rows"}
					}
					return nil
				}
			}

			// Grouped passes with multiple counterparts always go to the batch path.
			// Single-counterpart grouped pairs may use the partitioned backend when
			// chooseIndexBackend selects it; otherwise they fall through to batch below.
			if containsBatchOnlyGroupedPass(pair.Passes) && len(counterparts) != 1 {
				if auditMode {
					return fmt.Errorf("--audit is not yet supported with grouped passes")
				}
				leftTxns, parseErr := engine.ParseWithTelemetry(ctx, pair.Left, leftPath, leftSrc.Parser, strings.Join(counterparts, ","), telemetry)
				if parseErr != nil {
					return fmt.Errorf("parse left source: %w", parseErr)
				}
				cps := make([]engine.CounterpartInput, 0, len(counterparts))
				for _, name := range counterparts {
					src := cfg.Sources[name]
					cpPath := rightPaths[name]
					cpTxns, cpParseErr := engine.ParseWithTelemetry(ctx, name, cpPath, src.Parser, pair.Left, telemetry)
					if cpParseErr != nil {
						return fmt.Errorf("parse counterpart %q: %w", name, cpParseErr)
					}
					cps = append(cps, engine.CounterpartInput{
						SourceName:   name,
						Transactions: cpTxns,
						ParserCfg:    src.Parser,
					})
				}
				batchResult, parseErr := engine.ReconcileMultiSourceWithTelemetry(pairName, pair.Left, leftTxns, cps, pair, telemetry,
					engine.ReconcileOptions{LeftPolicy: leftSrc.Parser.ResolvedDuplicatePolicy()})
				if parseErr != nil {
					return fmt.Errorf("reconciliation failed: %w", parseErr)
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
				rightSrc := cfg.Sources[counterparts[0]]
				rightPath := rightPaths[counterparts[0]]
				decision, err := chooseIndexBackend(cfg.Index, leftPath, rightPath, leftSrc.Parser, rightSrc.Parser, pair, 1)
				if err != nil {
					return fmt.Errorf("select index backend: %w", err)
				}
				if decision.Partitioned && auditMode {
					return configErr("--audit is not supported with the partitioned backend")
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
				if err := reportIndexSelection(w, decision.Selection); err != nil {
					return err
				}

				if decision.Partitioned {
					options := engine.PartitionedOptions{
						MaxTokenBuffer: maxTokenBuffer,
						Partitions:     decision.Selection.PartitionCount,
						SpillDir:       cfg.Index.SpillDir,
						Workers:        partitionWorkers,
						QueueCapacity:  partitionQueueCapacity,
						MaxChunkBytes:  partitionMaxChunkBytes,
					}
					var runErr error
					if telemetryEnabled {
						runErr = engine.ReconcilePartitionedWithOptionsAndTelemetry(ctx, pairName, pair.Left, counterparts[0], leftPath, rightPath, leftSrc.Parser, rightSrc.Parser, pair, w, options, telemetry)
					} else {
						runErr = engine.ReconcilePartitionedWithOptions(ctx, pairName, pair.Left, counterparts[0], leftPath, rightPath, leftSrc.Parser, rightSrc.Parser, pair, w, options)
					}
					if runErr != nil {
						return fmt.Errorf("reconciliation failed: %w", runErr)
					}
					if err := output.Commit(); err != nil {
						return err
					}
					if progress {
						fmt.Fprintf(os.Stderr, "progress: reconcile done pair=%s elapsed=%s\n", pairName, time.Since(jobStart).Round(time.Second))
					}
					if sc != nil && (sc.captured.UnmatchedLeft+sc.captured.UnmatchedRight) > 0 && failIfUnmatched {
						return &Error{Code: ErrCodeUnmatched, ErrCode: "unmatched", Msg: "reconciliation has unmatched rows"}
					}
					return nil
				}

				// Grouped passes that did not qualify for the partitioned backend fall
				// back to the in-memory batch path (streaming cannot handle groups).
				if containsBatchOnlyGroupedPass(pair.Passes) {
					if auditMode {
						return fmt.Errorf("--audit is not yet supported with grouped passes")
					}
					leftTxns, parseErr := engine.ParseWithTelemetry(ctx, pair.Left, leftPath, leftSrc.Parser, counterparts[0], telemetry)
					if parseErr != nil {
						return fmt.Errorf("parse left source: %w", parseErr)
					}
					rightTxns, rParseErr := engine.ParseWithTelemetry(ctx, counterparts[0], rightPath, rightSrc.Parser, pair.Left, telemetry)
					if rParseErr != nil {
						return fmt.Errorf("parse right source: %w", rParseErr)
					}
					batchResult, batchErr := engine.ReconcileWithTelemetry(pairName, pair.Left, counterparts[0], leftTxns, rightTxns, pair, telemetry,
						engine.ReconcileOptions{
							LeftPolicy:  leftSrc.Parser.ResolvedDuplicatePolicy(),
							RightPolicy: rightSrc.Parser.ResolvedDuplicatePolicy(),
						})
					if batchErr != nil {
						return fmt.Errorf("reconciliation failed: %w", batchErr)
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

				idx, err := openSelectedIndex(cfg.Index, decision)
				if err != nil {
					return fmt.Errorf("init right index: %w", err)
				}
				defer func() {
					if closeErr := idx.Close(); closeErr != nil {
						fmt.Fprintf(os.Stderr, "warning: close index backend: %v\n", closeErr)
					}
				}()
				run := func() error {
					if telemetryEnabled {
						return engine.ReconcileStreamingWithTelemetry(
							ctx, pairName, pair.Left, counterparts[0], leftPath, rightPath,
							leftSrc.Parser, rightSrc.Parser, pair, idx, w, maxTokenBuffer, telemetry,
						)
					}
					return engine.ReconcileStreaming(
						ctx,
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
				cps := make([]engine.CounterpartStream, 0, len(counterparts))
				var indexes []engine.RightIndex
				defer func() {
					for _, idx := range indexes {
						if closeErr := idx.Close(); closeErr != nil {
							fmt.Fprintf(os.Stderr, "warning: close index backend: %v\n", closeErr)
						}
					}
				}()

				for i, name := range counterparts {
					src := cfg.Sources[name]
					path := rightPaths[name]
					idx, err := openSelectedIndex(cfg.Index, multiDecisions[i])
					if err != nil {
						return fmt.Errorf("init index for counterpart %q: %w", name, err)
					}
					fmt.Fprintf(os.Stderr, "index: counterpart=%s %s\n", name, multiDecisions[i].Selection.String())
					indexes = append(indexes, idx)
					cps = append(cps, engine.CounterpartStream{
						SourceName: name,
						RightPath:  path,
						RightCfg:   src.Parser,
						Index:      idx,
					})
				}

				if err := engine.ReconcileStreamingMultiSourceWithTelemetry(
					ctx,
					pairName,
					pair.Left,
					leftPath,
					leftSrc.Parser,
					cps,
					pair,
					w,
					maxTokenBuffer,
					telemetry,
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
	cmd.Flags().IntVar(&partitionWorkers, "partition-workers", 0,
		"Concurrent partition workers for the partitioned backend (0 = serial; 1 = serial)")
	cmd.Flags().IntVar(&partitionQueueCapacity, "partition-queue-capacity", 0,
		"Maximum completed partition descriptors waiting for the final writer (0 = derived from workers)")
	cmd.Flags().Int64Var(&partitionMaxChunkMB, "partition-max-chunk-mb", 0,
		"Maximum size of one disk-backed partition result chunk in MiB (0 = unlimited)")
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
	cmd.Flags().StringVar(&heartbeatEvery, "heartbeat-every", "30s",
		"Wall-clock telemetry heartbeat interval (for example 30s)")
	cmd.Flags().StringVar(&progressOut, "progress-out", "",
		"Write live telemetry events as NDJSON to this path (must differ from --out)")
	cmd.Flags().BoolVar(&failIfUnmatched, "fail-if-unmatched", false,
		"Exit with code 3 if reconciliation completes with any unmatched rows on either side")
	cmd.Flags().StringVar(&resultModeFlag, "result-mode", "",
		`Emission mode: all (default), exceptions_only (suppress clean matches), summary_only (suppress all item events).
Overrides result_mode in the pair config when set.`)

	return cmd
}
