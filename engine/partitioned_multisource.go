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

// PartitionedCounterpartInput describes one CSV counterpart in an ordered
// partitioned multi-source run. ParserCfg and RightCfg are equivalent; RightCfg
// mirrors CounterpartStream while ParserCfg keeps the type convenient for API
// callers that build inputs from config.Source values.
type PartitionedCounterpartInput struct {
	SourceName string
	RightPath  string
	ParserCfg  config.ParserCfg
	RightCfg   config.ParserCfg
}

// PartitionedCounterpart is a shorter compatibility alias for callers that
// prefer the name used by the coordinator rather than the input type name.
type PartitionedCounterpart = PartitionedCounterpartInput

func (c PartitionedCounterpartInput) parserCfg() config.ParserCfg {
	if c.ParserCfg != (config.ParserCfg{}) {
		return c.ParserCfg
	}
	return c.RightCfg
}

// ReconcilePartitionedMultiSource runs ordered counterpart passes with a
// bounded-memory, disk-backed carry-forward of unmatched left rows.
func ReconcilePartitionedMultiSource(
	ctx context.Context,
	pairName, leftSource, leftPath string,
	leftCfg config.ParserCfg,
	counterparts []PartitionedCounterpartInput,
	pair config.Pair,
	w ResultWriter,
	maxTokenBuffer, partitions int,
) error {
	return reconcilePartitionedMultiSource(ctx, pairName, leftSource, leftPath, leftCfg, counterparts, pair, w, PartitionedOptions{
		MaxTokenBuffer: maxTokenBuffer,
		Partitions:     partitions,
	}, nil)
}

// ReconcilePartitionedMultiSourceWithOptions uses the configured spill
// directory and partition count for an ordered multi-source run.
func ReconcilePartitionedMultiSourceWithOptions(
	ctx context.Context,
	pairName, leftSource, leftPath string,
	leftCfg config.ParserCfg,
	counterparts []PartitionedCounterpartInput,
	pair config.Pair,
	w ResultWriter,
	options PartitionedOptions,
) error {
	return reconcilePartitionedMultiSource(ctx, pairName, leftSource, leftPath, leftCfg, counterparts, pair, w, options, nil)
}

// ReconcilePartitionedMultiSourceWithTelemetry is the legacy-options telemetry
// wrapper corresponding to ReconcilePartitionedWithTelemetry.
func ReconcilePartitionedMultiSourceWithTelemetry(
	ctx context.Context,
	pairName, leftSource, leftPath string,
	leftCfg config.ParserCfg,
	counterparts []PartitionedCounterpartInput,
	pair config.Pair,
	w ResultWriter,
	maxTokenBuffer, partitions int,
	telemetry TelemetryOptions,
) error {
	return ReconcilePartitionedMultiSourceWithOptionsAndTelemetry(ctx, pairName, leftSource, leftPath, leftCfg, counterparts, pair, w, PartitionedOptions{
		MaxTokenBuffer: maxTokenBuffer,
		Partitions:     partitions,
	}, telemetry)
}

// ReconcilePartitionedMultiSourceWithOptionsAndTelemetry combines spill
// options with the typed, best-effort telemetry API.
func ReconcilePartitionedMultiSourceWithOptionsAndTelemetry(
	ctx context.Context,
	pairName, leftSource, leftPath string,
	leftCfg config.ParserCfg,
	counterparts []PartitionedCounterpartInput,
	pair config.Pair,
	w ResultWriter,
	options PartitionedOptions,
	telemetry TelemetryOptions,
) error {
	reporter := newTelemetryReporter(telemetry)
	if reporter != nil {
		defer reporter.close()
	}
	err := reconcilePartitionedMultiSource(ctx, pairName, leftSource, leftPath, leftCfg, counterparts, pair, w, options, reporter)
	if err != nil {
		reporter.fail(0)
	}
	return err
}

