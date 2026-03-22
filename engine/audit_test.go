package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/reconify/reconify/config"
)

func TestHashFile_KnownGood(t *testing.T) {
	content := "hello, reconify\n"
	// Pre-computed: echo -n 'hello, reconify\n' | sha256sum
	expected := hex.EncodeToString(func() []byte {
		h := sha256.Sum256([]byte(content))
		return h[:]
	}())

	dir := t.TempDir()
	path := filepath.Join(dir, "test.csv")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile error: %v", err)
	}
	if got != expected {
		t.Errorf("hashFile = %q, want %q", got, expected)
	}
	if len(got) != 64 {
		t.Errorf("hash length = %d, want 64", len(got))
	}
}

func TestHashFile_Missing(t *testing.T) {
	_, err := hashFile("/tmp/reconify_no_such_file_xyzzy.csv")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestHashFile_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.csv")
	if err := os.WriteFile(path, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile error: %v", err)
	}
	// SHA-256 of empty input is well-known
	empty := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got != empty {
		t.Errorf("hash of empty file = %q, want %q", got, empty)
	}
}

func TestBuildRunInfo_FieldPopulation(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	if err := os.WriteFile(leftPath, []byte("date,amount\n2024-01-01,100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte("date,amount\n2024-01-01,100\n"), 0600); err != nil {
		t.Fatal(err)
	}

	pair := config.Pair{
		DateWindow:           "1d",
		AmountToleranceMinor: 50,
		NameMode:             "tokens",
	}
	ts := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)

	info, err := BuildRunInfo("v1.2.3", leftPath, rightPath, pair, ts)
	if err != nil {
		t.Fatalf("BuildRunInfo error: %v", err)
	}

	if info.ToolVersion != "v1.2.3" {
		t.Errorf("ToolVersion = %q, want %q", info.ToolVersion, "v1.2.3")
	}
	if !info.Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", info.Timestamp, ts)
	}
	if info.LeftFile.Path != leftPath {
		t.Errorf("LeftFile.Path = %q, want %q", info.LeftFile.Path, leftPath)
	}
	if info.RightFile.Path != rightPath {
		t.Errorf("RightFile.Path = %q, want %q", info.RightFile.Path, rightPath)
	}
	if len(info.LeftFile.SHA256) != 64 {
		t.Errorf("LeftFile.SHA256 length = %d, want 64", len(info.LeftFile.SHA256))
	}
	if len(info.RightFile.SHA256) != 64 {
		t.Errorf("RightFile.SHA256 length = %d, want 64", len(info.RightFile.SHA256))
	}
	if info.PairConfig.DateWindow != "1d" {
		t.Errorf("PairConfig.DateWindow = %q, want %q", info.PairConfig.DateWindow, "1d")
	}
	if info.PairConfig.AmountToleranceMinor != 50 {
		t.Errorf("PairConfig.AmountToleranceMinor = %d, want 50", info.PairConfig.AmountToleranceMinor)
	}
	if info.PairConfig.NameMode != "tokens" {
		t.Errorf("PairConfig.NameMode = %q, want %q", info.PairConfig.NameMode, "tokens")
	}
	if len(info.RunID) != 16 {
		t.Errorf("RunID length = %d, want 16", len(info.RunID))
	}
}

func TestBuildRunInfo_MissingFile(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	if err := os.WriteFile(leftPath, []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := BuildRunInfo("v1", leftPath, "/tmp/no_such_right_xyzzy.csv", config.Pair{}, time.Now())
	if err == nil {
		t.Fatal("expected error for missing right file, got nil")
	}
}

func TestBuildRunID_Deterministic(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id1 := buildRunID("aaa", "bbb", ts)
	id2 := buildRunID("aaa", "bbb", ts)
	if id1 != id2 {
		t.Errorf("buildRunID is not deterministic: %q != %q", id1, id2)
	}
	if len(id1) != 16 {
		t.Errorf("RunID length = %d, want 16", len(id1))
	}
	// Different inputs must produce different IDs
	id3 := buildRunID("aaa", "ccc", ts)
	if id1 == id3 {
		t.Errorf("different inputs produced same RunID %q", id1)
	}
}

func TestBuildRunID_HexChars(t *testing.T) {
	ts := time.Now()
	id := buildRunID("x", "y", ts)
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("RunID %q contains non-hex char %q", id, c)
		}
	}
}
