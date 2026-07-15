package cli

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

const (
	defaultPartitionCount                = 32
	resourceHeadroomBytes          int64 = 64 << 20
	resourceRowBytes               int64 = 128
	resourceMapEntryBytes          int64 = 32
	resourceGroupedResultBytes     int64 = 64
	resourceGroupedSortBufferBytes int64 = 4 << 20
	autoPartitionTargetRows        int64 = 1_000_000
	maxAutoPartitionCount                = 4096
)

type indexResourceEstimate struct {
	RightBytes              int64
	LeftBytes               int64
	RightRows               int64
	LeftRows                int64
	RetainedFieldBytes      int64
	LeftRetainedFieldBytes  int64
	MemoryIndexBytes        int64
	DiskMemoryBytes         int64
	DiskIndexBytes          int64
	PartitionMemoryBytes    int64
	PartitionTempDiskBytes  int64
	PerIndexMemoryBytes     int64
	PerIndexDiskMemoryBytes int64
	SharedMemoryBytes       int64
}

func resolvePartitionCount(indexCfg config.IndexCfg, left, right inputShape, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int) int {
	if indexCfg.PartitionCount >= 2 {
		return indexCfg.PartitionCount
	}
	rows := left.rows
	if right.rows > rows {
		rows = right.rows
	}
	count := defaultPartitionCount
	for count < maxAutoPartitionCount && (rows+int64(count)-1)/int64(count) > autoPartitionTargetRows {
		count *= 2
	}
	if indexCfg.MaxMemoryMB <= 0 {
		return count
	}
	budget := mbToBytes(indexCfg.MaxMemoryMB)
	for candidate := count; candidate <= maxAutoPartitionCount; candidate *= 2 {
		estimate := estimateIndexResourcesFromShapes(left, right, leftCfg, rightCfg, pair, candidate, counterpartCount)
		if estimate.PartitionMemoryBytes <= budget {
			return candidate
		}
	}
	return maxAutoPartitionCount
}

type inputShape struct {
	bytes      int64
	rows       int64
	fieldBytes int64
}

func estimateIndexResources(leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, partitionCount, counterpartCount int, detailed bool) (indexResourceEstimate, error) {
	left, err := inspectInputShape(leftPath, leftCfg, detailed)
	if err != nil {
		return indexResourceEstimate{}, fmt.Errorf("inspect left input: %w", err)
	}
	right, err := inspectInputShape(rightPath, rightCfg, detailed)
	if err != nil {
		return indexResourceEstimate{}, fmt.Errorf("inspect right input: %w", err)
	}
	return estimateIndexResourcesFromShapes(left, right, leftCfg, rightCfg, pair, partitionCount, counterpartCount), nil
}

