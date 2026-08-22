package matching_test

import (
	"testing"
	"time"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/matching"
)

func subsetSumPass(opts ...func(*config.PassConfig)) config.PassConfig {
	p := config.PassConfig{Type: config.PassTypeSubsetSum}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func withMaxCandidates(n int) func(*config.PassConfig) {
	return func(p *config.PassConfig) { p.MaxCandidates = n }
}

func withMaxSubsetSize(n int) func(*config.PassConfig) {
	return func(p *config.PassConfig) { p.MaxSubsetSize = n }
}

func withMaxAlternatives(n int) func(*config.PassConfig) {
	return func(p *config.PassConfig) { p.MaxAlternatives = n }
}

var baseDate = time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

func mkTx(id string, amount int64) Transaction {
	return Transaction{
		ID:        id,
		Date:      baseDate,
		Amount:    amount,
		Currency:  "USD",
		Reference: id,
	}
}

func mkTxDate(id string, amount int64, daysOffset int) Transaction {
	tx := mkTx(id, amount)
	tx.Date = baseDate.AddDate(0, 0, daysOffset)
	return tx
}

func TestMatchBySubsetSum_ExactSubset(t *testing.T) {
	left := []Transaction{mkTx("L1", 100)}
	right := []Transaction{mkTx("R1", 60), mkTx("R2", 40), mkTx("R3", 200)}

	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0, subsetSumPass())

	if len(result.SubsetSumMatched) != 1 {
		t.Fatalf("expected 1 subset_sum_matched, got %d", len(result.SubsetSumMatched))
	}
	m := result.SubsetSumMatched[0]
	if m.Left.ID != "L1" {
		t.Errorf("expected Left=L1, got %s", m.Left.ID)
	}
	if len(m.Rights) != 2 {
		t.Errorf("expected 2 rights, got %d", len(m.Rights))
	}
	// L1 is matched, R3 is unmatched
	if len(unmL) != 0 {
		t.Errorf("expected 0 unmatched left, got %d", len(unmL))
	}
	if len(unmR) != 1 || unmR[0].ID != "R3" {
		t.Errorf("expected R3 as only unmatched right, got %v", unmR)
	}
	if len(result.SubsetSumSkipped) != 0 {
		t.Errorf("expected no skipped, got %d", len(result.SubsetSumSkipped))
	}
}

func TestMatchBySubsetSum_ToleranceMatch(t *testing.T) {
	left := []Transaction{mkTx("L1", 100)}
	right := []Transaction{mkTx("R1", 61), mkTx("R2", 40)}

	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 2, 0, subsetSumPass())

	// 61+40=101, diff=1, within tolerance=2 → matched
	if len(result.SubsetSumMatched) != 1 {
		t.Fatalf("expected 1 subset_sum_matched (within tolerance), got %d", len(result.SubsetSumMatched))
	}
	if len(unmL) != 0 || len(unmR) != 0 {
		t.Errorf("expected no unmatched rows, got left=%d right=%d", len(unmL), len(unmR))
	}
}

func TestMatchBySubsetSum_NoMatch(t *testing.T) {
	left := []Transaction{mkTx("L1", 100)}
	right := []Transaction{mkTx("R1", 30), mkTx("R2", 25)}

	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0, subsetSumPass())

	if len(result.SubsetSumMatched) != 0 {
		t.Errorf("expected no matches, got %d", len(result.SubsetSumMatched))
	}
	if len(unmL) != 1 {
		t.Errorf("expected 1 unmatched left, got %d", len(unmL))
	}
	if len(unmR) != 2 {
		t.Errorf("expected 2 unmatched right (not consumed), got %d", len(unmR))
	}
}

func TestMatchBySubsetSum_AmbiguousMultipleSubsets(t *testing.T) {
	// L1=100; R1=60, R2=40, R3=70, R4=30 → two valid subsets: {R1,R2} and {R3,R4}
	left := []Transaction{mkTx("L1", 100)}
	right := []Transaction{mkTx("R1", 60), mkTx("R2", 40), mkTx("R3", 70), mkTx("R4", 30)}

	result := &Result{}
	unmL, _ := matching.MatchBySubsetSum(result, left, right, 0, 0,
		subsetSumPass(withMaxAlternatives(3)))

	if len(result.SubsetSumAmbiguous) != 1 {
		t.Fatalf("expected 1 ambiguous event, got %d (matched=%d)", len(result.SubsetSumAmbiguous), len(result.SubsetSumMatched))
	}
	if len(result.SubsetSumAmbiguous[0].Alternatives) < 2 {
		t.Errorf("expected ≥2 alternatives, got %d", len(result.SubsetSumAmbiguous[0].Alternatives))
	}
	// Left row goes back to unmatched
	if len(unmL) != 1 || unmL[0].ID != "L1" {
		t.Errorf("expected L1 in unmatched left for ambiguous case, got %v", unmL)
	}
}

