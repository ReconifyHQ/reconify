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

func TestBuildConfigFromInitAnswersClearsDelimitedSeparatorsForNonCSV(t *testing.T) {
	answers := validConfigInitAnswers("bank.json")
	answers.Left.Decimal = "."
	answers.Left.Thousands = ","
	answers.Right.FilePath = "stripe.xlsx"
	answers.Right.Decimal = ","
	answers.Right.Thousands = "."

	cfg, err := buildConfigFromInitAnswers(answers)
	if err != nil {
		t.Fatalf("buildConfigFromInitAnswers() error = %v", err)
	}

	for _, sourceName := range []string{"bank", "stripe"} {
		parser := cfg.Sources[sourceName].Parser
		if parser.Decimal != "" || parser.Thousands != "" {
			t.Errorf(
				"%s separators = decimal %q, thousands %q; want both empty for %s parser",
				sourceName,
				parser.Decimal,
				parser.Thousands,
				parser.Type,
			)
		}
	}
}

func TestWriteConfigInitFileOmitsDelimitedSeparatorsForNonCSV(t *testing.T) {
	answers := validConfigInitAnswers("bank.json")
	answers.Right.FilePath = "stripe.xlsx"
	cfg, err := buildConfigFromInitAnswers(answers)
	if err != nil {
		t.Fatalf("buildConfigFromInitAnswers() error = %v", err)
	}

	outPath := filepath.Join(t.TempDir(), "reconify.yaml")
	if err := writeConfigInitFile(outPath, false, cfg); err != nil {
		t.Fatalf("writeConfigInitFile() error = %v", err)
	}
	data, err := os.ReadFile(outPath) // #nosec G304 -- outPath is inside t.TempDir().
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	generated := string(data)
	if strings.Contains(generated, "decimal:") || strings.Contains(generated, "thousands:") {
		t.Fatalf("non-CSV config should omit separator keys:\n%s", generated)
	}
}

func TestUsesDelimitedSeparators(t *testing.T) {
	tests := []struct {
		parserType string
		want       bool
	}{
		{parserType: "csv", want: true},
		{parserType: "auto", want: true},
		{parserType: "json", want: false},
		{parserType: "xlsx", want: false},
		{parserType: " JSON ", want: false},
	}

	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.parserType), func(t *testing.T) {
			if got := usesDelimitedSeparators(tt.parserType); got != tt.want {
				t.Fatalf("usesDelimitedSeparators(%q) = %t, want %t", tt.parserType, got, tt.want)
			}
		})
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
	if err := os.WriteFile(outPath, []byte("existing"), 0o600); err != nil {
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
