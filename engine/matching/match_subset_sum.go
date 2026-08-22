//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package matching

import (
	"sort"
	"time"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
)

// SubsetSumSkipReasonCandidateLimit is emitted when the candidate pool exceeds max_candidates.
const SubsetSumSkipReasonCandidateLimit = "candidate_limit_exceeded"

// SubsetSumSkipReasonTimeout is emitted when the per-row search deadline is exceeded.
const SubsetSumSkipReasonTimeout = "timeout"

// MatchBySubsetSum runs the bounded subset-sum pass. For each unmatched left row
// it builds a constrained candidate pool from right, searches subsets of right rows
// whose sum is within amount_tolerance_minor of the left amount, and classifies the
// result as matched, ambiguous, or skipped. Consumed right rows are removed from the
// returned unmatchedRight slice; unconsumed rows remain available to later passes.
func MatchBySubsetSum(
	result *Result,
	left, right []Transaction,
	tolerance int64,
	windowDays int,
	pass config.PassConfig,
) (unmatchedLeft, unmatchedRight []Transaction) {
	maxCandidates := pass.ResolvedMaxCandidates()
	maxSubsetSize := pass.ResolvedMaxSubsetSize()
	maxAlts := pass.ResolvedMaxAlternatives()
	timeoutMS := pass.ResolvedTimeoutMS()
	filters := pass.ResolvedCandidateFilters()

	// Track which right rows have been consumed by this pass.
	consumed := make(map[string]bool, len(right))

	for _, ltx := range left {
		candidates, exceeded := buildCandidates(ltx, right, consumed, filters, tolerance, windowDays, maxCandidates)

		if exceeded {
			result.SubsetSumSkipped = append(result.SubsetSumSkipped, SubsetSumSkippedPair{
				Left:   ltx,
				Reason: SubsetSumSkipReasonCandidateLimit,
			})
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}

		if len(candidates) == 0 {
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}

		deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
		alts, timedOut := searchSubsets(candidates, ltx.Amount, tolerance, maxSubsetSize, maxAlts, deadline)

		if timedOut {
			result.SubsetSumSkipped = append(result.SubsetSumSkipped, SubsetSumSkippedPair{
				Left:   ltx,
				Reason: SubsetSumSkipReasonTimeout,
			})
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}

		switch len(alts) {
		case 0:
			unmatchedLeft = append(unmatchedLeft, ltx)
		case 1:
			subset := alts[0]
			for _, r := range subset {
				consumed[r.ID] = true
			}
			result.SubsetSumMatched = append(result.SubsetSumMatched, SubsetSumMatchedPair{
				Left:   ltx,
				Rights: subset,
			})
		default:
			// Multiple valid subsets — consume all involved right rows to prevent
			// downstream passes from using rows that are ambiguously claimed.
			// The left row is returned as unmatched; the caller reports the ambiguity.
			seen := make(map[string]bool)
			for _, alt := range alts {
				for _, r := range alt {
					if !seen[r.ID] {
						seen[r.ID] = true
						consumed[r.ID] = true
					}
				}
			}
			result.SubsetSumAmbiguous = append(result.SubsetSumAmbiguous, SubsetSumAmbiguousPair{
				Left:         ltx,
				Alternatives: alts,
			})
			unmatchedLeft = append(unmatchedLeft, ltx)
		}
	}

	// Collect unconsumed right rows.
	for _, r := range right {
		if !consumed[r.ID] {
			unmatchedRight = append(unmatchedRight, r)
		}
	}
	return unmatchedLeft, unmatchedRight
}

