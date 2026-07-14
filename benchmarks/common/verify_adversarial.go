//go:build ignore

// verify_adversarial validates reconciliation output against an adversarial
// benchmark manifest. Reads the manifest.json produced by the adversarial
// generator and the actual output from a reconcile run, then asserts that
// classification counts and per-currency monetary totals match.
//
// Usage:
//
//	go run benchmarks/common/verify_adversarial.go \
//	    --manifest manifest.json \
//	    --actual   output.ndjson \
//	    --format   ndjson \
//	    [--backend  memory]
//
// Formats: ndjson, json, json-stream, csv, table
// Exit code 0 = PASS, 1 = FAIL, 2 = SKIP (unsupported matrix combination).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Manifest types (must stay in sync with adversarial.go)
// ---------------------------------------------------------------------------

type manifest struct {
	Scenario       string                    `json:"scenario"`
	Seed           int64                     `json:"seed"`
	PassType       string                    `json:"pass_type"`
	Summary        manifestSummary           `json:"summary"`
	CurrencyTotals map[string]currencyTotals `json:"currency_totals"`
	SkippedMatrix  []skippedEntry            `json:"skipped_matrix,omitempty"`
}

type manifestSummary struct {
	TotalLeft              int `json:"total_left"`
	TotalRight             int `json:"total_right"`
	Matched                int `json:"matched"`
	UnmatchedLeft          int `json:"unmatched_left"`
	UnmatchedRight         int `json:"unmatched_right"`
	AmountDiffCount        int `json:"amount_diff_count"`
	TimingDiffCount        int `json:"timing_diff_count"`
	DuplicateCount         int `json:"duplicate_count"`
	GroupedMatchedCount    int `json:"grouped_matched_count,omitempty"`
	ManyToManyMatchedCount int `json:"many_to_many_matched_count,omitempty"`
}

type currencyTotals struct {
	MatchedAmountLeft    int64 `json:"matched_amount_left"`
	MatchedAmountRight   int64 `json:"matched_amount_right"`
	UnmatchedAmountLeft  int64 `json:"unmatched_amount_left"`
	UnmatchedAmountRight int64 `json:"unmatched_amount_right"`
}

type skippedEntry struct {
	Backend string `json:"backend,omitempty"`
	Format  string `json:"format,omitempty"`
	Reason  string `json:"reason"`
}

// ---------------------------------------------------------------------------
// Engine Summary types (subset we care about)
// ---------------------------------------------------------------------------

type engineSummary struct {
	TotalLeft              int   `json:"total_left"`
	TotalRight             int   `json:"total_right"`
	MatchedCount           int   `json:"matched"`
	UnmatchedLeft          int   `json:"unmatched_left"`
	UnmatchedRight         int   `json:"unmatched_right"`
	AmountDiffCount        int   `json:"amount_diff_count"`
	TimingDiffCount        int   `json:"timing_diff_count"`
	DuplicateCount         int   `json:"duplicate_count"`
	GroupedMatchedCount    int   `json:"grouped_matched_count"`
	ManyToManyMatchedCount int   `json:"many_to_many_matched_count"`
	MatchedAmountLeft      int64 `json:"matched_amount_left"`
	MatchedAmountRight     int64 `json:"matched_amount_right"`
	UnmatchedAmountLeft    int64 `json:"unmatched_amount_left"`
	UnmatchedAmountRight   int64 `json:"unmatched_amount_right"`
}

type ndjsonEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// jsonResult is the top-level JSON output shape (format=json or json-stream).
type jsonResult struct {
	Summary engineSummary `json:"summary"`
}

// ---------------------------------------------------------------------------
// Parsers
// ---------------------------------------------------------------------------

func parseNDJSON(path string) (engineSummary, error) {
	f, err := os.Open(path) // #nosec G304 -- benchmark input path passed via trusted CLI flag
	if err != nil {
		return engineSummary{}, err
	}
	defer f.Close()

	var s engineSummary
	var found bool
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	sc.Buffer(buf, 16*1024*1024)
	for sc.Scan() {
		var ev ndjsonEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return engineSummary{}, fmt.Errorf("parse ndjson: %w", err)
		}
		if ev.Type == "summary" {
			if err := json.Unmarshal(ev.Data, &s); err != nil {
				return engineSummary{}, fmt.Errorf("parse summary: %w", err)
			}
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		return engineSummary{}, err
	}
	if !found {
		return engineSummary{}, fmt.Errorf("summary event not found in %s", path)
	}
	return s, nil
}

