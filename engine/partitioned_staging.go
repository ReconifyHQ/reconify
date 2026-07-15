package engine

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

const groupedSortChunkRows = 8192

type groupedPartitionFiles struct {
	data  []string
	rows  []string
	count int
}

func partitionCSVWithSidecars(ctx context.Context, input, keyCol, prefix string, n int, progress func(int)) (groupedPartitionFiles, error) {
	parts, _, err := partitionCSVWithDuplicateSpill(ctx, input, keyCol, "", prefix, n, progress)
	return parts, err
}

// partitionCSVWithDuplicateSpill stages the matching partition files and,
// when duplicateKeyCol is non-empty, a second set of files hashed by the
// duplicate grouping key in the same input pass. Both sets use row sidecars so
// the staging phase does not retain original row mappings in memory.
func partitionCSVWithDuplicateSpill(ctx context.Context, input, keyCol, duplicateKeyCol, prefix string, n int, progress func(int)) (groupedPartitionFiles, groupedPartitionFiles, error) {
	if n < 2 {
		return groupedPartitionFiles{}, groupedPartitionFiles{}, fmt.Errorf("partition count must be at least 2 (got %d)", n)
	}
	in, err := os.Open(input) // #nosec G304 -- input is an explicit caller-selected reconciliation file.
	if err != nil {
		return groupedPartitionFiles{}, groupedPartitionFiles{}, err
	}
	defer func() { _ = in.Close() }()

	r := csv.NewReader(in)
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return groupedPartitionFiles{}, groupedPartitionFiles{}, err
	}
	keyIdx := csvColumnIndex(header, keyCol)
	if keyIdx < 0 {
		return groupedPartitionFiles{}, groupedPartitionFiles{}, fmt.Errorf("partition key column %q not found", keyCol)
	}
	duplicateKeyIdx := -1
	if duplicateKeyCol != "" {
		duplicateKeyIdx = csvColumnIndex(header, duplicateKeyCol)
		if duplicateKeyIdx < 0 {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, fmt.Errorf("duplicate grouping column %q not found", duplicateKeyCol)
		}
	}

	files := make([]*os.File, n)
	rowFiles := make([]*os.File, n)
	writers := make([]*csv.Writer, n)
	rowWriters := make([]*bufio.Writer, n)
	dataPaths := make([]string, n)
	rowPaths := make([]string, n)
	duplicateFiles := make([]*os.File, n)
	duplicateRowFiles := make([]*os.File, n)
	duplicateWriters := make([]*csv.Writer, n)
	duplicateRowWriters := make([]*bufio.Writer, n)
	duplicateDataPaths := make([]string, n)
	duplicateRowPaths := make([]string, n)
	closeAll := func() {
		for i := range files {
			if files[i] != nil {
				_ = files[i].Close()
			}
			if rowFiles[i] != nil {
				_ = rowFiles[i].Close()
			}
			if duplicateFiles[i] != nil {
				_ = duplicateFiles[i].Close()
			}
			if duplicateRowFiles[i] != nil {
				_ = duplicateRowFiles[i].Close()
			}
		}
	}
	defer closeAll()

	for i := 0; i < n; i++ {
		dataPaths[i] = fmt.Sprintf("%s-%03d.csv", prefix, i)
		rowPaths[i] = fmt.Sprintf("%s-%03d.rows", prefix, i)
		if err := os.MkdirAll(filepath.Dir(dataPaths[i]), 0o750); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		f, err := os.Create(dataPaths[i])
		if err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		files[i] = f
		writers[i] = csv.NewWriter(f)
		if err := writers[i].Write(header); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}

		rf, err := os.Create(rowPaths[i])
		if err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		rowFiles[i] = rf
		rowWriters[i] = bufio.NewWriterSize(rf, 64<<10)
		if duplicateKeyCol != "" {
			duplicateDataPaths[i] = fmt.Sprintf("%s-duplicates-%03d.csv", prefix, i)
			duplicateRowPaths[i] = fmt.Sprintf("%s-duplicates-%03d.rows", prefix, i)
			df, err := os.Create(duplicateDataPaths[i])
			if err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
			duplicateFiles[i] = df
			duplicateWriters[i] = csv.NewWriter(df)
			if err := duplicateWriters[i].Write(header); err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
			drf, err := os.Create(duplicateRowPaths[i])
			if err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
			duplicateRowFiles[i] = drf
			duplicateRowWriters[i] = bufio.NewWriterSize(drf, 64<<10)
		}
	}

	rows := 0
	for rowNum := 2; ; rowNum++ {
		if err := ctx.Err(); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, fmt.Errorf("row %d: %w", rowNum, err)
		}
		key := ""
		if keyIdx < len(record) {
			key = strings.TrimSpace(record[keyIdx])
		}
		h := fnv.New32a()
		_, _ = h.Write([]byte(key))
		p := int(uint64(h.Sum32()) % uint64(n)) // #nosec G115 -- result is bounded by positive partition count.
		if err := writers[p].Write(record); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		if _, err := rowWriters[p].WriteString(strconv.Itoa(rowNum - 1)); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		if err := rowWriters[p].WriteByte('\n'); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		if duplicateKeyCol != "" {
			duplicateKey := ""
			if duplicateKeyIdx < len(record) {
				duplicateKey = strings.TrimSpace(record[duplicateKeyIdx])
			}
			duplicateHash := fnv.New32a()
			_, _ = duplicateHash.Write([]byte(duplicateKey))
			duplicatePart := int(uint64(duplicateHash.Sum32()) % uint64(n)) // #nosec G115 -- n is validated above.
			if err := duplicateWriters[duplicatePart].Write(record); err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
			if _, err := duplicateRowWriters[duplicatePart].WriteString(strconv.Itoa(rowNum - 1)); err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
			if err := duplicateRowWriters[duplicatePart].WriteByte('\n'); err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
		}
		rows++
		if progress != nil {
			progress(rows)
		}
	}

	for i := range writers {
		writers[i].Flush()
		if err := writers[i].Error(); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		if err := rowWriters[i].Flush(); err != nil {
			return groupedPartitionFiles{}, groupedPartitionFiles{}, err
		}
		if duplicateKeyCol != "" {
			duplicateWriters[i].Flush()
			if err := duplicateWriters[i].Error(); err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
			if err := duplicateRowWriters[i].Flush(); err != nil {
				return groupedPartitionFiles{}, groupedPartitionFiles{}, err
			}
		}
	}
	main := groupedPartitionFiles{data: dataPaths, rows: rowPaths, count: rows}
	if duplicateKeyCol == "" {
		return main, groupedPartitionFiles{}, nil
	}
	return main, groupedPartitionFiles{data: duplicateDataPaths, rows: duplicateRowPaths, count: rows}, nil
}

func csvColumnIndex(header []string, keyCol string) int {
	for i, col := range header {
		if strings.EqualFold(strings.TrimSpace(col), strings.TrimSpace(keyCol)) {
			return i
		}
	}
	return -1
}

func removeGroupedPartitionInputs(dataPath, rowPath string) error {
	if err := os.Remove(dataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(rowPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func groupedPartitionSelector(pair config.Pair) (string, error) {
	for _, pass := range pair.Passes {
		if pass.Type != config.PassTypeOneToMany && pass.Type != config.PassTypeManyToMany {
			continue
		}
		return pass.ResolvedGroupBy(), nil
	}
	return "", errors.New("grouped partition selector is missing")
}

func groupedTransactionKey(tx Transaction, selector string) string {
	switch selector {
	case config.GroupByName:
		return tx.Name
	case config.GroupByGroupKey:
		return tx.GroupKey
	default:
		return tx.Reference
	}
}
