package engine

import (
	"fmt"

	"github.com/reconifyhq/reconify/config"
)

type matchOutcome int

const (
	outcomeUnmatched matchOutcome = iota
	outcomeExact
	outcomeAmountDiff
	outcomeTimingDiff
)

// matchDecision is the result of matching one left transaction against a
// RightIndex bucket list. For non-unmatched outcomes, right is the winning
// bucket — callers must call idx.MarkUsed(right) themselves once they've
// decided to act on it (decideMatch never mutates the index).
type matchDecision struct {
	outcome         matchOutcome
	right           *bucket
	amountDiffMinor int64
	daysDiff        int
}

// decideMatch runs the two-pass best-candidate selection against ltx.Reference's
// bucket list in idx: an exact match (amount within tolerance and date within
// window) always wins immediately. Otherwise the best amount-diff candidate and
// best timing-diff candidate are tracked by smallest diff, so a worse candidate
// earlier in the bucket list never wins over a better one later in it. This is
// the single shared implementation used by both ReconcileStreaming and the
// multi-source streaming driver — keeping best-candidate selection in one place
// avoids the two paths drifting apart.
func decideMatch(ltx Transaction, idx RightIndex, tolerance int64, dateWindowDays int) (matchDecision, error) {
	if ltx.Reference == "" {
		return matchDecision{outcome: outcomeUnmatched}, nil
	}
	buckets, err := idx.Get(ltx.Reference)
	if err != nil {
		return matchDecision{}, fmt.Errorf("index get reference %q: %w", ltx.Reference, err)
	}

	ltxDateNano := ltx.Date.UnixNano()
	var exact *bucket
	var bestAmountDiff *bucket
	var bestAmountDiffAbs int64
	var bestTimingDiff *bucket
	var bestTimingDiffDays int

	for _, b := range buckets {
		if b.used {
			continue
		}

		amtDiff := ltx.Amount - b.amount
		if amtDiff < 0 {
			amtDiff = -amtDiff
		}
		daysDiff := daysBetweenNano(ltxDateNano, b.dateUnix)
		amtOk := amtDiff <= tolerance
		dateOk := dateWindowDays == 0 || daysDiff <= dateWindowDays

		if amtOk && dateOk {
			exact = b
			break
		}
		if !amtOk && dateOk {
			if bestAmountDiff == nil || amtDiff < bestAmountDiffAbs {
				bestAmountDiff = b
				bestAmountDiffAbs = amtDiff
			}
			continue
		}
		if amtOk && !dateOk {
			if bestTimingDiff == nil || daysDiff < bestTimingDiffDays {
				bestTimingDiff = b
				bestTimingDiffDays = daysDiff
			}
		}
	}

	switch {
	case exact != nil:
		return matchDecision{outcome: outcomeExact, right: exact}, nil
	case bestAmountDiff != nil:
		return matchDecision{outcome: outcomeAmountDiff, right: bestAmountDiff, amountDiffMinor: ltx.Amount - bestAmountDiff.amount}, nil
	case bestTimingDiff != nil:
		return matchDecision{outcome: outcomeTimingDiff, right: bestTimingDiff, daysDiff: bestTimingDiffDays}, nil
	default:
		return matchDecision{outcome: outcomeUnmatched}, nil
	}
}

