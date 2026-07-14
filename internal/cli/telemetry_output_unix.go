//go:build darwin || linux

package cli

import (
	"os"
	"syscall"
)

func openTelemetryFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600) // #nosec G304 -- explicit destination is validated before opening.
}
