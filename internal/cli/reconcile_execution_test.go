package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/reconifyhq/reconify/engine"
	"github.com/reconifyhq/reconify/engine/output"
)

// TestDrainResultToWriter_EmitsSubsetSumEvents is a regression test: subset_sum
// events (matched, ambiguous, skipped) were never drained to the writer in the
// batch CLI path, so a pair that also had a many_to_many/one_to_many pass (or
// used the multi-source "rights:" batch path) would report non-zero
// subset_sum counts in the summary while emitting no row-level events at all.
func TestDrainResultToWriter_EmitsSubsetSumEvents(t *testing.T) {
	tx := func(id string, amount int64) engine.Transaction {
		return engine.Transaction{
			ID:        id,
			Date:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			Amount:    amount,
			Currency:  "USD",
			Reference: id,
		}
	}

	res := &engine.Result{
		SubsetSumMatched: []engine.SubsetSumMatchedPair{
			{Left: tx("l1", 100), Rights: []engine.Transaction{tx("r1", 60), tx("r2", 40)}},
		},
		SubsetSumAmbiguous: []engine.SubsetSumAmbiguousPair{
			{Left: tx("l2", 50), Alternatives: [][]engine.Transaction{{tx("r3", 50)}, {tx("r4", 50)}}},
		},
		SubsetSumSkipped: []engine.SubsetSumSkippedPair{
			{Left: tx("l3", 999), Reason: "candidate_limit_exceeded"},
		},
		Summary: engine.Summary{
			SubsetSumMatchedCount:   1,
			SubsetSumAmbiguousCount: 1,
			SubsetSumSkippedCount:   1,
		},
	}

	var out bytes.Buffer
	w := output.NewJSONWriter(&out)
	w.SetMeta("p", "left", "right")

	if err := drainResultToWriter(w, res); err != nil {
		t.Fatalf("drainResultToWriter: %v", err)
	}

	var decoded engine.Result
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(decoded.SubsetSumMatched) != 1 {
		t.Errorf("SubsetSumMatched in output = %d, want 1 (events were dropped)", len(decoded.SubsetSumMatched))
	}
	if len(decoded.SubsetSumAmbiguous) != 1 {
		t.Errorf("SubsetSumAmbiguous in output = %d, want 1 (events were dropped)", len(decoded.SubsetSumAmbiguous))
	}
	if len(decoded.SubsetSumSkipped) != 1 {
		t.Errorf("SubsetSumSkipped in output = %d, want 1 (events were dropped)", len(decoded.SubsetSumSkipped))
	}
}
