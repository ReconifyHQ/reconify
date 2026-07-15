package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine"
)

const defaultAutoMaxRightFileMB int64 = 2048

type indexSelectionDecision struct {
	Selection   engine.IndexSelection
	Partitioned bool
}

var freeDiskBytes = availableDiskBytes

func chooseIndexBackend(indexCfg config.IndexCfg, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int) (indexSelectionDecision, error) {
	resourcePolicy := indexCfg.MaxMemoryMB > 0 || indexCfg.MaxTempDiskMB > 0
	leftShape, err := inspectInputShape(leftPath, leftCfg, resourcePolicy)
	if err != nil {
		return indexSelectionDecision{}, fmt.Errorf("inspect left input: %w", err)
	}
	rightShape, err := inspectInputShape(rightPath, rightCfg, resourcePolicy)
	if err != nil {
		return indexSelectionDecision{}, fmt.Errorf("inspect right input: %w", err)
	}
	partitionCount := resolvePartitionCount(indexCfg, leftShape, rightShape, leftCfg, rightCfg, pair, counterpartCount)
	estimate := estimateIndexResourcesFromShapes(leftShape, rightShape, leftCfg, rightCfg, pair, partitionCount, counterpartCount)
	requested := indexCfg.Backend
	if requested == "" {
		requested = "memory"
	}

	selection := engine.IndexSelection{
		RequestedBackend: requested,
	}
	if requested == "auto" {
		return chooseAutoBackend(indexCfg, leftPath, rightPath, leftCfg, rightCfg, pair, counterpartCount, estimate, selection, partitionCount)
	}

	reason, ok := backendFits(requested, indexCfg, leftPath, rightPath, leftCfg, rightCfg, pair, counterpartCount, estimate)
	if !ok {
		return indexSelectionDecision{}, fmt.Errorf("index backend %q rejected: %s", requested, reason)
	}
	selection.Backend = requested
	selection.Reason = reason
	applySelectedEstimate(&selection, requested, estimate)
	if requested == "partitioned" {
		selection.PartitionCount = partitionCount
	}
	return indexSelectionDecision{Selection: selection, Partitioned: requested == "partitioned"}, nil
}

func chooseAutoBackend(indexCfg config.IndexCfg, leftPath, rightPath string, leftCfg, rightCfg config.ParserCfg, pair config.Pair, counterpartCount int, estimate indexResourceEstimate, selection engine.IndexSelection, partitionCount int) (indexSelectionDecision, error) {
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
		if candidate == "partitioned" {
			selection.PartitionCount = partitionCount
		}
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
		allPartitioned := len(decisions) > 0
		sharedCount := defaultPartitionCount
		for i := range decisions {
			if !decisions[i].Partitioned {
				allPartitioned = false
				break
			}
			if decisions[i].Selection.PartitionCount > sharedCount {
				sharedCount = decisions[i].Selection.PartitionCount
			}
		}
		if allPartitioned {
			for i := range decisions {
				decisions[i].Selection.PartitionCount = sharedCount
			}
		}
		return decisions, nil
	}

	detailed := true
	leftShape, err := inspectInputShape(leftPath, leftCfg, detailed)
	if err != nil {
		return nil, fmt.Errorf("inspect left input: %w", err)
	}
	sharedPartitionCount := indexCfg.PartitionCount
	if sharedPartitionCount < 2 {
		sharedPartitionCount = defaultPartitionCount
		for _, name := range counterparts {
			src := sources[name]
			rightShape, shapeErr := inspectInputShape(rightPaths[name], src.Parser, detailed)
			if shapeErr != nil {
				return nil, fmt.Errorf("counterpart %q: %w", name, shapeErr)
			}
			candidate := resolvePartitionCount(indexCfg, leftShape, rightShape, leftCfg, src.Parser, pair, len(counterparts))
			if candidate > sharedPartitionCount {
				sharedPartitionCount = candidate
			}
		}
	}
	estimates := make([]indexResourceEstimate, 0, len(counterparts))
	for _, name := range counterparts {
		src := sources[name]
		rightShape, err := inspectInputShape(rightPaths[name], src.Parser, detailed)
		if err != nil {
			return nil, fmt.Errorf("counterpart %q: %w", name, err)
		}
		estimate := estimateIndexResourcesFromShapes(leftShape, rightShape, leftCfg, src.Parser, pair, sharedPartitionCount, len(counterparts))
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
				selection.PartitionCount = sharedPartitionCount
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
func reportIndexSelection(w engine.ResultWriter, selection engine.IndexSelection) error {
	target := w
	if capture, ok := w.(*summaryCapture); ok {
		target = capture.inner
	}
	if setter, ok := target.(engine.IndexSelectionSetter); ok {
		return setter.SetIndexSelection(selection)
	}
	fmt.Fprintf(os.Stderr, "index: %s\n", selection.String())
	for _, fallback := range selection.Fallbacks {
		fmt.Fprintf(os.Stderr, "index: fallback backend=%s reason=%s\n", fallback.Backend, fallback.Reason)
	}
	return nil
}
