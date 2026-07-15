package engine

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

type partitionCarryWriter struct {
	inner       ResultWriter
	manifest    *os.File
	manifestBuf *bufio.Writer
	source      string
}

func newPartitionCarryWriter(inner ResultWriter, manifestPath, source string) (*partitionCarryWriter, error) {
	f, err := os.Create(manifestPath) // #nosec G304 -- manifestPath is generated in the private spill directory.
	if err != nil {
		return nil, err
	}
	return &partitionCarryWriter{inner: inner, manifest: f, manifestBuf: bufio.NewWriterSize(f, 64<<10), source: source}, nil
}

func (w *partitionCarryWriter) Close() error {
	if err := w.manifestBuf.Flush(); err != nil {
		_ = w.manifest.Close()
		return err
	}
	return w.manifest.Close()
}

func (w *partitionCarryWriter) WriteMatch(p MatchedPair) error { return w.inner.WriteMatch(p) }
func (w *partitionCarryWriter) WriteAmountDiff(p AmountDiffPair) error {
	return w.inner.WriteAmountDiff(p)
}
func (w *partitionCarryWriter) WriteTimingDiff(p TimingDiffPair) error {
	return w.inner.WriteTimingDiff(p)
}
func (w *partitionCarryWriter) WriteUnmatched(tx Transaction, side string) error {
	if side != "left" {
		return w.inner.WriteUnmatched(tx, side)
	}
	_, err := w.manifestBuf.WriteString(tx.ID + "\n")
	return err
}
func (w *partitionCarryWriter) WriteDuplicate(DuplicateGroup) error { return nil }
func (w *partitionCarryWriter) WriteSummary(Summary) error          { return nil }
func (w *partitionCarryWriter) Flush() error                        { return nil }

func (w *partitionCarryWriter) WriteGroupedMatch(p GroupedMatchedPair) error {
	if gw, ok := w.inner.(GroupedEventWriter); ok {
		return gw.WriteGroupedMatch(p)
	}
	return nil
}
func (w *partitionCarryWriter) WriteGroupedAmountDiff(p GroupedAmountDiffPair) error {
	if gw, ok := w.inner.(GroupedEventWriter); ok {
		return gw.WriteGroupedAmountDiff(p)
	}
	return nil
}
func (w *partitionCarryWriter) WriteGroupedTimingDiff(p GroupedTimingDiffPair) error {
	if gw, ok := w.inner.(GroupedEventWriter); ok {
		return gw.WriteGroupedTimingDiff(p)
	}
	return nil
}
func (w *partitionCarryWriter) WriteAmbiguousGroup(p AmbiguousGroupPair) error {
	if gw, ok := w.inner.(GroupedEventWriter); ok {
		return gw.WriteAmbiguousGroup(p)
	}
	return nil
}
func (w *partitionCarryWriter) WriteManyToManyMatch(p ManyToManyMatchedPair) error {
	if mw, ok := w.inner.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyMatch(p)
	}
	return nil
}
func (w *partitionCarryWriter) WriteManyToManyAmountDiff(p ManyToManyAmountDiffPair) error {
	if mw, ok := w.inner.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyAmountDiff(p)
	}
	return nil
}
func (w *partitionCarryWriter) WriteManyToManyTimingDiff(p ManyToManyTimingDiffPair) error {
	if mw, ok := w.inner.(ManyToManyEventWriter); ok {
		return mw.WriteManyToManyTimingDiff(p)
	}
	return nil
}
func (w *partitionCarryWriter) WriteSourceSummary(name string, sum Summary) error {
	if sbw, ok := w.inner.(SourceBreakdownWriter); ok {
		return sbw.WriteSourceSummary(name, sum)
	}
	return nil
}

