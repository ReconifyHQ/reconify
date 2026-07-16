//nolint:revive // This package preserves stable compatibility names.
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"bufio"
	"context"
	"fmt"

	//nolint:staticcheck // Domain types are deliberately imported into the implementation namespace.
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/matching"

	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/reconifyhq/reconify/config"
	engineTelemetry "github.com/reconifyhq/reconify/engine/telemetry"
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
	reporter := engineTelemetry.NewReporter(telemetry)
	if reporter != nil {
		defer reporter.Close()
	}
	err := reconcilePartitionedMultiSource(ctx, pairName, leftSource, leftPath, leftCfg, counterparts, pair, w, options, reporter)
	if err != nil {
		reporter.Fail(0)
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
	reporter *engineTelemetry.Reporter,
) error {
	if err := validateCollaborator("result writer", w); err != nil {
		return err
	}
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

	reporter.Start("partitioning", leftSource, strings.Join(counterpartNames(counterparts), ","), nil)
	leftParts, leftDuplicateParts, err := partitionCSVWithDuplicateSpill(ctx, leftPath, leftKey, duplicateSpillColumn(leftCfg), filepath.Join(spillDir, "left-original"), partitions, reporter.Progress)
	if err != nil {
		return fmt.Errorf("partition left source: %w", err)
	}
	reporter.Complete(leftParts.count)

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

		reporter.Start("counterpart_pass", leftSource, counterpart.SourceName, nil)
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
		reporter.Complete(passSummary.TotalLeft + passSummary.TotalRight)
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
	reporter.Start("finalization", leftSource, strings.Join(names, ","), nil)
	reporter.Progress(leftParts.count + totalRight)
	if err := w.WriteSummary(aggregate); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	reporter.Complete(leftParts.count + totalRight)
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
	cc := &matching.CurrencyTracker{}
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
	return cc.Currency(), nil
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
	reporter *engineTelemetry.Reporter,
) (groupedPartitionFiles, *partitionedDuplicateDisposition, error) {
	_, rightKey, _, ok, reason := partitionKeyColumns(pair, leftCfg, cfg)
	if !ok {
		return groupedPartitionFiles{}, nil, fmt.Errorf("%s", reason)
	}
	prefix := filepath.Join(spillDir, fmt.Sprintf("right-%02d", passIndex))
	reporter.Start("counterpart_partitioning", counterpart.SourceName, counterpart.SourceName, nil)
	parts, duplicateParts, err := partitionCSVWithDuplicateSpill(ctx, counterpart.RightPath, rightKey, duplicateSpillColumn(cfg), prefix, partitions, reporter.Progress)
	if err != nil {
		return groupedPartitionFiles{}, nil, err
	}
	reporter.Complete(parts.count)
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
	reporter *engineTelemetry.Reporter,
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
	queueErr := processPartitionQueue(ctx, partitions, options.Workers, options.QueueCapacity, options.Metrics,
		func(workCtx context.Context, i int) (partitionChunkDescriptor, error) {
			chunkPath := filepath.Join(spillDir, fmt.Sprintf("result-%02d-%03d.gob", passIndex, i))
			chunk, err := newPartitionChunkWriter(chunkPath, options.MaxChunkBytes)
			if err != nil {
				return partitionChunkDescriptor{}, err
			}
			manifestPath := filepath.Join(spillDir, fmt.Sprintf("carry-%02d-%03d.ids", passIndex, i))
			carry, err := newPartitionCarryWriter(chunk, manifestPath, leftSource)
			if err != nil {
				_ = chunk.close()
				return partitionChunkDescriptor{}, err
			}
			partWriter := &partitionSummaryWriter{ResultWriter: carry}
			var runErr error
			var sortedLeftData, sortedLeftRows, sortedRightData, sortedRightRows string
			leftRows, readErr := readPartitionRows(leftParts.rows[i])
			if readErr == nil {
				var rightRows []int
				rightRows, readErr = readPartitionRows(rightParts.rows[i])
				if readErr == nil {
					if containsGroupedPass(pair.Passes) {
						sortedLeftData, sortedLeftRows, runErr = sortGroupedPartition(workCtx, leftParts.data[i], leftParts.rows[i], filepath.Join(spillDir, fmt.Sprintf("sorted-left-%02d-%03d", passIndex, i)), leftKey)
						if runErr == nil {
							sortedRightData, sortedRightRows, runErr = sortGroupedPartition(workCtx, rightParts.data[i], rightParts.rows[i], filepath.Join(spillDir, fmt.Sprintf("sorted-right-%02d-%03d", passIndex, i)), rightKey)
						}
						if runErr == nil {
							runErr = reconcileGroupedPartition(workCtx, pairName, leftSource, rightSource, sortedLeftData, sortedLeftRows, sortedRightData, sortedRightRows, leftCfg, rightCfg, pair, partWriter)
						}
					} else {
						leftRepresentatives, repErr := leftDisposition.Representatives(i)
						if repErr == nil {
							rightRepresentatives, rightRepErr := rightDisposition.Representatives(i)
							if rightRepErr != nil {
								readErr = rightRepErr
							} else {
								idx := NewMemoryIndex()
								runErr = reconcileStreamingWithOptions(workCtx, pairName, leftSource, rightSource,
									leftParts.data[i], rightParts.data[i], leftCfgForPass, rightCfgForPass, pair, idx, partWriter,
									options.MaxTokenBuffer, reporter, streamingDuplicateOptions{
										rightRepresentativeRows: rightRepresentatives, leftRepresentativeRows: leftRepresentatives,
										rightPartitionOriginalRows: rightRows, leftPartitionOriginalRows: leftRows,
									})
								if closeErr := idx.Close(); runErr == nil {
									runErr = closeErr
								}
							}
						} else {
							readErr = repErr
						}
					}
				}
			}
			if runErr == nil {
				runErr = readErr
			}
			if closeErr := carry.Close(); runErr == nil {
				runErr = closeErr
			}
			if closeErr := chunk.close(); runErr == nil {
				runErr = closeErr
			}
			if runErr == nil {
				runErr = chunk.warningErr
			}
			if runErr != nil {
				_ = os.Remove(chunkPath)
				return partitionChunkDescriptor{}, fmt.Errorf("partition %d: %w", i, runErr)
			}
			return partitionChunkDescriptor{
				partition: i, path: chunkPath, summary: partWriter.summary, manifest: manifestPath,
				nextData: fmt.Sprintf("%s-%03d.csv", nextPrefix, i), nextRows: fmt.Sprintf("%s-%03d.rows", nextPrefix, i),
				sorted: []string{sortedLeftData, sortedLeftRows, sortedRightData, sortedRightRows},
			}, nil
		},
		func(descriptor partitionChunkDescriptor) error {
			partWriter := &partitionSummaryWriter{ResultWriter: w}
			if err := replayPartitionChunk(ctx, descriptor.path, partWriter); err != nil {
				return fmt.Errorf("write partition %d: %w", descriptor.partition, err)
			}
			passSummary = addSummaries(passSummary, descriptor.summary)
			next.data[descriptor.partition], next.rows[descriptor.partition] = descriptor.nextData, descriptor.nextRows
			nextCount, err := filterPartitionByManifest(ctx, leftSource, leftParts.data[descriptor.partition], leftParts.rows[descriptor.partition], descriptor.nextData, descriptor.nextRows, descriptor.manifest)
			if err != nil {
				return fmt.Errorf("stage carry-forward partition %d: %w", descriptor.partition, err)
			}
			next.count += nextCount
			if err := os.Remove(descriptor.manifest); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove carry manifest for partition %d: %w", descriptor.partition, err)
			}
			paths := append([]string{leftParts.data[descriptor.partition], leftParts.rows[descriptor.partition], rightParts.data[descriptor.partition], rightParts.rows[descriptor.partition], descriptor.path}, descriptor.sorted...)
			if err := removePartitionPassFiles(paths...); err != nil {
				return fmt.Errorf("remove consumed partition %d files: %w", descriptor.partition, err)
			}
			return nil
		})
	if queueErr != nil {
		return groupedPartitionFiles{}, Summary{}, queueErr
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
