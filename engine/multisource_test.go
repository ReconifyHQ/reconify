package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func TestReconcileMultiSource_ProgressiveConsumption(t *testing.T) {
	left := []Transaction{
		makeTx("l1", 100, "REF-1"),
		makeTx("l2", 200, "REF-2"),
		makeTx("l3", 300, "REF-3"),
	}
	counterparts := []CounterpartInput{
		{SourceName: "stripe", Transactions: []Transaction{makeTx("a1", 100, "REF-1")}},
		{SourceName: "paypal", Transactions: []Transaction{
			makeTx("b1", 200, "REF-2"),
			makeTx("b2", 100, "REF-1"), // would match l1, but stripe already consumed it
		}},
		{SourceName: "ledger", Transactions: []Transaction{makeTx("c1", 300, "REF-3")}},
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := ReconcileMultiSource("p", "left", left, counterparts, pair)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Matched) != 3 {
		t.Fatalf("Matched = %d, want 3", len(res.Matched))
	}
	if len(res.UnmatchedLeft) != 0 {
		t.Fatalf("UnmatchedLeft = %d, want 0 (every left row should be consumed across passes)", len(res.UnmatchedLeft))
	}
	if len(res.UnmatchedRight) != 1 || res.UnmatchedRight[0].ID != "b2" {
		t.Fatalf("UnmatchedRight = %+v, want [b2] (REF-1 already consumed by stripe)", res.UnmatchedRight)
	}

	if len(res.BySource) != 3 {
		t.Fatalf("BySource has %d entries, want 3", len(res.BySource))
	}
	if res.BySource["stripe"].MatchedCount != 1 {
		t.Errorf("stripe MatchedCount = %d, want 1", res.BySource["stripe"].MatchedCount)
	}
	if res.BySource["paypal"].MatchedCount != 1 || res.BySource["paypal"].UnmatchedRight != 1 {
		t.Errorf("paypal summary = %+v, want matched=1 unmatched_right=1", res.BySource["paypal"])
	}
	if res.BySource["ledger"].MatchedCount != 1 {
		t.Errorf("ledger MatchedCount = %d, want 1", res.BySource["ledger"].MatchedCount)
	}

	// Aggregate summary: TotalRight sums every counterpart's input, TotalLeft is
	// the original (un-shrunk) left count.
	if res.Summary.TotalLeft != 3 {
		t.Errorf("Summary.TotalLeft = %d, want 3", res.Summary.TotalLeft)
	}
	if res.Summary.TotalRight != 4 {
		t.Errorf("Summary.TotalRight = %d, want 4", res.Summary.TotalRight)
	}
	if res.Summary.MatchedCount != 3 {
		t.Errorf("Summary.MatchedCount = %d, want 3", res.Summary.MatchedCount)
	}
}

