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
	"golang.org/x/sys/unix"
)

const (
	defaultAutoMaxRightFileMB int64 = 2048
	defaultPartitionCount           = 32
	resourceHeadroomBytes     int64 = 64 << 20
	resourceRowBytes          int64 = 128
)

type indexResourceEstimate struct {
	RightBytes             int64
	LeftBytes              int64
	RightRows              int64
	RetainedFieldBytes     int64
	MemoryIndexBytes       int64
	DiskMemoryBytes        int64
	DiskIndexBytes         int64
	PartitionMemoryBytes   int64
	PartitionTempDiskBytes int64
}

type indexSelectionDecision struct {
	Selection   engine.IndexSelection
	Partitioned bool
}

var freeDiskBytes = availableDiskBytes

func chooseIndexBackend(indexCfg config.IndexCfg, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int) (indexSelectionDecision, error) {
	estimate, err := estimateIndexResources(leftPath, rightPath, rightCfg, indexCfg.PartitionCount)
	if err != nil {
		return indexSelectionDecision{}, fmt.Errorf("estimate index resources: %w", err)
	}
	requested := indexCfg.Backend
	if requested == "" {
		requested = "memory"
	}

	if requested == "partitioned" && counterpartCount != 1 {
		return indexSelectionDecision{}, fmt.Errorf("partitioned backend requires exactly one counterpart")
	}

	selection := engine.IndexSelection{
		RequestedBackend:       requested,
		EstimatedMemoryBytes:   estimate.MemoryIndexBytes,
		EstimatedTempDiskBytes: estimate.DiskIndexBytes,
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
		if estimate.RightBytes/(1024*1024) <= threshold {
			candidates = []string{"memory", "disk"}
		} else {
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
	if counterpartCount != 1 {
		return "partitioned backend supports one counterpart only", false
	}
	if !isCSVPath(leftPath, leftCfg) || !isCSVPath(rightPath, rightCfg) {
		return "partitioned backend supports CSV inputs only", false
	}
	if leftCfg.RefCol == "" || rightCfg.RefCol == "" {
		return "partitioned backend requires ref_col on both sources", false
	}
	if pair.NameMode == "tokens" {
		return "partitioned backend does not support name-token matching", false
	}
	for _, pass := range pair.Passes {
		if pass.Type != config.PassTypeReferenceOneToOne {
			return fmt.Sprintf("partitioned backend does not support pass %q", pass.Type), false
		}
	}
	return "", true
}

func estimateIndexResources(leftPath, rightPath string, rightCfg config.ParserCfg, partitionCount int) (indexResourceEstimate, error) {
	var estimate indexResourceEstimate
	leftInfo, err := os.Stat(leftPath)
	if err == nil {
		estimate.LeftBytes = leftInfo.Size()
	}
	rightInfo, err := os.Stat(rightPath)
	if err != nil {
		return estimate, err
	}
	estimate.RightBytes = rightInfo.Size()
	if isCSVPath(rightPath, rightCfg) {
		if err := scanCSVShape(rightPath, rightCfg, &estimate); err != nil {
			return estimate, err
		}
	} else {
		estimate.RightRows = estimate.RightBytes / 128
		estimate.RetainedFieldBytes = estimate.RightBytes
	}
	retained := estimate.RightRows*resourceRowBytes + estimate.RetainedFieldBytes + estimate.RightRows*32
	if retained < resourceHeadroomBytes {
		retained = resourceHeadroomBytes
	}
	estimate.MemoryIndexBytes = retained*2 + resourceHeadroomBytes
	estimate.DiskMemoryBytes = 2 * resourceHeadroomBytes
	estimate.DiskIndexBytes = estimate.RightBytes*3 + resourceHeadroomBytes
	if estimate.DiskIndexBytes < resourceHeadroomBytes {
		estimate.DiskIndexBytes = resourceHeadroomBytes
	}
	if partitionCount < 2 {
		partitionCount = defaultPartitionCount
	}
	estimate.PartitionMemoryBytes = (estimate.MemoryIndexBytes+int64(partitionCount)-1)/int64(partitionCount) + resourceHeadroomBytes
	estimate.PartitionTempDiskBytes = estimate.LeftBytes + estimate.RightBytes + resourceHeadroomBytes
	return estimate, nil
}

func applySelectedEstimate(selection *engine.IndexSelection, backend string, estimate indexResourceEstimate) {
	if backend == "disk" {
		selection.EstimatedMemoryBytes = estimate.DiskMemoryBytes
	}
	if backend == "partitioned" {
		selection.EstimatedMemoryBytes = estimate.PartitionMemoryBytes
		selection.EstimatedTempDiskBytes = estimate.PartitionTempDiskBytes
	}
}

func scanCSVShape(path string, cfg config.ParserCfg, estimate *indexResourceEstimate) error {
	f, err := os.Open(path) // #nosec G304 -- caller resolved the explicit input path.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return err
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
			return nil
		}
		if readErr != nil {
			return readErr
		}
		estimate.RightRows++
		estimate.RetainedFieldBytes += fieldLen(record, refIndex) + fieldLen(record, currencyIndex) + fieldLen(record, nameIndex)
	}
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

func availableDiskBytes(path string) (int64, error) {
	base := path
	if base == "" {
		base = os.TempDir()
	}
	for {
		if _, err := os.Stat(base); err == nil {
			var stat unix.Statfs_t
			if err := unix.Statfs(base, &stat); err != nil {
				return 0, err
			}
			available := stat.Bavail
			if stat.Bsize <= 0 {
				return 0, fmt.Errorf("temporary disk reported invalid block size %d", stat.Bsize)
			}
			blockSize := uint64(stat.Bsize) // #nosec G115 -- block size is checked positive above.
			const maxInt64Uint = uint64(1<<63 - 1)
			if blockSize != 0 && available > maxInt64Uint/blockSize {
				return int64(maxInt64Uint), nil
			}
			return int64(available * blockSize), nil // #nosec G115 -- product is bounded by maxInt64Uint above.
		}
		parent := filepath.Dir(base)
		if parent == base {
			return 0, fmt.Errorf("no existing parent for %q", path)
		}
		base = parent
	}
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
