package engine

import "runtime"

func sampleResources() resourceMetrics {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	heap, gc := stats.HeapAlloc, stats.NumGC
	metrics := resourceMetrics{heapBytes: &heap, gcCycles: &gc}
	return addPlatformResources(metrics)
}
