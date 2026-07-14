package engine

// This file implements an opt-in, bounded-memory hash join for large CSV
// reconciliations. Both inputs are hash-partitioned by their reference key and
// reconciled one partition at a time with the normal streaming engine.

import (
	"context"
	"encoding/csv"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

// PartitionedOptions controls temporary storage and partition sizing.
type PartitionedOptions struct {
	MaxTokenBuffer int
	Partitions     int
	SpillDir       string
}

// ReconcilePartitioned reconciles exact/reference passes using bounded memory.
// Grouped and token passes are intentionally rejected because their semantics
// require cross-partition state.
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
	return reconcilePartitioned(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, w, options, reporter)
}

func reconcilePartitioned(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, options PartitionedOptions, reporter *telemetryReporter) error {
	partitions := options.Partitions
	if partitions < 2 {
		partitions = 32
	}
	reporter.start("partitioning", leftSource, rightSource, nil)
	if len(pair.Passes) > 0 {
		for _, pass := range pair.Passes {
			if pass.Type != config.PassTypeReferenceOneToOne {
				return fmt.Errorf("partitioned backend supports reference_one_to_one only (got %q)", pass.Type)
			}
		}
	}
	if pair.NameMode == "tokens" {
		return fmt.Errorf("partitioned backend supports reference matching only")
	}
	if leftCfg.RefCol == "" || rightCfg.RefCol == "" {
		return fmt.Errorf("partitioned backend requires ref_col on both sources")
	}

	// Duplicate groups are keyed independently from the reference used for
	// partition routing. Discover flagged groups from the complete inputs before
	// partitioning so a group whose rows span multiple reference partitions has
	// the same semantics as the regular streaming backend.
	var rightDuplicateKeys map[string]bool
	var rightDuplicateGroups []DuplicateGroup
	var rightRepresentativeRows map[string]int
	if rightCfg.ResolvedDuplicatePolicy() == config.DuplicatePolicyFlag {
		var err error
		rightDuplicateKeys, rightDuplicateGroups, err = collectPartitionDuplicateGroups(ctx, rightSource, rightPath, rightCfg)
		if err != nil {
			return fmt.Errorf("collect right duplicates: %w", err)
		}
	} else if policy := rightCfg.ResolvedDuplicatePolicy(); policy == config.DuplicatePolicyMerge || policy == config.DuplicatePolicyLatest {
		var err error
		rightRepresentativeRows, err = collectPartitionDuplicateRepresentatives(ctx, rightSource, rightPath, rightCfg, policy == config.DuplicatePolicyLatest)
		if err != nil {
			return fmt.Errorf("collect right duplicate representatives: %w", err)
		}
	}
	var leftDuplicateKeys map[string]bool
	var leftDuplicateGroups []DuplicateGroup
	var leftRepresentativeRows map[string]int
	if leftCfg.ResolvedDuplicatePolicy() == config.DuplicatePolicyFlag {
		var err error
		leftDuplicateKeys, leftDuplicateGroups, err = collectPartitionDuplicateGroups(ctx, leftSource, leftPath, leftCfg)
		if err != nil {
			return fmt.Errorf("collect left duplicates: %w", err)
		}
	} else if policy := leftCfg.ResolvedDuplicatePolicy(); policy == config.DuplicatePolicyMerge || policy == config.DuplicatePolicyLatest {
		var err error
		leftRepresentativeRows, err = collectPartitionDuplicateRepresentatives(ctx, leftSource, leftPath, leftCfg, policy == config.DuplicatePolicyLatest)
		if err != nil {
			return fmt.Errorf("collect left duplicate representatives: %w", err)
		}
	}
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
	leftParts, leftRows, leftOriginalRows, err := partitionCSV(ctx, leftPath, leftCfg.RefCol, filepath.Join(dir, "left"), partitions, reporter.progress)
	if err != nil {
		return fmt.Errorf("partition left source: %w", err)
	}
	rightParts, rightRows, rightOriginalRows, err := partitionCSV(ctx, rightPath, rightCfg.RefCol, filepath.Join(dir, "right"), partitions, func(rows int) {
		reporter.progress(leftRows + rows)
	})
	if err != nil {
		return fmt.Errorf("partition right source: %w", err)
	}
	reporter.complete(leftRows + rightRows)
	for _, group := range rightDuplicateGroups {
		if err := w.WriteDuplicate(group); err != nil {
			return err
		}
	}
	agg := &partitionSummaryWriter{ResultWriter: w}
	for i := 0; i < partitions; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		idx := NewMemoryIndex()
		err := reconcileStreamingWithOptions(ctx, pairName, leftSource, rightSource, leftParts[i], rightParts[i], leftCfg, rightCfg, pair, idx, agg, options.MaxTokenBuffer, reporter, streamingDuplicateOptions{
			globalRightDuplicateKeys:      rightDuplicateKeys,
			globalLeftDuplicateKeys:       leftDuplicateKeys,
			globalRightRepresentativeRows: rightRepresentativeRows,
			globalLeftRepresentativeRows:  leftRepresentativeRows,
			rightPartitionOriginalRows:    rightOriginalRows[i],
			leftPartitionOriginalRows:     leftOriginalRows[i],
		})
		closeErr := idx.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for _, group := range leftDuplicateGroups {
		if err := w.WriteDuplicate(group); err != nil {
			return err
		}
	}
	reporter.start("finalization", leftSource, rightSource, nil)
	if err := agg.FlushSummary(); err != nil {
		return err
	}
	reporter.complete(0)
	return nil
}

