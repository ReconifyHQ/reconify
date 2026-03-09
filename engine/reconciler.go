package engine

import (
	"context"
	"fmt"
	"math"
	"os"
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
	// Monetary totals are emitted in summary. To keep those totals meaningful,
	// reject runs that mix non-empty currencies.
	cc := currencyTracker{}
	for _, tx := range left {
		if err := cc.Observe(leftSource, tx); err != nil {
			return nil, err
		}
	}
	for _, tx := range right {
		if err := cc.Observe(rightSource, tx); err != nil {
			return nil, err
		}
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

	// Compute monetary totals from accumulated result slices.
	var matchedAmtLeft, matchedAmtRight int64
	for _, mp := range result.Matched {
		matchedAmtLeft += mp.Left.Amount
		matchedAmtRight += mp.Right.Amount
	}
	var unmatchedAmtLeft int64
	for _, tx := range result.UnmatchedLeft {
		unmatchedAmtLeft += tx.Amount
	}
	var unmatchedAmtRight int64
	for _, tx := range result.UnmatchedRight {
		unmatchedAmtRight += tx.Amount
	}
	var amountDiffTotal int64
	for _, ap := range result.AmountDiff {
		d := ap.DiffMinor
		if d < 0 {
			d = -d
		}
		amountDiffTotal += d
	}

	result.Summary = Summary{
		TotalLeft:            len(left),
		TotalRight:           len(right),
		MatchedCount:         len(result.Matched),
		UnmatchedLeft:        len(result.UnmatchedLeft),
		UnmatchedRight:       len(result.UnmatchedRight),
		AmountDiffCount:      len(result.AmountDiff),
		TimingDiffCount:      len(result.TimingDiff),
		DuplicateCount:       len(result.Duplicates),
		MatchRatePct:         matchRate,
		MatchedAmountLeft:    matchedAmtLeft,
		MatchedAmountRight:   matchedAmtRight,
		UnmatchedAmountLeft:  unmatchedAmtLeft,
		UnmatchedAmountRight: unmatchedAmtRight,
		AmountDiffTotal:      amountDiffTotal,
		TotalDiscrepancy:     unmatchedAmtLeft + unmatchedAmtRight + amountDiffTotal,
	}

	return result, nil
}

// ReconcileStreaming is the canonical streaming reconciliation function.
//
// It accepts a RightIndex (created by the caller) and a ResultWriter, and emits
// events incrementally as it processes both sides. ReconcileStreaming never
// assumes a specific RightIndex implementation — passing newMemoryIndex() is the
// default; a disk-backed index can be substituted without changing this function.
//
// Token mode ordering: reference matching always runs first. Token/name matching
// applies only to transactions that remain unmatched after the reference pass.
// Token matches cannot override reference matches.
//
// Memory complexity:
//   - Right-side index: O(n_right) via the provided RightIndex
//   - Right dup tracking: O(unique_refs_right) for counting + O(dup_right_txns) for groups
//   - Left ref tracking: O(unique_refs_left) for counting + O(dup_left_txns) for groups
//   - Token mode buffer: O(n_unmatched) worst case — guarded by maxTokenBuffer
//
// Empty references ("") are treated as unmatched and are never grouped as duplicates.
func ReconcileStreaming(
	ctx context.Context,
	pairName string,
	leftSource string,
	rightSource string,
	leftPath string,
	rightPath string,
	leftCfg config.CSVParserCfg,
	rightCfg config.CSVParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
) error {
	return reconcileStreaming(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, idx, w, maxTokenBuffer, nil, 0)
}

// ProgressEvent reports incremental progress for long-running reconciliations.
// Phase is one of: "right_index", "left_match".
type ProgressEvent struct {
	Phase   string
	Rows    int
	Elapsed time.Duration
	Done    bool
}

// ProgressFunc is invoked by ReconcileStreamingWithProgress at row intervals.
// It runs on the calling goroutine; avoid heavy work.
type ProgressFunc func(ProgressEvent)

// ReconcileStreamingWithProgress is identical to ReconcileStreaming, but emits progress
// updates every progressEvery rows when progress != nil. If progressEvery <= 0,
// it defaults to 1,000,000 rows.
func ReconcileStreamingWithProgress(
	ctx context.Context,
	pairName string,
	leftSource string,
	rightSource string,
	leftPath string,
	rightPath string,
	leftCfg config.CSVParserCfg,
	rightCfg config.CSVParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
	progress ProgressFunc,
	progressEvery int,
) error {
	return reconcileStreaming(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, idx, w, maxTokenBuffer, progress, progressEvery)
}

func reconcileStreaming(
	ctx context.Context,
	pairName string,
	leftSource string,
	rightSource string,
	leftPath string,
	rightPath string,
	leftCfg config.CSVParserCfg,
	rightCfg config.CSVParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
	progress ProgressFunc,
	progressEvery int,
) error {
	dateWindowDays, err := parseDateWindow(pair.DateWindow)
	if err != nil {
		return fmt.Errorf("invalid date_window: %w", err)
	}

	tolerance := pair.AmountToleranceMinor
	cc := currencyTracker{}
	if progressEvery <= 0 {
		progressEvery = 1_000_000
	}
	startRight := time.Now()
	startLeft := time.Now()
	nextRight := progressEvery
	nextLeft := progressEvery

	// -----------------------------------------------------------------------
	// Pass 1: stream right CSV into index, track right duplicates
	// -----------------------------------------------------------------------
	// Force SkipRaw for the right side — Raw data in the index wastes memory.
	rightCfgNoRaw := rightCfg
	rightCfgNoRaw.SkipRaw = true

	// Lightweight right-side duplicate detection.
	// rightSeen tracks occurrence count per reference, capped at 2 (saturating).
	// rightDupRefs collects references that appeared ≥ 2 times; the full
	// Transaction set is retrieved via a targeted re-scan after this pass.
	rightSeen := make(map[string]uint8)
	rightDupRefs := make(map[string]bool)
	var totalRight int

	if err := ParseCSVEach(ctx, rightSource, rightPath, rightCfgNoRaw, func(tx Transaction, _ int) error {
		totalRight++
		if err := cc.Observe(rightSource, tx); err != nil {
			return err
		}
		if progress != nil && totalRight >= nextRight {
			progress(ProgressEvent{Phase: "right_index", Rows: totalRight, Elapsed: time.Since(startRight)})
			nextRight += progressEvery
		}
		if tx.Reference == "" {
			return idx.Add(tx)
		}
		if rightSeen[tx.Reference] < 2 {
			rightSeen[tx.Reference]++
		}
		if rightSeen[tx.Reference] == 2 {
			rightDupRefs[tx.Reference] = true
		}
		return idx.Add(tx)
	}); err != nil {
		return fmt.Errorf("parse right source: %w", err)
	}
	if progress != nil {
		progress(ProgressEvent{
			Phase:   "right_index",
			Rows:    totalRight,
			Elapsed: time.Since(startRight),
			Done:    true,
		})
	}

	// Emit right duplicate groups via targeted re-scan of the right file.
	// collectDuplicates is only called when duplicates actually exist;
	// for datasets with no duplicates this is a no-op.
	if len(rightDupRefs) > 0 {
		groups, err := collectDuplicates(ctx, rightSource, rightPath, rightCfg, rightDupRefs)
		if err != nil {
			return fmt.Errorf("collect right duplicates: %w", err)
		}
		for _, g := range groups {
			if err := w.WriteDuplicate(g); err != nil {
				return err
			}
		}
	}

	// -----------------------------------------------------------------------
	// Pass 2: stream left CSV, match against index, emit events immediately
	// -----------------------------------------------------------------------
	startLeft = time.Now()
	// Lightweight left-side duplicate detection — same saturating-uint8 approach
	// as the right side. leftFirst/leftDups are replaced by a targeted re-scan
	// after this pass (collectDuplicates), called only when duplicates exist.
	leftSeen := make(map[string]uint8)
	leftDupRefs := make(map[string]bool)

	var (
		matchedCount       int
		amountDiffCount    int
		timingDiffCount    int
		unmatchedLeftCount int
		tokenUnmatchedLeft []Transaction

		// Monetary accumulators (minor units). Always populated; negligible cost.
		matchedAmtLeft    int64
		matchedAmtRight   int64
		unmatchedAmtLeft  int64
		unmatchedAmtRight int64
		amountDiffTotal   int64
	)
	var totalLeft int

	if err := ParseCSVEach(ctx, leftSource, leftPath, leftCfg, func(ltx Transaction, _ int) error {
		totalLeft++
		if err := cc.Observe(leftSource, ltx); err != nil {
			return err
		}
		if progress != nil && totalLeft >= nextLeft {
			progress(ProgressEvent{Phase: "left_match", Rows: totalLeft, Elapsed: time.Since(startLeft)})
			nextLeft += progressEvery
		}

		// Track left duplicates (count only; full transactions retrieved in third pass)
		if ltx.Reference != "" {
			if leftSeen[ltx.Reference] < 2 {
				leftSeen[ltx.Reference]++
			}
			if leftSeen[ltx.Reference] == 2 {
				leftDupRefs[ltx.Reference] = true
			}
		}

		// Empty reference: classify as unmatched immediately
		if ltx.Reference == "" {
			unmatchedLeftCount++
			unmatchedAmtLeft += ltx.Amount
			if pair.NameMode == "tokens" {
				tokenUnmatchedLeft = append(tokenUnmatchedLeft, ltx)
				return nil
			}
			return w.WriteUnmatched(ltx, "left")
		}

		// Attempt reference matching.
		// Cache left date as Unix nanos once per row — avoids recomputing
		// it for every right-side bucket candidate.
		buckets, err := idx.Get(ltx.Reference)
		if err != nil {
			return fmt.Errorf("index get reference %q: %w", ltx.Reference, err)
		}
		matched := false
		ltxDateNano := ltx.Date.UnixNano()
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
				if err := idx.MarkUsed(b); err != nil {
					return fmt.Errorf("mark used: %w", err)
				}
				matched = true
				matchedCount++
				matchedAmtLeft += ltx.Amount
				matchedAmtRight += b.amount
				return w.WriteMatch(MatchedPair{Left: ltx, Right: b.toTransaction(ltx.Reference)})
			}
			if !amtOk && dateOk {
				if err := idx.MarkUsed(b); err != nil {
					return fmt.Errorf("mark used: %w", err)
				}
				matched = true
				amountDiffCount++
				diff := ltx.Amount - b.amount
				if diff < 0 {
					amountDiffTotal += -diff
				} else {
					amountDiffTotal += diff
				}
				return w.WriteAmountDiff(AmountDiffPair{
					Left:      ltx,
					Right:     b.toTransaction(ltx.Reference),
					DiffMinor: ltx.Amount - b.amount,
				})
			}
			if amtOk && !dateOk {
				if err := idx.MarkUsed(b); err != nil {
					return fmt.Errorf("mark used: %w", err)
				}
				matched = true
				timingDiffCount++
				return w.WriteTimingDiff(TimingDiffPair{
					Left:     ltx,
					Right:    b.toTransaction(ltx.Reference),
					DaysDiff: daysDiff,
				})
			}
		}

		if !matched {
			unmatchedLeftCount++
			unmatchedAmtLeft += ltx.Amount
			if pair.NameMode == "tokens" {
				tokenUnmatchedLeft = append(tokenUnmatchedLeft, ltx)
				return nil
			}
			return w.WriteUnmatched(ltx, "left")
		}
		return nil
	}); err != nil {
		return fmt.Errorf("parse left source: %w", err)
	}
	if progress != nil {
		progress(ProgressEvent{
			Phase:   "left_match",
			Rows:    totalLeft,
			Elapsed: time.Since(startLeft),
			Done:    true,
		})
	}

	// Emit left duplicate groups via targeted re-scan of the left file.
	if len(leftDupRefs) > 0 {
		groups, err := collectDuplicates(ctx, leftSource, leftPath, leftCfg, leftDupRefs)
		if err != nil {
			return fmt.Errorf("collect left duplicates: %w", err)
		}
		for _, g := range groups {
			if err := w.WriteDuplicate(g); err != nil {
				return err
			}
		}
	}

	// -----------------------------------------------------------------------
	// After pass 2: collect unused right-side transactions
	// -----------------------------------------------------------------------
	var tokenUnmatchedRight []Transaction
	unmatchedRightCount := 0

	if err := idx.IterateUnused(func(tx Transaction) error {
		unmatchedRightCount++
		unmatchedAmtRight += tx.Amount
		if pair.NameMode == "tokens" {
			tokenUnmatchedRight = append(tokenUnmatchedRight, tx)
			return nil
		}
		return w.WriteUnmatched(tx, "right")
	}); err != nil {
		return err
	}

	// -----------------------------------------------------------------------
	// Optional token-mode second pass on unmatched transactions
	//
	// Token mode is an edge case / fallback. It buffers unmatched transactions
	// from both sides. Worst case (all unmatched): O(n_total) memory.
	// Guarded by maxTokenBuffer advisory limit.
	// -----------------------------------------------------------------------
	if pair.NameMode == "tokens" {
		bufTotal := len(tokenUnmatchedLeft) + len(tokenUnmatchedRight)
		if maxTokenBuffer > 0 && bufTotal > maxTokenBuffer {
			fmt.Fprintf(os.Stderr,
				"warning: token mode unmatched buffer is %d rows (limit %d); memory usage may be high\n",
				bufTotal, maxTokenBuffer)
		}

		// Run Jaccard secondary matching on the buffered unmatched transactions.
		// Returns matched pairs plus the remaining (still unmatched) left/right slices.
		tokenMatches, remainLeft, remainRight := matchByNameTokensStreaming(
			tokenUnmatchedLeft, tokenUnmatchedRight, tolerance, dateWindowDays,
		)
		for _, mp := range tokenMatches {
			matchedCount++
			unmatchedLeftCount--
			unmatchedRightCount--
			matchedAmtLeft += mp.Left.Amount
			matchedAmtRight += mp.Right.Amount
			unmatchedAmtLeft -= mp.Left.Amount
			unmatchedAmtRight -= mp.Right.Amount
			if err := w.WriteMatch(mp); err != nil {
				return err
			}
		}
		for _, tx := range remainLeft {
			if err := w.WriteUnmatched(tx, "left"); err != nil {
				return err
			}
		}
		for _, tx := range remainRight {
			if err := w.WriteUnmatched(tx, "right"); err != nil {
				return err
			}
		}
	}

	// -----------------------------------------------------------------------
	// Summary
	// -----------------------------------------------------------------------
	dupCount := len(leftDupRefs) + len(rightDupRefs)
	total := totalLeft
	if totalRight > total {
		total = totalRight
	}
	matchRate := 0.0
	if total > 0 {
		matchRate = math.Round(float64(matchedCount)/float64(total)*10000) / 100
	}

	if err := w.WriteSummary(Summary{
		TotalLeft:            totalLeft,
		TotalRight:           totalRight,
		MatchedCount:         matchedCount,
		UnmatchedLeft:        unmatchedLeftCount,
		UnmatchedRight:       unmatchedRightCount,
		AmountDiffCount:      amountDiffCount,
		TimingDiffCount:      timingDiffCount,
		DuplicateCount:       dupCount,
		MatchRatePct:         matchRate,
		MatchedAmountLeft:    matchedAmtLeft,
		MatchedAmountRight:   matchedAmtRight,
		UnmatchedAmountLeft:  unmatchedAmtLeft,
		UnmatchedAmountRight: unmatchedAmtRight,
		AmountDiffTotal:      amountDiffTotal,
		TotalDiscrepancy:     unmatchedAmtLeft + unmatchedAmtRight + amountDiffTotal,
	}); err != nil {
		return err
	}

	return w.Flush()
}

