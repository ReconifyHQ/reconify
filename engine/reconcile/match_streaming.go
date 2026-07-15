//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"fmt"
	"time"

	. "github.com/reconifyhq/reconify/engine/domain"
)

type matchOutcome int

const (
	outcomeUnmatched matchOutcome = iota
	outcomeExact
	outcomeAmountDiff
	outcomeTimingDiff
)

type matchDecision struct {
	outcome         matchOutcome
	right           any
	rightTx         Transaction
	amountDiffMinor int64
	daysDiff        int
}

func decideMatch(ltx Transaction, idx RightIndex, tolerance int64, dateWindowDays int) (matchDecision, error) {
	if ltx.Reference == "" {
		return matchDecision{outcome: outcomeUnmatched}, nil
	}
	buckets, err := idx.Get(ltx.Reference)
	if err != nil {
		return matchDecision{}, fmt.Errorf("index get reference %q: %w", ltx.Reference, err)
	}
	var exact, amount, timing any
	var amountAbs int64
	var timingDays int
	for _, bucket := range buckets {
		if bucket.Used() {
			continue
		}
		rtx := bucket.Transaction(ltx.Reference)
		diff := ltx.Amount - rtx.Amount
		abs := diff
		if abs < 0 {
			abs = -abs
		}
		days := daysBetweenNano(ltx.Date.UnixNano(), rtx.Date.UnixNano())
		amountOK := abs <= tolerance
		dateOK := dateWindowDays == 0 || days <= dateWindowDays
		if amountOK && dateOK {
			exact = bucket
			break
		}
		if !amountOK && dateOK && (amount == nil || abs < amountAbs) {
			amount, amountAbs = bucket, abs
		}
		if amountOK && !dateOK && (timing == nil || days < timingDays) {
			timing, timingDays = bucket, days
		}
	}
	decision := matchDecision{outcome: outcomeUnmatched}
	switch {
	case exact != nil:
		decision.outcome, decision.right = outcomeExact, exact
	case amount != nil:
		decision.outcome, decision.right = outcomeAmountDiff, amount
	case timing != nil:
		decision.outcome, decision.right, decision.daysDiff = outcomeTimingDiff, timing, timingDays
	}
	if decision.right != nil {
		for _, bucket := range buckets {
			if bucket == decision.right {
				decision.rightTx = bucket.Transaction(ltx.Reference)
				break
			}
		}
		decision.amountDiffMinor = ltx.Amount - decision.rightTx.Amount
	}
	return decision, nil
}

func daysBetween(a, b time.Time) int { return daysBetweenNano(a.UnixNano(), b.UnixNano()) }

func daysBetweenNano(aNano, bNano int64) int {
	delta := aNano - bNano
	if delta < 0 {
		delta = -delta
	}
	return int(delta / 86_400_000_000_000)
}
