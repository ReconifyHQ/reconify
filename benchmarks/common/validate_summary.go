//go:build ignore

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type expectedManifest struct {
	Scenario        string          `json:"scenario"`
	Summary         expectedSummary `json:"summary"`
	DuplicateGroups int             `json:"duplicate_groups"`
}

type expectedSummary struct {
	TotalLeft         int     `json:"total_left"`
	TotalRight        int     `json:"total_right"`
	Matched           int     `json:"matched"`
	UnmatchedLeft     int     `json:"unmatched_left"`
	UnmatchedRight    int     `json:"unmatched_right"`
	AmountDiffCount   int     `json:"amount_diff_count"`
	TimingDiffCount   int     `json:"timing_diff_count"`
	DuplicateCount    int     `json:"duplicate_count"`
	MatchRatePct      float64 `json:"match_rate_pct,omitempty"`
	ReconciledRatePct float64 `json:"reconciled_rate_pct,omitempty"`
}

type ndjsonEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func main() {
	expectedPath := flag.String("expected", "", "expected manifest JSON path")
	actualPath := flag.String("actual", "", "actual NDJSON output path")
	flag.Parse()

	if *expectedPath == "" || *actualPath == "" {
		fmt.Fprintln(os.Stderr, "usage: go run benchmarks/common/validate_summary.go --expected expected.json --actual output.ndjson")
		os.Exit(2)
	}

	expected, err := loadExpected(*expectedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load expected: %v\n", err)
		os.Exit(1)
	}
	actual, duplicateGroups, err := loadActual(*actualPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load actual: %v\n", err)
		os.Exit(1)
	}

	var failed bool
	check := func(name string, want, got int) {
		if want != got {
			fmt.Fprintf(os.Stderr, "%s: %s mismatch: want %d got %d\n", expected.Scenario, name, want, got)
			failed = true
		}
	}
	check("total_left", expected.Summary.TotalLeft, actual.TotalLeft)
	check("total_right", expected.Summary.TotalRight, actual.TotalRight)
	check("matched", expected.Summary.Matched, actual.Matched)
	check("unmatched_left", expected.Summary.UnmatchedLeft, actual.UnmatchedLeft)
	check("unmatched_right", expected.Summary.UnmatchedRight, actual.UnmatchedRight)
	check("amount_diff_count", expected.Summary.AmountDiffCount, actual.AmountDiffCount)
	check("timing_diff_count", expected.Summary.TimingDiffCount, actual.TimingDiffCount)
	check("duplicate_count", expected.Summary.DuplicateCount, actual.DuplicateCount)
	check("duplicate_groups", expected.DuplicateGroups, duplicateGroups)

	if failed {
		os.Exit(1)
	}
	fmt.Printf("PASS %s matched=%d amount_diff=%d timing_diff=%d unmatched_left=%d unmatched_right=%d duplicate_count=%d duplicate_groups=%d\n",
		expected.Scenario,
		actual.Matched,
		actual.AmountDiffCount,
		actual.TimingDiffCount,
		actual.UnmatchedLeft,
		actual.UnmatchedRight,
		actual.DuplicateCount,
		duplicateGroups,
	)
}

func loadExpected(path string) (expectedManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return expectedManifest{}, err
	}
	var manifest expectedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return expectedManifest{}, err
	}
	return manifest, nil
}

func loadActual(path string) (expectedSummary, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return expectedSummary{}, 0, err
	}
	defer f.Close()

	var summary expectedSummary
	var foundSummary bool
	var duplicateGroups int
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	for scanner.Scan() {
		var event ndjsonEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return expectedSummary{}, 0, err
		}
		switch event.Type {
		case "summary":
			if err := json.Unmarshal(event.Data, &summary); err != nil {
				return expectedSummary{}, 0, err
			}
			foundSummary = true
		case "duplicate":
			duplicateGroups++
		}
	}
	if err := scanner.Err(); err != nil {
		return expectedSummary{}, 0, err
	}
	if !foundSummary {
		return expectedSummary{}, 0, fmt.Errorf("summary event not found")
	}
	return summary, duplicateGroups, nil
}
