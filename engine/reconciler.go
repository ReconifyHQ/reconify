package engine

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/reconify/reconify/config"
)

// Reconcile runs the reconciliation algorithm between two slices of transactions.
func Reconcile(pairName, leftSource, rightSource string, left, right []Transaction, pair config.Pair) (*Result, error) {
	dateWindowDays, err := parseDateWindow(pair.DateWindow)
	if err != nil {
		return nil, fmt.Errorf("invalid date_window: %w", err)
	}

	result := &Result{
		PairName:    pairName,
		LeftSource:  leftSource,
		RightSource: rightSource,
	}

	// 1. Detect duplicates within each source
	leftDups, leftDeduped := detectDuplicates(left)
	rightDups, rightDeduped := detectDuplicates(right)
	result.Duplicates = append(leftDups, rightDups...)

	// 2. Match by reference
	unmatchedLeft, unmatchedRight := matchByReference(
		result,
		leftDeduped,
		rightDeduped,
		pair.AmountToleranceMinor,
		dateWindowDays,
	)

	// 3. Optional name-token matching for remaining unmatched
	if pair.NameMode == "tokens" {
		unmatchedLeft, unmatchedRight = matchByNameTokens(result, unmatchedLeft, unmatchedRight, pair.AmountToleranceMinor, dateWindowDays)
	}

	result.UnmatchedLeft = unmatchedLeft
	result.UnmatchedRight = unmatchedRight

	// 4. Populate summary
	total := len(left)
	if len(right) > total {
		total = len(right)
	}
	matchRate := 0.0
	if total > 0 {
		matchRate = math.Round(float64(len(result.Matched))/float64(total)*10000) / 100
	}

	result.Summary = Summary{
		TotalLeft:       len(left),
		TotalRight:      len(right),
		MatchedCount:    len(result.Matched),
		UnmatchedLeft:   len(result.UnmatchedLeft),
		UnmatchedRight:  len(result.UnmatchedRight),
		AmountDiffCount: len(result.AmountDiff),
		TimingDiffCount: len(result.TimingDiff),
		DuplicateCount:  len(result.Duplicates),
		MatchRatePct:    matchRate,
	}

	return result, nil
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

		matched := false
		for _, rtx := range candidates {
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
				result.Matched = append(result.Matched, MatchedPair{Left: ltx, Right: rtx})
				usedRight[rtx.ID] = true
				matched = true
				break
			}

			if !amtOk && dateOk {
				// Amount diff — still classify as a partial match
				signed := ltx.Amount - rtx.Amount
				result.AmountDiff = append(result.AmountDiff, AmountDiffPair{Left: ltx, Right: rtx, DiffMinor: signed})
				usedRight[rtx.ID] = true
				matched = true
				break
			}

			if amtOk && !dateOk {
				// Timing diff
				result.TimingDiff = append(result.TimingDiff, TimingDiffPair{Left: ltx, Right: rtx, DaysDiff: daysDiff})
				usedRight[rtx.ID] = true
				matched = true
				break
			}
		}

		if !matched {
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

// matchByNameTokens attempts secondary matching using word-token overlap on the Name field.
func matchByNameTokens(
	result *Result,
	left, right []Transaction,
	tolerance int64,
	windowDays int,
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

		if bestScore > 0.5 && bestIdx >= 0 {
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

// detectDuplicates groups transactions by reference and identifies duplicates (ref appears > 1 time).
// Returns duplicate groups and a deduplicated slice (first occurrence kept).
func detectDuplicates(txns []Transaction) ([]DuplicateGroup, []Transaction) {
	seen := make(map[string][]Transaction)
	order := make([]string, 0)

	for _, tx := range txns {
		if tx.Reference == "" {
			continue
		}
		if _, exists := seen[tx.Reference]; !exists {
			order = append(order, tx.Reference)
		}
		seen[tx.Reference] = append(seen[tx.Reference], tx)
	}

	var dups []DuplicateGroup
	usedRefs := make(map[string]bool)

	for _, ref := range order {
		group := seen[ref]
		if len(group) > 1 {
			dups = append(dups, DuplicateGroup{
				Source:       group[0].Source,
				Reference:    ref,
				Transactions: group,
			})
			usedRefs[ref] = true
		}
	}

	// Deduped list: keep first occurrence, skip subsequent duplicates
	seenID := make(map[string]bool)
	var deduped []Transaction
	for _, tx := range txns {
		if usedRefs[tx.Reference] && seenID[tx.Reference] {
			continue
		}
		if tx.Reference != "" {
			seenID[tx.Reference] = true
		}
		deduped = append(deduped, tx)
	}

	return dups, deduped
}

// parseDateWindow parses a date window string like "1d", "3d" to a number of days.
// Returns 0 if empty (meaning no window limit).
func parseDateWindow(window string) (int, error) {
	if window == "" {
		return 0, nil
	}
	var days int
	var unit string
	if _, err := fmt.Sscanf(window, "%d%s", &days, &unit); err != nil {
		return 0, fmt.Errorf("expected format like '1d', got %q", window)
	}
	if unit != "d" && unit != "D" {
		return 0, fmt.Errorf("unit must be 'd', got %q", unit)
	}
	return days, nil
}

// daysBetween returns the absolute number of days between two times.
func daysBetween(a, b time.Time) int {
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return int(diff.Hours() / 24)
}

// tokenize splits a string into lower-case word tokens.
func tokenize(s string) map[string]bool {
	tokens := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(s)) {
		if len(word) > 1 {
			tokens[word] = true
		}
	}
	return tokens
}

// tokenOverlap returns the Jaccard similarity between two token sets.
func tokenOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for tok := range a {
		if b[tok] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