func estimateIndexResourcesFromShapes(left, right inputShape, leftCfg, rightCfg config.ParserCfg, pair config.Pair, partitionCount, counterpartCount int) indexResourceEstimate {
	estimate := indexResourceEstimate{
		RightBytes:             right.bytes,
		LeftBytes:              left.bytes,
		RightRows:              right.rows,
		LeftRows:               left.rows,
		RetainedFieldBytes:     right.fieldBytes,
		LeftRetainedFieldBytes: left.fieldBytes,
	}
	retained := right.rows*resourceRowBytes + right.fieldBytes + right.rows*32
	if retained < resourceHeadroomBytes {
		retained = resourceHeadroomBytes
	}
	rightIndexBytes := retained*2 + resourceHeadroomBytes
	rightTrackingBytes := estimateReferenceTracking(right.rows, rightCfg.ResolvedDuplicatePolicy(), right.fieldBytes, right.rows)
	leftTrackingBytes := estimateReferenceTracking(left.rows, leftCfg.ResolvedDuplicatePolicy(), left.fieldBytes, left.rows)
	leftPayloadBytes := estimateRowPayload(left.rows, left.bytes)
	leftBufferBytes := int64(0)
	if pair.NameMode == "tokens" || containsIndexPass(pair.Passes, config.PassTypeNameTokensOneToOne) || counterpartCount > 1 || leftCfg.ResolvedDuplicatePolicy() == config.DuplicatePolicyLatest {
		leftBufferBytes = leftPayloadBytes
	}
	if containsBatchOnlyGroupedPass(pair.Passes) {
		// Grouped reconciliation externally sorts partition rows and retains only
		// bounded sort buffers plus the current grouped result.
		leftBufferBytes += leftPayloadBytes + estimateRowPayload(right.rows, right.bytes) +
			(left.rows+right.rows)*resourceGroupedResultBytes + resourceGroupedSortBufferBytes
	}
	estimate.SharedMemoryBytes = leftTrackingBytes + leftBufferBytes
	estimate.PerIndexMemoryBytes = rightIndexBytes + rightTrackingBytes
	estimate.PerIndexDiskMemoryBytes = resourceHeadroomBytes + rightTrackingBytes
	estimate.MemoryIndexBytes = estimate.PerIndexMemoryBytes + estimate.SharedMemoryBytes
	estimate.DiskMemoryBytes = estimate.PerIndexDiskMemoryBytes + estimate.SharedMemoryBytes
	estimate.DiskIndexBytes = right.bytes*3 + resourceHeadroomBytes
	if estimate.DiskIndexBytes < resourceHeadroomBytes {
		estimate.DiskIndexBytes = resourceHeadroomBytes
	}
	if partitionCount < 2 {
		partitionCount = defaultPartitionCount
	}
	partitionRows := (right.rows + int64(partitionCount) - 1) / int64(partitionCount)
	partitionIndexBytes := (rightIndexBytes - resourceHeadroomBytes + int64(partitionCount) - 1) / int64(partitionCount)
	partitionFieldBytes := (right.fieldBytes + int64(partitionCount) - 1) / int64(partitionCount)
	partitionTrackingBytes := estimateReferenceTracking(partitionRows, rightCfg.ResolvedDuplicatePolicy(), partitionFieldBytes, partitionRows)
	partitionLeftRows := (left.rows + int64(partitionCount) - 1) / int64(partitionCount)
	partitionLeftFieldBytes := (left.fieldBytes + int64(partitionCount) - 1) / int64(partitionCount)
	partitionLeftTracking := estimateReferenceTracking(partitionLeftRows, leftCfg.ResolvedDuplicatePolicy(), partitionLeftFieldBytes, partitionLeftRows)
	estimate.PartitionMemoryBytes = resourceHeadroomBytes + partitionIndexBytes + partitionTrackingBytes + partitionLeftTracking
	if containsBatchOnlyGroupedPass(pair.Passes) {
		partitionLeftRows := (left.rows + int64(partitionCount) - 1) / int64(partitionCount)
		partitionLeftFieldBytes := (left.fieldBytes + int64(partitionCount) - 1) / int64(partitionCount)
		partitionLeftTracking := estimateReferenceTracking(partitionLeftRows, leftCfg.ResolvedDuplicatePolicy(), partitionLeftFieldBytes, partitionLeftRows)
		partitionLeftPayload := estimateRowPayload(partitionLeftRows, partitionLeftFieldBytes)
		partitionRightPayload := estimateRowPayload(partitionRows, partitionFieldBytes)
		partitionResultBytes := (partitionLeftRows + partitionRows) * resourceGroupedResultBytes
		estimate.PartitionMemoryBytes = resourceHeadroomBytes + partitionIndexBytes + partitionTrackingBytes +
			partitionLeftTracking + partitionLeftPayload + partitionRightPayload + partitionResultBytes + resourceGroupedSortBufferBytes
	}
	estimate.PartitionTempDiskBytes = estimate.LeftBytes + estimate.RightBytes + resourceHeadroomBytes
	if counterpartCount > 1 {
		// Carry-forward replaces the active left partitions incrementally. Reserve
		// one extra left-sized generation for the partition currently being copied.
		estimate.PartitionTempDiskBytes += estimate.LeftBytes
	}
	if containsBatchOnlyGroupedPass(pair.Passes) {
		// Staging retains the original partitions while external sort runs and
		// sorted outputs are created. Reserve two additional input-sized copies for
		// this transient scratch space.
		estimate.PartitionTempDiskBytes += 2 * (estimate.LeftBytes + estimate.RightBytes)
	}
	if !containsBatchOnlyGroupedPass(pair.Passes) &&
		(leftCfg.ResolvedDuplicatePolicy() != config.DuplicatePolicyKeep || rightCfg.ResolvedDuplicatePolicy() != config.DuplicatePolicyKeep) {
		// Duplicate grouping is staged into a second set of hash buckets and
		// externally sorted before matching. Reserve the duplicate copy plus its
		// transient sorted output for the active side.
		estimate.PartitionTempDiskBytes += 2 * (estimate.LeftBytes + estimate.RightBytes)
	}
	return estimate
}

