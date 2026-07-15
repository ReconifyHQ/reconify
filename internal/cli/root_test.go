package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGetConfigPath_Default(t *testing.T) {
	t.Setenv("RECONIFY_CONFIG", "")
	configFile = "reconify.yaml"
	configExplicit = false

	if got := getConfigPath(); got != "reconify.yaml" {
		t.Fatalf("getConfigPath() = %q, want %q", got, "reconify.yaml")
	}
}

func TestGetConfigPath_EnvFallback(t *testing.T) {
	t.Setenv("RECONIFY_CONFIG", "env.yaml")
	configFile = "reconify.yaml"
	configExplicit = false

	if got := getConfigPath(); got != "env.yaml" {
		t.Fatalf("getConfigPath() = %q, want %q", got, "env.yaml")
	}
}

func TestGetConfigPath_ExplicitFlagWinsOverEnv(t *testing.T) {
	t.Setenv("RECONIFY_CONFIG", "env.yaml")
	configFile = "flag.yaml"
	configExplicit = true

	if got := getConfigPath(); got != "flag.yaml" {
		t.Fatalf("getConfigPath() = %q, want %q", got, "flag.yaml")
	}
}

func TestRootCommandPropagatesCanceledContextToParse(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	if err := os.WriteFile(inputPath, []byte("date,amount\n2026-01-01,1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := "version: 1\nsources:\n  source:\n    file_pattern: " + inputPath + "\n    parser:\n      type: csv\n      date_col: date\n      date_layout: \"2006-01-02\"\n      amount_col: amount\n      multiplier: 1\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := newRootCmd("test", "test")
	root.SetContext(ctx)
	root.SetArgs([]string{
		"parse", "--config", configPath, "--source", "source", "--file", inputPath, "--format", "json",
	})

	err := root.Execute()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestCanceledReconcileDoesNotPublishOutput(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	configPath := filepath.Join(dir, "reconify.yaml")
	outputPath := filepath.Join(dir, "result.ndjson")
	content := "date,amount,reference\n2026-01-01,1,REF-1\n"
	for _, path := range []string{leftPath, rightPath} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	config := "version: 1\nindex:\n  backend: partitioned\n  partition_count: 2\nsources:\n  left:\n    file_pattern: " + leftPath + "\n    parser: &parser\n      type: csv\n      date_col: date\n      date_layout: \"2006-01-02\"\n      amount_col: amount\n      multiplier: 1\n      ref_col: reference\n  right:\n    file_pattern: " + rightPath + "\n    parser: *parser\npairs:\n  pair:\n    left: left\n    right: right\n    date_window: 0d\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := newRootCmd("test", "test")
	root.SetContext(ctx)
	root.SetArgs([]string{
		"reconcile", "--config", configPath, "--pair", "pair", "--format", "ndjson", "--out", outputPath,
	})

	err := root.Execute()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("output stat error = %v, want output to remain unpublished", statErr)
	}
}
