//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package telemetry

import "runtime"

func sampleResources() resourceMetrics {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	heap, gc := stats.HeapAlloc, stats.NumGC
	metrics := resourceMetrics{heapBytes: &heap, gcCycles: &gc}
	return addPlatformResources(metrics)
}
