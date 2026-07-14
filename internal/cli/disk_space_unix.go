//go:build !windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

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
			if available > maxInt64Uint/blockSize {
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