func parseJSON(path string) (engineSummary, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- benchmark input path passed via trusted CLI flag
	if err != nil {
		return engineSummary{}, err
	}
	var r jsonResult
	if err := json.Unmarshal(data, &r); err != nil {
		return engineSummary{}, fmt.Errorf("parse json: %w", err)
	}
	return r.Summary, nil
}

// parseCSV extracts summary counts from CSV output. The CSV writer always
// emits one "summary" row at the end with type="summary".
func parseCSV(path string) (engineSummary, error) {
	f, err := os.Open(path) // #nosec G304 -- benchmark input path passed via trusted CLI flag
	if err != nil {
		return engineSummary{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)

	var headers []string
	for sc.Scan() {
		line := sc.Text()
		if headers == nil {
			headers = splitCSV(line)
			continue
		}
		fields := splitCSV(line)
		row := make(map[string]string, len(headers))
		for i, h := range headers {
			if i < len(fields) {
				row[h] = fields[i]
			}
		}
		if row["type"] != "summary" {
			continue
		}
		// Extract counts from the summary row.
		s := engineSummary{
			TotalLeft:       parseInt(row["total_left"]),
			TotalRight:      parseInt(row["total_right"]),
			MatchedCount:    parseInt(row["matched"]),
			UnmatchedLeft:   parseInt(row["unmatched_left"]),
			UnmatchedRight:  parseInt(row["unmatched_right"]),
			AmountDiffCount: parseInt(row["amount_diff_count"]),
			TimingDiffCount: parseInt(row["timing_diff_count"]),
			DuplicateCount:  parseInt(row["duplicate_count"]),
		}
		return s, nil
	}
	if err := sc.Err(); err != nil {
		return engineSummary{}, err
	}
	return engineSummary{}, fmt.Errorf("summary row not found in CSV %s", path)
}

func parseTable(path string) (engineSummary, error) {
	f, err := os.Open(path) // #nosec G304 -- benchmark input path passed via trusted CLI flag
	if err != nil {
		return engineSummary{}, err
	}
	defer f.Close()

	var s engineSummary
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "warning:") || strings.HasPrefix(line, "TYPE ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "match":
			s.TotalLeft++
			s.TotalRight++
			s.MatchedCount++
		case "amount_diff":
			s.TotalLeft++
			s.TotalRight++
			s.AmountDiffCount++
		case "timing_diff":
			s.TotalLeft++
			s.TotalRight++
			s.TimingDiffCount++
		case "unmatched_left":
			s.TotalLeft++
			s.UnmatchedLeft++
		case "unmatched_right":
			s.TotalRight++
			s.UnmatchedRight++
		case "duplicate":
			for _, field := range fields[1:] {
				if strings.HasPrefix(field, "count=") {
					s.DuplicateCount += parseInt(strings.TrimPrefix(field, "count="))
				}
			}
		case "summary":
			return s, nil
		}
	}
	if err := sc.Err(); err != nil {
		return engineSummary{}, err
	}
	return engineSummary{}, fmt.Errorf("summary row not found in table %s", path)
}

func splitCSV(line string) []string {
	// Simple CSV split (no quote handling needed for summary rows).
	return strings.Split(line, ",")
}

func parseInt(s string) int {
	if s == "" {
		return 0
	}
	var v int
	fmt.Sscanf(s, "%d", &v)
	return v
}

// ---------------------------------------------------------------------------
// Verification
// ---------------------------------------------------------------------------

func isSkipped(m manifest, format, backend string) (bool, string) {
	for _, e := range m.SkippedMatrix {
		if e.Format != "" && e.Format == format {
			return true, e.Reason
		}
		if e.Backend != "" && e.Backend == backend {
			return true, e.Reason
		}
	}
	return false, ""
}

