package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFile_RejectsGlobOutsideConfigDir(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	outsideDir := filepath.Join(root, "outside")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "bank.csv")
	if err := os.WriteFile(outsideFile, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveFile("", "../outside/*.csv", configDir)
	if err == nil {
		t.Fatal("expected error for glob that resolves outside config directory")
	}
}

func TestResolveFile_AllowsGlobInsideConfigDir(t *testing.T) {
	configDir := t.TempDir()
	dataDir := filepath.Join(configDir, "data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataDir, "bank.csv")
	if err := os.WriteFile(want, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveFile("", "data/*.csv", configDir)
	if err != nil {
		t.Fatalf("resolveFile returned error: %v", err)
	}
	if got != want {
		t.Fatalf("resolveFile = %q, want %q", got, want)
	}
}

func TestResolveFile_AllowsAbsoluteGlobOutsideConfigDir(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	outsideDir := filepath.Join(root, "outside")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(outsideDir, "bank.csv")
	if err := os.WriteFile(want, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveFile("", filepath.Join(outsideDir, "*.csv"), configDir)
	if err != nil {
		t.Fatalf("resolveFile returned error: %v", err)
	}
	if got != want {
		t.Fatalf("resolveFile = %q, want %q", got, want)
	}
}

func TestResolveFile_RejectsSymlinkThatEscapesConfigDir(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	outsideDir := filepath.Join(root, "outside")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outsideDir, 0700); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "bank.csv")
	if err := os.WriteFile(outsideFile, []byte("date,amount\n"), 0600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(configDir, "bank.csv")
	if err := os.Symlink(outsideFile, linkPath); err != nil {
		t.Fatal(err)
	}

	_, err := resolveFile("", "*.csv", configDir)
	if err == nil {
		t.Fatal("expected error for symlinked match that resolves outside config directory")
	}
}

func TestReconcileOutputDoesNotPublishBeforeCommit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "results.json")
	output, err := openReconcileOutput(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	if _, err := output.File.WriteString(`{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target existed before commit; err=%v", err)
	}
}

func TestReconcileOutputCommitWritesFinalFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "results.json")
	output, err := openReconcileOutput(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	if _, err := output.File.WriteString(`{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if err := output.Commit(); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	got, err := os.ReadFile(target) // #nosec G304 -- target is inside t.TempDir() for this test.
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("target contents = %q", got)
	}
}

func TestReconcileOutputCommitRefusesSymlinkCreatedAfterOpen(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "results.json")
	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("victim"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := openReconcileOutput(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Cleanup()

	if _, err := output.File.WriteString(`{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, target); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := output.Commit(); err == nil {
		t.Fatal("expected Commit to refuse symlink output path")
	}
	got, err := os.ReadFile(victim) // #nosec G304 -- victim is inside t.TempDir() for this test.
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "victim" {
		t.Fatalf("victim was modified: %q", got)
	}
}
