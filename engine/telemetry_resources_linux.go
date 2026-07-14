//go:build linux

package engine

import "syscall"

func addPlatformResources(metrics resourceMetrics) resourceMetrics {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return metrics
	}
	// Linux reports Maxrss in KiB. Guard both the signed-to-unsigned
	// conversion and the conversion to bytes so unavailable/invalid values stay
	// nullable instead of wrapping.
	const maxRSSKiB = (1 << 54) - 1
	if usage.Maxrss < 0 || usage.Maxrss > maxRSSKiB {
		return metrics
	}
	rss := uint64(usage.Maxrss) * 1024
	cpu := float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6 +
		float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6
	metrics.rssBytes, metrics.cpuSeconds = &rss, &cpu
	return metrics
}
