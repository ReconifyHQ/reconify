//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"context"
	"encoding/gob"
	"errors"
	"io"
	"os"
	"sync"

	. "github.com/reconifyhq/reconify/engine/domain"
)

// PartitionQueueMetrics describes the bounded result hand-off. Values are
// snapshots; callbacks may be invoked from more than one worker goroutine.
type PartitionQueueMetrics struct {
	QueueDepth      int
	SpillBytes      int64
	ChunksCompleted int
	WriterBytes     int64
	WorkerBlocks    uint64
}

type partitionQueueStats struct {
	mu       sync.Mutex
	metrics  PartitionQueueMetrics
	callback func(PartitionQueueMetrics)
}

func newPartitionQueueStats(callback func(PartitionQueueMetrics)) *partitionQueueStats {
	return &partitionQueueStats{callback: callback}
}

func (s *partitionQueueStats) emit(update func(*PartitionQueueMetrics)) {
	if s == nil || s.callback == nil {
		return
	}
	s.mu.Lock()
	update(&s.metrics)
	snapshot := s.metrics
	s.mu.Unlock()
	s.callback(snapshot)
}

// partitionChunkWriter is a private, disk-backed ResultWriter. It deliberately
// does not expose the final writer to workers: ResultWriter is not thread-safe,
// and a chunk contains only typed events plus a per-partition summary.
type partitionChunkWriter struct {
	file       *os.File
	enc        *gob.Encoder
	maxBytes   int64
	summary    Summary
	closed     bool
	warningErr error
}

type partitionChunkEvent struct {
	Kind    uint8
	Side    string
	Warning Warning

	Match             MatchedPair
	AmountDiff        AmountDiffPair
	TimingDiff        TimingDiffPair
	Unmatched         Transaction
	Duplicate         DuplicateGroup
	GroupedMatch      GroupedMatchedPair
	GroupedAmountDiff GroupedAmountDiffPair
	GroupedTimingDiff GroupedTimingDiffPair
	AmbiguousGroup    AmbiguousGroupPair
	ManyToManyMatch   ManyToManyMatchedPair
	ManyToManyAmount  ManyToManyAmountDiffPair
	ManyToManyTiming  ManyToManyTimingDiffPair
}

const (
	chunkMatch uint8 = iota + 1
	chunkAmountDiff
	chunkTimingDiff
	chunkUnmatched
	chunkDuplicate
	chunkGroupedMatch
	chunkGroupedAmountDiff
	chunkGroupedTimingDiff
	chunkAmbiguousGroup
	chunkManyToManyMatch
	chunkManyToManyAmountDiff
	chunkManyToManyTimingDiff
	chunkWarning
)

func newPartitionChunkWriter(path string, maxBytes int64) (*partitionChunkWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- path is generated in the private spill directory.
	if err != nil {
		return nil, err
	}
	return &partitionChunkWriter{file: f, enc: gob.NewEncoder(f), maxBytes: maxBytes}, nil
}

func (w *partitionChunkWriter) write(event partitionChunkEvent) error {
	if w.closed {
		return errors.New("partition result chunk is closed")
	}
	if err := w.enc.Encode(event); err != nil {
		return err
	}
	if w.maxBytes > 0 {
		info, err := w.file.Stat()
		if err != nil {
			return err
		}
		if info.Size() > w.maxBytes {
			return errors.New("partition result chunk exceeds configured max bytes")
		}
	}
	return nil
}

func (w *partitionChunkWriter) close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.file.Close()
}

func (w *partitionChunkWriter) WriteMatch(p MatchedPair) error {
	return w.write(partitionChunkEvent{Kind: chunkMatch, Match: p})
}
func (w *partitionChunkWriter) WriteAmountDiff(p AmountDiffPair) error {
	return w.write(partitionChunkEvent{Kind: chunkAmountDiff, AmountDiff: p})
}
func (w *partitionChunkWriter) WriteTimingDiff(p TimingDiffPair) error {
	return w.write(partitionChunkEvent{Kind: chunkTimingDiff, TimingDiff: p})
}
func (w *partitionChunkWriter) WriteUnmatched(tx Transaction, side string) error {
	return w.write(partitionChunkEvent{Kind: chunkUnmatched, Side: side, Unmatched: tx})
}
func (w *partitionChunkWriter) WriteDuplicate(group DuplicateGroup) error {
	return w.write(partitionChunkEvent{Kind: chunkDuplicate, Duplicate: group})
}
func (w *partitionChunkWriter) WriteSummary(s Summary) error {
	w.summary = addSummaries(w.summary, s)
	return nil
}
func (w *partitionChunkWriter) Flush() error { return nil }

