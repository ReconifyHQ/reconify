package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

// CounterpartInput pairs a counterpart source name with its already-parsed
// transactions, for use with ReconcileMultiSource.
type CounterpartInput struct {
	SourceName   string
	Transactions []Transaction
}

// ReconcileMultiSource reconciles left against each counterpart in order: the
// transactions left unmatched after pass i become the left input to pass i+1.
// This is the 1-N source orchestration entry point — each individual pass reuses
// the exact same Reconcile() matching primitives (best-candidate selection,
// non-gating duplicate annotation, configurable name-match threshold) as a 1-1
// pair, so behavior at each step is identical to today's single-pair semantics.
//
// The returned Result aggregates all passes: Matched/AmountDiff/TimingDiff/
// UnmatchedRight are the concatenation of every pass's output (each Transaction's
// Source field already identifies which counterpart it came from). UnmatchedLeft
// is whatever remains after the final pass. Result.Summary is the aggregate across
// all passes; Result.BySource gives the per-counterpart breakdown.
//
// Duplicate annotation: left-side duplicates are computed once against the
// original (pre-pass) left slice, not per pass — otherwise a left row that
// remains unmatched across multiple passes would be reported as a duplicate once
// per pass it survives into. Right-side duplicates are naturally one-per-pass
// since each counterpart's transactions are disjoint across passes.
func ReconcileMultiSource(
	pairName, leftSource string,
	left []Transaction,
	counterparts []CounterpartInput,
	pair config.Pair,
) (*Result, error) {
	if len(counterparts) == 0 {
		return nil, fmt.Errorf("at least one counterpart source is required")
	}

	result := &Result{
		PairName:   pairName,
		LeftSource: leftSource,
		BySource:   make(map[string]Summary, len(counterparts)),
		Duplicates: annotateDuplicates(left),
	}

	names := make([]string, 0, len(counterparts))
	totalRight := 0
	remainingLeft := left

	for _, cp := range counterparts {
		names = append(names, cp.SourceName)
		totalRight += len(cp.Transactions)

		passResult, err := Reconcile(pairName, leftSource, cp.SourceName, remainingLeft, cp.Transactions, pair)
		if err != nil {
			return nil, fmt.Errorf("counterpart %q: %w", cp.SourceName, err)
		}

		result.Matched = append(result.Matched, passResult.Matched...)
		result.AmountDiff = append(result.AmountDiff, passResult.AmountDiff...)
		result.TimingDiff = append(result.TimingDiff, passResult.TimingDiff...)
		result.UnmatchedRight = append(result.UnmatchedRight, passResult.UnmatchedRight...)
		result.GroupedMatched = append(result.GroupedMatched, passResult.GroupedMatched...)
		result.GroupedAmountDiff = append(result.GroupedAmountDiff, passResult.GroupedAmountDiff...)
		result.GroupedTimingDiff = append(result.GroupedTimingDiff, passResult.GroupedTimingDiff...)
		result.AmbiguousGroups = append(result.AmbiguousGroups, passResult.AmbiguousGroups...)
		result.Warnings = append(result.Warnings, passResult.Warnings...)
		result.BySource[cp.SourceName] = passResult.Summary

		// Only keep this pass's right-side duplicate groups — its left-side
		// groups are a re-annotation of the carried-forward left set, already
		// covered by the single annotateDuplicates(left) call above.
		for _, g := range passResult.Duplicates {
			if g.Source == cp.SourceName {
				result.Duplicates = append(result.Duplicates, g)
			}
		}

		remainingLeft = passResult.UnmatchedLeft
	}

	result.RightSource = strings.Join(names, ",")
	result.UnmatchedLeft = remainingLeft
	result.Summary = buildSummary(len(left), totalRight, result)

	return result, nil
}

// CounterpartStream describes one streaming counterpart pass: its source name,
// right-side CSV file path + parser config, and a pre-built (empty) RightIndex
// that this pass populates. The caller owns the Index (e.g. for Close()), the
// same way callers of ReconcileStreaming own the single RightIndex they pass in.
type CounterpartStream struct {
	SourceName string
	RightPath  string
	RightCfg   config.CSVParserCfg
	Index      RightIndex
}

