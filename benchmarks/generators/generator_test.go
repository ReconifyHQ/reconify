package generators

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
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

// ---------------------------------------------------------------------------
// Adversarial generator tests
// ---------------------------------------------------------------------------

type adversarialManifestShape struct {
	Scenario string `json:"scenario"`
	Seed     int64  `json:"seed"`
	Rows     int    `json:"rows"`
	PassType string `json:"pass_type"`
	Summary  struct {
		TotalLeft              int `json:"total_left"`
		TotalRight             int `json:"total_right"`
		Matched                int `json:"matched"`
		UnmatchedLeft          int `json:"unmatched_left"`
		UnmatchedRight         int `json:"unmatched_right"`
		AmountDiffCount        int `json:"amount_diff_count"`
		TimingDiffCount        int `json:"timing_diff_count"`
		DuplicateCount         int `json:"duplicate_count"`
		GroupedMatchedCount    int `json:"grouped_matched_count"`
		ManyToManyMatchedCount int `json:"many_to_many_matched_count"`
	} `json:"summary"`
	CurrencyTotals map[string]struct {
		MatchedAmountLeft    int64 `json:"matched_amount_left"`
		MatchedAmountRight   int64 `json:"matched_amount_right"`
		UnmatchedAmountLeft  int64 `json:"unmatched_amount_left"`
		UnmatchedAmountRight int64 `json:"unmatched_amount_right"`
	} `json:"currency_totals"`
}

func runAdversarialGenerator(t *testing.T, scenario, outDir string, rows int) adversarialManifestShape {
	t.Helper()
	cmd := exec.Command("go", "run", "adversarial.go", // #nosec G204 -- fixed local generator command with test-controlled arguments.
		"--scenario", scenario,
		"--rows", fmt.Sprintf("%d", rows),
		"--output-dir", outDir,
		"--seed", "42",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run adversarial generator scenario=%s: %v\n%s", scenario, err, output)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "manifest.json")) // #nosec G304 -- file is inside t.TempDir().
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m adversarialManifestShape
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return m
}

func TestAdversarialGenerator_UniformUniqueRefs(t *testing.T) {
	out := t.TempDir()
	m := runAdversarialGenerator(t, "uniform_unique_refs", out, 100)

	if m.Scenario != "uniform_unique_refs" {
		t.Fatalf("scenario = %q", m.Scenario)
	}
	if m.Seed != 42 {
		t.Fatalf("seed = %d, want 42", m.Seed)
	}
	if m.Summary.TotalLeft != 100 {
		t.Fatalf("total_left = %d, want 100", m.Summary.TotalLeft)
	}
	if m.Summary.Matched != 85 {
		t.Fatalf("matched = %d, want 85", m.Summary.Matched)
	}
	if m.Summary.AmountDiffCount != 5 {
		t.Fatalf("amount_diff_count = %d, want 5", m.Summary.AmountDiffCount)
	}
	if m.Summary.TimingDiffCount != 5 {
		t.Fatalf("timing_diff_count = %d, want 5", m.Summary.TimingDiffCount)
	}
	if csvDataRows(t, filepath.Join(out, "left.csv")) != 100 {
		t.Fatal("left.csv row count mismatch")
	}
	if csvDataRows(t, filepath.Join(out, "right.csv")) != m.Summary.TotalRight {
		t.Fatal("right.csv row count does not match manifest total_right")
	}
	if _, ok := m.CurrencyTotals["USD"]; !ok {
		t.Fatal("manifest missing USD currency totals")
	}
}

func TestAdversarialGenerator_HotSkewedRefs(t *testing.T) {
	out := t.TempDir()
	m := runAdversarialGenerator(t, "hot_skewed_refs", out, 20)

	if m.Summary.TotalLeft != 20 {
		t.Fatalf("total_left = %d, want 20", m.Summary.TotalLeft)
	}
	// total_right = rows * hotBucketSize (5)
	if m.Summary.TotalRight != 100 {
		t.Fatalf("total_right = %d, want 100 (20 * 5)", m.Summary.TotalRight)
	}
	if m.Summary.Matched != 20 {
		t.Fatalf("matched = %d, want 20", m.Summary.Matched)
	}
	if m.Summary.UnmatchedRight != 80 {
		t.Fatalf("unmatched_right = %d, want 80 (20 * 4)", m.Summary.UnmatchedRight)
	}
}

func TestAdversarialGenerator_HighDuplicatePressure(t *testing.T) {
	out := t.TempDir()
	m := runAdversarialGenerator(t, "high_duplicate_pressure", out, 100)

	if m.Summary.TotalLeft != 100 {
		t.Fatalf("total_left = %d, want 100", m.Summary.TotalLeft)
	}
	// 20% dup left rows (rounded to even)
	wantDup := 100 * 20 / 100
	if wantDup%2 != 0 {
		wantDup--
	}
	if m.Summary.DuplicateCount != wantDup {
		t.Fatalf("duplicate_count = %d, want %d", m.Summary.DuplicateCount, wantDup)
	}
	if m.Summary.UnmatchedLeft != wantDup {
		t.Fatalf("unmatched_left = %d, want %d", m.Summary.UnmatchedLeft, wantDup)
	}
}

func TestAdversarialGenerator_OneToManySettlement(t *testing.T) {
	out := t.TempDir()
	m := runAdversarialGenerator(t, "one_to_many_settlement", out, 30)

	if m.PassType != "one_to_many" {
		t.Fatalf("pass_type = %q, want one_to_many", m.PassType)
	}
	if m.Summary.TotalLeft != 30 {
		t.Fatalf("total_left = %d, want 30", m.Summary.TotalLeft)
	}
	if m.Summary.TotalRight != 90 {
		t.Fatalf("total_right = %d, want 90 (30*3)", m.Summary.TotalRight)
	}
	if m.Summary.GroupedMatchedCount != 30 {
		t.Fatalf("grouped_matched_count = %d, want 30", m.Summary.GroupedMatchedCount)
	}
	// Verify skipped matrix covers CSV and table formats.
	raw, err := os.ReadFile(filepath.Join(out, "manifest.json")) // #nosec G304 -- t.TempDir() path.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"csv"`) {
		t.Fatal("expected CSV to appear in skipped_matrix for one_to_many_settlement")
	}
}

