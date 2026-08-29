package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/output"
	"github.com/reconifyhq/reconify/engine/parser"
)

func TestFinancialFindingsBatchStreamingParity(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	rightPath := filepath.Join(dir, "right.csv")
	data := "date,amount,ref,gross,fee,net\n2024-01-01,98.50,A,100.00,1.50,98.50\n2024-01-02,98.48,B,100.00,1.52,98.48\n"
	if err := os.WriteFile(leftPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rightPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", RefCol: "ref", Multiplier: 100,
		Financials: &config.FinancialsCfg{GrossCol: "gross", NetCol: "net", Fields: map[string]string{"fee": "fee"}, Expectations: map[string]config.ExpectationCfg{"fee": {Percentage: &config.PercentageCfg{Base: "gross", Rate: 1.5}, Operation: "subtract", ToleranceMinor: 1}}}}
	left, err := parser.Parse("left", leftPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	right, err := parser.Parse("right", rightPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := Reconcile("p", "left", "right", left, right, config.Pair{DateWindow: "0d"})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w, err := output.NewResultWriter("json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReconcileStreaming(context.Background(), "p", "left", "right", leftPath, rightPath, cfg, cfg, config.Pair{DateWindow: "0d"}, NewMemoryIndex(), w, 0); err != nil {
		t.Fatal(err)
	}
	var streamed domain.Result
	if err := json.Unmarshal(buf.Bytes(), &streamed); err != nil {
		t.Fatal(err)
	}
	if len(batch.FinancialEffectDiffs) != len(streamed.FinancialEffectDiffs) || len(batch.SettlementMatches) != len(streamed.SettlementMatches) || batch.Summary.FinancialEffectDiffCount != streamed.Summary.FinancialEffectDiffCount {
		t.Fatalf("financial parity mismatch: batch diffs=%d settlements=%d summary=%d; stream diffs=%d settlements=%d summary=%d", len(batch.FinancialEffectDiffs), len(batch.SettlementMatches), batch.Summary.FinancialEffectDiffCount, len(streamed.FinancialEffectDiffs), len(streamed.SettlementMatches), streamed.Summary.FinancialEffectDiffCount)
	}
}
