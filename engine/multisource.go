package engine

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

// CounterpartInput pairs a counterpart source name with its already-parsed
// transactions, for use with ReconcileMultiSource.
type CounterpartInput struct {
	SourceName   string
	Transactions []Transaction
	// ParserCfg carries the source's parser configuration, used to resolve
	// the duplicate_policy for this counterpart. Zero value defaults to "flag".
	ParserCfg config.ParserCfg
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
	opts ...ReconcileOptions,
) (*Result, error) {
	if len(counterparts) == 0 {
		return nil, fmt.Errorf("at least one counterpart source is required")
	}
	if err := validateCounterpartNames(counterpartInputNames(counterparts)); err != nil {
		return nil, err
	}

	var leftPolicy config.DuplicatePolicy
	if len(opts) > 0 {
		leftPolicy = opts[0].LeftPolicy
	}
	if leftPolicy == "" {
		leftPolicy = config.DuplicatePolicyFlag
	}

	result := &Result{
		PairName:   pairName,
		LeftSource: leftSource,
		BySource:   make(map[string]Summary, len(counterparts)),
	}
	// Annotate left-side duplicates once (before any pass) for "flag" only.
	if leftPolicy == config.DuplicatePolicyFlag {
		result.Duplicates = annotateDuplicates(left)
	}

	names := make([]string, 0, len(counterparts))
	totalRight := 0
	remainingLeft := left

	for _, cp := range counterparts {
		names = append(names, cp.SourceName)
		totalRight += len(cp.Transactions)

		passResult, err := Reconcile(pairName, leftSource, cp.SourceName, remainingLeft, cp.Transactions, pair,
			ReconcileOptions{LeftPolicy: leftPolicy, RightPolicy: cp.ParserCfg.ResolvedDuplicatePolicy()})
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
		result.ManyToManyMatched = append(result.ManyToManyMatched, passResult.ManyToManyMatched...)
		result.ManyToManyAmountDiff = append(result.ManyToManyAmountDiff, passResult.ManyToManyAmountDiff...)
		result.ManyToManyTimingDiff = append(result.ManyToManyTimingDiff, passResult.ManyToManyTimingDiff...)
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
	// Use the currency from the first pass summary; all passes share the same validated currency.
	var currency string
	for _, cp := range counterparts {
		if s, ok := result.BySource[cp.SourceName]; ok && s.Currency != "" {
			currency = s.Currency
			break
		}
	}
	summary, err := buildSummary(len(left), totalRight, result, currency)
	if err != nil {
		return nil, err
	}
	result.Summary = summary

	return result, nil
}

// ReconcileMultiSourceWithTelemetry emits lifecycle events for every counterpart
// while retaining ReconcileMultiSource's ordered carry-forward semantics.
func ReconcileMultiSourceWithTelemetry(
	pairName, leftSource string,
	left []Transaction,
	counterparts []CounterpartInput,
	pair config.Pair,
	telemetry TelemetryOptions,
	opts ...ReconcileOptions,
) (*Result, error) {
	if len(counterparts) == 0 {
		return nil, fmt.Errorf("at least one counterpart source is required")
	}
	if err := validateCounterpartNames(counterpartInputNames(counterparts)); err != nil {
		return nil, err
	}
	reporter := newTelemetryReporter(telemetry)
	if reporter != nil {
		defer reporter.close()
	}
	leftTotal := len(left)
	names := make([]string, 0, len(counterparts))
	for _, cp := range counterparts {
		names = append(names, cp.SourceName)
	}
	rightLabel := strings.Join(names, ",")
	stage := "left_match"
	if len(pair.Passes) > 0 {
		stage = "grouped_pass"
	}
	reporter.start(stage, leftSource, rightLabel, &leftTotal)
	result, err := ReconcileMultiSource(pairName, leftSource, left, counterparts, pair, opts...)
	if err != nil {
		reporter.fail(leftTotal)
		return nil, err
	}
	reporter.complete(leftTotal)
	reporter.start("finalization", leftSource, rightLabel, nil)
	reporter.complete(0)
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
	return reconcileStreamingMultiSource(ctx, pairName, leftSource, leftPath, leftCfg, counterparts, pair, w, maxLeftBuffer, nil)
}

// ReconcileStreamingMultiSourceWithTelemetry is the streaming multi-source
// entry point with typed, best-effort telemetry.
func ReconcileStreamingMultiSourceWithTelemetry(
	ctx context.Context,
	pairName string,
	leftSource string,
	leftPath string,
	leftCfg config.CSVParserCfg,
	counterparts []CounterpartStream,
	pair config.Pair,
	w ResultWriter,
	maxLeftBuffer int,
	telemetry TelemetryOptions,
) error {
	reporter := newTelemetryReporter(telemetry)
	if reporter != nil {
		defer reporter.close()
	}
	err := reconcileStreamingMultiSource(ctx, pairName, leftSource, leftPath, leftCfg, counterparts, pair, w, maxLeftBuffer, reporter)
	if err != nil {
		reporter.fail(0)
	}
	return err
}

func reconcileStreamingMultiSource(
	ctx context.Context,
	pairName string,
	leftSource string,
	leftPath string,
	leftCfg config.CSVParserCfg,
	counterparts []CounterpartStream,
	pair config.Pair,
	w ResultWriter,
	maxLeftBuffer int,
	reporter *telemetryReporter,
) error {
	if err := validateCollaborator("result writer", w); err != nil {
		return err
	}
	if len(counterparts) == 0 {
		return fmt.Errorf("at least one counterpart source is required")
	}
	if err := validateCounterpartStreams(counterparts); err != nil {
		return err
	}
	if pair.NameMode == "tokens" {
		return fmt.Errorf("name_mode=tokens is not yet supported for multi-source (rights) reconciliation")
	}
	if containsPass(pair.Passes, config.PassTypeOneToMany) || containsPass(pair.Passes, config.PassTypeManyToMany) {
		return fmt.Errorf("grouped passes are not supported in the streaming " +
			"multi-source path — the CLI routes to batch automatically when " +
			"one_to_many or many_to_many is configured")
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
			reporter,
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
			observeWarning(w, reporter, Warning{Code: WarningCarryBufferPressure, Message: fmt.Sprintf("multi-source carry-forward buffer is %d rows (limit %d) after counterpart %q; memory usage may be high", len(leftover), maxLeftBuffer, cp.SourceName)})
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
		observeWarning(w, reporter, Warning{Code: WarningEmptyCurrency, Message: warning})
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
		Currency:             cc.base,
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

	reporter.start("finalization", leftSource, strings.Join(names, ","), nil)
	reporter.progress(totalLeft + totalRight)
	if err := w.WriteSummary(aggregate); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	reporter.complete(totalLeft + totalRight)
	return nil
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
	reporter *telemetryReporter,
) (leftover []Transaction, summary Summary, leftDups []DuplicateGroup, err error) {
	dateWindowDays, err := parseDateWindow(pair.DateWindow)
	if err != nil {
		return nil, Summary{}, nil, fmt.Errorf("invalid date_window: %w", err)
	}
	tolerance := pair.AmountToleranceMinor

	rightCfgNoRaw := rightCfg
	rightCfgNoRaw.SkipRaw = true

	rightPolicy := rightCfg.ResolvedDuplicatePolicy()
	rightSeen := make(map[string]uint8)
	rightDupKeys := make(map[string]bool)
	rightMergeSeen := make(map[string]bool)
	var rightLatestBuf map[string]Transaction
	if rightPolicy == config.DuplicatePolicyLatest {
		rightLatestBuf = make(map[string]Transaction)
	}
	var totalRight int
	reporter.start("right_index", rightSource, rightSource, nil)

	if perr := ParseCSVEach(ctx, rightSource, rightPath, rightCfgNoRaw, func(tx Transaction, _ int) error {
		totalRight++
		reporter.progress(totalRight)
		if oerr := cc.Observe(rightSource, tx); oerr != nil {
			return oerr
		}
		switch rightPolicy {
		case config.DuplicatePolicyFlag:
			if tx.GroupKey != "" {
				if rightSeen[tx.GroupKey] < 2 {
					rightSeen[tx.GroupKey]++
				}
				if rightSeen[tx.GroupKey] == 2 {
					rightDupKeys[tx.GroupKey] = true
				}
			}
			return idx.Add(tx)
		case config.DuplicatePolicyKeep:
			return idx.Add(tx)
		case config.DuplicatePolicyMerge:
			if tx.GroupKey != "" && rightMergeSeen[tx.GroupKey] {
				return nil
			}
			if tx.GroupKey != "" {
				rightMergeSeen[tx.GroupKey] = true
			}
			return idx.Add(tx)
		case config.DuplicatePolicyLatest:
			if tx.GroupKey != "" {
				rightLatestBuf[tx.GroupKey] = tx
				return nil
			}
			return idx.Add(tx)
		}
		return idx.Add(tx)
	}); perr != nil {
		return nil, Summary{}, nil, fmt.Errorf("parse right source %q: %w", rightSource, perr)
	}
	if rightPolicy == config.DuplicatePolicyLatest {
		for _, tx := range rightLatestBuf {
			if aerr := idx.Add(tx); aerr != nil {
				return nil, Summary{}, nil, aerr
			}
		}
	}
	reporter.complete(totalRight)

	dupTxnCount := 0
	if rightPolicy == config.DuplicatePolicyFlag && len(rightDupKeys) > 0 {
		reporter.start("right_duplicate_scan", rightSource, rightSource, nil)
		groups, derr := collectDuplicates(ctx, rightSource, rightPath, rightCfg, rightDupKeys)
		if derr != nil {
			return nil, Summary{}, nil, fmt.Errorf("collect right duplicates: %w", derr)
		}
		for _, g := range groups {
			dupTxnCount += len(g.Transactions)
			reporter.progress(dupTxnCount)
			if werr := w.WriteDuplicate(g); werr != nil {
				return nil, Summary{}, nil, werr
			}
		}
		reporter.complete(dupTxnCount)
	}

	leftPolicy := leftCfg.ResolvedDuplicatePolicy()
	leftSeen := make(map[string]uint8)
	leftDupKeys := make(map[string]bool)
	leftMergeSeen := make(map[string]bool)
	var leftLatestBuf map[string]Transaction
	if fromFile && leftPolicy == config.DuplicatePolicyLatest {
		leftLatestBuf = make(map[string]Transaction)
	}

	var (
		matchedCount, amountDiffCount, timingDiffCount, unmatchedRightCount int
		matchedAmtLeft, matchedAmtRight                                     int64
		unmatchedAmtLeft, unmatchedAmtRight, amountDiffTotal                int64
		totalLeft                                                           int
	)

	// matchLeft contains only matching and outcome accounting. The first pass
	// calls it for ordinary rows, while latest representatives call it directly
	// after the scan so replay cannot re-enter the deduplication buffer.
	matchLeft := func(ltx Transaction) error {
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

	processLeft := func(ltx Transaction) error {
		totalLeft++
		reporter.progress(totalLeft)
		if fromFile {
			if oerr := cc.Observe(leftSource, ltx); oerr != nil {
				return oerr
			}
			if leftPolicy == config.DuplicatePolicyFlag && ltx.GroupKey != "" {
				if leftSeen[ltx.GroupKey] < 2 {
					leftSeen[ltx.GroupKey]++
				}
				if leftSeen[ltx.GroupKey] == 2 {
					leftDupKeys[ltx.GroupKey] = true
				}
			}
			// Dedup for merge: skip if GroupKey already seen (first-seen wins).
			if leftPolicy == config.DuplicatePolicyMerge && ltx.GroupKey != "" {
				if leftMergeSeen[ltx.GroupKey] {
					return nil
				}
				leftMergeSeen[ltx.GroupKey] = true
			}
			// Dedup for latest: buffer and defer processing until after scan.
			if leftPolicy == config.DuplicatePolicyLatest && ltx.GroupKey != "" {
				leftLatestBuf[ltx.GroupKey] = ltx
				return nil
			}
		}
		return matchLeft(ltx)
	}

	reporter.start("left_match", leftSource, rightSource, nil)
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
	// For "latest" (first pass only): process buffered left rows now that the scan
	// is complete and we know which row is last for each GroupKey.
	if fromFile && leftPolicy == config.DuplicatePolicyLatest {
		// These rows were already counted and currency-validated during the file
		// scan; replay only the final representative for each non-empty group key.
		for _, ltx := range leftLatestBuf {
			if ctx.Err() != nil {
				return nil, Summary{}, nil, ctx.Err()
			}
			if perr := matchLeft(ltx); perr != nil {
				return nil, Summary{}, nil, perr
			}
		}
	}
	reporter.complete(totalLeft)

	if fromFile && leftPolicy == config.DuplicatePolicyFlag && len(leftDupKeys) > 0 {
		reporter.start("left_duplicate_scan", leftSource, rightSource, nil)
		groups, derr := collectDuplicates(ctx, leftSource, leftPath, leftCfg, leftDupKeys)
		if derr != nil {
			return nil, Summary{}, nil, fmt.Errorf("collect left duplicates: %w", derr)
		}
		leftDups = groups
		reporter.complete(len(groups))
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

func counterpartInputNames(counterparts []CounterpartInput) []string {
	names := make([]string, 0, len(counterparts))
	for _, counterpart := range counterparts {
		names = append(names, counterpart.SourceName)
	}
	return names
}

// validateCounterpartStreams performs all streaming preflight checks before any
// right file is indexed, preventing partial output for invalid multi-source input.
func validateCounterpartStreams(counterparts []CounterpartStream) error {
	if err := validateCounterpartNames(counterpartStreamNames(counterparts)); err != nil {
		return err
	}
	for index, counterpart := range counterparts {
		if counterpart.Index == nil {
			return fmt.Errorf("counterpart[%d] %q: right index cannot be nil", index, counterpart.SourceName)
		}
	}
	return nil
}

func counterpartStreamNames(counterparts []CounterpartStream) []string {
	names := make([]string, 0, len(counterparts))
	for _, counterpart := range counterparts {
		names = append(names, counterpart.SourceName)
	}
	return names
}

// validateCounterpartNames preserves configured order while rejecting names that
// would make per-source summaries ambiguous or unsafe to populate.
func validateCounterpartNames(names []string) error {
	seen := make(map[string]int, len(names))
	for index, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("counterpart[%d]: source name cannot be empty", index)
		}
		if previousIndex, exists := seen[trimmed]; exists {
			return fmt.Errorf("counterpart[%d] %q: duplicate source name (already configured at index %d)", index, name, previousIndex)
		}
		seen[trimmed] = index
	}
	return nil
}
