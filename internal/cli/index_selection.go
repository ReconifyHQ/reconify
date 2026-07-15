package cli

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine"
)

const (
	defaultAutoMaxRightFileMB      int64 = 2048
	defaultPartitionCount                = 32
	resourceHeadroomBytes          int64 = 64 << 20
	resourceRowBytes               int64 = 128
	resourceMapEntryBytes          int64 = 32
	resourceGroupedResultBytes     int64 = 64
	resourceGroupedSortBufferBytes int64 = 4 << 20
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

type indexSelectionDecision struct {
	Selection   engine.IndexSelection
	Partitioned bool
}

var freeDiskBytes = availableDiskBytes

func chooseIndexBackend(indexCfg config.IndexCfg, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int) (indexSelectionDecision, error) {
	resourcePolicy := indexCfg.MaxMemoryMB > 0 || indexCfg.MaxTempDiskMB > 0
	estimate, err := estimateIndexResources(leftPath, rightPath, leftCfg, rightCfg, pair, indexCfg.PartitionCount, counterpartCount, resourcePolicy)
	if err != nil {
		return indexSelectionDecision{}, fmt.Errorf("estimate index resources: %w", err)
	}
	requested := indexCfg.Backend
	if requested == "" {
		requested = "memory"
	}

	selection := engine.IndexSelection{
		RequestedBackend: requested,
	}
	if requested == "auto" {
		return chooseAutoBackend(indexCfg, leftPath, rightPath, leftCfg, rightCfg, pair, counterpartCount, estimate, selection)
	}

	reason, ok := backendFits(requested, indexCfg, leftPath, rightPath, leftCfg, rightCfg, pair, counterpartCount, estimate)
	if !ok {
		return indexSelectionDecision{}, fmt.Errorf("index backend %q rejected: %s", requested, reason)
	}
	selection.Backend = requested
	selection.Reason = reason
	applySelectedEstimate(&selection, requested, estimate)
	return indexSelectionDecision{Selection: selection, Partitioned: requested == "partitioned"}, nil
}

func chooseAutoBackend(indexCfg config.IndexCfg, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int, estimate indexResourceEstimate, selection engine.IndexSelection) (indexSelectionDecision, error) {
	var failures []engine.IndexFallback
	resourcePolicy := indexCfg.MaxMemoryMB > 0 || indexCfg.MaxTempDiskMB > 0
	threshold := indexCfg.AutoMaxRightFileMB
	if threshold <= 0 {
		threshold = defaultAutoMaxRightFileMB
	}

	candidates := []string{"memory", "disk", "partitioned"}
	if !resourcePolicy {
		switch {
		case containsBatchOnlyGroupedPass(pair.Passes) && estimate.RightBytes/(1024*1024) <= threshold:
			// Disk is a right-side streaming index and cannot execute grouped
			// passes. Prefer bounded partitioning once memory is over threshold.
			candidates = []string{"memory", "partitioned"}
		case containsBatchOnlyGroupedPass(pair.Passes):
			// Disk is a right-side streaming index and cannot execute grouped
			// passes. Prefer bounded partitioning once memory is over threshold.
			candidates = []string{"partitioned"}
		case estimate.RightBytes/(1024*1024) <= threshold:
			candidates = []string{"memory", "disk"}
		default:
			candidates = []string{"disk"}
		}
	}
	for _, candidate := range candidates {
		reason, ok := backendFits(candidate, indexCfg, leftPath, rightPath, leftCfg, rightCfg, pair, counterpartCount, estimate)
		if !ok {
			failures = append(failures, engine.IndexFallback{Backend: candidate, Reason: reason})
			continue
		}
		selection.Backend = candidate
		selection.Reason = reason
		selection.Fallbacks = failures
		applySelectedEstimate(&selection, candidate, estimate)
		return indexSelectionDecision{Selection: selection, Partitioned: candidate == "partitioned"}, nil
	}
	selection.Fallbacks = failures
	return indexSelectionDecision{}, fmt.Errorf("no suitable index backend: %s", formatIndexFailures(failures))
}

func backendFits(backend string, indexCfg config.IndexCfg, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int, estimate indexResourceEstimate) (string, bool) {
	switch backend {
	case "memory":
		if indexCfg.MaxMemoryMB > 0 && estimate.MemoryIndexBytes > mbToBytes(indexCfg.MaxMemoryMB) {
			return fmt.Sprintf("estimated memory %s exceeds max_memory_mb=%d", formatBytes(estimate.MemoryIndexBytes), indexCfg.MaxMemoryMB), false
		}
		return "memory estimate fits configured policy", true
	case "disk":
		if containsBatchOnlyGroupedPass(pair.Passes) {
			return "disk backend does not support grouped passes; use memory or partitioned", false
		}
		if indexCfg.MaxMemoryMB > 0 && estimate.DiskMemoryBytes > mbToBytes(indexCfg.MaxMemoryMB) {
			return fmt.Sprintf("estimated disk memory %s exceeds max_memory_mb=%d", formatBytes(estimate.DiskMemoryBytes), indexCfg.MaxMemoryMB), false
		}
		if reason, ok := diskFits(indexCfg, indexCfg.SpillDir, estimate.DiskIndexBytes); !ok {
			return reason, false
		}
		return "temporary disk estimate fits configured policy", true
	case "partitioned":
		if reason, ok := partitionedEligible(leftPath, rightPath, leftCfg, rightCfg, pair, counterpartCount); !ok {
			return reason, false
		}
		if indexCfg.MaxMemoryMB > 0 && estimate.PartitionMemoryBytes > mbToBytes(indexCfg.MaxMemoryMB) {
			return fmt.Sprintf("estimated partition memory %s exceeds max_memory_mb=%d", formatBytes(estimate.PartitionMemoryBytes), indexCfg.MaxMemoryMB), false
		}
		if reason, ok := diskFits(indexCfg, indexCfg.SpillDir, estimate.PartitionTempDiskBytes); !ok {
			return reason, false
		}
		return "partitioned estimate fits configured memory and disk policy", true
	default:
		return fmt.Sprintf("unsupported backend %q", backend), false
	}
}

func diskFits(indexCfg config.IndexCfg, path string, required int64) (string, bool) {
	if indexCfg.MaxTempDiskMB > 0 && required > mbToBytes(indexCfg.MaxTempDiskMB) {
		return fmt.Sprintf("estimated temporary disk %s exceeds max_temp_disk_mb=%d", formatBytes(required), indexCfg.MaxTempDiskMB), false
	}
	free, err := freeDiskBytes(path)
	if err != nil {
		return fmt.Sprintf("cannot inspect temporary disk: %v", err), false
	}
	if free < required {
		return fmt.Sprintf("available temporary disk %s is below required %s", formatBytes(free), formatBytes(required)), false
	}
	return "", true
}

func partitionedEligible(leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int) (string, bool) {
	if counterpartCount < 1 {
		return "partitioned backend requires at least one counterpart", false
	}
	if !isCSVPath(leftPath, leftCfg) || !isCSVPath(rightPath, rightCfg) {
		return "partitioned backend supports CSV inputs only", false
	}
	_, _, ok, reason := engine.PartitionKeyColumns(pair, leftCfg, rightCfg)
	if !ok {
		return reason, false
	}
	if safe, reason := engine.PartitionDuplicateSafe(pair, leftCfg, rightCfg); !safe {
		return reason, false
	}
	return "", true
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
	estimate.PartitionMemoryBytes = resourceHeadroomBytes + partitionIndexBytes + partitionTrackingBytes + leftTrackingBytes
	if counterpartCount > 1 && !containsBatchOnlyGroupedPass(pair.Passes) {
		partitionLeftRows := (left.rows + int64(partitionCount) - 1) / int64(partitionCount)
		partitionLeftFieldBytes := (left.fieldBytes + int64(partitionCount) - 1) / int64(partitionCount)
		partitionLeftTracking := estimateReferenceTracking(partitionLeftRows, leftCfg.ResolvedDuplicatePolicy(), partitionLeftFieldBytes, partitionLeftRows)
		estimate.PartitionMemoryBytes = resourceHeadroomBytes + partitionIndexBytes + partitionTrackingBytes + partitionLeftTracking +
			estimatePartitionDispositionMemory(left, leftCfg) + estimatePartitionDispositionMemory(right, rightCfg)
	}
	if containsBatchOnlyGroupedPass(pair.Passes) {
		partitionLeftRows := (left.rows + int64(partitionCount) - 1) / int64(partitionCount)
		partitionLeftFieldBytes := (left.fieldBytes + int64(partitionCount) - 1) / int64(partitionCount)
		partitionLeftTracking := estimateReferenceTracking(partitionLeftRows, leftCfg.ResolvedDuplicatePolicy(), partitionLeftFieldBytes, partitionLeftRows)
		partitionLeftPayload := estimateRowPayload(partitionLeftRows, partitionLeftFieldBytes)
		partitionRightPayload := estimateRowPayload(partitionRows, partitionFieldBytes)
		partitionResultBytes := (partitionLeftRows + partitionRows) * resourceGroupedResultBytes
		estimate.PartitionMemoryBytes = resourceHeadroomBytes + partitionIndexBytes + partitionTrackingBytes +
			partitionLeftTracking + partitionLeftPayload + partitionRightPayload + partitionResultBytes + resourceGroupedSortBufferBytes
		if counterpartCount > 1 {
			estimate.PartitionMemoryBytes += estimatePartitionDispositionMemory(left, leftCfg) + estimatePartitionDispositionMemory(right, rightCfg)
		}
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

func applySelectedEstimate(selection *engine.IndexSelection, backend string, estimate indexResourceEstimate) {
	selection.EstimatedMemoryBytes = 0
	selection.EstimatedTempDiskBytes = 0
	switch backend {
	case "memory":
		selection.EstimatedMemoryBytes = estimate.MemoryIndexBytes
	case "disk":
		selection.EstimatedMemoryBytes = estimate.DiskMemoryBytes
		selection.EstimatedTempDiskBytes = estimate.DiskIndexBytes
	case "partitioned":
		selection.EstimatedMemoryBytes = estimate.PartitionMemoryBytes
		selection.EstimatedTempDiskBytes = estimate.PartitionTempDiskBytes
	}
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

func chooseMultiIndexBackends(indexCfg config.IndexCfg, leftPath string, leftCfg config.ParserCfg, counterparts []string, rightPaths map[string]string, sources map[string]config.Source, pair config.Pair) ([]indexSelectionDecision, error) {
	if len(counterparts) == 0 {
		return nil, fmt.Errorf("at least one counterpart source is required")
	}
	resourcePolicy := indexCfg.MaxMemoryMB > 0 || indexCfg.MaxTempDiskMB > 0
	if !resourcePolicy {
		decisions := make([]indexSelectionDecision, 0, len(counterparts))
		for _, name := range counterparts {
			src := sources[name]
			decision, err := chooseIndexBackend(indexCfg, leftPath, rightPaths[name], leftCfg, src.Parser, pair, len(counterparts))
			if err != nil {
				return nil, fmt.Errorf("counterpart %q: %w", name, err)
			}
			decisions = append(decisions, decision)
		}
		return decisions, nil
	}

	detailed := true
	leftShape, err := inspectInputShape(leftPath, leftCfg, detailed)
	if err != nil {
		return nil, fmt.Errorf("inspect left input: %w", err)
	}
	estimates := make([]indexResourceEstimate, 0, len(counterparts))
	for _, name := range counterparts {
		src := sources[name]
		rightShape, err := inspectInputShape(rightPaths[name], src.Parser, detailed)
		if err != nil {
			return nil, fmt.Errorf("counterpart %q: %w", name, err)
		}
		estimate := estimateIndexResourcesFromShapes(leftShape, rightShape, leftCfg, src.Parser, pair, indexCfg.PartitionCount, len(counterparts))
		estimates = append(estimates, estimate)
	}

	requested := indexCfg.Backend
	if requested == "" {
		requested = "memory"
	}
	candidates := []string{requested}
	if requested == "auto" {
		candidates = []string{"memory", "disk", "partitioned"}
	}
	var failures []engine.IndexFallback
	for _, candidate := range candidates {
		if candidate == "partitioned" {
			eligible := true
			for _, name := range counterparts {
				src := sources[name]
				if partitionReason, ok := partitionedEligible(leftPath, rightPaths[name], leftCfg, src.Parser, pair, len(counterparts)); !ok {
					failures = append(failures, engine.IndexFallback{Backend: candidate, Reason: fmt.Sprintf("counterpart %q: %s", name, partitionReason)})
					eligible = false
					break
				}
			}
			if !eligible {
				continue
			}
		}
		reason, ok := aggregateBackendFits(candidate, indexCfg, estimates)
		if !ok {
			failures = append(failures, engine.IndexFallback{Backend: candidate, Reason: reason})
			continue
		}
		aggregateMemory, aggregateTempDisk := aggregateBackendResources(candidate, estimates)
		decisions := make([]indexSelectionDecision, 0, len(estimates))
		for _, estimate := range estimates {
			selection := engine.IndexSelection{
				RequestedBackend: requested,
				Backend:          candidate,
				Reason:           reason,
				Fallbacks:        append([]engine.IndexFallback(nil), failures...),
			}
			applySelectedEstimate(&selection, candidate, estimate)
			if candidate == "partitioned" {
				selection.EstimatedMemoryBytes = aggregateMemory
				selection.EstimatedTempDiskBytes = aggregateTempDisk
			}
			decisions = append(decisions, indexSelectionDecision{Selection: selection, Partitioned: candidate == "partitioned"})
		}
		return decisions, nil
	}
	return nil, fmt.Errorf("no suitable index backend for counterparts: %s", formatIndexFailures(failures))
}

func aggregateBackendFits(backend string, indexCfg config.IndexCfg, estimates []indexResourceEstimate) (string, bool) {
	if backend != "memory" && backend != "disk" && backend != "partitioned" {
		return fmt.Sprintf("unsupported backend %q for multi-counterpart streaming", backend), false
	}
	memory, tempDisk := aggregateBackendResources(backend, estimates)
	if indexCfg.MaxMemoryMB > 0 && memory > mbToBytes(indexCfg.MaxMemoryMB) {
		return fmt.Sprintf("estimated aggregate memory %s exceeds max_memory_mb=%d", formatBytes(memory), indexCfg.MaxMemoryMB), false
	}
	if backend == "disk" || backend == "partitioned" {
		if reason, ok := diskFits(indexCfg, indexCfg.SpillDir, tempDisk); !ok {
			return fmt.Sprintf("aggregate temporary disk: %s", reason), false
		}
	}
	return fmt.Sprintf("aggregate %s estimate fits configured policy", backend), true
}

func aggregateBackendResources(backend string, estimates []indexResourceEstimate) (memory, tempDisk int64) {
	for _, estimate := range estimates {
		switch backend {
		case "memory":
			memory += estimate.PerIndexMemoryBytes
		case "disk":
			memory += estimate.PerIndexDiskMemoryBytes
			tempDisk += estimate.DiskIndexBytes
		case "partitioned":
			if estimate.PartitionMemoryBytes > memory {
				memory = estimate.PartitionMemoryBytes
			}
			if estimate.PartitionTempDiskBytes > tempDisk {
				tempDisk = estimate.PartitionTempDiskBytes
			}
		}
	}
	if backend != "partitioned" && len(estimates) > 0 {
		memory += estimates[0].SharedMemoryBytes
	}
	return memory, tempDisk
}

func openSelectedIndex(indexCfg config.IndexCfg, selection indexSelectionDecision) (engine.RightIndex, error) {
	switch selection.Selection.Backend {
	case "memory":
		return engine.NewMemoryIndex(), nil
	case "disk":
		return engine.NewDiskIndex(indexCfg.SpillDir)
	default:
		return nil, fmt.Errorf("backend %q does not create a streaming index", selection.Selection.Backend)
	}
}

func mbToBytes(mb int64) int64 { return mb * 1024 * 1024 }

func formatBytes(bytes int64) string {
	if bytes < 1024*1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}

func formatIndexFailures(failures []engine.IndexFallback) string {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		parts = append(parts, failure.Backend+": "+failure.Reason)
	}
	return strings.Join(parts, "; ")
}