func TestMatchBySubsetSum_CandidateLimitExceeded(t *testing.T) {
	left := []Transaction{mkTx("L1", 100)}
	// 5 candidates, limit set to 3
	right := []Transaction{
		mkTx("R1", 20), mkTx("R2", 30), mkTx("R3", 50),
		mkTx("R4", 10), mkTx("R5", 90),
	}

	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0,
		subsetSumPass(withMaxCandidates(3)))

	if len(result.SubsetSumSkipped) != 1 {
		t.Fatalf("expected 1 skipped (candidate limit), got %d", len(result.SubsetSumSkipped))
	}
	if result.SubsetSumSkipped[0].Reason != matching.SubsetSumSkipReasonCandidateLimit {
		t.Errorf("expected reason %q, got %q", matching.SubsetSumSkipReasonCandidateLimit, result.SubsetSumSkipped[0].Reason)
	}
	// L1 stays unmatched, all right rows stay unconsumed
	if len(unmL) != 1 {
		t.Errorf("expected 1 unmatched left, got %d", len(unmL))
	}
	if len(unmR) != 5 {
		t.Errorf("expected 5 unconsumed right rows, got %d", len(unmR))
	}
}

func TestMatchBySubsetSum_DateWindowFilter(t *testing.T) {
	left := []Transaction{mkTx("L1", 100)}
	// R1 is within window, R2 is outside → only R1 is a candidate
	right := []Transaction{
		mkTxDate("R1", 60, 1),  // 1 day offset — within window=3
		mkTxDate("R2", 40, 10), // 10 days offset — outside window=3
	}

	result := &Result{}
	unmL, _ := matching.MatchBySubsetSum(result, left, right, 0, 3,
		subsetSumPass())

	// Only R1 is a candidate; 60 ≠ 100 → no match
	if len(result.SubsetSumMatched) != 0 {
		t.Errorf("expected no match (R2 filtered by date), got %d", len(result.SubsetSumMatched))
	}
	if len(unmL) != 1 {
		t.Errorf("expected L1 still unmatched, got %d unmatched left", len(unmL))
	}
}

func TestMatchBySubsetSum_InteractionWithPriorPass(t *testing.T) {
	// Simulate subset_sum only seeing rows unmatched by a prior pass.
	// L1 was matched by reference pass → not in left slice passed to subset_sum.
	// L2 still needs matching.
	left := []Transaction{mkTx("L2", 70)}
	right := []Transaction{mkTx("R1", 30), mkTx("R2", 40), mkTx("R3", 99)}

	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0, subsetSumPass())

	if len(result.SubsetSumMatched) != 1 {
		t.Fatalf("expected 1 match for L2, got %d", len(result.SubsetSumMatched))
	}
	if len(unmL) != 0 {
		t.Errorf("expected L2 matched, got %d unmatched left", len(unmL))
	}
	if len(unmR) != 1 || unmR[0].ID != "R3" {
		t.Errorf("expected R3 unconsumed, got %v", unmR)
	}
}

func TestMatchBySubsetSum_MaxSubsetSizeRespected(t *testing.T) {
	// L1=100; need 3 rows to sum to 100, but max_subset_size=2
	left := []Transaction{mkTx("L1", 100)}
	right := []Transaction{mkTx("R1", 20), mkTx("R2", 30), mkTx("R3", 50)}

	result := &Result{}
	// With max_subset_size=2: {R1,R2}=50, {R1,R3}=70, {R2,R3}=80 — none equal 100
	// Only {R1,R2,R3}=100 works but that's size 3 → should not match
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0,
		subsetSumPass(withMaxSubsetSize(2)))

	if len(result.SubsetSumMatched) != 0 {
		t.Errorf("expected no match with max_subset_size=2, got %d", len(result.SubsetSumMatched))
	}
	if len(unmL) != 1 {
		t.Errorf("expected L1 unmatched, got %d", len(unmL))
	}
	if len(unmR) != 3 {
		t.Errorf("expected all 3 rights unconsumed, got %d", len(unmR))
	}
}

func TestMatchBySubsetSum_SameSignFilter(t *testing.T) {
	left := []Transaction{mkTx("L1", 100)}
	// R1 is negative — same_sign=true (default) should exclude it
	right := []Transaction{mkTx("R1", -60), mkTx("R2", 100)}

	result := &Result{}
	unmL, _ := matching.MatchBySubsetSum(result, left, right, 0, 0, subsetSumPass())

	if len(result.SubsetSumMatched) != 1 {
		t.Fatalf("expected 1 match (R2=100), got %d", len(result.SubsetSumMatched))
	}
	if result.SubsetSumMatched[0].Rights[0].ID != "R2" {
		t.Errorf("expected R2 matched, got %s", result.SubsetSumMatched[0].Rights[0].ID)
	}
	if len(unmL) != 0 {
		t.Errorf("expected no unmatched left, got %d", len(unmL))
	}
}

