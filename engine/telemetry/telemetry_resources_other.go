//go:build !darwin && !linux

//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package telemetry

func addPlatformResources(metrics resourceMetrics) resourceMetrics { return metrics }