// buildCandidates filters right rows by the configured constraints. Returns
// exceeded=true when the candidate count exceeds maxCandidates (signals skip);
// otherwise returns the (possibly empty) candidate slice.
func buildCandidates(
	ltx Transaction,
	right []Transaction,
	consumed map[string]bool,
	filters config.SubsetSumFilters,
	tolerance int64,
	windowDays int,
	maxCandidates int,
) (candidates []Transaction, exceeded bool) {
	for _, rtx := range right {
		if consumed[rtx.ID] {
			continue
		}
		if filters.Currency && ltx.Currency != "" && rtx.Currency != "" && ltx.Currency != rtx.Currency {
			continue
		}
		if filters.SameSign && !sameSign(ltx.Amount, rtx.Amount) {
			continue
		}
		if filters.DateWindow && windowDays > 0 && daysBetween(ltx.Date, rtx.Date) > windowDays {
			continue
		}
		candidates = append(candidates, rtx)
		if len(candidates) > maxCandidates {
			return nil, true // exceeded limit
		}
	}
	return candidates, false
}

// sameSign returns true when a and b are both non-negative or both negative.
// Zero is considered non-negative.
func sameSign(a, b int64) bool {
	return (a >= 0) == (b >= 0)
}

// searchSubsets uses branch-and-bound to find subsets of candidates summing to
// target (within tolerance). It stops after collecting maxAlts+1 alternatives so
// callers can distinguish "exactly one" from "ambiguous". Returns timedOut=true
// when the deadline is exceeded before the search completes.
func searchSubsets(
	candidates []Transaction,
	target int64,
	tolerance int64,
	maxSubsetSize int,
	maxAlts int,
	deadline time.Time,
) (alts [][]Transaction, timedOut bool) {
	// Sort ascending by absolute amount for bound pruning.
	sorted := make([]Transaction, len(candidates))
	copy(sorted, candidates)
	sort.Slice(sorted, func(i, j int) bool {
		ai := sorted[i].Amount
		if ai < 0 {
			ai = -ai
		}
		aj := sorted[j].Amount
		if aj < 0 {
			aj = -aj
		}
		return ai < aj
	})

	// Precompute suffix positive/negative sums so the achievable-range prune
	// works correctly even when candidates have mixed signs (same_sign: false).
	// For any remaining index range, the achievable additional sum from an
	// arbitrary subset lies between "sum of remaining negatives" (include all
	// negatives, no positives) and "sum of remaining positives" (include all
	// positives, no negatives).
	suffixPosSum := make([]int64, len(sorted)+1)
	suffixNegSum := make([]int64, len(sorted)+1)
	for i := len(sorted) - 1; i >= 0; i-- {
		v := sorted[i].Amount
		suffixPosSum[i] = suffixPosSum[i+1]
		suffixNegSum[i] = suffixNegSum[i+1]
		if v > 0 {
			suffixPosSum[i] += v
		} else {
			suffixNegSum[i] += v
		}
	}

	var current []Transaction
	checkCount := 0

	var dfs func(idx int, runningSum int64) bool
	dfs = func(idx int, runningSum int64) bool {
		checkCount++
		if checkCount%512 == 0 && time.Now().After(deadline) {
			timedOut = true
			return true // signal stop
		}

		if len(current) > 0 {
			diff := target - runningSum
			if diff < 0 {
				diff = -diff
			}
			if diff <= tolerance {
				cp := make([]Transaction, len(current))
				copy(cp, current)
				alts = append(alts, cp)
				if len(alts) > maxAlts {
					return true // collected enough, stop
				}
			}
		}

		if idx >= len(sorted) || len(current) >= maxSubsetSize {
			return false
		}

		// Prune: even the best achievable additional sum from the remaining
		// candidates can't bring the total within tolerance of target.
		low := runningSum + suffixNegSum[idx]
		high := runningSum + suffixPosSum[idx]
		targetLow := target - tolerance
		targetHigh := target + tolerance
		if high < targetLow || low > targetHigh {
			return false
		}

		for i := idx; i < len(sorted); i++ {
			current = append(current, sorted[i])
			if stop := dfs(i+1, runningSum+sorted[i].Amount); stop {
				current = current[:len(current)-1]
				return true
			}
			current = current[:len(current)-1]
		}
		return false
	}

	dfs(0, 0)
	if timedOut {
		return nil, true
	}
	return alts, false
}