func TestMatchBySubsetSum_CurrencyFilter(t *testing.T) {
	left := []Transaction{mkTx("L1", 100)}
	left[0].Currency = "USD"

	right := []Transaction{mkTx("R1", 60), mkTx("R2", 40)}
	right[0].Currency = "EUR" // different currency — excluded by filter
	right[1].Currency = "USD"

	result := &Result{}
	// R1 (EUR) excluded; R2 (USD)=40 ≠ 100 → no match
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0, subsetSumPass())

	if len(result.SubsetSumMatched) != 0 {
		t.Errorf("expected no match (R1 filtered by currency), got %d", len(result.SubsetSumMatched))
	}
	if len(unmL) != 1 {
		t.Errorf("expected L1 unmatched, got %d", len(unmL))
	}
	// R1 should still be unconsumed (filtered, not used)
	if len(unmR) != 2 {
		t.Errorf("expected both rights unconsumed, got %d", len(unmR))
	}
}

func TestMatchBySubsetSum_ZeroCandidatesIsNotCandidateLimitExceeded(t *testing.T) {
	// All right rows filtered out by currency — zero candidates, well under
	// max_candidates. This must NOT be reported as candidate_limit_exceeded.
	left := []Transaction{mkTx("L1", 100)}
	right := []Transaction{mkTx("R1", 60), mkTx("R2", 40)}
	right[0].Currency = "EUR"
	right[1].Currency = "EUR"

	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0, subsetSumPass())

	if len(result.SubsetSumSkipped) != 0 {
		t.Fatalf("expected no skipped events for zero candidates, got %d (reason=%v)",
			len(result.SubsetSumSkipped), result.SubsetSumSkipped)
	}
	if len(unmL) != 1 {
		t.Errorf("expected L1 unmatched, got %d", len(unmL))
	}
	if len(unmR) != 2 {
		t.Errorf("expected both rights unconsumed, got %d", len(unmR))
	}
}

func TestMatchBySubsetSum_EmptySubsetNotAccepted(t *testing.T) {
	// Left amount is 0, tolerance is large enough that an empty subset would
	// trivially satisfy |target - 0| <= tolerance. It must not be reported
	// as a match with zero rights.
	left := []Transaction{mkTx("L1", 0)}
	right := []Transaction{mkTx("R1", 50)}

	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 10, 0, subsetSumPass())

	if len(result.SubsetSumMatched) != 0 {
		t.Fatalf("expected no match (empty subset must not be accepted), got %d", len(result.SubsetSumMatched))
	}
	if len(unmL) != 1 {
		t.Errorf("expected L1 unmatched, got %d", len(unmL))
	}
	if len(unmR) != 1 {
		t.Errorf("expected R1 unconsumed, got %d", len(unmR))
	}
}

func TestMatchBySubsetSum_MixedSignPruningFindsMatch(t *testing.T) {
	// same_sign disabled; candidates include a large offsetting negative value.
	// The branch-and-bound prune must not incorrectly discard this subset.
	left := []Transaction{mkTx("L1", 100)}
	right := []Transaction{mkTx("R1", 110), mkTx("R2", 490), mkTx("R3", -500)}

	result := &Result{}
	filters := config.SubsetSumFilters{Currency: true, DateWindow: true, SameSign: false}
	unmL, unmR := matching.MatchBySubsetSum(result, left, right, 0, 0,
		subsetSumPass(func(p *config.PassConfig) { p.CandidateFilters = &filters }))

	if len(result.SubsetSumMatched) != 1 {
		t.Fatalf("expected 1 match (110+490-500=100), got %d matched, %d ambiguous, %d skipped",
			len(result.SubsetSumMatched), len(result.SubsetSumAmbiguous), len(result.SubsetSumSkipped))
	}
	if len(unmL) != 0 {
		t.Errorf("expected L1 matched, got %d unmatched left", len(unmL))
	}
	if len(unmR) != 0 {
		t.Errorf("expected all rights consumed, got %d unmatched right", len(unmR))
	}
}

func TestMatchBySubsetSum_EmptyInputs(t *testing.T) {
	result := &Result{}
	unmL, unmR := matching.MatchBySubsetSum(result, nil, nil, 0, 0, subsetSumPass())

	if len(unmL) != 0 || len(unmR) != 0 {
		t.Errorf("expected empty outputs for empty inputs")
	}
	if len(result.SubsetSumMatched)+len(result.SubsetSumAmbiguous)+len(result.SubsetSumSkipped) != 0 {
		t.Errorf("expected no events for empty inputs")
	}
}