func (w *partitionChunkWriter) ObserveWarning(warning Warning) {
	// Warning observation is intentionally best effort, matching the public
	// WarningObserver contract. Encoding errors surface through the worker's
	// event path only; a warning must never change reconciliation semantics.
	if w.warningErr == nil {
		w.warningErr = w.write(partitionChunkEvent{Kind: chunkWarning, Warning: warning})
	}
}

func (w *partitionChunkWriter) WriteGroupedMatch(p GroupedMatchedPair) error {
	return w.write(partitionChunkEvent{Kind: chunkGroupedMatch, GroupedMatch: p})
}
func (w *partitionChunkWriter) WriteGroupedAmountDiff(p GroupedAmountDiffPair) error {
	return w.write(partitionChunkEvent{Kind: chunkGroupedAmountDiff, GroupedAmountDiff: p})
}
func (w *partitionChunkWriter) WriteGroupedTimingDiff(p GroupedTimingDiffPair) error {
	return w.write(partitionChunkEvent{Kind: chunkGroupedTimingDiff, GroupedTimingDiff: p})
}
func (w *partitionChunkWriter) WriteAmbiguousGroup(p AmbiguousGroupPair) error {
	return w.write(partitionChunkEvent{Kind: chunkAmbiguousGroup, AmbiguousGroup: p})
}
func (w *partitionChunkWriter) WriteManyToManyMatch(p ManyToManyMatchedPair) error {
	return w.write(partitionChunkEvent{Kind: chunkManyToManyMatch, ManyToManyMatch: p})
}
func (w *partitionChunkWriter) WriteManyToManyAmountDiff(p ManyToManyAmountDiffPair) error {
	return w.write(partitionChunkEvent{Kind: chunkManyToManyAmountDiff, ManyToManyAmount: p})
}
func (w *partitionChunkWriter) WriteManyToManyTimingDiff(p ManyToManyTimingDiffPair) error {
	return w.write(partitionChunkEvent{Kind: chunkManyToManyTimingDiff, ManyToManyTiming: p})
}

func replayPartitionChunk(ctx context.Context, path string, target ResultWriter) error {
	f, err := os.Open(path) // #nosec G304 -- path is generated in the private spill directory.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	dec := gob.NewDecoder(f)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event partitionChunkEvent
		err := dec.Decode(&event)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch event.Kind {
		case chunkMatch:
			err = target.WriteMatch(event.Match)
		case chunkAmountDiff:
			err = target.WriteAmountDiff(event.AmountDiff)
		case chunkTimingDiff:
			err = target.WriteTimingDiff(event.TimingDiff)
		case chunkUnmatched:
			err = target.WriteUnmatched(event.Unmatched, event.Side)
		case chunkDuplicate:
			err = target.WriteDuplicate(event.Duplicate)
		case chunkGroupedMatch:
			if writer, ok := target.(GroupedEventWriter); ok {
				err = writer.WriteGroupedMatch(event.GroupedMatch)
			}
		case chunkGroupedAmountDiff:
			if writer, ok := target.(GroupedEventWriter); ok {
				err = writer.WriteGroupedAmountDiff(event.GroupedAmountDiff)
			}
		case chunkGroupedTimingDiff:
			if writer, ok := target.(GroupedEventWriter); ok {
				err = writer.WriteGroupedTimingDiff(event.GroupedTimingDiff)
			}
		case chunkAmbiguousGroup:
			if writer, ok := target.(GroupedEventWriter); ok {
				err = writer.WriteAmbiguousGroup(event.AmbiguousGroup)
			}
		case chunkManyToManyMatch:
			if writer, ok := target.(ManyToManyEventWriter); ok {
				err = writer.WriteManyToManyMatch(event.ManyToManyMatch)
			}
		case chunkManyToManyAmountDiff:
			if writer, ok := target.(ManyToManyEventWriter); ok {
				err = writer.WriteManyToManyAmountDiff(event.ManyToManyAmount)
			}
		case chunkManyToManyTimingDiff:
			if writer, ok := target.(ManyToManyEventWriter); ok {
				err = writer.WriteManyToManyTimingDiff(event.ManyToManyTiming)
			}
		case chunkWarning:
			observeWarning(target, nil, event.Warning)
		default:
			return errors.New("partition result chunk contains an unknown event")
		}
		if err != nil {
			return err
		}
	}
}