// matchByReference matches transactions by reference string.
// It classifies matches, amount diffs, and timing diffs.
// Returns remaining unmatched slices.
func matchByReference(
	result *Result,
	left, right []Transaction,
	tolerance int64,
	windowDays int,
) (unmatchedLeft, unmatchedRight []Transaction) {
	// Index right transactions by reference for O(1) lookup
	rightByRef := make(map[string][]Transaction, len(right))
	for _, tx := range right {
		if tx.Reference != "" {
			rightByRef[tx.Reference] = append(rightByRef[tx.Reference], tx)
		}
	}

	usedRight := make(map[string]bool)

	for _, ltx := range left {
		if ltx.Reference == "" {
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}

		candidates, ok := rightByRef[ltx.Reference]
		if !ok {
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}

		// Two-pass best-candidate selection: an exact match always wins immediately.
		// Otherwise track the best amount-diff and best timing-diff candidates by
		// smallest diff, so a worse candidate earlier in the list never wins over a
		// better one later in it.
		var exact *Transaction
		var bestAmountDiff *Transaction
		var bestAmountDiffAbs int64
		var bestTimingDiff *Transaction
		var bestTimingDiffDays int

		for i := range candidates {
			rtx := &candidates[i]
			if usedRight[rtx.ID] {
				continue
			}

			amtDiff := ltx.Amount - rtx.Amount
			if amtDiff < 0 {
				amtDiff = -amtDiff
			}
			daysDiff := daysBetween(ltx.Date, rtx.Date)

			amtOk := amtDiff <= tolerance
			dateOk := windowDays == 0 || daysDiff <= windowDays

			if amtOk && dateOk {
				exact = rtx
				break
			}
			if !amtOk && dateOk {
				if bestAmountDiff == nil || amtDiff < bestAmountDiffAbs {
					bestAmountDiff = rtx
					bestAmountDiffAbs = amtDiff
				}
				continue
			}
			if amtOk && !dateOk {
				if bestTimingDiff == nil || daysDiff < bestTimingDiffDays {
					bestTimingDiff = rtx
					bestTimingDiffDays = daysDiff
				}
			}
		}

		switch {
		case exact != nil:
			result.Matched = append(result.Matched, MatchedPair{Left: ltx, Right: *exact})
			usedRight[exact.ID] = true
		case bestAmountDiff != nil:
			signed := ltx.Amount - bestAmountDiff.Amount
			result.AmountDiff = append(result.AmountDiff, AmountDiffPair{Left: ltx, Right: *bestAmountDiff, DiffMinor: signed})
			usedRight[bestAmountDiff.ID] = true
		case bestTimingDiff != nil:
			result.TimingDiff = append(result.TimingDiff, TimingDiffPair{Left: ltx, Right: *bestTimingDiff, DaysDiff: bestTimingDiffDays})
			usedRight[bestTimingDiff.ID] = true
		default:
			unmatchedLeft = append(unmatchedLeft, ltx)
		}
	}

	// Collect right transactions that were never used
	for _, rtx := range right {
		if !usedRight[rtx.ID] {
			unmatchedRight = append(unmatchedRight, rtx)
		}
	}

	return unmatchedLeft, unmatchedRight
}