func collectPartitionDuplicateGroups(ctx context.Context, sourceName, path string, cfg config.ParserCfg) (map[string]bool, []DuplicateGroup, error) {
	counts := make(map[string]uint8)
	if err := ParseEach(ctx, sourceName, path, cfg, func(tx Transaction, _ int) error {
		if tx.GroupKey != "" && counts[tx.GroupKey] < 2 {
			counts[tx.GroupKey]++
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	keys := make(map[string]bool)
	for key, count := range counts {
		if count >= 2 {
			keys[key] = true
		}
	}
	if len(keys) == 0 {
		return keys, nil, nil
	}
	groups, err := collectDuplicates(ctx, sourceName, path, cfg, keys)
	if err != nil {
		return nil, nil, err
	}
	return keys, groups, nil
}

func collectPartitionDuplicateRepresentatives(ctx context.Context, sourceName, path string, cfg config.ParserCfg, latest bool) (map[string]int, error) {
	representatives := make(map[string]int)
	return representatives, ParseEach(ctx, sourceName, path, cfg, func(tx Transaction, rowNum int) error {
		if tx.GroupKey == "" {
			return nil
		}
		if latest || representatives[tx.GroupKey] == 0 {
			representatives[tx.GroupKey] = rowNum
		}
		return nil
	})
}

type partitionSummaryWriter struct {
	ResultWriter
	summary Summary
	seen    bool
}

func (w *partitionSummaryWriter) WriteSummary(s Summary) error {
	w.summary = addSummaries(w.summary, s)
	w.seen = true
	return nil
}

func (w *partitionSummaryWriter) Flush() error { return nil }

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
		a.MatchRatePct = float64(a.MatchedCount) * 100 / float64(denom)
		a.ReconciledRatePct = float64(a.MatchedCount+a.AmountDiffCount+a.TimingDiffCount) * 100 / float64(denom)
	}
	return a
}

func partitionCSV(ctx context.Context, input, refCol, prefix string, n int, progress func(int)) ([]string, int, [][]int, error) {
	if n < 2 {
		return nil, 0, nil, fmt.Errorf("partition count must be at least 2 (got %d)", n)
	}
	in, err := os.Open(input) // #nosec G304 -- input is an explicit caller-selected reconciliation file.
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = in.Close() }()
	r := csv.NewReader(in)
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, 0, nil, err
	}
	refIdx := -1
	for i, col := range header {
		if strings.EqualFold(strings.TrimSpace(col), strings.TrimSpace(refCol)) {
			refIdx = i
			break
		}
	}
	if refIdx < 0 {
		return nil, 0, nil, fmt.Errorf("reference column %q not found", refCol)
	}
	files := make([]*os.File, n)
	writers := make([]*csv.Writer, n)
	paths := make([]string, n)
	originalRows := make([][]int, n)
	closeAll := func() {
		for i := range files {
			if files[i] != nil {
				_ = files[i].Close()
			}
		}
	}
	defer closeAll()
	for i := 0; i < n; i++ {
		paths[i] = fmt.Sprintf("%s-%03d.csv", prefix, i)
		if err := os.MkdirAll(filepath.Dir(paths[i]), 0o750); err != nil {
			return nil, 0, nil, err
		}
		f, err := os.Create(paths[i])
		if err != nil {
			return nil, 0, nil, err
		}
		files[i] = f
		writers[i] = csv.NewWriter(f)
		if err := writers[i].Write(header); err != nil {
			return nil, 0, nil, err
		}
	}
	rows := 0
	for row := 2; ; row++ {
		if err := ctx.Err(); err != nil {
			return nil, 0, nil, err
		}
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, nil, fmt.Errorf("row %d: %w", row, err)
		}
		key := ""
		if refIdx < len(record) {
			// Keep partition routing identical to parser normalization: the
			// reference value is trimmed before it becomes an index key.
			key = strings.TrimSpace(record[refIdx])
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(key))
		p := int(uint64(h.Sum32()) % uint64(n)) // #nosec G115 -- result is bounded by positive partition count.
		if err := writers[p].Write(record); err != nil {
			return nil, 0, nil, err
		}
		originalRows[p] = append(originalRows[p], row-1)
		rows++
		if progress != nil {
			progress(rows)
		}
	}
	for _, w := range writers {
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, 0, nil, err
		}
	}
	return paths, rows, originalRows, nil
}
