//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"bufio"
	"container/heap"
	"context"
	"encoding/csv"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
)

type groupedSortRecord struct {
	Key    string
	Row    int
	Fields []string
}

func sortGroupedPartition(ctx context.Context, dataPath, rowPath, prefix, keyCol string) (dataOut, rowsOut string, err error) {
	in, err := os.Open(dataPath) // #nosec G304 -- path was created by the partition staging pass.
	if err != nil {
		return "", "", err
	}
	defer func() { _ = in.Close() }()
	rowIn, err := os.Open(rowPath) // #nosec G304 -- path was created by the partition staging pass.
	if err != nil {
		return "", "", err
	}
	defer func() { _ = rowIn.Close() }()

	r := csv.NewReader(in)
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return "", "", err
	}
	keyIdx := csvColumnIndex(header, keyCol)
	if keyIdx < 0 {
		return "", "", fmt.Errorf("partition key column %q not found", keyCol)
	}

	scanner := bufio.NewScanner(rowIn)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	runPaths := make([]string, 0)
	chunk := make([]groupedSortRecord, 0, groupedSortChunkRows)
	flushRun := func() error {
		if len(chunk) == 0 {
			return nil
		}
		sort.SliceStable(chunk, func(i, j int) bool {
			if chunk[i].Key != chunk[j].Key {
				return chunk[i].Key < chunk[j].Key
			}
			return chunk[i].Row < chunk[j].Row
		})
		runPath := fmt.Sprintf("%s-run-%05d.gob", prefix, len(runPaths))
		run, createErr := os.Create(runPath) // #nosec G304 -- runPath is generated inside the private spill directory.
		if createErr != nil {
			return createErr
		}
		enc := gob.NewEncoder(run)
		for i := range chunk {
			if encodeErr := enc.Encode(chunk[i]); encodeErr != nil {
				_ = run.Close()
				return encodeErr
			}
		}
		if closeErr := run.Close(); closeErr != nil {
			return closeErr
		}
		runPaths = append(runPaths, runPath)
		chunk = chunk[:0]
		return nil
	}
	defer func() {
		for _, path := range runPaths {
			_ = os.Remove(path)
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		record, readErr := r.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", readErr
		}
		if !scanner.Scan() {
			if scanErr := scanner.Err(); scanErr != nil {
				return "", "", scanErr
			}
			return "", "", errors.New("partition row sidecar ended before data rows")
		}
		row, parseErr := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if parseErr != nil || row <= 0 {
			return "", "", fmt.Errorf("invalid partition row number %q", scanner.Text())
		}
		key := ""
		if keyIdx < len(record) {
			key = strings.TrimSpace(record[keyIdx])
		}
		chunk = append(chunk, groupedSortRecord{
			Key:    key,
			Row:    row,
			Fields: append([]string(nil), record...),
		})
		if len(chunk) >= groupedSortChunkRows {
			if err := flushRun(); err != nil {
				return "", "", err
			}
		}
	}
	if scanner.Scan() {
		return "", "", errors.New("partition row sidecar has more rows than data")
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if err := flushRun(); err != nil {
		return "", "", err
	}

	dataOut = prefix + "-sorted.csv"
	rowsOut = prefix + "-sorted.rows"
	out, err := os.Create(dataOut) // #nosec G304 -- dataOut is generated inside the private spill directory.
	if err != nil {
		return "", "", err
	}
	defer func() { _ = out.Close() }()
	outRows, err := os.Create(rowsOut) // #nosec G304 -- rowsOut is generated inside the private spill directory.
	if err != nil {
		return "", "", err
	}
	defer func() { _ = outRows.Close() }()
	csvOut := csv.NewWriter(out)
	if err := csvOut.Write(header); err != nil {
		return "", "", err
	}
	rowWriter := bufio.NewWriterSize(outRows, 64<<10)

	if len(runPaths) == 0 {
		csvOut.Flush()
		if err := csvOut.Error(); err != nil {
			return "", "", err
		}
		if err := rowWriter.Flush(); err != nil {
			return "", "", err
		}
		return dataOut, rowsOut, nil
	}

	cursors, queue, err := openGroupedRunCursors(runPaths)
	if err != nil {
		return "", "", err
	}
	defer closeGroupedRunCursors(cursors)

	for queue.Len() > 0 {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		cursor := heap.Pop(&queue).(*groupedRunCursor)
		if err := csvOut.Write(cursor.current.Fields); err != nil {
			return "", "", err
		}
		if _, err := rowWriter.WriteString(strconv.Itoa(cursor.current.Row)); err != nil {
			return "", "", err
		}
		if err := rowWriter.WriteByte('\n'); err != nil {
			return "", "", err
		}
		if err := cursor.advance(); err != nil {
			return "", "", err
		}
		if !cursor.done {
			heap.Push(&queue, cursor)
		}
	}
	csvOut.Flush()
	if err := csvOut.Error(); err != nil {
		return "", "", err
	}
	if err := rowWriter.Flush(); err != nil {
		return "", "", err
	}
	return dataOut, rowsOut, nil
}

