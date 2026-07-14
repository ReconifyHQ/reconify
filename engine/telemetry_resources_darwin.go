//go:build darwin

package engine

import "syscall"

func addPlatformResources(metrics resourceMetrics) resourceMetrics {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return metrics
	}
	// Darwin reports Maxrss in bytes. A negative value is unavailable rather
	// than a usable RSS measurement, so leave the field nullable in that case.
	if usage.Maxrss < 0 {
		return metrics
	}
	rss := uint64(usage.Maxrss)
	cpu := float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6 +
		float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6
	metrics.rssBytes, metrics.cpuSeconds = &rss, &cpu
	return metrics
}
