package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/config"
)

// Reconcile runs the reconciliation algorithm between two slices of transactions.
// Pass a ReconcileOptions as the last argument to configure duplicate handling policy;
// omit it to preserve the existing default behavior (DuplicatePolicyFlag).
func Reconcile(pairName, leftSource, rightSource string, left, right []Transaction, pair config.Pair, opts ...ReconcileOptions) (*Result, error) {
	var leftPolicy, rightPolicy config.DuplicatePolicy
	if len(opts) > 0 {
		leftPolicy = opts[0].LeftPolicy
		rightPolicy = opts[0].RightPolicy
	}
	if leftPolicy == "" {
		leftPolicy = config.DuplicatePolicyFlag
	}
	if rightPolicy == "" {
		rightPolicy = config.DuplicatePolicyFlag
	}

	// Apply dedup policy before matching; no-op for flag/keep.
	left = applyDuplicatePolicy(left, leftPolicy)
	right = applyDuplicatePolicy(right, rightPolicy)

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

	threshold := resolveNameMatchThreshold(pair.NameMatchThreshold)

	// Dispatches explicit passes when configured; falls back to the legacy
	// reference-first + optional NameMode path when Passes is empty.
	if len(pair.Passes) > 0 {
		unmatchedLeft, unmatchedRight := left, right
		for _, pass := range pair.Passes {
			switch pass.Type {
			case config.PassTypeReferenceOneToOne:
				unmatchedLeft, unmatchedRight = matchByReference(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays)
			case config.PassTypeNameTokensOneToOne:
				unmatchedLeft, unmatchedRight = matchByNameTokens(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays, threshold)
			case config.PassTypeOneToMany:
				unmatchedLeft, unmatchedRight = matchByReferenceOneToMany(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays, pass.ResolvedGroupBy())
			case config.PassTypeManyToMany:
				unmatchedLeft, unmatchedRight = matchByReferenceManyToMany(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays, pass.ResolvedGroupBy())
			default:
				return nil, fmt.Errorf("unsupported pass type %q", pass.Type)
			}
		}
		result.UnmatchedLeft = unmatchedLeft
		result.UnmatchedRight = unmatchedRight
	} else {
		// Legacy path: reference always runs; name-token pass enabled by NameMode.
		unmatchedLeft, unmatchedRight := matchByReference(
			result, left, right,
			pair.AmountToleranceMinor, dateWindowDays)
		if pair.NameMode == "tokens" {
			unmatchedLeft, unmatchedRight = matchByNameTokens(
				result, unmatchedLeft, unmatchedRight,
				pair.AmountToleranceMinor, dateWindowDays, threshold)
		}
		result.UnmatchedLeft = unmatchedLeft
		result.UnmatchedRight = unmatchedRight
	}
	result.Warnings = cc.Warnings()

	// 3. Duplicate annotation — only for "flag" policy; other policies suppress reporting.
	if leftPolicy == config.DuplicatePolicyFlag {
		result.Duplicates = append(result.Duplicates, annotateDuplicates(left)...)
	}
	if rightPolicy == config.DuplicatePolicyFlag {
		result.Duplicates = append(result.Duplicates, annotateDuplicates(right)...)
	}

	// 4. Populate summary
	summary, err := buildSummary(len(left), len(right), result, cc.base)
	if err != nil {
		return nil, err
	}
	result.Summary = summary
	if len(opts) > 0 {
		if opts[0].ResultMode != "" {
			result.Summary.ResultMode = string(opts[0].ResultMode)
		}
		if opts[0].RunID != "" {
			result.Summary.RunID = opts[0].RunID
		}
	}

	return result, nil
}

// ReconcileWithTelemetry is the batch equivalent of Reconcile. It adds only
// observational lifecycle records; matching and returned results are identical.
func ReconcileWithTelemetry(pairName, leftSource, rightSource string, left, right []Transaction, pair config.Pair, telemetry TelemetryOptions, opts ...ReconcileOptions) (*Result, error) {
	reporter := newTelemetryReporter(telemetry)
	if reporter != nil {
		defer reporter.close()
	}
	leftTotal := len(left)
	stage := "left_match"
	if len(pair.Passes) > 0 {
		stage = "grouped_pass"
	}
	reporter.start(stage, leftSource, rightSource, &leftTotal)
	result, err := Reconcile(pairName, leftSource, rightSource, left, right, pair, opts...)
	if err != nil {
		reporter.fail(leftTotal)
		return nil, err
	}
	reporter.complete(leftTotal)
	reporter.start("finalization", leftSource, rightSource, nil)
	reporter.complete(0)
	return result, nil
}

