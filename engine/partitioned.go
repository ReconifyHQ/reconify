package engine

// This file implements an opt-in, bounded-memory hash join for large CSV
// reconciliations. Both inputs are hash-partitioned by their reference key and
// reconciled one partition at a time with the normal streaming engine.

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/reconifyhq/reconify/config"
)

// PartitionedOptions controls temporary storage and partition sizing.
type PartitionedOptions struct {
	MaxTokenBuffer int
	Partitions     int
	SpillDir       string
}

// ReconcilePartitioned reconciles supported passes using bounded memory. Grouped
// passes run as complete batch operations within one partition; token passes
// remain unsupported because their fallback buffer can span partitions.
func ReconcilePartitioned(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, maxTokenBuffer, partitions int) error {
	return reconcilePartitioned(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, w, PartitionedOptions{
		MaxTokenBuffer: maxTokenBuffer,
		Partitions:     partitions,
	}, nil)
}

// ReconcilePartitionedWithOptions runs the bounded-memory backend using the
// configured spill location when provided.
func ReconcilePartitionedWithOptions(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, options PartitionedOptions) error {
	return reconcilePartitioned(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, w, options, nil)
}

// ReconcilePartitionedWithTelemetry emits partitioning and per-partition
// lifecycle records without changing bounded-memory reconciliation behavior.
func ReconcilePartitionedWithTelemetry(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, maxTokenBuffer, partitions int, telemetry TelemetryOptions) error {
	return ReconcilePartitionedWithOptionsAndTelemetry(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, w, PartitionedOptions{
		MaxTokenBuffer: maxTokenBuffer,
		Partitions:     partitions,
	}, telemetry)
}

// ReconcilePartitionedWithOptionsAndTelemetry combines configured spill storage
// with the lifecycle telemetry API.
func ReconcilePartitionedWithOptionsAndTelemetry(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, options PartitionedOptions, telemetry TelemetryOptions) error {
	reporter := newTelemetryReporter(telemetry)
	if reporter != nil {
		defer reporter.close()
	}
	err := reconcilePartitioned(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, w, options, reporter)
	if err != nil {
		reporter.fail(0)
	}
	return err
}