// matchByReferenceOneToMany handles the case where one left transaction is settled
// by N right transactions sharing the same reference (e.g. one invoice paid via
// installments). All right rows for a reference are summed before comparison.
//
// Ambiguity detection runs first: when more than one left row shares a reference
// the grouping is undetermined (first-wins would silently mis-reconcile money), so
// those rows are emitted as AmbiguousGroupPair and excluded from matching entirely.
//
// Users must set group_col to a per-row-unique column on the right source when right
// rows are intentional installments sharing a reference — otherwise the duplicate
// detector will flag them before this pass runs.
func matchByReferenceOneToMany(
	result *Result,
	left, right []Transaction,
	tolerance int64,
	windowDays int,
	groupBy string,
) (unmatchedLeft, unmatchedRight []Transaction) {
	keyOf := func(tx Transaction) string {
		switch groupBy {
		case config.GroupByName:
			return tx.Name
		case config.GroupByGroupKey:
			return tx.GroupKey
		default:
			return tx.Reference
		}
	}

	// Build groupBy → right-rows index, skipping empty keys.
	rightByRef := make(map[string][]Transaction, len(right))
	for _, tx := range right {
		if k := keyOf(tx); k != "" {
			rightByRef[k] = append(rightByRef[k], tx)
		}
	}

	// Build groupBy → left-indices index in a single pass.
	leftByRef := make(map[string][]int, len(left))
	for i, tx := range left {
		if k := keyOf(tx); k != "" {
			leftByRef[k] = append(leftByRef[k], i)
		}
	}

	// Track consumed left rows and right rows.
	consumedLeft := make(map[int]bool)
	usedRight := make(map[string]bool)

	// Ambiguity pass: any reference claimed by >1 left row is undetermined.
	// Emit one AmbiguousGroupPair per such reference and mark all involved rows consumed.
	for ref, indices := range leftByRef {
		if len(indices) <= 1 {
			continue
		}
		rows := make([]Transaction, len(indices))
		for i, idx := range indices {
			rows[i] = left[idx]
			consumedLeft[idx] = true
		}
		rights := rightByRef[ref]
		for _, r := range rights {
			usedRight[r.ID] = true
		}
		result.AmbiguousGroups = append(result.AmbiguousGroups, AmbiguousGroupPair{
			Reference: ref,
			LeftRows:  rows,
			Rights:    rights,
		})
	}

	// Matching loop over non-ambiguous left rows.
	for i, ltx := range left {
		if consumedLeft[i] {
			continue
		}
		if keyOf(ltx) == "" {
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}
		candidates, ok := rightByRef[keyOf(ltx)]
		if !ok {
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}

		// Collect all unused rights for this reference.
		available := candidates[:0:0] // nil-like but avoids an alloc on the common path
		for _, r := range candidates {
			if !usedRight[r.ID] {
				available = append(available, r)
			}
		}
		if len(available) == 0 {
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}

		// Sum right amounts and compute max date distance.
		var sum int64
		maxDaysDiff := 0
		for _, r := range available {
			sum += r.Amount
			if d := daysBetween(ltx.Date, r.Date); d > maxDaysDiff {
				maxDaysDiff = d
			}
		}

		amtDiff := ltx.Amount - sum
		if amtDiff < 0 {
			amtDiff = -amtDiff
		}
		amtOk := amtDiff <= tolerance
		dateOk := windowDays == 0 || maxDaysDiff <= windowDays

		switch {
		case amtOk && dateOk:
			result.GroupedMatched = append(result.GroupedMatched, GroupedMatchedPair{
				Left:   ltx,
				Rights: available,
			})
			for _, r := range available {
				usedRight[r.ID] = true
			}
		case !amtOk && dateOk:
			signed := ltx.Amount - sum
			result.GroupedAmountDiff = append(result.GroupedAmountDiff, GroupedAmountDiffPair{
				Left:      ltx,
				Rights:    available,
				DiffMinor: signed,
			})
			for _, r := range available {
				usedRight[r.ID] = true
			}
		case amtOk && !dateOk:
			result.GroupedTimingDiff = append(result.GroupedTimingDiff, GroupedTimingDiffPair{
				Left:     ltx,
				Rights:   available,
				DaysDiff: maxDaysDiff,
			})
			for _, r := range available {
				usedRight[r.ID] = true
			}
		default:
			// Both amount and date fail — left is unmatched, rights NOT consumed
			// so a later pass can still match them.
			unmatchedLeft = append(unmatchedLeft, ltx)
		}
	}

	// Collect unused rights.
	for _, rtx := range right {
		if !usedRight[rtx.ID] {
			unmatchedRight = append(unmatchedRight, rtx)
		}
	}

	return unmatchedLeft, unmatchedRight
}

