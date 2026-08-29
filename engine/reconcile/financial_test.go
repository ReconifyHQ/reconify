package reconcile

import (
	"testing"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
)

func TestReconcileFinancialChecksAreIndependentOfMatchClassification(t *testing.T) {
	left := Transaction{ID: "l1", Reference: "r1", FinancialChecks: []FinancialCheck{{Field: "fee", Actual: 152, Expected: 150, DiffMinor: 2, ToleranceMinor: 1, Status: "diff"}}}
	right := Transaction{ID: "r1", Reference: "r1"}
	result, err := Reconcile("p", "left", "right", []Transaction{left}, []Transaction{right}, config.Pair{DateWindow: "0d"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matched) != 1 || len(result.FinancialEffectDiffs) != 1 || result.Summary.MatchedCount != 1 {
		t.Fatalf("financial result changed matching: matched=%d findings=%d summary=%+v", len(result.Matched), len(result.FinancialEffectDiffs), result.Summary)
	}
}