// matchByNameTokensStreaming runs the Jaccard secondary pass on pre-buffered
// unmatched slices and returns a slice of MatchedPairs plus the remaining
// (still unmatched) slices in-place.
func matchByNameTokensStreaming(
	left, right []Transaction,
	tolerance int64,
	windowDays int,
) (matches []MatchedPair, remainLeft, remainRight []Transaction) {
	usedRight := make(map[string]bool)

	for _, ltx := range left {
		if ltx.Name == "" {
			remainLeft = append(remainLeft, ltx)
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
			score := tokenOverlap(ltokens, tokenize(rtx.Name))
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		if bestScore > 0.5 && bestIdx >= 0 {
			matches = append(matches, MatchedPair{Left: ltx, Right: right[bestIdx]})
			usedRight[right[bestIdx].ID] = true
		} else {
			remainLeft = append(remainLeft, ltx)
		}
	}
	for _, rtx := range right {
		if !usedRight[rtx.ID] {
			remainRight = append(remainRight, rtx)
		}
	}
	return matches, remainLeft, remainRight
}

// collectDuplicates re-scans a CSV file and returns a DuplicateGroup for every
// reference in dupRefs. It is invoked only when duplicates were detected during
// the primary pass; for datasets with no duplicates this function is never called.
//
// Using the original cfg (not rightCfgNoRaw) preserves the Raw field if the
// caller has configured SkipRaw = false.
//
// Memory: O(n_dup_rows) — only rows whose reference is in dupRefs are retained.
func collectDuplicates(
	ctx context.Context,
	sourceName string,
	path string,
	cfg config.CSVParserCfg,
	dupRefs map[string]bool,
) ([]DuplicateGroup, error) {
	byRef := make(map[string][]Transaction, len(dupRefs))
	if err := ParseCSVEach(ctx, sourceName, path, cfg, func(tx Transaction, _ int) error {
		if dupRefs[tx.Reference] {
			byRef[tx.Reference] = append(byRef[tx.Reference], tx)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	groups := make([]DuplicateGroup, 0, len(byRef))
	for ref, txns := range byRef {
		groups = append(groups, DuplicateGroup{
			Source:       sourceName,
			Reference:    ref,
			Transactions: txns,
		})
	}
	return groups, nil
}

// currencyTracker validates that all non-empty currency values in a run are the same.
// This protects monetary summary totals from accidental cross-currency aggregation.
type currencyTracker struct {
	base string
}

func (c *currencyTracker) Observe(source string, tx Transaction) error {
	cur := strings.TrimSpace(tx.Currency)
	if cur == "" {
		return nil
	}
	if c.base == "" {
		c.base = cur
		return nil
	}
	if cur != c.base {
		return fmt.Errorf(
			"mixed currencies are not supported for monetary totals: saw %q and %q (source=%s, id=%s, reference=%s); reconcile one currency per run",
			c.base, cur, source, tx.ID, tx.Reference,
		)
	}
	return nil
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

// daysBetweenNano returns the absolute number of days between two Unix nanosecond
// timestamps. Used in ReconcileStreaming where the right-side date is stored as
// int64 in the bucket, avoiding time.Time reconstruction for comparison.
func daysBetweenNano(aNano, bNano int64) int {
	const nsPerDay = 24 * 60 * 60 * int64(1e9)
	diff := aNano - bNano
	if diff < 0 {
		diff = -diff
	}
	return int(diff / nsPerDay)
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
