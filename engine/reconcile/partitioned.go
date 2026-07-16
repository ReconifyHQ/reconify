//nolint:revive // This package preserves stable compatibility names.
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"context"
	"fmt"

	//nolint:staticcheck // Domain types are deliberately imported into the implementation namespace.
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/matching"

	"os"
	"path/filepath"

	"github.com/reconifyhq/reconify/config"
	engineTelemetry "github.com/reconifyhq/reconify/engine/telemetry"
)

// PartitionedOptions controls temporary storage and partition sizing.
type PartitionedOptions struct {
	MaxTokenBuffer int
	Partitions     int
	SpillDir       string
	// Workers controls the number of independent partitions processed at once.
	// A value below two keeps the historical serial execution path.
	Workers int
	// QueueCapacity bounds completed chunk descriptors waiting for the writer.
	// Zero selects a small capacity derived from Workers.
	QueueCapacity int
	// MaxChunkBytes bounds an individual temporary result chunk. Zero disables
	// this optional safeguard. Chunks contain events, not full in-memory results.
	MaxChunkBytes int64
	// Metrics receives best-effort queue and spool observations. It may be
	// called by worker goroutines and must be safe for concurrent use.
	Metrics func(PartitionQueueMetrics)
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
	reporter := engineTelemetry.NewReporter(telemetry)
	if reporter != nil {
		defer reporter.Close()
	}
	err := reconcilePartitioned(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, w, options, reporter)
	if err != nil {
		reporter.Fail(0)
	}
	return err
}

