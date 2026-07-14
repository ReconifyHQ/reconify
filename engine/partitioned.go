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

	"github.com/reconifyhq/reconify/config"
)

// ReconcilePartitioned reconciles exact/reference passes using bounded memory.
// Grouped and token passes are intentionally rejected because their semantics
// require cross-partition state.
func ReconcilePartitioned(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, maxTokenBuffer, partitions int) error {
	if partitions < 2 {
		partitions = 32
	}
	if len(pair.Passes) > 0 {
		for _, pass := range pair.Passes {
			if pass.Type != config.PassTypeReferenceOneToOne {
				return fmt.Errorf("partitioned backend supports reference_one_to_one only (got %q)", pass.Type)
			}
		}
	}
	if leftCfg.RefCol == "" || rightCfg.RefCol == "" {
		return fmt.Errorf("partitioned backend requires ref_col on both sources")
	}
	dir, err := os.MkdirTemp("", "reconify-partitions-*")
	if err != nil {
		return fmt.Errorf("create partition directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	leftParts, err := partitionCSV(ctx, leftPath, leftCfg.RefCol, filepath.Join(dir, "left"), partitions)
	if err != nil {
		return fmt.Errorf("partition left source: %w", err)
	}
	rightParts, err := partitionCSV(ctx, rightPath, rightCfg.RefCol, filepath.Join(dir, "right"), partitions)
	if err != nil {
		return fmt.Errorf("partition right source: %w", err)
	}
	agg := &partitionSummaryWriter{ResultWriter: w}
	for i := 0; i < partitions; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		idx := NewMemoryIndex()
		err := ReconcileStreaming(ctx, pairName, leftSource, rightSource, leftParts[i], rightParts[i], leftCfg, rightCfg, pair, idx, agg, maxTokenBuffer)
		closeErr := idx.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return agg.FlushSummary()
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

func partitionCSV(ctx context.Context, input, refCol, prefix string, n int) ([]string, error) {
	in, err := os.Open(input) // #nosec G304 -- input is an explicit caller-selected reconciliation file.
	if err != nil {
		return nil, err
	}
	defer func() { _ = in.Close() }()
	r := csv.NewReader(in)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	refIdx := -1
	for i, col := range header {
		if col == refCol {
			refIdx = i
			break
		}
	}
	if refIdx < 0 {
		return nil, fmt.Errorf("reference column %q not found", refCol)
	}
	files := make([]*os.File, n)
	writers := make([]*csv.Writer, n)
	paths := make([]string, n)
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
			return nil, err
		}
		f, err := os.Create(paths[i])
		if err != nil {
			return nil, err
		}
		files[i] = f
		writers[i] = csv.NewWriter(f)
		if err := writers[i].Write(header); err != nil {
			return nil, err
		}
	}
	for row := 2; ; row++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		key := ""
		if refIdx < len(record) {
			key = record[refIdx]
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(key))
		p := int(uint64(h.Sum32()) % uint64(n)) // #nosec G115 -- result is bounded by positive partition count.
		if err := writers[p].Write(record); err != nil {
			return nil, err
		}
	}
	for _, w := range writers {
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, err
		}
	}
	return paths, nil
}