// ReconcileStreamingMultiSource is the streaming equivalent of ReconcileMultiSource.
// The left file is streamed from disk exactly once, during the first counterpart's
// pass — matched/amount-diff/timing-diff/right-unmatched/duplicate events are
// written to w immediately, the same as ReconcileStreaming. Left transactions that
// remain unmatched after that pass are buffered in memory (bounded by
// maxLeftBuffer, advisory like ReconcileStreaming's token-mode buffer) and
// replayed against each subsequent counterpart's index in turn, with no second
// read of the left file.
//
// name_mode=tokens is not supported here yet — token-mode buffering across
// multiple counterpart passes is unimplemented; pass NameMode="none" or "".
//
// Single-counterpart callers should use ReconcileStreaming directly — it is
// unchanged and remains the documented large-file path. This function is for
// len(counterparts) > 1 only.
func ReconcileStreamingMultiSource(
	ctx context.Context,
	pairName string,
	leftSource string,
	leftPath string,
	leftCfg config.CSVParserCfg,
	counterparts []CounterpartStream,
	pair config.Pair,
	w ResultWriter,
	maxLeftBuffer int,
) error {
	if len(counterparts) == 0 {
		return fmt.Errorf("at least one counterpart source is required")
	}
	if pair.NameMode == "tokens" {
		return fmt.Errorf("name_mode=tokens is not yet supported for multi-source (rights) reconciliation")
	}
	if containsPass(pair.Passes, config.PassTypeOneToMany) {
		return fmt.Errorf("one_to_many pass is not supported in the streaming " +
			"multi-source path — the CLI routes to batch automatically when " +
			"one_to_many is configured")
	}

	cc := &currencyTracker{}
	bySource := make(map[string]Summary, len(counterparts))
	names := make([]string, 0, len(counterparts))

	var (
		totalLeft, totalRight                                               int
		matchedCount, amountDiffCount, timingDiffCount, unmatchedRightCount int
		matchedAmtLeft, matchedAmtRight, unmatchedAmtRight, amountDiffTotal int64
		dupTxnCount                                                         int
		leftover                                                            []Transaction
	)

	for i, cp := range counterparts {
		fromFile := i == 0
		var rows []Transaction
		if !fromFile {
			rows = leftover
		}

		passLeftover, passSummary, leftDups, err := runStreamingPass(
			ctx, leftSource, cp.SourceName, fromFile, leftPath, leftCfg, rows,
			cp.RightPath, cp.RightCfg, cp.Index, pair, cc, w,
		)
		if err != nil {
			return fmt.Errorf("counterpart %q: %w", cp.SourceName, err)
		}

		names = append(names, cp.SourceName)
		bySource[cp.SourceName] = passSummary

		if fromFile {
			totalLeft = passSummary.TotalLeft
		}
		totalRight += passSummary.TotalRight
		matchedCount += passSummary.MatchedCount
		amountDiffCount += passSummary.AmountDiffCount
		timingDiffCount += passSummary.TimingDiffCount
		unmatchedRightCount += passSummary.UnmatchedRight
		matchedAmtLeft += passSummary.MatchedAmountLeft
		matchedAmtRight += passSummary.MatchedAmountRight
		unmatchedAmtRight += passSummary.UnmatchedAmountRight
		amountDiffTotal += passSummary.AmountDiffTotal
		dupTxnCount += passSummary.DuplicateCount // right-side dups for this counterpart

		for _, g := range leftDups {
			dupTxnCount += len(g.Transactions)
			if err := w.WriteDuplicate(g); err != nil {
				return err
			}
		}

		leftover = passLeftover
		if maxLeftBuffer > 0 && len(leftover) > maxLeftBuffer {
			fmt.Fprintf(os.Stderr,
				"warning: multi-source carry-forward buffer is %d rows (limit %d) after counterpart %q; memory usage may be high\n",
				len(leftover), maxLeftBuffer, cp.SourceName)
		}
	}

	// Whatever remains after the final pass is the true final unmatched_left.
	var unmatchedAmtLeft int64
	for _, tx := range leftover {
		unmatchedAmtLeft += tx.Amount
		if err := w.WriteUnmatched(tx, "left"); err != nil {
			return err
		}
	}

	for _, warning := range cc.Warnings() {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}

	total := totalLeft
	if totalRight > total {
		total = totalRight
	}
	matchRate := 0.0
	if total > 0 {
		matchRate = math.Round(float64(matchedCount)/float64(total)*10000) / 100
	}
	reconciledRate := 0.0
	if total > 0 {
		reconciledRate = math.Round(float64(matchedCount+amountDiffCount+timingDiffCount)/float64(total)*10000) / 100
	}

	aggregate := Summary{
		TotalLeft:            totalLeft,
		TotalRight:           totalRight,
		MatchedCount:         matchedCount,
		UnmatchedLeft:        len(leftover),
		UnmatchedRight:       unmatchedRightCount,
		AmountDiffCount:      amountDiffCount,
		TimingDiffCount:      timingDiffCount,
		DuplicateCount:       dupTxnCount,
		MatchRatePct:         matchRate,
		ReconciledRatePct:    reconciledRate,
		MatchedAmountLeft:    matchedAmtLeft,
		MatchedAmountRight:   matchedAmtRight,
		UnmatchedAmountLeft:  unmatchedAmtLeft,
		UnmatchedAmountRight: unmatchedAmtRight,
		AmountDiffTotal:      amountDiffTotal,
		TotalDiscrepancy:     unmatchedAmtLeft + unmatchedAmtRight + amountDiffTotal,
	}

	if sbw, ok := w.(SourceBreakdownWriter); ok {
		for _, name := range names {
			if err := sbw.WriteSourceSummary(name, bySource[name]); err != nil {
				return err
			}
		}
	}

	if err := w.WriteSummary(aggregate); err != nil {
		return err
	}
	return w.Flush()
}