func openGroupedRunCursors(runPaths []string) ([]*groupedRunCursor, groupedRunHeap, error) {
	return openGroupedRunCursorsWith(runPaths,
		os.Open,
		func(cursor *groupedRunCursor) error {
			return cursor.advance()
		},
	)
}

func openGroupedRunCursorsWith(
	runPaths []string,
	open func(string) (*os.File, error),
	advance func(*groupedRunCursor) error,
) (cursors []*groupedRunCursor, queue groupedRunHeap, err error) {
	cursors = make([]*groupedRunCursor, len(runPaths))
	queue = make(groupedRunHeap, 0, len(runPaths))
	cleanupOnReturn := true
	defer func() {
		if cleanupOnReturn {
			closeGroupedRunCursors(cursors)
		}
	}()

	for i, path := range runPaths {
		f, openErr := open(path)
		if openErr != nil {
			return cursors, queue, openErr
		}
		cursor := &groupedRunCursor{index: i, file: f, decoder: gob.NewDecoder(f)}
		cursors[i] = cursor
		if advanceErr := advance(cursor); advanceErr != nil {
			return cursors, queue, advanceErr
		}
		if !cursor.done {
			heap.Push(&queue, cursor)
		}
	}
	cleanupOnReturn = false
	return cursors, queue, nil
}

func closeGroupedRunCursors(cursors []*groupedRunCursor) {
	for _, cursor := range cursors {
		if cursor != nil && cursor.file != nil {
			_ = cursor.file.Close()
		}
	}
}

type groupedRunCursor struct {
	index   int
	file    *os.File
	decoder *gob.Decoder
	current groupedSortRecord
	done    bool
}

func (c *groupedRunCursor) advance() error {
	var record groupedSortRecord
	err := c.decoder.Decode(&record)
	if errors.Is(err, io.EOF) {
		c.done = true
		return nil
	}
	if err != nil {
		return err
	}
	c.current = record
	return nil
}

type groupedRunHeap []*groupedRunCursor