func reconcilePartitioned(ctx context.Context, pairName, leftSource, rightSource, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, w ResultWriter, options PartitionedOptions, reporter *engineTelemetry.Reporter) error {
	if err := validateCollaborator("result writer", w); err != nil {
		return err
	}
	partitions := options.Partitions
	if partitions < 2 {
		partitions = adaptivePartitionCountFromFiles(leftPath, rightPath)
	}
	reporter.Start("partitioning", leftSource, rightSource, nil)
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
		leftGrouped, err = partitionCSVWithSidecars(ctx, leftPath, leftKeyCol, filepath.Join(dir, "left"), partitions, reporter.Progress)
		if err != nil {
			return fmt.Errorf("partition left source: %w", err)
		}
		leftRows = leftGrouped.count
		leftParts = leftGrouped
		rightGrouped, err = partitionCSVWithSidecars(ctx, rightPath, rightKeyCol, filepath.Join(dir, "right"), partitions, func(rows int) {
			reporter.Progress(leftRows + rows)
		})
		if err != nil {
			return fmt.Errorf("partition right source: %w", err)
		}
		rightRows = rightGrouped.count
		rightParts = rightGrouped
	} else {
		leftParts, leftDuplicateParts, err = partitionCSVWithDuplicateSpill(ctx, leftPath, leftKeyCol, duplicateSpillColumn(leftCfg), filepath.Join(dir, "left"), partitions, reporter.Progress)
		if err != nil {
			return fmt.Errorf("partition left source: %w", err)
		}
		leftRows = leftParts.count
		rightParts, rightDuplicateParts, err = partitionCSVWithDuplicateSpill(ctx, rightPath, rightKeyCol, duplicateSpillColumn(rightCfg), filepath.Join(dir, "right"), partitions, func(rows int) {
			reporter.Progress(leftRows + rows)
		})
		if err != nil {
			return fmt.Errorf("partition right source: %w", err)
		}
		rightRows = rightParts.count
	}
	reporter.Complete(leftRows + rightRows)
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
		err := processPartitionQueue(ctx, partitions, options.Workers, options.QueueCapacity, options.Metrics,
			func(workCtx context.Context, i int) (partitionChunkDescriptor, error) {
				chunkPath := filepath.Join(dir, fmt.Sprintf("result-%03d.gob", i))
				chunk, err := newPartitionChunkWriter(chunkPath, options.MaxChunkBytes)
				if err != nil {
					return partitionChunkDescriptor{}, err
				}
				partWriter := &partitionSummaryWriter{ResultWriter: chunk}
				leftData, leftRowsPath, err := sortGroupedPartition(workCtx, leftGrouped.data[i], leftGrouped.rows[i], filepath.Join(dir, fmt.Sprintf("sorted-left-%03d", i)), leftKeyCol)
				if err == nil {
					rightData, rightRowsPath, sortErr := sortGroupedPartition(workCtx, rightGrouped.data[i], rightGrouped.rows[i], filepath.Join(dir, fmt.Sprintf("sorted-right-%03d", i)), rightKeyCol)
					if sortErr != nil {
						err = sortErr
					} else {
						err = reconcileGroupedPartition(workCtx, pairName, leftSource, rightSource, leftData, leftRowsPath, rightData, rightRowsPath, leftCfg, rightCfg, pair, partWriter)
					}
				}
				closeErr := chunk.close()
				if err == nil {
					err = closeErr
				}
				if err == nil {
					err = chunk.warningErr
				}
				if err != nil {
					_ = os.Remove(chunkPath)
					return partitionChunkDescriptor{}, fmt.Errorf("reconcile partition %d: %w", i, err)
				}
				return partitionChunkDescriptor{partition: i, path: chunkPath, summary: partWriter.summary}, nil
			},
			func(descriptor partitionChunkDescriptor) error {
				if err := replayPartitionChunk(ctx, descriptor.path, agg); err != nil {
					return fmt.Errorf("write partition %d: %w", descriptor.partition, err)
				}
				agg.summary = addSummaries(agg.summary, descriptor.summary)
				agg.seen = true
				if err := os.Remove(descriptor.path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove partition %d result chunk: %w", descriptor.partition, err)
				}
				return nil
			})
		if err != nil {
			return err
		}
	} else {
		err := processPartitionQueue(ctx, partitions, options.Workers, options.QueueCapacity, options.Metrics,
			func(workCtx context.Context, i int) (partitionChunkDescriptor, error) {
				chunkPath := filepath.Join(dir, fmt.Sprintf("result-%03d.gob", i))
				chunk, err := newPartitionChunkWriter(chunkPath, options.MaxChunkBytes)
				if err != nil {
					return partitionChunkDescriptor{}, err
				}
				partWriter := &partitionSummaryWriter{ResultWriter: chunk}
				leftOriginalRows, err := readPartitionRows(leftParts.rows[i])
				if err == nil {
					var rightOriginalRows []int
					rightOriginalRows, err = readPartitionRows(rightParts.rows[i])
					if err == nil {
						var leftRepresentatives, rightRepresentatives map[int]struct{}
						leftRepresentatives, err = leftDisposition.Representatives(i)
						if err == nil {
							rightRepresentatives, err = rightDisposition.Representatives(i)
							if err == nil {
								idx := NewMemoryIndex()
								err = reconcileStreamingWithOptions(workCtx, pairName, leftSource, rightSource, leftParts.data[i], rightParts.data[i], partitionedPolicyConfig(leftCfg), partitionedPolicyConfig(rightCfg), pair, idx, partWriter, options.MaxTokenBuffer, reporter, streamingDuplicateOptions{
									rightRepresentativeRows: rightRepresentatives, leftRepresentativeRows: leftRepresentatives,
									rightPartitionOriginalRows: rightOriginalRows, leftPartitionOriginalRows: leftOriginalRows,
								})
								closeErr := idx.Close()
								if err == nil {
									err = closeErr
								}
							}
						}
					}
				}
				closeErr := chunk.close()
				if err == nil {
					err = closeErr
				}
				if err == nil {
					err = chunk.warningErr
				}
				if err != nil {
					_ = os.Remove(chunkPath)
					return partitionChunkDescriptor{}, fmt.Errorf("reconcile partition %d: %w", i, err)
				}
				return partitionChunkDescriptor{partition: i, path: chunkPath, summary: partWriter.summary}, nil
			},
			func(descriptor partitionChunkDescriptor) error {
				if err := replayPartitionChunk(ctx, descriptor.path, agg); err != nil {
					return fmt.Errorf("write partition %d: %w", descriptor.partition, err)
				}
				agg.summary = addSummaries(agg.summary, descriptor.summary)
				agg.seen = true
				if err := os.Remove(descriptor.path); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("remove partition %d result chunk: %w", descriptor.partition, err)
				}
				return nil
			})
		if err != nil {
			return err
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
	reporter.Start("finalization", leftSource, rightSource, nil)
	reporter.Progress(leftRows + rightRows)
	if err := agg.FlushSummary(); err != nil {
		return err
	}
	reporter.Complete(0)
	return nil
}

func validatePartitionCurrencies(ctx context.Context, leftSource, leftPath string, leftCfg config.ParserCfg, rightSource, rightPath string, rightCfg config.ParserCfg) error {
	cc := matching.CurrencyTracker{}
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
