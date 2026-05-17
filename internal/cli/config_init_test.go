package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestBuildConfigFromInitAnswersValidates(t *testing.T) {
	cfg, err := buildConfigFromInitAnswers(validConfigInitAnswers(filepath.Join("data", "bank.csv")))
	if err != nil {
		t.Fatalf("buildConfigFromInitAnswers() error = %v", err)
	}

	if cfg.Version != 1 {
		t.Fatalf("Version = %d, want 1", cfg.Version)
	}
	if _, ok := cfg.Sources["bank"]; !ok {
		t.Fatal("generated config missing bank source")
	}
	if _, ok := cfg.Sources["stripe"]; !ok {
		t.Fatal("generated config missing stripe source")
	}
	if cfg.Sources["bank"].Parser.Type != "csv" {
		t.Fatalf("bank parser type = %q, want csv", cfg.Sources["bank"].Parser.Type)
	}
	if errs := cfg.Validate(); len(errs) > 0 {
		t.Fatalf("generated config should validate, got %v", errs)
	}
}

func TestBuildConfigFromInitAnswersRejectsMissingRequiredMapping(t *testing.T) {
	answers := validConfigInitAnswers("bank.csv")
	answers.Left.DateCol = ""

	_, err := buildConfigFromInitAnswers(answers)
	if err == nil {
		t.Fatal("expected error for missing date column")
	}
	if !strings.Contains(err.Error(), "date_col") {
		t.Fatalf("error = %q, want date_col context", err)
	}
}

func TestRunConfigInitRefusesExistingOutputBeforePrompt(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "reconify.yaml")
	if err := os.WriteFile(outPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	called := false
	err := runConfigInit(testConfigInitCmd(), outPath, false, func(cmd *cobra.Command, defaultOutPath string) (configInitAnswers, bool, error) {
		called = true
		return configInitAnswers{}, false, nil
	})

	if err == nil {
		t.Fatal("expected existing output error")
	}
	if called {
		t.Fatal("prompt should not be called when output already exists without --force")
	}
}

func TestRunConfigInitCancelWritesNoFile(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "reconify.yaml")

	err := runConfigInit(testConfigInitCmd(), outPath, false, func(cmd *cobra.Command, defaultOutPath string) (configInitAnswers, bool, error) {
		answers := validConfigInitAnswers("bank.csv")
		answers.OutPath = outPath
		answers.Confirm = false
		return answers, false, nil
	})
	if err != nil {
		t.Fatalf("runConfigInit() error = %v", err)
	}

	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatalf("expected no output file, stat error = %v", err)
	}
}

func validConfigInitAnswers(leftPath string) configInitAnswers {
	return configInitAnswers{
		OutPath:  "reconify.yaml",
		Confirm:  true,
		Timezone: "UTC",
		Left: configInitSourceAnswers{
			Name:       "bank",
			FilePath:   leftPath,
			DateCol:    "Date",
			DateLayout: "2006-01-02",
			AmountCol:  "Amount",
			Decimal:    ".",
			Thousands:  ",",
			Multiplier: 100,
			RefCol:     "Reference",
		},
		Right: configInitSourceAnswers{
			Name:       "stripe",
			FilePath:   "stripe.ndjson",
			DateCol:    "created",
			DateLayout: "2006-01-02",
			AmountCol:  "amount",
			Decimal:    ".",
			Multiplier: 100,
			NameCol:    "description",
			RefCol:     "id",
		},
		PairName:             "bank_vs_stripe",
		DateWindow:           "1d",
		AmountToleranceMinor: 0,
		NameMode:             "tokens",
	}
}

func testConfigInitCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetErr(&bytes.Buffer{})
	return cmd
}
