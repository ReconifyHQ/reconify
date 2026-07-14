//go:build !darwin && !linux

package cli

import (
	"fmt"
	"os"
)

func openTelemetryFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("telemetry output path %q is a symlink; refusing to follow it", path)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- explicit destination is validated before opening.
}