type partitionChunkDescriptor struct {
	partition int
	path      string
	summary   Summary
	bytes     int64
	manifest  string
	nextData  string
	nextRows  string
	sorted    []string
}

// processPartitionQueue runs partition work concurrently while the consumer
// remains the only owner of the final ResultWriter. Descriptors are bounded;
// event payloads live in private chunks on disk.
func processPartitionQueue(
	ctx context.Context,
	partitions, workers, queueCapacity int,
	metrics func(PartitionQueueMetrics),
	work func(context.Context, int) (partitionChunkDescriptor, error),
	consume func(partitionChunkDescriptor) error,
) error {
	if workers < 1 {
		workers = 1
	}
	if workers > partitions {
		workers = partitions
	}
	if queueCapacity < 1 {
		queueCapacity = workers * 2
		if queueCapacity < 1 {
			queueCapacity = 1
		}
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stats := newPartitionQueueStats(metrics)
	type result struct {
		descriptor partitionChunkDescriptor
		err        error
	}
	results := make(chan result, queueCapacity)
	var next int
	var nextMu sync.Mutex
	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for {
			nextMu.Lock()
			partition := next
			next++
			nextMu.Unlock()
			if partition >= partitions {
				return
			}
			descriptor, err := work(workCtx, partition)
			if err != nil {
				select {
				case results <- result{err: err}:
				case <-workCtx.Done():
				}
				cancel()
				return
			}
			if descriptor.bytes == 0 {
				if info, statErr := os.Stat(descriptor.path); statErr == nil {
					descriptor.bytes = info.Size()
				}
			}
			sent := false
			select {
			case results <- result{descriptor: descriptor}:
				sent = true
			default:
				stats.emit(func(m *PartitionQueueMetrics) { m.WorkerBlocks++ })
			}
			if !sent {
				select {
				case results <- result{descriptor: descriptor}:
					sent = true
				case <-workCtx.Done():
					_ = os.Remove(descriptor.path)
					return
				}
			}
			stats.emit(func(m *PartitionQueueMetrics) {
				m.QueueDepth++
				m.SpillBytes += descriptor.bytes
			})
		}
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	pending := make(map[int]partitionChunkDescriptor, queueCapacity)
	nextPartition := 0
	var firstErr error
	for item := range results {
		if item.err != nil {
			if firstErr == nil {
				firstErr = item.err
			}
			continue
		}
		pending[item.descriptor.partition] = item.descriptor
		for {
			descriptor, ok := pending[nextPartition]
			if !ok {
				break
			}
			delete(pending, nextPartition)
			if firstErr == nil {
				stats.emit(func(m *PartitionQueueMetrics) {
					if m.QueueDepth > 0 {
						m.QueueDepth--
					}
					m.SpillBytes -= descriptor.bytes
					if m.SpillBytes < 0 {
						m.SpillBytes = 0
					}
					m.ChunksCompleted++
					m.WriterBytes += descriptor.bytes
				})
				if err := consume(descriptor); err != nil {
					firstErr = err
					cancel()
				}
			} else {
				stats.emit(func(m *PartitionQueueMetrics) {
					if m.QueueDepth > 0 {
						m.QueueDepth--
					}
					m.SpillBytes -= descriptor.bytes
					if m.SpillBytes < 0 {
						m.SpillBytes = 0
					}
				})
				_ = os.Remove(descriptor.path)
			}
			nextPartition++
		}
	}
	if firstErr != nil {
		for _, descriptor := range pending {
			_ = os.Remove(descriptor.path)
		}
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if nextPartition != partitions {
		return errors.New("partition worker queue ended before all partitions completed")
	}
	return nil
}