func reconcilePartitionedMultiSource(
	ctx context.Context,
	pairName, leftSource, leftPath string,
	leftCfg config.ParserCfg,
	counterparts []PartitionedCounterpartInput,
	pair config.Pair,
	w ResultWriter,
	options PartitionedOptions,
	reporter *telemetryReporter,
) error {
	if len(counterparts) == 0 {
		return fmt.Errorf("at least one counterpart source is required")
	}
	if err := validateCounterpartNames(counterpartNames(counterparts)); err != nil {
		return err
	}
	if err := validatePartitionedMultiSourceInputs(leftPath, leftCfg, counterparts, pair); err != nil {
		return err
	}
	partitions := options.Partitions
	if partitions < 2 {
		paths := make([]string, 0, len(counterparts)+1)
		paths = append(paths, leftPath)
		for _, counterpart := range counterparts {
			paths = append(paths, counterpart.RightPath)
		}
		partitions = adaptivePartitionCountFromFiles(paths...)
	}
	baseDir := options.SpillDir
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return fmt.Errorf("create partition spill base directory: %w", err)
	}
	spillDir, err := os.MkdirTemp(baseDir, "reconify-multisource-partitions-*")
	if err != nil {
		return fmt.Errorf("create partition directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(spillDir) }()

	leftKey, err := partitionedMultiSourceKey(leftCfg, counterparts, pair)
	if err != nil {
		return err
	}
	currency, err := validatePartitionedMultiSourceCurrencies(ctx, leftSource, leftPath, leftCfg, counterparts)
	if err != nil {
		return err
	}

	reporter.start("partitioning", leftSource, strings.Join(counterpartNames(counterparts), ","), nil)
	leftParts, leftDuplicateParts, err := partitionCSVWithDuplicateSpill(ctx, leftPath, leftKey, duplicateSpillColumn(leftCfg), filepath.Join(spillDir, "left-original"), partitions, reporter.progress)
	if err != nil {
		return fmt.Errorf("partition left source: %w", err)
	}
	reporter.complete(leftParts.count)

	_, _, leftSelector, _, _ := partitionKeyColumns(pair, leftCfg, counterparts[0].parserCfg())
	leftDisposition, err := buildPartitionedDuplicateDisposition(ctx, leftSource, leftCfg, leftSelector, leftDuplicateParts, filepath.Join(spillDir, "left-duplicate"), partitions)
	if err != nil {
		return fmt.Errorf("prepare left disposition: %w", err)
	}
	defer leftDisposition.Cleanup()

	currentLeft := leftParts
	var aggregate Summary
	bySource := make(map[string]Summary, len(counterparts))
	totalRight := 0
	rightDuplicateTotal := 0
	names := counterpartNames(counterparts)

	for passIndex, counterpart := range counterparts {
		if err := ctx.Err(); err != nil {
			return err
		}
		rightCfg := counterpart.parserCfg()
		rightParts, rightDisposition, err := preparePartitionedCounterpart(
			ctx, spillDir, passIndex, counterpart, rightCfg, pair, leftCfg, partitions, reporter,
		)
		if err != nil {
			return fmt.Errorf("prepare counterpart %q: %w", counterpart.SourceName, err)
		}
		if err := rightDisposition.Emit(w, ctx); err != nil {
			return fmt.Errorf("emit duplicates for counterpart %q: %w", counterpart.SourceName, err)
		}

		reporter.start("counterpart_pass", leftSource, counterpart.SourceName, nil)
		nextLeft, passSummary, err := reconcilePartitionedCounterpartPass(
			ctx, pairName, leftSource, leftCfg, currentLeft, counterpart.SourceName, rightCfg, rightParts,
			pair, w, options, passIndex, spillDir, leftDisposition, rightDisposition, reporter,
		)
		if err != nil {
			return fmt.Errorf("counterpart %q: %w", counterpart.SourceName, err)
		}
		if rightDisposition != nil {
			passSummary.DuplicateCount = rightDisposition.duplicateCount
		}
		passSummary.TotalRight = rightParts.count
		bySource[counterpart.SourceName] = passSummary
		aggregate = addSummaries(aggregate, passSummary)
		totalRight += rightParts.count
		if rightDisposition != nil {
			rightDuplicateTotal += rightDisposition.duplicateCount
		}

		if passIndex == 0 {
			if err := leftDisposition.Emit(w, ctx); err != nil {
				return fmt.Errorf("emit left duplicates: %w", err)
			}
		}
		currentLeft = nextLeft
		rightDisposition.Cleanup()
		reporter.complete(passSummary.TotalLeft + passSummary.TotalRight)
	}

	finalUnmatched, finalUnmatchedAmount, err := emitPartitionedLeft(ctx, leftSource, leftCfg, currentLeft, w)
	if err != nil {
		return fmt.Errorf("emit final unmatched left: %w", err)
	}

	aggregate.TotalLeft = leftParts.count
	aggregate.TotalRight = totalRight
	aggregate.UnmatchedLeft = finalUnmatched
	aggregate.UnmatchedAmountLeft = finalUnmatchedAmount
	aggregate.DuplicateCount = rightDuplicateTotal
	if leftDisposition != nil {
		aggregate.DuplicateCount += leftDisposition.duplicateCount
	}
	aggregate.Currency = currency
	finalizeAggregateSummary(&aggregate)

	if sbw, ok := w.(SourceBreakdownWriter); ok {
		for _, name := range names {
			if err := sbw.WriteSourceSummary(name, bySource[name]); err != nil {
				return err
			}
		}
	}
	reporter.start("finalization", leftSource, strings.Join(names, ","), nil)
	reporter.progress(leftParts.count + totalRight)
	if err := w.WriteSummary(aggregate); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	reporter.complete(leftParts.count + totalRight)
	return nil
}

func validatePartitionedMultiSourceInputs(leftPath string, leftCfg config.ParserCfg, counterparts []PartitionedCounterpartInput, pair config.Pair) error {
	if !partitionedCSVPath(leftPath, leftCfg) {
		return fmt.Errorf("partitioned backend supports CSV inputs only")
	}
	for _, counterpart := range counterparts {
		cfg := counterpart.parserCfg()
		if !partitionedCSVPath(counterpart.RightPath, cfg) {
			return fmt.Errorf("counterpart %q: partitioned backend supports CSV inputs only", counterpart.SourceName)
		}
		_, _, _, ok, reason := partitionKeyColumns(pair, leftCfg, cfg)
		if !ok {
			return fmt.Errorf("counterpart %q: %s", counterpart.SourceName, reason)
		}
		if safe, reason := PartitionDuplicateSafe(pair, leftCfg, cfg); !safe {
			return fmt.Errorf("counterpart %q: %s", counterpart.SourceName, reason)
		}
	}
	return nil
}

func partitionedCSVPath(path string, cfg config.ParserCfg) bool {
	typ := strings.ToLower(strings.TrimSpace(cfg.Type))
	if typ == "csv" {
		return true
	}
	return (typ == "" || typ == "auto") && strings.EqualFold(filepath.Ext(path), ".csv")
}

func partitionedMultiSourceKey(leftCfg config.ParserCfg, counterparts []PartitionedCounterpartInput, pair config.Pair) (string, error) {
	var selector, leftKey string
	for _, counterpart := range counterparts {
		cfg := counterpart.parserCfg()
		currentLeft, _, currentSelector, ok, reason := partitionKeyColumns(pair, leftCfg, cfg)
		if !ok {
			return "", fmt.Errorf("counterpart %q: %s", counterpart.SourceName, reason)
		}
		if selector == "" {
			selector, leftKey = currentSelector, currentLeft
			continue
		}
		if selector != currentSelector || !strings.EqualFold(strings.TrimSpace(leftKey), strings.TrimSpace(currentLeft)) {
			return "", fmt.Errorf("counterparts resolve to incompatible partition selectors (%q vs %q)", selector, currentSelector)
		}
	}
	return leftKey, nil
}

func counterpartNames(counterparts []PartitionedCounterpartInput) []string {
	names := make([]string, 0, len(counterparts))
	for _, counterpart := range counterparts {
		names = append(names, counterpart.SourceName)
	}
	return names
}

func validatePartitionedMultiSourceCurrencies(ctx context.Context, leftSource, leftPath string, leftCfg config.ParserCfg, counterparts []PartitionedCounterpartInput) (string, error) {
	cc := &currencyTracker{}
	if err := ParseEach(ctx, leftSource, leftPath, leftCfg, func(tx Transaction, _ int) error {
		return cc.Observe(leftSource, tx)
	}); err != nil {
		return "", fmt.Errorf("validate left currencies: %w", err)
	}
	for _, counterpart := range counterparts {
		cfg := counterpart.parserCfg()
		if err := ParseEach(ctx, counterpart.SourceName, counterpart.RightPath, cfg, func(tx Transaction, _ int) error {
			return cc.Observe(counterpart.SourceName, tx)
		}); err != nil {
			return "", fmt.Errorf("validate counterpart %q currencies: %w", counterpart.SourceName, err)
		}
	}
	return cc.base, nil
}

func preparePartitionedCounterpart(
	ctx context.Context,
	spillDir string,
	passIndex int,
	counterpart PartitionedCounterpartInput,
	cfg config.ParserCfg,
	pair config.Pair,
	leftCfg config.ParserCfg,
	partitions int,
	reporter *telemetryReporter,
) (groupedPartitionFiles, *partitionedDuplicateDisposition, error) {
	_, rightKey, _, ok, reason := partitionKeyColumns(pair, leftCfg, cfg)
	if !ok {
		return groupedPartitionFiles{}, nil, fmt.Errorf("%s", reason)
	}
	prefix := filepath.Join(spillDir, fmt.Sprintf("right-%02d", passIndex))
	reporter.start("counterpart_partitioning", counterpart.SourceName, counterpart.SourceName, nil)
	parts, duplicateParts, err := partitionCSVWithDuplicateSpill(ctx, counterpart.RightPath, rightKey, duplicateSpillColumn(cfg), prefix, partitions, reporter.progress)
	if err != nil {
		return groupedPartitionFiles{}, nil, err
	}
	reporter.complete(parts.count)
	_, _, selector, _, _ := partitionKeyColumns(pair, leftCfg, cfg)
	disposition, err := buildPartitionedDuplicateDisposition(ctx, counterpart.SourceName, cfg, selector, duplicateParts, filepath.Join(spillDir, fmt.Sprintf("right-%02d-duplicate", passIndex)), partitions)
	if err != nil {
		return groupedPartitionFiles{}, nil, err
	}
	return parts, disposition, nil
}

func reconcilePartitionedCounterpartPass(
	ctx context.Context,
	pairName, leftSource string,
	leftCfg config.ParserCfg,
	leftParts groupedPartitionFiles,
	rightSource string,
	rightCfg config.ParserCfg,
	rightParts groupedPartitionFiles,
	pair config.Pair,
	w ResultWriter,
	options PartitionedOptions,
	passIndex int,
	spillDir string,
	leftDisposition, rightDisposition *partitionedDuplicateDisposition,
	reporter *telemetryReporter,
) (groupedPartitionFiles, Summary, error) {
	partitions := len(leftParts.data)
	nextPrefix := filepath.Join(spillDir, fmt.Sprintf("left-carry-%02d", passIndex))
	next := groupedPartitionFiles{data: make([]string, partitions), rows: make([]string, partitions)}
	var passSummary Summary
	leftKey, rightKey, _, ok, reason := partitionKeyColumns(pair, leftCfg, rightCfg)
	if !ok {
		return groupedPartitionFiles{}, Summary{}, fmt.Errorf("%s", reason)
	}

	leftCfgForPass := partitionedPolicyConfig(leftCfg)
	rightCfgForPass := partitionedPolicyConfig(rightCfg)
	for i := 0; i < partitions; i++ {
		if err := ctx.Err(); err != nil {
			return groupedPartitionFiles{}, Summary{}, err
		}
		leftRows, err := readPartitionRows(leftParts.rows[i])
		if err != nil {
			return groupedPartitionFiles{}, Summary{}, fmt.Errorf("read left partition %d rows: %w", i, err)
		}
		rightRows, err := readPartitionRows(rightParts.rows[i])
		if err != nil {
			return groupedPartitionFiles{}, Summary{}, fmt.Errorf("read right partition %d rows: %w", i, err)
		}
		manifestPath := filepath.Join(spillDir, fmt.Sprintf("carry-%02d-%03d.ids", passIndex, i))
		carry, err := newPartitionCarryWriter(w, manifestPath, leftSource)
		if err != nil {
			return groupedPartitionFiles{}, Summary{}, err
		}
		partWriter := &partitionSummaryWriter{ResultWriter: carry}

		var runErr, sortErr, rightSortErr error
		var sortedLeftData, sortedLeftRows, sortedRightData, sortedRightRows string
		if containsGroupedPass(pair.Passes) {
			sortedLeftData, sortedLeftRows, sortErr = sortGroupedPartition(ctx, leftParts.data[i], leftParts.rows[i], filepath.Join(spillDir, fmt.Sprintf("sorted-left-%02d-%03d", passIndex, i)), leftKey)
			if sortErr == nil {
				sortedRightData, sortedRightRows, rightSortErr = sortGroupedPartition(ctx, rightParts.data[i], rightParts.rows[i], filepath.Join(spillDir, fmt.Sprintf("sorted-right-%02d-%03d", passIndex, i)), rightKey)
				if rightSortErr != nil {
					sortErr = rightSortErr
				} else {
					runErr = reconcileGroupedPartition(ctx, pairName, leftSource, rightSource, sortedLeftData, sortedLeftRows, sortedRightData, sortedRightRows, leftCfg, rightCfg, pair, partWriter)
				}
			}
			runErr = firstPartitionError(runErr, sortErr)
		} else {
			idx := NewMemoryIndex()
			leftRepresentatives, repErr := leftDisposition.Representatives(i)
			if repErr != nil {
				return groupedPartitionFiles{}, Summary{}, fmt.Errorf("read left representatives for partition %d: %w", i, repErr)
			}
			rightRepresentatives, repErr := rightDisposition.Representatives(i)
			if repErr != nil {
				return groupedPartitionFiles{}, Summary{}, fmt.Errorf("read right representatives for partition %d: %w", i, repErr)
			}
			runErr = reconcileStreamingWithOptions(ctx, pairName, leftSource, rightSource,
				leftParts.data[i], rightParts.data[i], leftCfgForPass, rightCfgForPass, pair, idx, partWriter,
				options.MaxTokenBuffer, reporter, streamingDuplicateOptions{
					rightRepresentativeRows:    rightRepresentatives,
					leftRepresentativeRows:     leftRepresentatives,
					rightPartitionOriginalRows: rightRows,
					leftPartitionOriginalRows:  leftRows,
				})
			closeErr := idx.Close()
			runErr = firstPartitionError(runErr, closeErr)
		}
		closeErr := carry.Close()
		runErr = firstPartitionError(runErr, closeErr)
		if runErr != nil {
			return groupedPartitionFiles{}, Summary{}, fmt.Errorf("partition %d: %w", i, runErr)
		}
		passSummary = addSummaries(passSummary, partWriter.summary)

		next.data[i] = fmt.Sprintf("%s-%03d.csv", nextPrefix, i)
		next.rows[i] = fmt.Sprintf("%s-%03d.rows", nextPrefix, i)
		nextCount, err := filterPartitionByManifest(ctx, leftSource, leftParts.data[i], leftParts.rows[i], next.data[i], next.rows[i], manifestPath)
		if err != nil {
			return groupedPartitionFiles{}, Summary{}, fmt.Errorf("stage carry-forward partition %d: %w", i, err)
		}
		next.count += nextCount
		_ = os.Remove(manifestPath)
		if err := removePartitionPassFiles(
			leftParts.data[i], leftParts.rows[i], rightParts.data[i], rightParts.rows[i],
			sortedLeftData, sortedLeftRows, sortedRightData, sortedRightRows,
		); err != nil {
			return groupedPartitionFiles{}, Summary{}, fmt.Errorf("remove consumed partition %d files: %w", i, err)
		}
	}
	return next, passSummary, nil
}

func removePartitionPassFiles(paths ...string) error {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func firstPartitionError(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

func readPartitionRows(path string) ([]int, error) {
	f, err := os.Open(path) // #nosec G304 -- path was created inside the private spill directory.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	rows := make([]int, 0)
	for scanner.Scan() {
		row, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if err != nil || row <= 0 {
			return nil, fmt.Errorf("invalid partition row number %q", scanner.Text())
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

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
