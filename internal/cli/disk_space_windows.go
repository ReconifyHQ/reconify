//go:build windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func availableDiskBytes(path string) (int64, error) {
	base := path
	if base == "" {
		base = os.TempDir()
	}
	for {
		if _, err := os.Stat(base); err == nil {
			directoryName, err := windows.UTF16PtrFromString(base)
			if err != nil {
				return 0, fmt.Errorf("encode temporary disk path: %w", err)
			}
			var freeBytes uint64
			if err := windows.GetDiskFreeSpaceEx(directoryName, &freeBytes, nil, nil); err != nil {
				return 0, err
			}
			const maxInt64Uint = uint64(1<<63 - 1)
			if freeBytes > maxInt64Uint {
				return int64(maxInt64Uint), nil
			}
			return int64(freeBytes), nil
		}
		parent := filepath.Dir(base)
		if parent == base {
			return 0, fmt.Errorf("no existing parent for %q", path)
		}
		base = parent
	}
}
