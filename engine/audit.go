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

	"github.com/reconifyhq/reconify/config"
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
	leftInfo, err := hashFile(leftPath)
	if err != nil {
		return RunInfo{}, fmt.Errorf("audit: hash left file: %w", err)
	}
	rightInfo, err := hashFile(rightPath)
	if err != nil {
		return RunInfo{}, fmt.Errorf("audit: hash right file: %w", err)
	}
	return RunInfo{
		RunID:       buildRunID(leftInfo.SHA256, rightInfo.SHA256, ts),
		Timestamp:   ts,
		ToolVersion: toolVersion,
		LeftFile:    leftInfo,
		RightFile:   rightInfo,
		PairConfig: PairConfigSnap{
			DateWindow:           pair.DateWindow,
			AmountToleranceMinor: pair.AmountToleranceMinor,
			NameMode:             pair.NameMode,
		},
	}, nil
}

// hashFile computes the SHA-256 digest of a file and captures stat metadata at
// hash time. The definitive TOCTOU fix is to parse from the same descriptor used
// for hashing; until then, VerifyAuditFiles detects post-parse divergence.
func hashFile(path string) (FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileInfo{}, fmt.Errorf("open %q: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()
	st, err := f.Stat()
	if err != nil {
		return FileInfo{}, fmt.Errorf("stat %q: %w", path, err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReaderSize(f, 1<<20)); err != nil {
		return FileInfo{}, fmt.Errorf("read %q: %w", path, err)
	}
	return FileInfo{
		Path:    path,
		SHA256:  hex.EncodeToString(h.Sum(nil)),
		Size:    st.Size(),
		ModTime: st.ModTime(),
	}, nil
}

// VerifyAuditFiles verifies that audited files still have the same size and
// modification time after parsing as they did when hashed before parsing.
func VerifyAuditFiles(info RunInfo) error {
	if err := verifyAuditFile(info.LeftFile); err != nil {
		return fmt.Errorf("left file changed after audit hash: %w", err)
	}
	if err := verifyAuditFile(info.RightFile); err != nil {
		return fmt.Errorf("right file changed after audit hash: %w", err)
	}
	return nil
}

func verifyAuditFile(info FileInfo) error {
	st, err := os.Stat(info.Path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", info.Path, err)
	}
	if st.Size() != info.Size {
		return fmt.Errorf("%q size changed from %d to %d bytes", info.Path, info.Size, st.Size())
	}
	if !st.ModTime().Equal(info.ModTime) {
		return fmt.Errorf("%q mod_time changed from %s to %s", info.Path, info.ModTime.Format(time.RFC3339Nano), st.ModTime().Format(time.RFC3339Nano))
	}
	return nil
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
