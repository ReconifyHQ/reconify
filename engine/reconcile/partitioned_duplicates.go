//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"bufio"
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/parser"
)

// partitionedDuplicateDisposition contains only spill paths and scalar counts.
// Duplicate rows and representative rows are never retained for the complete
// input in memory.
type partitionedDuplicateDisposition struct {
	source             string
	cfg                config.ParserCfg
	policy             config.DuplicatePolicy
	sortedData         []string
	sortedRows         []string
	representativeRows []string
	duplicateCount     int
}

func partitionedPolicyConfig(cfg config.ParserCfg) config.ParserCfg {
	if cfg.ResolvedDuplicatePolicy() == config.DuplicatePolicyKeep {
		return cfg
	}
	copy := cfg
	copy.DuplicatePolicy = config.DuplicatePolicyKeep
	return copy
}

func effectiveGroupColumn(cfg config.ParserCfg) string {
	if strings.TrimSpace(cfg.GroupCol) != "" {
		return cfg.GroupCol
	}
	return cfg.RefCol
}

func duplicateSpillColumn(cfg config.ParserCfg) string {
	if cfg.ResolvedDuplicatePolicy() == config.DuplicatePolicyKeep {
		return ""
	}
	return effectiveGroupColumn(cfg)
}

func partitionedKey(tx Transaction, selector string) string {
	switch selector {
	case "name":
		return tx.Name
	case "group_key":
		return tx.GroupKey
	default:
		return tx.Reference
	}
}

func hashPartition(key string, partitions int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(uint64(h.Sum32()) % uint64(partitions)) // #nosec G115 -- partitions is validated by callers.
}

func adaptivePartitionCountFromFiles(paths ...string) int {
	const (
		defaultCount = 32
		maxCount     = 4096
		targetRows   = int64(1_000_000)
		rowBytes     = int64(128)
	)
	var maxBytes int64
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && info.Size() > maxBytes {
			maxBytes = info.Size()
		}
	}
	rows := maxBytes / rowBytes
	count := defaultCount
	for count < maxCount && (rows+int64(count)-1)/int64(count) > targetRows {
		count *= 2
	}
	return count
}

// buildPartitionedDuplicateDisposition externally sorts group-key spill files
// and produces either duplicate groups or per-matching-partition representative
// sidecars. The sorted files remain available until Cleanup is called.
func buildPartitionedDuplicateDisposition(
	ctx context.Context,
	source string,
	cfg config.ParserCfg,
	selector string,
	groupParts groupedPartitionFiles,
	prefix string,
	partitions int,
) (*partitionedDuplicateDisposition, error) {
	policy := cfg.ResolvedDuplicatePolicy()
	if policy == config.DuplicatePolicyKeep {
		return nil, nil
	}
	d := &partitionedDuplicateDisposition{
		source: source,
		cfg:    cfg,
		policy: policy,
	}
	for i := range groupParts.data {
		if err := ctx.Err(); err != nil {
			d.Cleanup()
			return nil, err
		}
		sortedData, sortedRows, err := sortGroupedPartition(
			ctx,
			groupParts.data[i],
			groupParts.rows[i],
			fmt.Sprintf("%s-sorted-%03d", prefix, i),
			effectiveGroupColumn(cfg),
		)
		if err != nil {
			d.Cleanup()
			return nil, err
		}
		d.sortedData = append(d.sortedData, sortedData)
		d.sortedRows = append(d.sortedRows, sortedRows)
		if err := removeGroupedPartitionInputs(groupParts.data[i], groupParts.rows[i]); err != nil {
			d.Cleanup()
			return nil, err
		}
	}

	if policy == config.DuplicatePolicyFlag {
		for i := range d.sortedData {
			err := scanSortedDuplicateGroups(ctx, source, d.sortedData[i], d.sortedRows[i], cfg, func(group DuplicateGroup) error {
				if len(group.Transactions) < 2 {
					return nil
				}
				d.duplicateCount += len(group.Transactions)
				return nil
			})
			if err != nil {
				d.Cleanup()
				return nil, err
			}
		}
		return d, nil
	}

	d.representativeRows = make([]string, partitions)
	for i := range d.representativeRows {
		d.representativeRows[i] = fmt.Sprintf("%s-representatives-%03d.rows", prefix, i)
	}
	files := make([]*os.File, partitions)
	writers := make([]*bufio.Writer, partitions)
	closeFiles := func() {
		for _, f := range files {
			if f != nil {
				_ = f.Close()
			}
		}
	}
	for i, path := range d.representativeRows {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			closeFiles()
			d.Cleanup()
			return nil, err
		}
		f, err := os.Create(path) // #nosec G304 -- path is generated inside the private spill directory.
		if err != nil {
			closeFiles()
			d.Cleanup()
			return nil, err
		}
		files[i] = f
		writers[i] = bufio.NewWriterSize(f, 64<<10)
	}
	for i := range d.sortedData {
		latest := policy == config.DuplicatePolicyLatest
		if err := scanSortedDuplicateRepresentatives(ctx, source, d.sortedData[i], d.sortedRows[i], cfg, latest, func(tx Transaction, originalRow int) error {
			part := hashPartition(partitionedKey(tx, selector), partitions)
			_, err := writers[part].WriteString(strconv.Itoa(originalRow) + "\n")
			return err
		}); err != nil {
			closeFiles()
			d.Cleanup()
			return nil, err
		}
	}
	for i := range writers {
		if err := writers[i].Flush(); err != nil {
			closeFiles()
			d.Cleanup()
			return nil, err
		}
	}
	closeFiles()
	return d, nil
}