// TestReconcileMultiSource_PassOrderDeterminesWinner verifies that when two
// counterparts both have a candidate that could match the same left row, the
// earlier pass in the configured order always wins — order is a deliberate,
// deterministic configuration choice, not an accident of input ordering.
func TestReconcileMultiSource_PassOrderDeterminesWinner(t *testing.T) {
	left := []Transaction{makeTx("l1", 100, "REF-1")}
	first := CounterpartInput{SourceName: "first", Transactions: []Transaction{makeTx("f1", 100, "REF-1")}}
	second := CounterpartInput{SourceName: "second", Transactions: []Transaction{makeTx("s1", 100, "REF-1")}}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	resFirstWins, err := ReconcileMultiSource("p", "left", left, []CounterpartInput{first, second}, pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(resFirstWins.Matched) != 1 || resFirstWins.Matched[0].Right.ID != "f1" {
		t.Fatalf("expected 'first' to win when listed first, got %+v", resFirstWins.Matched)
	}

	resSecondWins, err := ReconcileMultiSource("p", "left", left, []CounterpartInput{second, first}, pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(resSecondWins.Matched) != 1 || resSecondWins.Matched[0].Right.ID != "s1" {
		t.Fatalf("expected 'second' to win when listed first, got %+v", resSecondWins.Matched)
	}
}

func TestReconcileMultiSource_RequiresAtLeastOneCounterpart(t *testing.T) {
	_, err := ReconcileMultiSource("p", "left", nil, nil, config.Pair{})
	if err == nil {
		t.Fatal("expected an error when no counterparts are given")
	}
}

func TestReconcileMultiSource_DuplicatesAnnotatedOncePerLeftRow(t *testing.T) {
	// l1/l2 share a GroupKey but have distinct references; l1 matches in pass 1,
	// l2 only matches in pass 2 — the left duplicate group must still be reported
	// exactly once, not once per pass it survives into.
	left := []Transaction{
		makeTxGroup("l1", 100, "REF-1", "INV-1"),
		makeTxGroup("l2", 200, "REF-2", "INV-1"),
	}
	counterparts := []CounterpartInput{
		{SourceName: "a", Transactions: []Transaction{makeTx("a1", 100, "REF-1")}},
		{SourceName: "b", Transactions: []Transaction{makeTx("b1", 200, "REF-2")}},
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	res, err := ReconcileMultiSource("p", "left", left, counterparts, pair)
	if err != nil {
		t.Fatal(err)
	}

	leftDupGroups := 0
	for _, g := range res.Duplicates {
		if g.Reference == "INV-1" && g.Source == "test" {
			leftDupGroups++
		}
	}
	if leftDupGroups != 1 {
		t.Fatalf("left duplicate group INV-1 reported %d times, want exactly 1", leftDupGroups)
	}
}

func TestReconcileMultiSource_ManyToManyAggregatesBySource(t *testing.T) {
	left := []Transaction{
		makeTx("l1", 70, "STRIPE-PAYOUT"),
		makeTx("l2", 30, "STRIPE-PAYOUT"),
		makeTx("l3", 40, "PAYPAL-PAYOUT"),
		makeTx("l4", 60, "PAYPAL-PAYOUT"),
	}
	counterparts := []CounterpartInput{
		{SourceName: "stripe", Transactions: []Transaction{
			makeTx("s1", 50, "STRIPE-PAYOUT"),
			makeTx("s2", 50, "STRIPE-PAYOUT"),
		}},
		{SourceName: "paypal", Transactions: []Transaction{
			makeTx("p1", 100, "PAYPAL-PAYOUT"),
		}},
	}
	pair := config.Pair{
		DateWindow:           "0d",
		AmountToleranceMinor: 0,
		Passes:               []config.PassConfig{{Type: config.PassTypeManyToMany}},
	}

	res, err := ReconcileMultiSource("p", "left", left, counterparts, pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ManyToManyMatched) != 2 {
		t.Fatalf("ManyToManyMatched=%d want 2", len(res.ManyToManyMatched))
	}
	if len(res.UnmatchedLeft) != 0 || len(res.UnmatchedRight) != 0 {
		t.Fatalf("unmatched = left %d right %d, want 0/0", len(res.UnmatchedLeft), len(res.UnmatchedRight))
	}
	if res.BySource["stripe"].ManyToManyMatchedCount != 1 {
		t.Errorf("stripe ManyToManyMatchedCount=%d want 1", res.BySource["stripe"].ManyToManyMatchedCount)
	}
	if res.BySource["paypal"].ManyToManyMatchedCount != 1 {
		t.Errorf("paypal ManyToManyMatchedCount=%d want 1", res.BySource["paypal"].ManyToManyMatchedCount)
	}
	if res.Summary.ManyToManyMatchedCount != 2 {
		t.Errorf("summary ManyToManyMatchedCount=%d want 2", res.Summary.ManyToManyMatchedCount)
	}
}

func TestReconcileStreamingMultiSource_ProgressiveConsumption(t *testing.T) {
	dir := t.TempDir()
	leftPath := filepath.Join(dir, "left.csv")
	stripePath := filepath.Join(dir, "stripe.csv")
	paypalPath := filepath.Join(dir, "paypal.csv")

	leftCSV := "id,date,amount,currency,reference,name\n" +
		"l1,2024-01-01,1.00,USD,REF-1,Left1\n" +
		"l2,2024-01-01,2.00,USD,REF-2,Left2\n"
	stripeCSV := "id,date,amount,currency,reference,name\n" +
		"a1,2024-01-01,1.00,USD,REF-1,Stripe1\n"
	paypalCSV := "id,date,amount,currency,reference,name\n" +
		"b1,2024-01-01,2.00,USD,REF-2,Paypal1\n" +
		"b2,2024-01-01,1.00,USD,REF-1,Paypal2\n" // would match l1, but stripe already consumed it

	for path, content := range map[string]string{leftPath: leftCSV, stripePath: stripeCSV, paypalPath: paypalCSV} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}

	parserCfg := config.CSVParserCfg{
		Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount",
		Decimal: ".", Multiplier: 100, CurrencyCol: "currency", RefCol: "reference", NameCol: "name",
	}
	pair := config.Pair{DateWindow: "0d", AmountToleranceMinor: 0}

	stripeIdx := NewMemoryIndex()
	defer stripeIdx.Close()
	paypalIdx := NewMemoryIndex()
	defer paypalIdx.Close()

	var out bytes.Buffer
	w := newJSONWriter(&out)
	w.SetMeta("p", "left", "stripe,paypal")

	counterparts := []CounterpartStream{
		{SourceName: "stripe", RightPath: stripePath, RightCfg: parserCfg, Index: stripeIdx},
		{SourceName: "paypal", RightPath: paypalPath, RightCfg: parserCfg, Index: paypalIdx},
	}

	if err := ReconcileStreamingMultiSource(
		context.Background(), "p", "left", leftPath, parserCfg, counterparts, pair, w, 0,
	); err != nil {
		t.Fatalf("ReconcileStreamingMultiSource error: %v", err)
	}

	var res Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if len(res.Matched) != 2 {
		t.Fatalf("Matched = %d, want 2", len(res.Matched))
	}
	if len(res.UnmatchedLeft) != 0 {
		t.Fatalf("UnmatchedLeft = %d, want 0", len(res.UnmatchedLeft))
	}
	if len(res.UnmatchedRight) != 1 || res.UnmatchedRight[0].Name != "Paypal2" {
		t.Fatalf("UnmatchedRight = %+v, want [Paypal2]", res.UnmatchedRight)
	}
	if res.Summary.MatchedCount != 2 {
		t.Errorf("Summary.MatchedCount = %d, want 2", res.Summary.MatchedCount)
	}
	if res.Summary.TotalRight != 3 {
		t.Errorf("Summary.TotalRight = %d, want 3", res.Summary.TotalRight)
	}
	if len(res.BySource) != 2 {
		t.Fatalf("BySource has %d entries, want 2", len(res.BySource))
	}
	if res.BySource["stripe"].MatchedCount != 1 {
		t.Errorf("stripe MatchedCount = %d, want 1", res.BySource["stripe"].MatchedCount)
	}
	if res.BySource["paypal"].MatchedCount != 1 || res.BySource["paypal"].UnmatchedRight != 1 {
		t.Errorf("paypal summary = %+v, want matched=1 unmatched_right=1", res.BySource["paypal"])
	}
}

func TestReconcileStreamingMultiSource_RequiresAtLeastOneCounterpart(t *testing.T) {
	var out bytes.Buffer
	w := newJSONWriter(&out)
	err := ReconcileStreamingMultiSource(context.Background(), "p", "left", "left.csv", config.CSVParserCfg{}, nil, config.Pair{}, w, 0)
	if err == nil {
		t.Fatal("expected an error when no counterparts are given")
	}
}

func TestReconcileStreamingMultiSource_RejectsTokenMode(t *testing.T) {
	var out bytes.Buffer
	w := newJSONWriter(&out)
	cp := []CounterpartStream{{SourceName: "a", RightPath: "a.csv", Index: NewMemoryIndex()}}
	err := ReconcileStreamingMultiSource(context.Background(), "p", "left", "left.csv", config.CSVParserCfg{}, cp, config.Pair{NameMode: "tokens"}, w, 0)
	if err == nil {
		t.Fatal("expected an error for name_mode=tokens in multi-source streaming")
	}
}