func filterPartitionByManifest(ctx context.Context, source, dataPath, rowsPath, outputPath, outputRowsPath, manifestPath string) (int, error) {
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return 0, err
	}
	in, err := os.Open(dataPath) // #nosec G304 -- path was created inside the private spill directory.
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	rowIn, err := os.Open(rowsPath) // #nosec G304 -- path was created inside the private spill directory.
	if err != nil {
		return 0, err
	}
	defer func() { _ = rowIn.Close() }()
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o750); err != nil {
		return 0, err
	}
	out, err := os.Create(outputPath) // #nosec G304 -- outputPath is generated in the private spill directory.
	if err != nil {
		return 0, err
	}
	defer func() { _ = out.Close() }()
	outRows, err := os.Create(outputRowsPath) // #nosec G304 -- outputRowsPath is generated in the private spill directory.
	if err != nil {
		return 0, err
	}
	defer func() { _ = outRows.Close() }()

	r := csv.NewReader(in)
	r.TrimLeadingSpace = true
	rowScanner := bufio.NewScanner(rowIn)
	rowScanner.Buffer(make([]byte, 64<<10), 1<<20)
	header, err := r.Read()
	if err != nil {
		return 0, err
	}
	w := csv.NewWriter(out)
	if err := w.Write(header); err != nil {
		return 0, err
	}
	rowWriter := bufio.NewWriterSize(outRows, 64<<10)
	count := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		record, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
		if !rowScanner.Scan() {
			if scanErr := rowScanner.Err(); scanErr != nil {
				return 0, scanErr
			}
			return 0, fmt.Errorf("partition row sidecar ended before data rows")
		}
		row, parseErr := strconv.Atoi(strings.TrimSpace(rowScanner.Text()))
		if parseErr != nil || row <= 0 {
			return 0, fmt.Errorf("invalid partition row number %q", rowScanner.Text())
		}
		if _, keep := manifest[strconv.Itoa(row)]; !keep {
			continue
		}
		if err := w.Write(record); err != nil {
			return 0, err
		}
		if _, err := rowWriter.WriteString(strconv.Itoa(row) + "\n"); err != nil {
			return 0, err
		}
		count++
	}
	if rowScanner.Scan() {
		return 0, fmt.Errorf("partition row sidecar has more rows than data")
	}
	if err := rowScanner.Err(); err != nil {
		return 0, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return 0, err
	}
	if err := rowWriter.Flush(); err != nil {
		return 0, err
	}
	return count, nil
}

func readManifest(path string) (map[string]struct{}, error) {
	f, err := os.Open(path) // #nosec G304 -- path was created inside the private spill directory.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	manifest := make(map[string]struct{})
	for scanner.Scan() {
		id := strings.TrimSpace(scanner.Text())
		if id == "" {
			continue
		}
		parts := strings.LastIndexByte(id, '-')
		if parts < 0 {
			return nil, fmt.Errorf("invalid carry-forward transaction id %q", id)
		}
		manifest[id[parts+1:]] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return manifest, nil
}

func emitPartitionedLeft(ctx context.Context, source string, cfg config.ParserCfg, parts groupedPartitionFiles, w ResultWriter) (int, int64, error) {
	count := 0
	var amount int64
	for i, dataPath := range parts.data {
		rows, err := readPartitionRows(parts.rows[i])
		if err != nil {
			return 0, 0, err
		}
		rowIndex := 0
		err = ParseEach(ctx, source, dataPath, cfg, func(tx Transaction, _ int) error {
			if rowIndex >= len(rows) {
				return fmt.Errorf("partition row sidecar ended before parsed rows")
			}
			tx.ID = fmt.Sprintf("%s-%d", source, rows[rowIndex])
			rowIndex++
			count++
			amount += tx.Amount
			return w.WriteUnmatched(tx, "left")
		})
		if err != nil {
			return 0, 0, err
		}
		if rowIndex != len(rows) {
			return 0, 0, fmt.Errorf("partition row sidecar has more rows than parsed data")
		}
	}
	return count, amount, nil
}

func finalizeAggregateSummary(summary *Summary) {
	denom := summary.TotalLeft
	if summary.TotalRight > denom {
		denom = summary.TotalRight
	}
	if denom == 0 {
		summary.MatchRatePct = 0
		summary.ReconciledRatePct = 0
	} else {
		summary.MatchRatePct = roundPct(float64(summary.MatchedCount) / float64(denom))
		reconciled := summary.MatchedCount + summary.AmountDiffCount + summary.TimingDiffCount +
			summary.GroupedMatchedCount + summary.GroupedAmountDiffCount + summary.GroupedTimingDiffCount +
			summary.ManyToManyMatchedCount + summary.ManyToManyAmountDiffCount + summary.ManyToManyTimingDiffCount
		summary.ReconciledRatePct = roundPct(float64(reconciled) / float64(denom))
	}
	summary.TotalDiscrepancy = summary.UnmatchedAmountLeft + summary.UnmatchedAmountRight + summary.AmountDiffTotal +
		summary.AmbiguousAmountLeft + summary.AmbiguousAmountRight
}

func roundPct(value float64) float64 {
	return float64(int(value*10000+0.5)) / 100
}
