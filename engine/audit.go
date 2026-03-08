package engine

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/reconify/reconify/config"
)

// BuildRunInfo hashes both input files and constructs a RunInfo value.
// It performs two sequential file reads (one per file) before parsing begins,
// so the OS page cache is warm for the subsequent ParseCSVEach passes.
// On M1 with hardware SHA-256 acceleration, hashing ~2 GB takes roughly 1-2 seconds.
func BuildRunInfo(
	toolVersion string,
	leftPath string,
	rightPath string,
	pair config.Pair,
	ts time.Time,
) (RunInfo, error) {
	leftHash, err := hashFile(leftPath)
	if err != nil {
		return RunInfo{}, fmt.Errorf("audit: hash left file: %w", err)
	}
	rightHash, err := hashFile(rightPath)
	if err != nil {
		return RunInfo{}, fmt.Errorf("audit: hash right file: %w", err)
	}
	return RunInfo{
		RunID:       buildRunID(leftHash, rightHash, ts),
		Timestamp:   ts,
		ToolVersion: toolVersion,
		LeftFile:    FileInfo{Path: leftPath, SHA256: leftHash},
		RightFile:   FileInfo{Path: rightPath, SHA256: rightHash},
		PairConfig: PairConfigSnap{
			DateWindow:           pair.DateWindow,
			AmountToleranceMinor: pair.AmountToleranceMinor,
			NameMode:             pair.NameMode,
		},
	}, nil
}

// hashFile computes the SHA-256 digest of a file and returns it as a lowercase
// hex string (64 characters). It uses a 1 MB copy buffer, matching the parser's
// read buffer size, to amortize syscall overhead.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReaderSize(f, 1<<20)); err != nil {
		return "", fmt.Errorf("read %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildRunID derives a short, stable run identifier from the two file hashes and
// the run timestamp. The result is 16 lowercase hex characters (8 bytes of entropy).
// It is deterministic for the same inputs, which makes it reproducible in tests.
func buildRunID(leftHash, rightHash string, ts time.Time) string {
	h := sha256.New()
	h.Write([]byte(leftHash))
	h.Write([]byte(rightHash))
	var tsBuf [8]byte
	binary.BigEndian.PutUint64(tsBuf[:], uint64(ts.UnixNano()))
	h.Write(tsBuf[:])
	return hex.EncodeToString(h.Sum(nil))[:16]
}
