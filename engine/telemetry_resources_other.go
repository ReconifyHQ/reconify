//go:build !darwin && !linux

package engine

func addPlatformResources(metrics resourceMetrics) resourceMetrics { return metrics }