// matchByReferenceManyToMany handles grouped settlement reconciliation where M
// left rows reconcile against N right rows that share the same grouping key. It
// does not search arbitrary row combinations; the configured key defines the
// group boundary.
func matchByReferenceManyToMany(
	result *Result,
	left, right []Transaction,
	tolerance int64,
	windowDays int,
	groupBy string,
) (unmatchedLeft, unmatchedRight []Transaction) {
	keyOf := func(tx Transaction) string {
		switch groupBy {
		case config.GroupByName:
			return tx.Name
		case config.GroupByGroupKey:
			return tx.GroupKey
		default:
			return tx.Reference
		}
	}

	rightByKey := make(map[string][]Transaction, len(right))
	for _, tx := range right {
		if k := keyOf(tx); k != "" {
			rightByKey[k] = append(rightByKey[k], tx)
		}
	}

	leftByKey := make(map[string][]int, len(left))
	leftKeyOrder := make([]string, 0)
	for i, tx := range left {
		if k := keyOf(tx); k != "" {
			if _, ok := leftByKey[k]; !ok {
				leftKeyOrder = append(leftKeyOrder, k)
			}
			leftByKey[k] = append(leftByKey[k], i)
		}
	}

	consumedLeft := make(map[int]bool)
	usedRight := make(map[string]bool)

	for _, key := range leftKeyOrder {
		indices := leftByKey[key]
		rights := rightByKey[key]
		if len(rights) == 0 {
			continue
		}

		lefts := make([]Transaction, len(indices))
		for i, idx := range indices {
			lefts[i] = left[idx]
		}

		leftSum := sumTransactions(lefts)
		rightSum := sumTransactions(rights)
		signedDiff := leftSum - rightSum
		absDiff := signedDiff
		if absDiff < 0 {
			absDiff = -absDiff
		}
		amtOk := absDiff <= tolerance
		maxDaysDiff := maxCrossSideDaysDiff(lefts, rights)
		dateOk := windowDays == 0 || maxDaysDiff <= windowDays

		switch {
		case amtOk && dateOk:
			result.ManyToManyMatched = append(result.ManyToManyMatched, ManyToManyMatchedPair{
				Lefts:  lefts,
				Rights: rights,
			})
		case !amtOk && dateOk:
			result.ManyToManyAmountDiff = append(result.ManyToManyAmountDiff, ManyToManyAmountDiffPair{
				Lefts:     lefts,
				Rights:    rights,
				DiffMinor: signedDiff,
			})
		case amtOk && !dateOk:
			result.ManyToManyTimingDiff = append(result.ManyToManyTimingDiff, ManyToManyTimingDiffPair{
				Lefts:    lefts,
				Rights:   rights,
				DaysDiff: maxDaysDiff,
			})
		default:
			continue
		}

		for _, idx := range indices {
			consumedLeft[idx] = true
		}
		for _, r := range rights {
			usedRight[r.ID] = true
		}
	}

	for i, ltx := range left {
		if !consumedLeft[i] {
			unmatchedLeft = append(unmatchedLeft, ltx)
		}
	}
	for _, rtx := range right {
		if !usedRight[rtx.ID] {
			unmatchedRight = append(unmatchedRight, rtx)
		}
	}

	return unmatchedLeft, unmatchedRight
}

func sumTransactions(txns []Transaction) int64 {
	var sum int64
	for _, tx := range txns {
		sum += tx.Amount
	}
	return sum
}

func maxCrossSideDaysDiff(lefts, rights []Transaction) int {
	maxDaysDiff := 0
	for _, l := range lefts {
		for _, r := range rights {
			if d := daysBetween(l.Date, r.Date); d > maxDaysDiff {
				maxDaysDiff = d
			}
		}
	}
	return maxDaysDiff
}

// matchByNameTokens attempts secondary matching using word-token overlap on the Name field.
func matchByNameTokens(
	result *Result,
	left, right []Transaction,
	tolerance int64,
	windowDays int,
	threshold float64,
) (unmatchedLeft, unmatchedRight []Transaction) {
	usedRight := make(map[string]bool)

	for _, ltx := range left {
		if ltx.Name == "" {
			unmatchedLeft = append(unmatchedLeft, ltx)
			continue
		}
		ltokens := tokenize(ltx.Name)

		bestScore := 0.0
		bestIdx := -1

		for i, rtx := range right {
			if usedRight[rtx.ID] || rtx.Name == "" {
				continue
			}

			amtDiff := ltx.Amount - rtx.Amount
			if amtDiff < 0 {
				amtDiff = -amtDiff
			}
			if amtDiff > tolerance {
				continue
			}
			if windowDays > 0 && daysBetween(ltx.Date, rtx.Date) > windowDays {
				continue
			}

			rtokens := tokenize(rtx.Name)
			score := tokenOverlap(ltokens, rtokens)
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}

		if bestScore > threshold && bestIdx >= 0 {
			rtx := right[bestIdx]
			result.Matched = append(result.Matched, MatchedPair{Left: ltx, Right: rtx})
			usedRight[rtx.ID] = true
		} else {
			unmatchedLeft = append(unmatchedLeft, ltx)
		}
	}

	for _, rtx := range right {
		if !usedRight[rtx.ID] {
			unmatchedRight = append(unmatchedRight, rtx)
		}
	}

	return unmatchedLeft, unmatchedRight
}

// annotateDuplicates groups transactions by GroupKey and returns groups with more
// than one member as a duplicate report. This is read-only: it does not affect