func estimatePartitionDispositionMemory(shape inputShape, cfg config.ParserCfg) int64 {
	bytes := estimateReferenceTracking(shape.rows, cfg.ResolvedDuplicatePolicy(), shape.fieldBytes, shape.rows)
	if cfg.ResolvedDuplicatePolicy() == config.DuplicatePolicyFlag {
		// Duplicate annotation retains every duplicated transaction until its
		// group is emitted. Budget the worst case where every input row is a duplicate.
		bytes += estimateRowPayload(shape.rows, shape.fieldBytes)
	}
	return bytes
}

func inspectInputShape(path string, cfg config.ParserCfg, detailed bool) (inputShape, error) {
	if path == "" {
		return inputShape{}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return inputShape{}, err
	}
	shape := inputShape{bytes: info.Size()}
	if detailed && isCSVPath(path, cfg) {
		return scanCSVShape(path, cfg)
	}
	shape.rows, shape.fieldBytes = approximateCSVShape(shape.bytes)
	return shape, nil
}

func approximateCSVShape(bytes int64) (int64, int64) {
	if bytes <= 0 {
		return 0, 0
	}
	rows := bytes / resourceRowBytes
	if rows == 0 {
		rows = 1
	}
	return rows, bytes
}

func estimateReferenceTracking(rows int64, policy config.DuplicatePolicy, fieldBytes, payloadRows int64) int64 {
	if rows <= 0 {
		return 0
	}
	var bytes int64
	switch policy {
	case config.DuplicatePolicyFlag, config.DuplicatePolicyMerge, config.DuplicatePolicyLatest:
		bytes = rows * resourceMapEntryBytes
	}
	if policy == config.DuplicatePolicyLatest {
		bytes += estimateRowPayload(payloadRows, fieldBytes)
	}
	return bytes
}

func estimateRowPayload(rows, fieldBytes int64) int64 {
	if rows <= 0 {
		return 0
	}
	perRow := resourceRowBytes
	if avg := fieldBytes / rows; avg > 0 {
		perRow += avg
	}
	return rows * perRow
}

func containsIndexPass(passes []config.PassConfig, passType string) bool {
	for _, pass := range passes {
		if pass.Type == passType {
			return true
		}
	}
	return false
}

func scanCSVShape(path string, cfg config.ParserCfg) (inputShape, error) {
	var shape inputShape
	f, err := os.Open(path) // #nosec G304 -- caller resolved the explicit input path.
	if err != nil {
		return shape, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return shape, err
	}
	columns := make(map[string]int, len(header))
	for i, name := range header {
		columns[strings.ToLower(strings.TrimSpace(name))] = i
	}
	refIndex := columnIndex(columns, cfg.RefCol)
	currencyIndex := columnIndex(columns, cfg.CurrencyCol)
	nameIndex := columnIndex(columns, cfg.NameCol)
	for {
		record, readErr := r.Read()
		if readErr == io.EOF {
			shape.bytes, _ = fileSize(path)
			return shape, nil
		}
		if readErr != nil {
			return shape, readErr
		}
		shape.rows++
		shape.fieldBytes += fieldLen(record, refIndex) + fieldLen(record, currencyIndex) + fieldLen(record, nameIndex)
	}
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func columnIndex(columns map[string]int, name string) int {
	if name == "" {
		return -1
	}
	index, ok := columns[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return -1
	}
	return index
}

func fieldLen(record []string, index int) int64 {
	if index < 0 || index >= len(record) {
		return 0
	}
	return int64(len(record[index]))
}

func isCSVPath(path string, cfg config.ParserCfg) bool {
	typ := strings.ToLower(cfg.Type)
	return typ == "csv" || (typ == "" && strings.EqualFold(filepath.Ext(path), ".csv")) || (typ == "auto" && strings.EqualFold(filepath.Ext(path), ".csv"))
}
