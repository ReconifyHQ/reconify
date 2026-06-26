package generators

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeterministicGeneratorRowCounts(t *testing.T) {
	out := t.TempDir()
	cmd := exec.Command("go", "run", "deterministic.go", "-rows", "100", "-sources", "3", "-with-error-cases=false", "-out", out) // #nosec G204 -- fixed local generator command with test-controlled arguments.
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run deterministic generator: %v\n%s", err, output)
	}

	if got := csvDataRows(t, filepath.Join(out, "left.csv")); got != 100 {
		t.Fatalf("left rows = %d, want 100", got)
	}
	totalRight := 0
	for i := 1; i <= 3; i++ {
		totalRight += csvDataRows(t, filepath.Join(out, "right_"+string(rune('0'+i))+".csv"))
	}
	if totalRight != 100 {
		t.Fatalf("total right rows = %d, want 100", totalRight)
	}
}

func TestRealisticGeneratorManifestAndSplitFiles(t *testing.T) {
	out := t.TempDir()
	cmd := exec.Command("go", "run", "realistic.go", "-rows", "100", "-scenario", "split_provider_exports", "-out", out) // #nosec G204 -- fixed local generator command with test-controlled arguments.
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run realistic generator: %v\n%s", err, output)
	}

	var manifest struct {
		Scenario string `json:"scenario"`
		Summary  struct {
			TotalLeft       int `json:"total_left"`
			TotalRight      int `json:"total_right"`
			Matched         int `json:"matched"`
			AmountDiffCount int `json:"amount_diff_count"`
			TimingDiffCount int `json:"timing_diff_count"`
			UnmatchedLeft   int `json:"unmatched_left"`
			UnmatchedRight  int `json:"unmatched_right"`
		} `json:"summary"`
	}
	data, err := os.ReadFile(filepath.Join(out, "expected.json")) // #nosec G304 -- file is inside t.TempDir() created by this test.
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Scenario != "split_provider_exports" {
		t.Fatalf("scenario = %q", manifest.Scenario)
	}
	if manifest.Summary.TotalLeft != 100 || manifest.Summary.TotalRight != 95 {
		t.Fatalf("unexpected totals: %+v", manifest.Summary)
	}
	if manifest.Summary.Matched != 78 || manifest.Summary.AmountDiffCount != 4 || manifest.Summary.TimingDiffCount != 8 || manifest.Summary.UnmatchedLeft != 10 || manifest.Summary.UnmatchedRight != 5 {
		t.Fatalf("unexpected counts: %+v", manifest.Summary)
	}
	for i := 1; i <= 3; i++ {
		path := filepath.Join(out, "provider_"+string(rune('0'+i))+".csv")
		if rows := csvDataRows(t, path); rows == 0 {
			t.Fatalf("%s has no data rows", path)
		}
	}
}

func TestRealisticGeneratorQuotedCSV(t *testing.T) {
	out := t.TempDir()
	cmd := exec.Command("go", "run", "realistic.go", "-rows", "100", "-scenario", "bank_statement_noise", "-out", out) // #nosec G204 -- fixed local generator command with test-controlled arguments.
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run realistic generator: %v\n%s", err, output)
	}

	data, err := os.ReadFile(filepath.Join(out, "ledger.csv")) // #nosec G304 -- file is inside t.TempDir() created by this test.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `""`) {
		t.Fatalf("ledger.csv does not contain escaped quotes")
	}
}

func csvDataRows(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- helper reads paths built inside t.TempDir() by these tests.
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		return 0
	}
	return len(rows) - 1
}