// runStreamingPass executes one counterpart pass: indexes the counterpart's right
// file, tracks right-side duplicates, then matches left rows — either streamed
// from leftPath (fromFile=true, used only for the first pass) or replayed from
// leftRows (every subsequent pass) — against that index using the same
// decideMatch best-candidate selection as ReconcileStreaming. Matched/amount-diff/
// timing-diff/right-duplicate/right-unmatched events are written to w immediately,
// since they are final for this counterpart. Left-unmatched rows are returned
// instead of written, so the driver can decide whether to carry them into the
// next pass or write them out as the final unmatched_left.
//
// Left-side duplicate detection only runs when fromFile=true: that is the one
// pass that sees every original left row exactly once, so it's the only place
// duplicate annotation can run without re-reporting the same group once per pass
// it survives into.
func runStreamingPass(
	ctx context.Context,
	leftSource, rightSource string,
	fromFile bool,
	leftPath string,
	leftCfg config.CSVParserCfg,
	leftRows []Transaction,
	rightPath string,
	rightCfg config.CSVParserCfg,
	idx RightIndex,
	pair config.Pair,
	cc *currencyTracker,
	w ResultWriter,
) (leftover []Transaction, summary Summary, leftDups []DuplicateGroup, err error) {
	dateWindowDays, err := parseDateWindow(pair.DateWindow)
	if err != nil {
		return nil, Summary{}, nil, fmt.Errorf("invalid date_window: %w", err)
	}
	tolerance := pair.AmountToleranceMinor

	rightCfgNoRaw := rightCfg
	rightCfgNoRaw.SkipRaw = true

	rightSeen := make(map[string]uint8)
	rightDupKeys := make(map[string]bool)
	var totalRight int

	if perr := ParseCSVEach(ctx, rightSource, rightPath, rightCfgNoRaw, func(tx Transaction, _ int) error {
		totalRight++
		if oerr := cc.Observe(rightSource, tx); oerr != nil {
			return oerr
		}
		if tx.GroupKey != "" {
			if rightSeen[tx.GroupKey] < 2 {
				rightSeen[tx.GroupKey]++
			}
			if rightSeen[tx.GroupKey] == 2 {
				rightDupKeys[tx.GroupKey] = true
			}
		}
		return idx.Add(tx)
	}); perr != nil {
		return nil, Summary{}, nil, fmt.Errorf("parse right source %q: %w", rightSource, perr)
	}

	dupTxnCount := 0
	if len(rightDupKeys) > 0 {
		groups, derr := collectDuplicates(ctx, rightSource, rightPath, rightCfg, rightDupKeys)
		if derr != nil {
			return nil, Summary{}, nil, fmt.Errorf("collect right duplicates: %w", derr)
		}
		for _, g := range groups {
			dupTxnCount += len(g.Transactions)
			if werr := w.WriteDuplicate(g); werr != nil {
				return nil, Summary{}, nil, werr
			}
		}
	}

	leftSeen := make(map[string]uint8)
	leftDupKeys := make(map[string]bool)

	var (
		matchedCount, amountDiffCount, timingDiffCount, unmatchedRightCount int
		matchedAmtLeft, matchedAmtRight                                     int64
		unmatchedAmtLeft, unmatchedAmtRight, amountDiffTotal                int64
		totalLeft                                                           int
	)

	processLeft := func(ltx Transaction) error {
		totalLeft++
		if fromFile {
			if oerr := cc.Observe(leftSource, ltx); oerr != nil {
				return oerr
			}
			if ltx.GroupKey != "" {
				if leftSeen[ltx.GroupKey] < 2 {
					leftSeen[ltx.GroupKey]++
				}
				if leftSeen[ltx.GroupKey] == 2 {
					leftDupKeys[ltx.GroupKey] = true
				}
			}
		}

		decision, derr := decideMatch(ltx, idx, tolerance, dateWindowDays)
		if derr != nil {
			return derr
		}

		switch decision.outcome {
		case outcomeExact:
			if merr := idx.MarkUsed(decision.right); merr != nil {
				return fmt.Errorf("mark used: %w", merr)
			}
			matchedCount++
			matchedAmtLeft += ltx.Amount
			matchedAmtRight += decision.right.amount
			return w.WriteMatch(MatchedPair{Left: ltx, Right: decision.right.toTransaction(ltx.Reference)})
		case outcomeAmountDiff:
			if merr := idx.MarkUsed(decision.right); merr != nil {
				return fmt.Errorf("mark used: %w", merr)
			}
			amountDiffCount++
			if decision.amountDiffMinor < 0 {
				amountDiffTotal += -decision.amountDiffMinor
			} else {
				amountDiffTotal += decision.amountDiffMinor
			}
			return w.WriteAmountDiff(AmountDiffPair{
				Left:      ltx,
				Right:     decision.right.toTransaction(ltx.Reference),
				DiffMinor: decision.amountDiffMinor,
			})
		case outcomeTimingDiff:
			if merr := idx.MarkUsed(decision.right); merr != nil {
				return fmt.Errorf("mark used: %w", merr)
			}
			timingDiffCount++
			return w.WriteTimingDiff(TimingDiffPair{
				Left:     ltx,
				Right:    decision.right.toTransaction(ltx.Reference),
				DaysDiff: decision.daysDiff,
			})
		default:
			unmatchedAmtLeft += ltx.Amount
			leftover = append(leftover, ltx)
			return nil
		}
	}

	if fromFile {
		if perr := ParseCSVEach(ctx, leftSource, leftPath, leftCfg, func(tx Transaction, _ int) error {
			return processLeft(tx)
		}); perr != nil {
			return nil, Summary{}, nil, fmt.Errorf("parse left source: %w", perr)
		}
	} else {
		for _, tx := range leftRows {
			if ctx.Err() != nil {
				return nil, Summary{}, nil, ctx.Err()
			}
			if perr := processLeft(tx); perr != nil {
				return nil, Summary{}, nil, perr
			}
		}
	}

	if fromFile && len(leftDupKeys) > 0 {
		groups, derr := collectDuplicates(ctx, leftSource, leftPath, leftCfg, leftDupKeys)
		if derr != nil {
			return nil, Summary{}, nil, fmt.Errorf("collect left duplicates: %w", derr)
		}
		leftDups = groups
	}

	if ierr := idx.IterateUnused(func(tx Transaction) error {
		unmatchedRightCount++
		unmatchedAmtRight += tx.Amount
		return w.WriteUnmatched(tx, "right")
	}); ierr != nil {
		return nil, Summary{}, nil, ierr
	}

	total := totalLeft
	if totalRight > total {
		total = totalRight
	}
	matchRate := 0.0
	if total > 0 {
		matchRate = math.Round(float64(matchedCount)/float64(total)*10000) / 100
	}
	reconciledRate := 0.0
	if total > 0 {
		reconciledRate = math.Round(float64(matchedCount+amountDiffCount+timingDiffCount)/float64(total)*10000) / 100
	}

	summary = Summary{
		TotalLeft:            totalLeft,
		TotalRight:           totalRight,
		MatchedCount:         matchedCount,
		UnmatchedLeft:        len(leftover),
		UnmatchedRight:       unmatchedRightCount,
		AmountDiffCount:      amountDiffCount,
		TimingDiffCount:      timingDiffCount,
		DuplicateCount:       dupTxnCount,
		MatchRatePct:         matchRate,
		ReconciledRatePct:    reconciledRate,
		MatchedAmountLeft:    matchedAmtLeft,
		MatchedAmountRight:   matchedAmtRight,
		UnmatchedAmountLeft:  unmatchedAmtLeft,
		UnmatchedAmountRight: unmatchedAmtRight,
		AmountDiffTotal:      amountDiffTotal,
		TotalDiscrepancy:     unmatchedAmtLeft + unmatchedAmtRight + amountDiffTotal,
	}

	return leftover, summary, leftDups, nil
}