func verify(m manifest, actual engineSummary) (failed bool) {
	check := func(name string, want, got int) {
		if want != got {
			fmt.Fprintf(os.Stderr, "FAIL %s: %s: want %d got %d\n", m.Scenario, name, want, got)
			failed = true
		}
	}
	checkAmt := func(name string, want, got int64) {
		if want != got {
			fmt.Fprintf(os.Stderr, "FAIL %s: %s: want %d got %d\n", m.Scenario, name, want, got)
			failed = true
		}
	}

	check("total_left", m.Summary.TotalLeft, actual.TotalLeft)
	check("total_right", m.Summary.TotalRight, actual.TotalRight)
	check("matched", m.Summary.Matched, actual.MatchedCount)
	check("unmatched_left", m.Summary.UnmatchedLeft, actual.UnmatchedLeft)
	check("unmatched_right", m.Summary.UnmatchedRight, actual.UnmatchedRight)
	check("amount_diff_count", m.Summary.AmountDiffCount, actual.AmountDiffCount)
	check("timing_diff_count", m.Summary.TimingDiffCount, actual.TimingDiffCount)
	check("duplicate_count", m.Summary.DuplicateCount, actual.DuplicateCount)
	check("grouped_matched_count", m.Summary.GroupedMatchedCount, actual.GroupedMatchedCount)
	check("many_to_many_matched_count", m.Summary.ManyToManyMatchedCount, actual.ManyToManyMatchedCount)

	for currency, want := range m.CurrencyTotals {
		// For multi-format runs the monetary totals come from the summary event.
		// Only the NDJSON and JSON paths carry full monetary fields.
		// Currency-keyed breakdown is single-currency here (USD), so the top-level
		// summary fields are the per-currency totals.
		checkAmt(currency+".matched_amount_left", want.MatchedAmountLeft, actual.MatchedAmountLeft)
		checkAmt(currency+".matched_amount_right", want.MatchedAmountRight, actual.MatchedAmountRight)
		checkAmt(currency+".unmatched_amount_left", want.UnmatchedAmountLeft, actual.UnmatchedAmountLeft)
		checkAmt(currency+".unmatched_amount_right", want.UnmatchedAmountRight, actual.UnmatchedAmountRight)
	}
	return
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	manifestPath := flag.String("manifest", "", "manifest.json path (required)")
	actualPath := flag.String("actual", "", "output file path (required)")
	format := flag.String("format", "ndjson", "output format: ndjson, json, json-stream, csv, table")
	backend := flag.String("backend", "memory", "index backend: memory, disk, partitioned")
	flag.Parse()

	if *manifestPath == "" || *actualPath == "" {
		fmt.Fprintln(os.Stderr, "usage: verify_adversarial --manifest manifest.json --actual output.ndjson [--format ndjson] [--backend memory]")
		os.Exit(2)
	}

	data, err := os.ReadFile(*manifestPath) // #nosec G304 -- trusted benchmark input path
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		fmt.Fprintf(os.Stderr, "parse manifest: %v\n", err)
		os.Exit(1)
	}

	// Check skipped matrix before attempting to parse output.
	if skipped, reason := isSkipped(m, *format, *backend); skipped {
		fmt.Printf("SKIP %s format=%s backend=%s reason=%s\n", m.Scenario, *format, *backend, reason)
		os.Exit(2)
	}

	var actual engineSummary
	switch *format {
	case "ndjson":
		actual, err = parseNDJSON(*actualPath)
	case "json", "json-stream":
		actual, err = parseJSON(*actualPath)
	case "csv":
		actual, err = parseCSV(*actualPath)
	case "table":
		// Table format does not carry structured monetary totals. Counts are
		// reconstructed from its event rows and duplicate summary annotations.
		actual, err = parseTable(*actualPath)
	default:
		fmt.Fprintf(os.Stderr, "unknown format %q\n", *format)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse actual %s: %v\n", *actualPath, err)
		os.Exit(1)
	}

	// Monetary totals are not available in CSV/table output.
	if *format == "csv" || *format == "table" {
		for currency := range m.CurrencyTotals {
			m.CurrencyTotals[currency] = currencyTotals{} // zero out; skip assertion
		}
	}

	if failed := verify(m, actual); failed {
		os.Exit(1)
	}
	fmt.Printf("PASS %s format=%s backend=%s matched=%d grouped_matched=%d many_to_many_matched=%d unmatched_left=%d unmatched_right=%d duplicate_count=%d\n",
		m.Scenario, *format, *backend,
		actual.MatchedCount,
		actual.GroupedMatchedCount,
		actual.ManyToManyMatchedCount,
		actual.UnmatchedLeft,
		actual.UnmatchedRight,
		actual.DuplicateCount,
	)
}