func (d *partitionedDuplicateDisposition) Emit(w ResultWriter, ctx context.Context) error {
	if d == nil || d.policy != config.DuplicatePolicyFlag {
		return nil
	}
	for i := range d.sortedData {
		if err := scanSortedDuplicateGroups(ctx, d.source, d.sortedData[i], d.sortedRows[i], d.cfg, func(group DuplicateGroup) error {
			if len(group.Transactions) < 2 {
				return nil
			}
			return w.WriteDuplicate(group)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (d *partitionedDuplicateDisposition) Representatives(partition int) (map[int]struct{}, error) {
	if d == nil || len(d.representativeRows) == 0 {
		return nil, nil
	}
	if partition < 0 || partition >= len(d.representativeRows) {
		return nil, fmt.Errorf("representative partition %d out of range", partition)
	}
	rows, err := readPartitionRows(d.representativeRows[partition])
	if err != nil {
		return nil, err
	}
	set := make(map[int]struct{}, len(rows))
	for _, row := range rows {
		set[row] = struct{}{}
	}
	return set, nil
}

func (d *partitionedDuplicateDisposition) Cleanup() {
	if d == nil {
		return
	}
	for _, path := range d.sortedData {
		_ = os.Remove(path)
	}
	for _, path := range d.sortedRows {
		_ = os.Remove(path)
	}
	for _, path := range d.representativeRows {
		_ = os.Remove(path)
	}
}

// scanSortedDuplicateGroups parses one group-key-sorted spill file and retains
// only the current group, which is required to emit a DuplicateGroup.
func scanSortedDuplicateGroups(ctx context.Context, source, dataPath, rowsPath string, cfg config.ParserCfg, fn func(DuplicateGroup) error) error {
	var currentKey string
	var current []Transaction
	flush := func() error {
		if currentKey == "" || len(current) < 2 {
			current = current[:0]
			return nil
		}
		group := DuplicateGroup{Source: source, Reference: currentKey, Transactions: append([]Transaction(nil), current...)}
		if err := fn(group); err != nil {
			return err
		}
		current = current[:0]
		return nil
	}
	if err := scanSortedDuplicateRows(ctx, source, dataPath, rowsPath, cfg, func(tx Transaction, _ int) error {
		if tx.GroupKey != currentKey {
			if err := flush(); err != nil {
				return err
			}
			currentKey = tx.GroupKey
		}
		if tx.GroupKey != "" {
			current = append(current, tx)
		}
		return nil
	}); err != nil {
		return err
	}
	return flush()
}

func scanSortedDuplicateRepresentatives(ctx context.Context, source, dataPath, rowsPath string, cfg config.ParserCfg, latest bool, fn func(Transaction, int) error) error {
	var currentKey string
	var representative Transaction
	var representativeRow int
	have := false
	flush := func() error {
		if !have || currentKey == "" {
			return nil
		}
		return fn(representative, representativeRow)
	}
	if err := scanSortedDuplicateRows(ctx, source, dataPath, rowsPath, cfg, func(tx Transaction, originalRow int) error {
		if tx.GroupKey != currentKey {
			if err := flush(); err != nil {
				return err
			}
			currentKey = tx.GroupKey
			have = false
		}
		if tx.GroupKey == "" {
			return nil
		}
		if !have || latest {
			representative = tx
			representativeRow = originalRow
			have = true
		}
		return nil
	}); err != nil {
		return err
	}
	return flush()
}

func scanSortedDuplicateRows(ctx context.Context, source, dataPath, rowsPath string, cfg config.ParserCfg, fn func(Transaction, int) error) error {
	rowsFile, err := os.Open(rowsPath) // #nosec G304 -- path is generated inside the private spill directory.
	if err != nil {
		return err
	}
	defer func() { _ = rowsFile.Close() }()
	scanner := bufio.NewScanner(rowsFile)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	err = parser.ParseCSVEach(ctx, source, dataPath, cfg, func(tx Transaction, _ int) error {
		if !scanner.Scan() {
			if scanErr := scanner.Err(); scanErr != nil {
				return scanErr
			}
			return fmt.Errorf("duplicate spill row sidecar ended before data rows")
		}
		originalRow, parseErr := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if parseErr != nil || originalRow <= 0 {
			return fmt.Errorf("invalid duplicate spill row number %q", scanner.Text())
		}
		tx.ID = fmt.Sprintf("%s-%d", source, originalRow)
		return fn(tx, originalRow)
	})
	if err != nil {
		return err
	}
	if scanner.Scan() {
		return fmt.Errorf("duplicate spill row sidecar has more rows than data")
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}