func TestAdversarialGenerator_ManyToManySettlement(t *testing.T) {
	out := t.TempDir()
	m := runAdversarialGenerator(t, "many_to_many_settlement", out, 30)

	if m.PassType != "many_to_many" {
		t.Fatalf("pass_type = %q, want many_to_many", m.PassType)
	}
	// rows=30, groups=10, left=30, right=30
	if m.Summary.TotalLeft != 30 {
		t.Fatalf("total_left = %d, want 30", m.Summary.TotalLeft)
	}
	if m.Summary.ManyToManyMatchedCount != 10 {
		t.Fatalf("many_to_many_matched_count = %d, want 10", m.Summary.ManyToManyMatchedCount)
	}
}

// TestAdversarialGenerator_DeterministicManifest verifies that repeated runs
// with the same seed produce byte-identical manifests.
func TestAdversarialGenerator_DeterministicManifest(t *testing.T) {
	out1 := t.TempDir()
	out2 := t.TempDir()
	runAdversarialGenerator(t, "uniform_unique_refs", out1, 50)
	runAdversarialGenerator(t, "uniform_unique_refs", out2, 50)

	b1, err1 := os.ReadFile(filepath.Join(out1, "manifest.json")) // #nosec G304
	b2, err2 := os.ReadFile(filepath.Join(out2, "manifest.json")) // #nosec G304
	if err1 != nil || err2 != nil {
		t.Fatalf("read manifests: %v / %v", err1, err2)
	}
	if string(b1) != string(b2) {
		t.Fatal("manifests differ between runs with same seed")
	}
	f1, err := os.ReadFile(filepath.Join(out1, "left.csv")) // #nosec G304 -- test path is inside t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	f2, err := os.ReadFile(filepath.Join(out2, "left.csv")) // #nosec G304 -- test path is inside t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	if string(f1) != string(f2) {
		t.Fatal("fixtures differ between runs with same seed")
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

func TestAdversarialGenerator_DifferentSeedChangesFixture(t *testing.T) {
	out1 := t.TempDir()
	out2 := t.TempDir()
	for _, run := range []struct {
		out  string
		seed string
	}{
		{out: out1, seed: "42"},
		{out: out2, seed: "43"},
	} {
		cmd := exec.Command("go", "run", "adversarial.go", // #nosec G204 -- fixed local generator command with test-controlled arguments.
			"--scenario", "uniform_unique_refs",
			"--rows", "50",
			"--output-dir", run.out,
			"--seed", run.seed,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run adversarial generator seed=%s: %v\n%s", run.seed, err, output)
		}
	}
	b1, err := os.ReadFile(filepath.Join(out1, "left.csv")) // #nosec G304 -- test path is inside t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	b2, err := os.ReadFile(filepath.Join(out2, "left.csv")) // #nosec G304 -- test path is inside t.TempDir().
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) == string(b2) {
		t.Fatal("different seeds produced identical fixtures")
	}
}