func reconcilePartitioned(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, options PartitionedOptions, reporter *telemetryReporter) error {
	partitions := options.Partitions
	if partitions < 2 {
		partitions = adaptivePartitionCountFromFiles(leftPath, rightPath)
	}
	reporter.start("partitioning", leftSource, rightSource, nil)
	leftKeyCol, rightKeyCol, ok, reason := PartitionKeyColumns(pair, leftCfg, rightCfg)
	if !ok {
		return fmt.Errorf("partitioned backend: %s", reason)
	}
	if safe, reason := PartitionDuplicateSafe(pair, leftCfg, rightCfg); !safe {
		return fmt.Errorf("partitioned backend: %s", reason)
	}
	if err := validatePartitionCurrencies(ctx, leftSource, leftPath, leftCfg, rightSource, rightPath, rightCfg); err != nil {
		return err
	}

	isGrouped := containsGroupedPass(pair.Passes)
	baseDir := options.SpillDir
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return fmt.Errorf("create partition spill base directory: %w", err)
	}
	dir, err := os.MkdirTemp(baseDir, "reconify-partitions-*")
	if err != nil {
		return fmt.Errorf("create partition directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var leftParts, rightParts groupedPartitionFiles
	var leftDuplicateParts, rightDuplicateParts groupedPartitionFiles
	var leftRows, rightRows int
	var leftGrouped, rightGrouped groupedPartitionFiles
	if isGrouped {
		leftGrouped, err = partitionCSVWithSidecars(ctx, leftPath, leftKeyCol, filepath.Join(dir, "left"), partitions, reporter.progress)
		if err != nil {
			return fmt.Errorf("partition left source: %w", err)
		}
		leftRows = leftGrouped.count
		leftParts = leftGrouped
		rightGrouped, err = partitionCSVWithSidecars(ctx, rightPath, rightKeyCol, filepath.Join(dir, "right"), partitions, func(rows int) {
			reporter.progress(leftRows + rows)
		})
		if err != nil {
			return fmt.Errorf("partition right source: %w", err)
		}
		rightRows = rightGrouped.count
		rightParts = rightGrouped
	} else {
		leftParts, leftDuplicateParts, err = partitionCSVWithDuplicateSpill(ctx, leftPath, leftKeyCol, duplicateSpillColumn(leftCfg), filepath.Join(dir, "left"), partitions, reporter.progress)
		if err != nil {
			return fmt.Errorf("partition left source: %w", err)
		}
		leftRows = leftParts.count
		rightParts, rightDuplicateParts, err = partitionCSVWithDuplicateSpill(ctx, rightPath, rightKeyCol, duplicateSpillColumn(rightCfg), filepath.Join(dir, "right"), partitions, func(rows int) {
			reporter.progress(leftRows + rows)
		})
		if err != nil {
			return fmt.Errorf("partition right source: %w", err)
		}
		rightRows = rightParts.count
	}
	reporter.complete(leftRows + rightRows)
	var leftDisposition, rightDisposition *partitionedDuplicateDisposition
	if !isGrouped {
		_, _, leftSelector, _, _ := partitionKeyColumns(pair, leftCfg, rightCfg)
		leftDisposition, err = buildPartitionedDuplicateDisposition(ctx, leftSource, leftCfg, leftSelector, leftDuplicateParts, filepath.Join(dir, "left-duplicate"), partitions)
		if err != nil {
			return fmt.Errorf("prepare left duplicate disposition: %w", err)
		}
		rightDisposition, err = buildPartitionedDuplicateDisposition(ctx, rightSource, rightCfg, leftSelector, rightDuplicateParts, filepath.Join(dir, "right-duplicate"), partitions)
		if err != nil {
			return fmt.Errorf("prepare right duplicate disposition: %w", err)
		}
		defer leftDisposition.Cleanup()
		defer rightDisposition.Cleanup()
		if err := rightDisposition.Emit(w, ctx); err != nil {
			return fmt.Errorf("emit right duplicates: %w", err)
		}
	}
	agg := &partitionSummaryWriter{ResultWriter: w}
	if isGrouped {
		// Grouped passes require whole-group state, but not whole-partition state.
		// Sort each partition externally by the configured group key and merge one
		// key-group at a time so a skewed distribution does not retain every group
		// in memory simultaneously.
		for i := 0; i < partitions; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			leftData, leftRowsPath, err := sortGroupedPartition(ctx, leftGrouped.data[i], leftGrouped.rows[i], filepath.Join(dir, fmt.Sprintf("sorted-left-%03d", i)), leftKeyCol)
			if err != nil {
				return fmt.Errorf("sort left partition %d: %w", i, err)
			}
			if err := removeGroupedPartitionInputs(leftGrouped.data[i], leftGrouped.rows[i]); err != nil {
				return fmt.Errorf("remove left partition %d staging files: %w", i, err)
			}
			rightData, rightRowsPath, err := sortGroupedPartition(ctx, rightGrouped.data[i], rightGrouped.rows[i], filepath.Join(dir, fmt.Sprintf("sorted-right-%03d", i)), rightKeyCol)
			if err != nil {
				return fmt.Errorf("sort right partition %d: %w", i, err)
			}
			if err := removeGroupedPartitionInputs(rightGrouped.data[i], rightGrouped.rows[i]); err != nil {
				return fmt.Errorf("remove right partition %d staging files: %w", i, err)
			}
			if err := reconcileGroupedPartition(ctx, pairName, leftSource, rightSource, leftData, leftRowsPath, rightData, rightRowsPath, leftCfg, rightCfg, pair, agg); err != nil {
				return fmt.Errorf("reconcile partition %d: %w", i, err)
			}
		}
	} else {
		for i := 0; i < partitions; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			leftOriginalRows, err := readPartitionRows(leftParts.rows[i])
			if err != nil {
				return fmt.Errorf("read left partition %d rows: %w", i, err)
			}
			rightOriginalRows, err := readPartitionRows(rightParts.rows[i])
			if err != nil {
				return fmt.Errorf("read right partition %d rows: %w", i, err)
			}
			leftRepresentatives, err := leftDisposition.Representatives(i)
			if err != nil {
				return fmt.Errorf("read left representatives for partition %d: %w", i, err)
			}
			rightRepresentatives, err := rightDisposition.Representatives(i)
			if err != nil {
				return fmt.Errorf("read right representatives for partition %d: %w", i, err)
			}
			idx := NewMemoryIndex()
			err = reconcileStreamingWithOptions(ctx, pairName, leftSource, rightSource, leftParts.data[i], rightParts.data[i], partitionedPolicyConfig(leftCfg), partitionedPolicyConfig(rightCfg), pair, idx, agg, options.MaxTokenBuffer, reporter, streamingDuplicateOptions{
				rightRepresentativeRows:    rightRepresentatives,
				leftRepresentativeRows:     leftRepresentatives,
				rightPartitionOriginalRows: rightOriginalRows,
				leftPartitionOriginalRows:  leftOriginalRows,
			})
			closeErr := idx.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	if !isGrouped {
		if err := leftDisposition.Emit(w, ctx); err != nil {
			return fmt.Errorf("emit left duplicates: %w", err)
		}
		if leftDisposition != nil {
			agg.summary.DuplicateCount += leftDisposition.duplicateCount
		}
		if rightDisposition != nil {
			agg.summary.DuplicateCount += rightDisposition.duplicateCount
		}
	}
	reporter.start("finalization", leftSource, rightSource, nil)
	reporter.progress(leftRows + rightRows)
	if err := agg.FlushSummary(); err != nil {
		return err
	}
	reporter.complete(0)
	return nil
}

func validatePartitionCurrencies(ctx context.Context, leftSource, leftPath string, leftCfg config.ParserCfg, rightSource, rightPath string, rightCfg config.ParserCfg) error {
	cc := currencyTracker{}
	if err := ParseEach(ctx, leftSource, leftPath, leftCfg, func(tx Transaction, _ int) error {
		return cc.Observe(leftSource, tx)
	}); err != nil {
		return fmt.Errorf("validate left currencies: %w", err)
	}
	if err := ParseEach(ctx, rightSource, rightPath, rightCfg, func(tx Transaction, _ int) error {
		return cc.Observe(rightSource, tx)
	}); err != nil {
		return fmt.Errorf("validate right currencies: %w", err)
	}
	return nil
}

type partitionSummaryWriter struct {
	ResultWriter
	summary       Summary
	seen          bool
	warnedGrouped bool
	warnedMany    bool
}

func (w *partitionSummaryWriter) WriteSummary(s Summary) error {
	w.summary = addSummaries(w.summary, s)
	w.seen = true
	return nil
}

func (w *partitionSummaryWriter) Flush() error { return nil }

func (w *partitionSummaryWriter) warnGroupedEventsUnsupported() {
	if w.warnedGrouped {
		return
	}
	w.warnedGrouped = true
	fmt.Fprintln(os.Stderr,
		"warning: current output format does not support grouped or ambiguous match events; "+
			"use --format=json or --format=ndjson to capture all one_to_many output")
}

func (w *partitionSummaryWriter) warnManyToManyEventsUnsupported() {
	if w.warnedMany {
		return
	}
	w.warnedMany = true
	fmt.Fprintln(os.Stderr,
		"warning: current output format does not support many_to_many match events; "+
			"use --format=json or --format=ndjson to capture all many_to_many output")
}

// GroupedEventWriter forwarding — prevents the embedded ResultWriter interface
// from hiding the inner concrete writer's optional methods.
func (w *partitionSummaryWriter) WriteGroupedMatch(p GroupedMatchedPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteGroupedMatch(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteGroupedAmountDiff(p GroupedAmountDiffPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteGroupedAmountDiff(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteGroupedTimingDiff(p GroupedTimingDiffPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteGroupedTimingDiff(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteAmbiguousGroup(p AmbiguousGroupPair) error {
	if gw, ok := w.ResultWriter.(GroupedEventWriter); ok {
		return gw.WriteAmbiguousGroup(p)
	}
	w.warnGroupedEventsUnsupported()
	return nil
}

// ManyToManyEventWriter forwarding.
func (w *partitionSummaryWriter) WriteManyToManyMatch(p ManyToManyMatchedPair) error {
	if mw, ok := w.ResultWriter.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyMatch(p)
	}
	w.warnManyToManyEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteManyToManyAmountDiff(p ManyToManyAmountDiffPair) error {
	if mw, ok := w.ResultWriter.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyAmountDiff(p)
	}
	w.warnManyToManyEventsUnsupported()
	return nil
}
func (w *partitionSummaryWriter) WriteManyToManyTimingDiff(p ManyToManyTimingDiffPair) error {
	if mw, ok := w.ResultWriter.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyTimingDiff(p)
	}
	w.warnManyToManyEventsUnsupported()
	return nil
}

// SourceBreakdownWriter forwarding.
func (w *partitionSummaryWriter) WriteSourceSummary(sourceName string, sum Summary) error {
	if sbw, ok := w.ResultWriter.(SourceBreakdownWriter); ok {
		return sbw.WriteSourceSummary(sourceName, sum)
	}
	return nil
}

func (w *partitionSummaryWriter) FlushSummary() error {
	if !w.seen {
		return w.ResultWriter.WriteSummary(Summary{})
	}
	if err := w.ResultWriter.WriteSummary(w.summary); err != nil {
		return err
	}
	return w.ResultWriter.Flush()
}

func addSummaries(a, b Summary) Summary {
	if a.Currency == "" && b.Currency != "" {
		a.Currency = b.Currency
	}
	if a.ResultMode == "" && b.ResultMode != "" {
		a.ResultMode = b.ResultMode
	}
	if a.RunID == "" && b.RunID != "" {
		a.RunID = b.RunID
	}
	a.TotalLeft += b.TotalLeft
	a.TotalRight += b.TotalRight
	a.MatchedCount += b.MatchedCount
	a.UnmatchedLeft += b.UnmatchedLeft
	a.UnmatchedRight += b.UnmatchedRight
	a.AmountDiffCount += b.AmountDiffCount
	a.TimingDiffCount += b.TimingDiffCount
	a.DuplicateCount += b.DuplicateCount
	a.GroupedMatchedCount += b.GroupedMatchedCount
	a.GroupedAmountDiffCount += b.GroupedAmountDiffCount
	a.GroupedTimingDiffCount += b.GroupedTimingDiffCount
	a.ManyToManyMatchedCount += b.ManyToManyMatchedCount
	a.ManyToManyAmountDiffCount += b.ManyToManyAmountDiffCount
	a.ManyToManyTimingDiffCount += b.ManyToManyTimingDiffCount
	a.AmbiguousGroupCount += b.AmbiguousGroupCount
	a.MatchedAmountLeft += b.MatchedAmountLeft
	a.MatchedAmountRight += b.MatchedAmountRight
	a.UnmatchedAmountLeft += b.UnmatchedAmountLeft
	a.UnmatchedAmountRight += b.UnmatchedAmountRight
	a.AmountDiffTotal += b.AmountDiffTotal
	a.AmbiguousAmountLeft += b.AmbiguousAmountLeft
	a.AmbiguousAmountRight += b.AmbiguousAmountRight
	a.TotalDiscrepancy += b.TotalDiscrepancy
	denom := a.TotalLeft
	if a.TotalRight > denom {
		denom = a.TotalRight
	}
	if denom > 0 {
		a.MatchRatePct = math.Round(float64(a.MatchedCount)/float64(denom)*10000) / 100
		reconciledCount := a.MatchedCount + a.AmountDiffCount + a.TimingDiffCount +
			a.GroupedMatchedCount + a.GroupedAmountDiffCount + a.GroupedTimingDiffCount +
			a.ManyToManyMatchedCount + a.ManyToManyAmountDiffCount + a.ManyToManyTimingDiffCount
		a.ReconciledRatePct = math.Round(float64(reconciledCount)/float64(denom)*10000) / 100
	}
	return a
}
