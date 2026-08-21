package explain

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/schemas"
)

func TestExplainJSONBoundsExceptions(t *testing.T) {
	result := domain.Result{
		Schema:        schemas.ResultSchemaV1,
		Summary:       domain.Summary{MatchedCount: 3, UnmatchedLeft: 2, AmountDiffCount: 1, TotalDiscrepancy: 15},
		UnmatchedLeft: []domain.Transaction{{ID: "left-1"}, {ID: "left-2"}},
		AmountDiff:    []domain.AmountDiffPair{{Left: domain.Transaction{ID: "left-3"}, DiffMinor: 15}},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Explain(bytes.NewReader(data), Options{TopN: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != schemas.ExplanationSchemaV1 || got.Summary.UnmatchedLeft != 2 {
		t.Fatalf("unexpected explanation: %+v", got)
	}
	if got.ExceptionsTotal != 3 || len(got.TopExceptions) != 2 || !got.Truncated {
		t.Fatalf("unexpected exception bounds: %+v", got)
	}
	if got.TopExceptions[0].Type != "unmatched_left" {
		t.Fatalf("first type = %q", got.TopExceptions[0].Type)
	}
}

func TestExplainNDJSONIncludesGroupedAndSourceEvents(t *testing.T) {
	lines := []string{
		`{"schema":"reconify.engine.result.v1","type":"grouped_amount_diff","data":{"diff_minor":7}}`,
		`{"type":"source_summary","data":{"source":"stripe","summary":{"amount_diff_count":1}}}`,
		`{"type":"summary","data":{"grouped_amount_diff_count":1,"amount_diff_count":1}}`,
	}
	got, err := Explain(strings.NewReader(strings.Join(lines, "\n")+"\n"), Options{TopN: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got.BySource["stripe"].AmountDiffCount != 1 || got.TopExceptions[0].Type != "grouped_amount_diff" {
		t.Fatalf("unexpected NDJSON explanation: %+v", got)
	}
}

func TestExplainRejectsMalformedNDJSON(t *testing.T) {
	_, err := Explain(strings.NewReader(`{"type":"summary","data":`), Options{TopN: 10})
	if err == nil {
		t.Fatal("expected malformed NDJSON error")
	}
}

func TestExplainAcceptsLegacyJSONWithoutSchema(t *testing.T) {
	got, err := Explain(strings.NewReader(`{"summary":{"matched":2},"matched":[]}`), Options{TopN: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.MatchedCount != 2 {
		t.Fatalf("matched = %d", got.Summary.MatchedCount)
	}
}

func TestExplainRetainsSuppressedExceptionCounts(t *testing.T) {
	result := domain.Result{Summary: domain.Summary{UnmatchedLeft: 2, AmountDiffCount: 1}}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Explain(bytes.NewReader(data), Options{TopN: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExceptionsTotal != 3 || !got.Truncated || len(got.TopExceptions) != 0 {
		t.Fatalf("unexpected summary-only explanation: %+v", got)
	}
}