func (h groupedRunHeap) Len() int { return len(h) }
func (h groupedRunHeap) Less(i, j int) bool {
	if h[i].current.Key != h[j].current.Key {
		return h[i].current.Key < h[j].current.Key
	}
	if h[i].current.Row != h[j].current.Row {
		return h[i].current.Row < h[j].current.Row
	}
	return h[i].index < h[j].index
}
func (h groupedRunHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *groupedRunHeap) Push(x any)   { *h = append(*h, x.(*groupedRunCursor)) }
func (h *groupedRunHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

type transactionStreamItem struct {
	tx  Transaction
	err error
}

type partitionTransactionStream struct {
	items  <-chan transactionStreamItem
	cancel context.CancelFunc
	done   <-chan struct{}
}

func newPartitionTransactionStream(ctx context.Context, source, dataPath, rowPath string, cfg config.ParserCfg) (*partitionTransactionStream, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	items := make(chan transactionStreamItem)
	done := make(chan struct{})
	go func() {
		defer close(items)
		defer close(done)
		send := func(item transactionStreamItem) {
			select {
			case items <- item:
			case <-streamCtx.Done():
			}
		}
		rowFile, err := os.Open(rowPath) // #nosec G304 -- path was created by the partition staging pass.
		if err != nil {
			send(transactionStreamItem{err: err})
			return
		}
		defer func() { _ = rowFile.Close() }()
		scanner := bufio.NewScanner(rowFile)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		parseErr := ParseEach(streamCtx, source, dataPath, cfg, func(tx Transaction, _ int) error {
			if !scanner.Scan() {
				if scanErr := scanner.Err(); scanErr != nil {
					return scanErr
				}
				return errors.New("partition row sidecar ended before parsed rows")
			}
			row, rowErr := strconv.Atoi(strings.TrimSpace(scanner.Text()))
			if rowErr != nil || row <= 0 {
				return fmt.Errorf("invalid partition row number %q", scanner.Text())
			}
			tx.ID = fmt.Sprintf("%s-%d", source, row)
			select {
			case items <- transactionStreamItem{tx: tx}:
				return nil
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
		})
		if parseErr == nil && scanner.Scan() {
			parseErr = errors.New("partition row sidecar has more rows than parsed data")
		}
		if parseErr == nil {
			parseErr = scanner.Err()
		}
		if parseErr != nil {
			send(transactionStreamItem{err: parseErr})
		}
	}()
	return &partitionTransactionStream{items: items, cancel: cancel, done: done}, nil
}

func (s *partitionTransactionStream) next(ctx context.Context) (Transaction, bool, error) {
	select {
	case item, ok := <-s.items:
		if !ok {
			return Transaction{}, false, nil
		}
		if item.err != nil {
			return Transaction{}, false, item.err
		}
		return item.tx, true, nil
	case <-ctx.Done():
		return Transaction{}, false, ctx.Err()
	}
}

func (s *partitionTransactionStream) close() {
	s.cancel()
	<-s.done
}

type partitionTransactionGroups struct {
	stream  *partitionTransactionStream
	keyOf   func(Transaction) string
	pending *Transaction
}

func (g *partitionTransactionGroups) next(ctx context.Context) (string, []Transaction, bool, error) {
	var first Transaction
	var err error
	if g.pending != nil {
		first = *g.pending
		g.pending = nil
	} else {
		var ok bool
		first, ok, err = g.stream.next(ctx)
		if err != nil || !ok {
			return "", nil, false, err
		}
	}

	key := g.keyOf(first)
	if key == "" {
		return key, []Transaction{first}, true, nil
	}
	group := []Transaction{first}
	for {
		tx, nextOK, nextErr := g.stream.next(ctx)
		if nextErr != nil {
			return "", nil, false, nextErr
		}
		if !nextOK {
			break
		}
		if g.keyOf(tx) != key {
			g.pending = &tx
			break
		}
		group = append(group, tx)
	}
	return key, group, true, nil
}

func reconcileGroupedPartition(
	ctx context.Context,
	pairName, leftSource, rightSource string,
	leftData, leftRows, rightData, rightRows string,
	leftCfg, rightCfg config.ParserCfg,
	pair config.Pair,
	agg *partitionSummaryWriter,
) error {
	selector, err := groupedPartitionSelector(pair)
	if err != nil {
		return err
	}
	leftStream, err := newPartitionTransactionStream(ctx, leftSource, leftData, leftRows, leftCfg)
	if err != nil {
		return fmt.Errorf("open left grouped partition: %w", err)
	}
	rightStream, err := newPartitionTransactionStream(ctx, rightSource, rightData, rightRows, rightCfg)
	if err != nil {
		leftStream.close()
		return fmt.Errorf("open right grouped partition: %w", err)
	}
	defer leftStream.close()
	defer rightStream.close()

	leftGroups := &partitionTransactionGroups{stream: leftStream, keyOf: func(tx Transaction) string {
		return groupedTransactionKey(tx, selector)
	}}
	rightGroups := &partitionTransactionGroups{stream: rightStream, keyOf: func(tx Transaction) string {
		return groupedTransactionKey(tx, selector)
	}}

	leftKey, leftGroup, leftOK, err := leftGroups.next(ctx)
	if err != nil {
		return fmt.Errorf("read left grouped partition: %w", err)
	}
	rightKey, rightGroup, rightOK, err := rightGroups.next(ctx)
	if err != nil {
		return fmt.Errorf("read right grouped partition: %w", err)
	}

	for leftOK || rightOK {
		if err := ctx.Err(); err != nil {
			return err
		}
		var left, right []Transaction
		switch {
		case leftOK && rightOK && leftKey == rightKey:
			left, right = leftGroup, rightGroup
			leftKey, leftGroup, leftOK, err = leftGroups.next(ctx)
			if err != nil {
				return fmt.Errorf("read left grouped partition: %w", err)
			}
			rightKey, rightGroup, rightOK, err = rightGroups.next(ctx)
			if err != nil {
				return fmt.Errorf("read right grouped partition: %w", err)
			}
		case !rightOK || (leftOK && leftKey < rightKey):
			left = leftGroup
			leftKey, leftGroup, leftOK, err = leftGroups.next(ctx)
			if err != nil {
				return fmt.Errorf("read left grouped partition: %w", err)
			}
		default:
			right = rightGroup
			rightKey, rightGroup, rightOK, err = rightGroups.next(ctx)
			if err != nil {
				return fmt.Errorf("read right grouped partition: %w", err)
			}
		}

		res, err := Reconcile(pairName, leftSource, rightSource, left, right, pair,
			ReconcileOptions{
				LeftPolicy:  leftCfg.ResolvedDuplicatePolicy(),
				RightPolicy: rightCfg.ResolvedDuplicatePolicy(),
			})
		if err != nil {
			return err
		}
		if err := WriteResultEvents(agg, res, false); err != nil {
			return err
		}
		if err := agg.WriteSummary(res.Summary); err != nil {
			return err
		}
	}
	return nil
}