// ParseWithTelemetry parses a complete source while emitting a live parsing
// stage. It is intended for batch callers that must read both inputs before
// reconciliation can begin.
func ParseWithTelemetry(ctx context.Context, sourceName, filePath string, cfg config.ParserCfg, counterpart string, telemetry TelemetryOptions) ([]Transaction, error) {
	reporter := newTelemetryReporter(telemetry)
	if reporter != nil {
		defer reporter.close()
	}
	var transactions []Transaction
	rows := 0
	reporter.start("parse", sourceName, counterpart, nil)
	err := ParseEach(ctx, sourceName, filePath, cfg, func(tx Transaction, _ int) error {
		rows++
		reporter.progress(rows)
		transactions = append(transactions, tx)
		return nil
	})
	if err != nil {
		reporter.fail(rows)
		return nil, err
	}
	reporter.complete(rows)
	return transactions, nil
}

// buildSummary computes aggregate counts, rates, and monetary totals from a fully
// populated Result (Matched/UnmatchedLeft/UnmatchedRight/AmountDiff/TimingDiff/
// Duplicates already set). totalLeft/totalRight are the original input row counts —
// callers that aggregate multiple passes (ReconcileMultiSource) pass the true
// original totals here, not a sum of intermediate per-pass unmatched counts.
// currency is the base currency for monetary totals, forwarded from currencyTracker.base.
func buildSummary(totalLeft, totalRight int, result *Result, currency string) (Summary, error) {
	total := totalLeft
	if totalRight > total {
		total = totalRight
	}
	matchRate := 0.0
	if total > 0 {
		matchRate = math.Round(float64(len(result.Matched))/float64(total)*10000) / 100
	}
	reconciledCount, err := checkedAddInt("reconciled count", len(result.Matched), len(result.AmountDiff), len(result.TimingDiff), len(result.GroupedMatched), len(result.GroupedAmountDiff), len(result.GroupedTimingDiff), len(result.ManyToManyMatched), len(result.ManyToManyAmountDiff), len(result.ManyToManyTimingDiff))
	if err != nil {
		return Summary{}, err
	}
	reconciledRate := 0.0
	if total > 0 {
		reconciledRate = math.Round(float64(reconciledCount)/float64(total)*10000) / 100
	}
	dupTxnCount := 0
	for _, g := range result.Duplicates {
		var err error
		dupTxnCount, err = checkedAddInt("duplicate count", dupTxnCount, len(g.Transactions))
		if err != nil {
			return Summary{}, err
		}
	}

	// Compute monetary totals from accumulated result slices.
	var matchedAmtLeft, matchedAmtRight int64
	for _, mp := range result.Matched {
		matchedAmtLeft, err = checkedAddInt64("matched left amount", matchedAmtLeft, mp.Left.Amount)
		if err != nil {
			return Summary{}, err
		}
		matchedAmtRight, err = checkedAddInt64("matched right amount", matchedAmtRight, mp.Right.Amount)
		if err != nil {
			return Summary{}, err
		}
	}
	// Grouped matches contribute to the same matched totals.
	for _, gm := range result.GroupedMatched {
		matchedAmtLeft, err = checkedAddInt64("matched left amount", matchedAmtLeft, gm.Left.Amount)
		if err != nil {
			return Summary{}, err
		}
		for _, r := range gm.Rights {
			matchedAmtRight, err = checkedAddInt64("matched right amount", matchedAmtRight, r.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
	}
	for _, mm := range result.ManyToManyMatched {
		for _, l := range mm.Lefts {
			matchedAmtLeft, err = checkedAddInt64("matched left amount", matchedAmtLeft, l.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
		for _, r := range mm.Rights {
			matchedAmtRight, err = checkedAddInt64("matched right amount", matchedAmtRight, r.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
	}
	var unmatchedAmtLeft int64
	for _, tx := range result.UnmatchedLeft {
		unmatchedAmtLeft, err = checkedAddInt64("unmatched left amount", unmatchedAmtLeft, tx.Amount)
		if err != nil {
			return Summary{}, err
		}
	}
	var unmatchedAmtRight int64
	for _, tx := range result.UnmatchedRight {
		unmatchedAmtRight, err = checkedAddInt64("unmatched right amount", unmatchedAmtRight, tx.Amount)
		if err != nil {
			return Summary{}, err
		}
	}
	var amountDiffTotal int64
	for _, ap := range result.AmountDiff {
		d, err := checkedAbsInt64("amount difference", ap.DiffMinor)
		if err != nil {
			return Summary{}, err
		}
		amountDiffTotal, err = checkedAddInt64("amount difference total", amountDiffTotal, d)
		if err != nil {
			return Summary{}, err
		}
	}
	for _, gd := range result.GroupedAmountDiff {
		d, err := checkedAbsInt64("amount difference", gd.DiffMinor)
		if err != nil {
			return Summary{}, err
		}
		amountDiffTotal, err = checkedAddInt64("amount difference total", amountDiffTotal, d)
		if err != nil {
			return Summary{}, err
		}
	}
	for _, md := range result.ManyToManyAmountDiff {
		d, err := checkedAbsInt64("amount difference", md.DiffMinor)
		if err != nil {
			return Summary{}, err
		}
		amountDiffTotal, err = checkedAddInt64("amount difference total", amountDiffTotal, d)
		if err != nil {
			return Summary{}, err
		}
	}
	// Ambiguous groups require manual reconciliation — their amounts count toward
	// TotalDiscrepancy separately from unmatched rows.
	var ambiguousAmtLeft, ambiguousAmtRight int64
	for _, ag := range result.AmbiguousGroups {
		for _, tx := range ag.LeftRows {
			ambiguousAmtLeft, err = checkedAddInt64("ambiguous left amount", ambiguousAmtLeft, tx.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
		for _, tx := range ag.Rights {
			ambiguousAmtRight, err = checkedAddInt64("ambiguous right amount", ambiguousAmtRight, tx.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
	}

	totalDiscrepancy, err := checkedAddInt64("total discrepancy", unmatchedAmtLeft, unmatchedAmtRight, amountDiffTotal, ambiguousAmtLeft, ambiguousAmtRight)
	if err != nil {
		return Summary{}, err
	}
	return Summary{
		Currency:                  currency,
		TotalLeft:                 totalLeft,
		TotalRight:                totalRight,
		MatchedCount:              len(result.Matched),
		UnmatchedLeft:             len(result.UnmatchedLeft),
		UnmatchedRight:            len(result.UnmatchedRight),
		AmountDiffCount:           len(result.AmountDiff),
		TimingDiffCount:           len(result.TimingDiff),
		DuplicateCount:            dupTxnCount,
		MatchRatePct:              matchRate,
		ReconciledRatePct:         reconciledRate,
		GroupedMatchedCount:       len(result.GroupedMatched),
		GroupedAmountDiffCount:    len(result.GroupedAmountDiff),
		GroupedTimingDiffCount:    len(result.GroupedTimingDiff),
		ManyToManyMatchedCount:    len(result.ManyToManyMatched),
		ManyToManyAmountDiffCount: len(result.ManyToManyAmountDiff),
		ManyToManyTimingDiffCount: len(result.ManyToManyTimingDiff),
		AmbiguousGroupCount:       len(result.AmbiguousGroups),
		MatchedAmountLeft:         matchedAmtLeft,
		MatchedAmountRight:        matchedAmtRight,
		UnmatchedAmountLeft:       unmatchedAmtLeft,
		UnmatchedAmountRight:      unmatchedAmtRight,
		AmountDiffTotal:           amountDiffTotal,
		AmbiguousAmountLeft:       ambiguousAmtLeft,
		AmbiguousAmountRight:      ambiguousAmtRight,
		TotalDiscrepancy:          totalDiscrepancy,
	}, nil
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
	leftCfg config.ParserCfg,
	rightCfg config.ParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
) error {
	return reconcileStreaming(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, idx, w, maxTokenBuffer, nil)
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
	leftCfg config.ParserCfg,
	rightCfg config.ParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
	progress ProgressFunc,
	progressEvery int,
) error {
	reporter := newTelemetryReporter(TelemetryOptions{
		ProgressEvery: progressEvery,
		Sink:          legacyProgressSink(progress),
	})
	defer reporter.close()
	return reconcileStreaming(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, idx, w, maxTokenBuffer, reporter)
}

// ReconcileStreamingWithTelemetry emits typed lifecycle telemetry while preserving
// the streaming reconciliation and ResultWriter behavior of ReconcileStreaming.
func ReconcileStreamingWithTelemetry(
	ctx context.Context,
	pairName string,
	leftSource string,
	rightSource string,
	leftPath string,
	rightPath string,
	leftCfg config.ParserCfg,
	rightCfg config.ParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
	telemetry TelemetryOptions,
) error {
	reporter := newTelemetryReporter(telemetry)
	if reporter != nil {
		defer reporter.close()
	}
	err := reconcileStreaming(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, idx, w, maxTokenBuffer, reporter)
	if err != nil {
		reporter.fail(0)
	}
	return err
}

func legacyProgressSink(progress ProgressFunc) TelemetrySink {
	if progress == nil {
		return nil
	}
	return func(event TelemetryEvent) error {
		if event.Type != "progress" || (event.Status != "running" && event.Status != "completed") {
			return nil
		}
		if event.Stage != "right_index" && event.Stage != "left_match" {
			return nil
		}
		progress(ProgressEvent{
			Phase:   event.Stage,
			Rows:    event.Rows,
			Elapsed: time.Duration(event.Elapsed * float64(time.Second)),
			Done:    event.Status == "completed",
		})
		return nil
	}
}

type streamingDuplicateOptions struct {
	globalRightDuplicateKeys      map[string]bool
	globalLeftDuplicateKeys       map[string]bool
	globalRightRepresentativeRows map[string]int
	globalLeftRepresentativeRows  map[string]int
	rightPartitionOriginalRows    []int
	leftPartitionOriginalRows     []int
}

func partitionOriginalRow(originalRows []int, rowNum int) int {
	if rowNum > 0 && rowNum <= len(originalRows) {
		return originalRows[rowNum-1]
	}
	return rowNum
}

func reconcileStreaming(
	ctx context.Context,
	pairName string,
	leftSource string,
	rightSource string,
	leftPath string,
	rightPath string,
	leftCfg config.ParserCfg,
	rightCfg config.ParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
	reporter *telemetryReporter,
) error {
	return reconcileStreamingWithOptions(ctx, pairName, leftSource, rightSource, leftPath, rightPath, leftCfg, rightCfg, pair, idx, w, maxTokenBuffer, reporter, streamingDuplicateOptions{})
}

func reconcileStreamingWithOptions(
	ctx context.Context,
	pairName string,
	leftSource string,
	rightSource string,
	leftPath string,
	rightPath string,
	leftCfg config.ParserCfg,
	rightCfg config.ParserCfg,
	pair config.Pair,
	idx RightIndex,
	w ResultWriter,
	maxTokenBuffer int,
	reporter *telemetryReporter,
	duplicateOptions streamingDuplicateOptions,
) error {
	if err := validateStreamingCollaborators(idx, w); err != nil {
		return err
	}
	dateWindowDays, err := parseDateWindow(pair.DateWindow)
	if err != nil {
		return fmt.Errorf("invalid date_window: %w", err)
	}

	tolerance := pair.AmountToleranceMinor
	cc := currencyTracker{}

	// Derive whether the name-token pass should run. Passes takes precedence;
	// fall back to the legacy NameMode field when no explicit passes are set.
	effectiveTokenMode := pair.NameMode == "tokens"
	if len(pair.Passes) > 0 {
		effectiveTokenMode = containsPass(pair.Passes, config.PassTypeNameTokensOneToOne)
		if err := validateStreamingPassOrder(pair.Passes); err != nil {
			return err
		}
	}
	// -----------------------------------------------------------------------
	// Pass 1: stream right CSV into index, track right duplicates
	// -----------------------------------------------------------------------
	// Force SkipRaw for the right side — Raw data in the index wastes memory.
	rightCfgNoRaw := rightCfg
	rightCfgNoRaw.SkipRaw = true

	rightPolicy := rightCfg.ResolvedDuplicatePolicy()

	// rightSeen/rightDupKeys are only used for the "flag" policy.
	rightSeen := make(map[string]uint8)
	rightDupKeys := make(map[string]bool)
	globalRightDuplicates := duplicateOptions.globalRightDuplicateKeys != nil
	// rightMergeSeen deduplicates for "merge" (first-seen wins).
	rightMergeSeen := make(map[string]bool)
	// rightLatestBuf accumulates last-seen rows per GroupKey for "latest".
	var rightLatestBuf map[string]Transaction
	if rightPolicy == config.DuplicatePolicyLatest {
		rightLatestBuf = make(map[string]Transaction)
	}
	var totalRight int
	rightDuplicateRows := 0
	reporter.start("right_index", rightSource, rightSource, nil)

	if err := ParseEach(ctx, rightSource, rightPath, rightCfgNoRaw, func(tx Transaction, rowNum int) error {
		totalRight++
		restorePartitionTransactionID(&tx, rightSource, duplicateOptions.rightPartitionOriginalRows, rowNum)
		if err := cc.Observe(rightSource, tx); err != nil {
			return err
		}
		reporter.progress(totalRight)
		if (rightPolicy == config.DuplicatePolicyMerge || rightPolicy == config.DuplicatePolicyLatest) && tx.GroupKey != "" && duplicateOptions.globalRightRepresentativeRows != nil {
			if representative, ok := duplicateOptions.globalRightRepresentativeRows[tx.GroupKey]; ok && representative != partitionOriginalRow(duplicateOptions.rightPartitionOriginalRows, rowNum) {
				return nil
			}
		}
		switch rightPolicy {
		case config.DuplicatePolicyFlag:
			if tx.GroupKey != "" {
				if globalRightDuplicates {
					if duplicateOptions.globalRightDuplicateKeys[tx.GroupKey] {
						rightDuplicateRows++
					}
				} else {
					if rightSeen[tx.GroupKey] < 2 {
						rightSeen[tx.GroupKey]++
					}
					if rightSeen[tx.GroupKey] == 2 {
						rightDupKeys[tx.GroupKey] = true
					}
				}
			}
			return idx.Add(tx)
		case config.DuplicatePolicyKeep:
			return idx.Add(tx)
		case config.DuplicatePolicyMerge:
			if tx.GroupKey != "" && rightMergeSeen[tx.GroupKey] {
				return nil // skip duplicate; first-seen already in index
			}
			if tx.GroupKey != "" {
				rightMergeSeen[tx.GroupKey] = true
			}
			return idx.Add(tx)
		case config.DuplicatePolicyLatest:
			if tx.GroupKey != "" {
				rightLatestBuf[tx.GroupKey] = tx // overwrite with latest row
				return nil
			}
			return idx.Add(tx)
		}
		return idx.Add(tx)
	}); err != nil {
		return fmt.Errorf("parse right source: %w", err)
	}
	// For "latest": bulk-add the last-seen row per GroupKey after the full scan.
	if rightPolicy == config.DuplicatePolicyLatest {
		for _, tx := range rightLatestBuf {
			if err := idx.Add(tx); err != nil {
				return err
			}
		}
	}
	reporter.complete(totalRight)

	// Emit right duplicate groups via targeted re-scan (flag policy only).
	// collectDuplicates is only called when duplicates actually exist.
	dupTxnCount := 0
	if rightPolicy == config.DuplicatePolicyFlag && globalRightDuplicates {
		dupTxnCount += rightDuplicateRows
	} else if rightPolicy == config.DuplicatePolicyFlag && len(rightDupKeys) > 0 {
		reporter.start("right_duplicate_scan", rightSource, rightSource, nil)
		groups, err := collectDuplicates(ctx, rightSource, rightPath, rightCfg, rightDupKeys)
		if err != nil {
			return fmt.Errorf("collect right duplicates: %w", err)
		}
		for _, g := range groups {
			dupTxnCount += len(g.Transactions)
			reporter.progress(dupTxnCount)
			if err := w.WriteDuplicate(g); err != nil {
				return err
			}
		}
		reporter.complete(dupTxnCount)
	}

	// -----------------------------------------------------------------------
	// Pass 2: stream left CSV, match against index, emit events immediately
	// -----------------------------------------------------------------------
	reporter.start("left_match", leftSource, rightSource, nil)
	leftPolicy := leftCfg.ResolvedDuplicatePolicy()
	// leftSeen/leftDupKeys are only used for the "flag" policy.
	leftSeen := make(map[string]uint8)
	leftDupKeys := make(map[string]bool)
	globalLeftDuplicates := duplicateOptions.globalLeftDuplicateKeys != nil
	// leftMergeSeen deduplicates for "merge" (first-seen wins).
	leftMergeSeen := make(map[string]bool)
	// leftLatestBuf accumulates last-seen rows per GroupKey for "latest".
	var leftLatestBuf map[string]Transaction
	if leftPolicy == config.DuplicatePolicyLatest {
		leftLatestBuf = make(map[string]Transaction)
	}
	threshold := resolveNameMatchThreshold(pair.NameMatchThreshold)

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
	leftDuplicateRows := 0

	// doMatchLeft executes the core match logic for one left transaction.
	// Extracted so it can be called both from the ParseEach callback and from
	// the post-scan loop used by the "latest" policy.
	doMatchLeft := func(ltx Transaction) error {
		if ltx.Reference == "" {
			unmatchedLeftCount++
			unmatchedAmtLeft += ltx.Amount
			if effectiveTokenMode {
				tokenUnmatchedLeft = append(tokenUnmatchedLeft, ltx)
				return nil
			}
			return w.WriteUnmatched(ltx, "left")
		}
		decision, err := decideMatch(ltx, idx, tolerance, dateWindowDays)
		if err != nil {
			return err
		}
		switch decision.outcome {
		case outcomeExact:
			if err := idx.MarkUsed(decision.right); err != nil {
				return fmt.Errorf("mark used: %w", err)
			}
			matchedCount++
			matchedAmtLeft += ltx.Amount
			matchedAmtRight += decision.right.amount
			return w.WriteMatch(MatchedPair{Left: ltx, Right: decision.right.toTransaction(ltx.Reference)})
		case outcomeAmountDiff:
			if err := idx.MarkUsed(decision.right); err != nil {
				return fmt.Errorf("mark used: %w", err)
			}
			amountDiffCount++
			diff := decision.amountDiffMinor
			if diff < 0 {
				amountDiffTotal += -diff
			} else {
				amountDiffTotal += diff
			}
			return w.WriteAmountDiff(AmountDiffPair{
				Left:      ltx,
				Right:     decision.right.toTransaction(ltx.Reference),
				DiffMinor: decision.amountDiffMinor,
			})
		case outcomeTimingDiff:
			if err := idx.MarkUsed(decision.right); err != nil {
				return fmt.Errorf("mark used: %w", err)
			}
			timingDiffCount++
			return w.WriteTimingDiff(TimingDiffPair{
				Left:     ltx,
				Right:    decision.right.toTransaction(ltx.Reference),
				DaysDiff: decision.daysDiff,
			})
		}
		unmatchedLeftCount++
		unmatchedAmtLeft += ltx.Amount
		if effectiveTokenMode {
			tokenUnmatchedLeft = append(tokenUnmatchedLeft, ltx)
			return nil
		}
		return w.WriteUnmatched(ltx, "left")
	}

	if err := ParseEach(ctx, leftSource, leftPath, leftCfg, func(ltx Transaction, rowNum int) error {
		totalLeft++
		restorePartitionTransactionID(&ltx, leftSource, duplicateOptions.leftPartitionOriginalRows, rowNum)
		if err := cc.Observe(leftSource, ltx); err != nil {
			return err
		}
		reporter.progress(totalLeft)
		if (leftPolicy == config.DuplicatePolicyMerge || leftPolicy == config.DuplicatePolicyLatest) && ltx.GroupKey != "" && duplicateOptions.globalLeftRepresentativeRows != nil {
			if representative, ok := duplicateOptions.globalLeftRepresentativeRows[ltx.GroupKey]; ok && representative != partitionOriginalRow(duplicateOptions.leftPartitionOriginalRows, rowNum) {
				return nil
			}
		}
		switch leftPolicy {
		case config.DuplicatePolicyFlag:
			if ltx.GroupKey != "" {
				if globalLeftDuplicates {
					if duplicateOptions.globalLeftDuplicateKeys[ltx.GroupKey] {
						leftDuplicateRows++
					}
				} else {
					if leftSeen[ltx.GroupKey] < 2 {
						leftSeen[ltx.GroupKey]++
					}
					if leftSeen[ltx.GroupKey] == 2 {
						leftDupKeys[ltx.GroupKey] = true
					}
				}
			}
			return doMatchLeft(ltx)
		case config.DuplicatePolicyKeep:
			return doMatchLeft(ltx)
		case config.DuplicatePolicyMerge:
			if ltx.GroupKey != "" && leftMergeSeen[ltx.GroupKey] {
				return nil // skip duplicate; already counted in totalLeft
			}
			if ltx.GroupKey != "" {
				leftMergeSeen[ltx.GroupKey] = true
			}
			return doMatchLeft(ltx)
		case config.DuplicatePolicyLatest:
			if ltx.GroupKey != "" {
				leftLatestBuf[ltx.GroupKey] = ltx // overwrite with latest row
				return nil
			}
			return doMatchLeft(ltx)
		}
		return doMatchLeft(ltx)
	}); err != nil {
		return fmt.Errorf("parse left source: %w", err)
	}
	// For "latest": process buffered left rows after the full scan completes.
	if leftPolicy == config.DuplicatePolicyLatest {
		for _, ltx := range leftLatestBuf {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := doMatchLeft(ltx); err != nil {
				return err
			}
		}
	}
	reporter.complete(totalLeft)

	// Emit left duplicate groups via targeted re-scan (flag policy only).
	if leftPolicy == config.DuplicatePolicyFlag && globalLeftDuplicates {
		dupTxnCount += leftDuplicateRows
	} else if leftPolicy == config.DuplicatePolicyFlag && len(leftDupKeys) > 0 {
		reporter.start("left_duplicate_scan", leftSource, rightSource, nil)
		groups, err := collectDuplicates(ctx, leftSource, leftPath, leftCfg, leftDupKeys)
		if err != nil {
			return fmt.Errorf("collect left duplicates: %w", err)
		}
		for _, g := range groups {
			dupTxnCount += len(g.Transactions)
			reporter.progress(dupTxnCount)
			if err := w.WriteDuplicate(g); err != nil {
				return err
			}
		}
		reporter.complete(dupTxnCount)
	}

	// -----------------------------------------------------------------------
	// After pass 2: collect unused right-side transactions
	// -----------------------------------------------------------------------
	var tokenUnmatchedRight []Transaction
	unmatchedRightCount := 0

	if err := idx.IterateUnused(func(tx Transaction) error {
		unmatchedRightCount++
		unmatchedAmtRight += tx.Amount
		if effectiveTokenMode {
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
	if effectiveTokenMode {
		reporter.start("token_match", leftSource, rightSource, nil)
		bufTotal := len(tokenUnmatchedLeft) + len(tokenUnmatchedRight)
		reporter.progress(bufTotal)
		if maxTokenBuffer > 0 && bufTotal > maxTokenBuffer {
			observeWarning(w, reporter, Warning{Code: WarningTokenBufferPressure, Message: fmt.Sprintf("token mode unmatched buffer is %d rows (limit %d); memory usage may be high", bufTotal, maxTokenBuffer)})
		}

		// Run Jaccard secondary matching on the buffered unmatched transactions.
		// Returns matched pairs plus the remaining (still unmatched) left/right slices.
		tokenMatches, remainLeft, remainRight := matchByNameTokensStreaming(
			tokenUnmatchedLeft, tokenUnmatchedRight, tolerance, dateWindowDays, threshold,
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
		reporter.complete(bufTotal)
	}

	// -----------------------------------------------------------------------
	// Summary
	// -----------------------------------------------------------------------
	total := totalLeft
	if totalRight > total {
		total = totalRight
	}
	matchRate := 0.0
	if total > 0 {
		matchRate = math.Round(float64(matchedCount)/float64(total)*10000) / 100
	}
	reconciledCount := matchedCount + amountDiffCount + timingDiffCount
	reconciledRate := 0.0
	if total > 0 {
		reconciledRate = math.Round(float64(reconciledCount)/float64(total)*10000) / 100
	}

	for _, warning := range cc.Warnings() {
		observeWarning(w, reporter, Warning{Code: WarningEmptyCurrency, Message: warning})
	}

	reporter.start("finalization", leftSource, rightSource, nil)
	reporter.progress(totalLeft + totalRight)
	if err := w.WriteSummary(Summary{
		Currency:             cc.base,
		TotalLeft:            totalLeft,
		TotalRight:           totalRight,
		MatchedCount:         matchedCount,
		UnmatchedLeft:        unmatchedLeftCount,
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
	}); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	reporter.complete(totalLeft + totalRight)
	return nil
}

func restorePartitionTransactionID(tx *Transaction, source string, originalRows []int, rowNum int) {
	if len(originalRows) == 0 {
		return
	}
	tx.ID = fmt.Sprintf("%s-%d", source, partitionOriginalRow(originalRows, rowNum))
}

// matchByNameTokensStreaming runs the Jaccard secondary pass on pre-buffered
// unmatched slices and returns a slice of MatchedPairs plus the remaining
// (still unmatched) slices in-place.
func matchByNameTokensStreaming(
	left, right []Transaction,
	tolerance int64,
	windowDays int,
	threshold float64,
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
		if bestScore > threshold && bestIdx >= 0 {
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

// containsPass returns true if passes contains a pass of the given type.
func containsPass(passes []config.PassConfig, passType string) bool {
	for _, p := range passes {
		if p.Type == passType {
			return true
		}
	}
	return false
}

// validateStreamingPassOrder returns an error when the pass list uses an ordering
// that the streaming path cannot support. Specifically: the streaming path runs
// reference matching inline during the left-file scan; name-token matching runs
// afterward on the unmatched buffer. A name_tokens_one_to_one pass that appears
// before reference_one_to_one would require buffering all left rows upfront, which
// violates the streaming memory contract.
func validateStreamingPassOrder(passes []config.PassConfig) error {
	sawRef := false
	for i, p := range passes {
		switch p.Type {
		case config.PassTypeReferenceOneToOne:
			sawRef = true
		case config.PassTypeNameTokensOneToOne:
			if !sawRef {
				return fmt.Errorf("streaming: %s at passes[%d] must be preceded by %s — streaming always indexes reference first",
					config.PassTypeNameTokensOneToOne, i, config.PassTypeReferenceOneToOne)
			}
		default:
			return fmt.Errorf("streaming: unsupported pass type %q at passes[%d]", p.Type, i)
		}
	}
	return nil
}

// matchOutcome classifies the result of decideMatch.
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

// collectDuplicates re-scans an input file and returns a DuplicateGroup for every
// group key in dupKeys. It is invoked only when duplicates were detected during
// the primary pass; for datasets with no duplicates this function is never called.
//
// Using the original cfg (not rightCfgNoRaw) preserves the Raw field if the
// caller has configured SkipRaw = false.
//
// Memory: O(n_dup_rows) — only rows whose group key is in dupKeys are retained.
func collectDuplicates(
	ctx context.Context,
	sourceName string,
	path string,
	cfg config.ParserCfg,
	dupKeys map[string]bool,
) ([]DuplicateGroup, error) {
	byKey := make(map[string][]Transaction, len(dupKeys))
	if err := ParseEach(ctx, sourceName, path, cfg, func(tx Transaction, _ int) error {
		if dupKeys[tx.GroupKey] {
			byKey[tx.GroupKey] = append(byKey[tx.GroupKey], tx)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	groups := make([]DuplicateGroup, 0, len(byKey))
	for key, txns := range byKey {
		groups = append(groups, DuplicateGroup{
			Source:       sourceName,
			Reference:    key,
			Transactions: txns,
		})
	}
	return groups, nil
}

// currencyTracker validates that all non-empty currency values in a run are the same.
// This protects monetary summary totals from accidental cross-currency aggregation.
// Rows with an empty currency are still included in monetary totals (no currency to
// validate against), but are counted per-source so callers can warn about the mix.
type currencyTracker struct {
	base       string
	emptyCount map[string]int
}

func (c *currencyTracker) Observe(source string, tx Transaction) error {
	cur := strings.TrimSpace(tx.Currency)
	if cur == "" {
		if c.emptyCount == nil {
			c.emptyCount = make(map[string]int)
		}
		c.emptyCount[source]++
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

// Warnings returns human-readable warnings for sources that mixed empty-currency
// rows with a non-empty base currency. Empty-currency rows still count toward
// monetary totals; this only adds visibility, it never changes validation behavior.
func (c *currencyTracker) Warnings() []string {
	if c.base == "" || len(c.emptyCount) == 0 {
		return nil
	}
	sources := make([]string, 0, len(c.emptyCount))
	for source := range c.emptyCount {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	warnings := make([]string, 0, len(sources))
	for _, source := range sources {
		warnings = append(warnings, fmt.Sprintf(
			"source %q: %d row(s) had an empty currency and were included in monetary totals alongside currency %q",
			source, c.emptyCount[source], c.base,
		))
	}
	return warnings
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
// which transactions participate in matching.
func annotateDuplicates(txns []Transaction) []DuplicateGroup {
	seen := make(map[string][]Transaction)
	order := make([]string, 0)

	for _, tx := range txns {
		if tx.GroupKey == "" {
			continue
		}
		if _, exists := seen[tx.GroupKey]; !exists {
			order = append(order, tx.GroupKey)
		}
		seen[tx.GroupKey] = append(seen[tx.GroupKey], tx)
	}

	var dups []DuplicateGroup
	for _, key := range order {
		group := seen[key]
		if len(group) > 1 {
			dups = append(dups, DuplicateGroup{
				Source:       group[0].Source,
				Reference:    key,
				Transactions: group,
			})
		}
	}
	return dups
}

// applyDuplicatePolicy collapses txns to one representative per GroupKey when
// policy is merge or latest. merge keeps the first occurrence; latest keeps the
// last. For flag/keep it returns txns unchanged (no allocation).
// Rows with an empty GroupKey are never collapsed regardless of policy.
func applyDuplicatePolicy(txns []Transaction, policy config.DuplicatePolicy) []Transaction {
	switch policy {
	case config.DuplicatePolicyMerge, config.DuplicatePolicyLatest:
	default:
		return txns
	}
	seen := make(map[string]int, len(txns)) // GroupKey → index in out
	out := make([]Transaction, 0, len(txns))
	for _, tx := range txns {
		if tx.GroupKey == "" {
			out = append(out, tx)
			continue
		}
		if idx, exists := seen[tx.GroupKey]; exists {
			if policy == config.DuplicatePolicyLatest {
				out[idx] = tx
			}
		} else {
			seen[tx.GroupKey] = len(out)
			out = append(out, tx)
		}
	}
	return out
}

// ApplyDuplicatePolicy applies the duplicate_policy from cfg to txns.
// Convenience wrapper for batch callers (Parse → Reconcile path).
func ApplyDuplicatePolicy(txns []Transaction, cfg config.ParserCfg) []Transaction {
	return applyDuplicatePolicy(txns, cfg.ResolvedDuplicatePolicy())
}

// ReconcileOptions carries optional overrides for a Reconcile or ReconcileMultiSource call.
// Pass as the last variadic argument; omit entirely to preserve existing default behavior.
type ReconcileOptions struct {
	// LeftPolicy overrides the left source's duplicate handling policy.
	// Defaults to DuplicatePolicyFlag (current behavior) when zero.
	LeftPolicy config.DuplicatePolicy
	// RightPolicy overrides the right source's duplicate handling policy.
	// Defaults to DuplicatePolicyFlag (current behavior) when zero.
	RightPolicy config.DuplicatePolicy
	// ResultMode is embedded in Summary.ResultMode. Empty means "all" (the default).
	ResultMode config.ResultMode
	// RunID is embedded in Summary.RunID when provided. Matches the telemetry RunID.
	RunID string
}

// resolveNameMatchThreshold returns t if it is a valid Jaccard threshold (0 < t <= 1),
// else the default of 0.5. Config validation already rejects out-of-range values when
// set, so this only needs to handle the unset (zero) case.
func resolveNameMatchThreshold(t float64) float64 {
	if t <= 0 {
		return 0.5
	}
	return t
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
