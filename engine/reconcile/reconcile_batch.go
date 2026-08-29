// Package reconcile orchestrates parsing, matching, indexing, and result emission.
//
//nolint:revive // This package preserves stable compatibility names.
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"context"
	"fmt"

	//nolint:staticcheck // Domain types are deliberately imported into the implementation namespace.
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/matching"

	"math"

	"github.com/reconifyhq/reconify/config"
	engineTelemetry "github.com/reconifyhq/reconify/engine/telemetry"
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
	left = matching.ApplyPolicy(left, leftPolicy)
	right = matching.ApplyPolicy(right, rightPolicy)

	dateWindowDays, err := matching.ParseDateWindow(pair.DateWindow)
	if err != nil {
		return nil, fmt.Errorf("invalid date_window: %w", err)
	}
	// Monetary totals are emitted in summary. To keep those totals meaningful,
	// reject runs that mix non-empty currencies.
	cc := matching.CurrencyTracker{}
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
		Schema:      ResultSchemaV1,
		PairName:    pairName,
		LeftSource:  leftSource,
		RightSource: rightSource,
	}
	for _, tx := range append(append([]Transaction(nil), left...), right...) {
		for _, check := range tx.FinancialChecks {
			finding := FinancialEffectFinding{Transaction: tx, Check: check}
			if check.Field == "settlement" {
				switch check.Status {
				case "match":
					result.SettlementMatches = append(result.SettlementMatches, finding)
				case "diff":
					result.SettlementDiffs = append(result.SettlementDiffs, finding)
				}
				continue
			}
			switch check.Status {
			case "match":
				result.FinancialEffectMatches = append(result.FinancialEffectMatches, finding)
			case "diff":
				result.FinancialEffectDiffs = append(result.FinancialEffectDiffs, finding)
			case "unchecked":
				result.FinancialUnchecked = append(result.FinancialUnchecked, finding)
			}
		}
	}

	threshold := matching.ResolveNameMatchThreshold(pair.NameMatchThreshold)

	// Dispatches explicit passes when configured; falls back to the legacy
	// reference-first + optional NameMode path when Passes is empty.
	if len(pair.Passes) > 0 {
		unmatchedLeft, unmatchedRight := left, right
		for _, pass := range pair.Passes {
			switch pass.Type {
			case config.PassTypeReferenceOneToOne:
				unmatchedLeft, unmatchedRight = matching.MatchByReference(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays)
			case config.PassTypeNameTokensOneToOne:
				unmatchedLeft, unmatchedRight = matching.MatchByNameTokens(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays, threshold)
			case config.PassTypeOneToMany:
				unmatchedLeft, unmatchedRight = matching.MatchByReferenceOneToMany(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays, pass.ResolvedGroupBy())
			case config.PassTypeManyToMany:
				unmatchedLeft, unmatchedRight = matching.MatchByReferenceManyToMany(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays, pass.ResolvedGroupBy())
			case config.PassTypeSubsetSum:
				unmatchedLeft, unmatchedRight = matching.MatchBySubsetSum(
					result, unmatchedLeft, unmatchedRight,
					pair.AmountToleranceMinor, dateWindowDays, pass)
			default:
				return nil, fmt.Errorf("unsupported pass type %q", pass.Type)
			}
		}
		result.UnmatchedLeft = unmatchedLeft
		result.UnmatchedRight = unmatchedRight
	} else {
		// Legacy path: reference always runs; name-token pass enabled by NameMode.
		unmatchedLeft, unmatchedRight := matching.MatchByReference(
			result, left, right,
			pair.AmountToleranceMinor, dateWindowDays)
		if pair.NameMode == "tokens" {
			unmatchedLeft, unmatchedRight = matching.MatchByNameTokens(
				result, unmatchedLeft, unmatchedRight,
				pair.AmountToleranceMinor, dateWindowDays, threshold)
		}
		result.UnmatchedLeft = unmatchedLeft
		result.UnmatchedRight = unmatchedRight
	}
	result.Warnings = cc.Warnings()

	// 3. Duplicate annotation — only for "flag" policy; other policies suppress reporting.
	if leftPolicy == config.DuplicatePolicyFlag {
		result.Duplicates = append(result.Duplicates, matching.AnnotateDuplicates(left)...)
	}
	if rightPolicy == config.DuplicatePolicyFlag {
		result.Duplicates = append(result.Duplicates, matching.AnnotateDuplicates(right)...)
	}

	// 4. Populate summary
	summary, err := buildSummary(len(left), len(right), result, cc.Currency())
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
	reporter := engineTelemetry.NewReporter(telemetry)
	if reporter != nil {
		defer reporter.Close()
	}
	leftTotal := len(left)
	stage := "left_match"
	if len(pair.Passes) > 0 {
		stage = "grouped_pass"
	}
	reporter.Start(stage, leftSource, rightSource, &leftTotal)
	result, err := Reconcile(pairName, leftSource, rightSource, left, right, pair, opts...)
	if err != nil {
		reporter.Fail(leftTotal)
		return nil, err
	}
	reporter.Complete(leftTotal)
	reporter.Start("finalization", leftSource, rightSource, nil)
	reporter.Complete(0)
	return result, nil
}

// ParseWithTelemetry parses a complete source while emitting a live parsing
// stage. It is intended for batch callers that must read both inputs before
// reconciliation can begin.
func ParseWithTelemetry(ctx context.Context, sourceName, filePath string, cfg config.ParserCfg, counterpart string, telemetry TelemetryOptions) ([]Transaction, error) {
	reporter := engineTelemetry.NewReporter(telemetry)
	if reporter != nil {
		defer reporter.Close()
	}
	var transactions []Transaction
	rows := 0
	reporter.Start("parse", sourceName, counterpart, nil)
	err := ParseEach(ctx, sourceName, filePath, cfg, func(tx Transaction, _ int) error {
		rows++
		reporter.Progress(rows)
		transactions = append(transactions, tx)
		return nil
	})
	if err != nil {
		reporter.Fail(rows)
		return nil, err
	}
	reporter.Complete(rows)
	return transactions, nil
}

// buildSummary computes aggregate counts, rates, and monetary totals from a fully
// populated Result (Matched/UnmatchedLeft/UnmatchedRight/AmountDiff/TimingDiff/
// Duplicates already set). totalLeft/totalRight are the original input row counts —
// callers that aggregate multiple passes (ReconcileMultiSource) pass the true
// original totals here, not a sum of intermediate per-pass unmatched counts.
// currency is the base currency for monetary totals, forwarded from matching.CurrencyTracker.base.
func buildSummary(totalLeft, totalRight int, result *Result, currency string) (Summary, error) {
	total := totalLeft
	if totalRight > total {
		total = totalRight
	}
	matchRate := 0.0
	if total > 0 {
		matchRate = math.Round(float64(len(result.Matched))/float64(total)*10000) / 100
	}
	reconciledCount, err := CheckedAddInt("reconciled count", len(result.Matched), len(result.AmountDiff), len(result.TimingDiff), len(result.GroupedMatched), len(result.GroupedAmountDiff), len(result.GroupedTimingDiff), len(result.ManyToManyMatched), len(result.ManyToManyAmountDiff), len(result.ManyToManyTimingDiff), len(result.SubsetSumMatched))
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
		dupTxnCount, err = CheckedAddInt("duplicate count", dupTxnCount, len(g.Transactions))
		if err != nil {
			return Summary{}, err
		}
	}

	// Compute monetary totals from accumulated result slices.
	var matchedAmtLeft, matchedAmtRight int64
	for _, mp := range result.Matched {
		matchedAmtLeft, err = CheckedAddInt64("matched left amount", matchedAmtLeft, mp.Left.Amount)
		if err != nil {
			return Summary{}, err
		}
		matchedAmtRight, err = CheckedAddInt64("matched right amount", matchedAmtRight, mp.Right.Amount)
		if err != nil {
			return Summary{}, err
		}
	}
	// Grouped matches contribute to the same matched totals.
	for _, gm := range result.GroupedMatched {
		matchedAmtLeft, err = CheckedAddInt64("matched left amount", matchedAmtLeft, gm.Left.Amount)
		if err != nil {
			return Summary{}, err
		}
		for _, r := range gm.Rights {
			matchedAmtRight, err = CheckedAddInt64("matched right amount", matchedAmtRight, r.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
	}
	for _, mm := range result.ManyToManyMatched {
		for _, l := range mm.Lefts {
			matchedAmtLeft, err = CheckedAddInt64("matched left amount", matchedAmtLeft, l.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
		for _, r := range mm.Rights {
			matchedAmtRight, err = CheckedAddInt64("matched right amount", matchedAmtRight, r.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
	}
	var unmatchedAmtLeft int64
	for _, tx := range result.UnmatchedLeft {
		unmatchedAmtLeft, err = CheckedAddInt64("unmatched left amount", unmatchedAmtLeft, tx.Amount)
		if err != nil {
			return Summary{}, err
		}
	}
	var unmatchedAmtRight int64
	for _, tx := range result.UnmatchedRight {
		unmatchedAmtRight, err = CheckedAddInt64("unmatched right amount", unmatchedAmtRight, tx.Amount)
		if err != nil {
			return Summary{}, err
		}
	}
	var amountDiffTotal int64
	for _, ap := range result.AmountDiff {
		d, err := CheckedAbsInt64("amount difference", ap.DiffMinor)
		if err != nil {
			return Summary{}, err
		}
		amountDiffTotal, err = CheckedAddInt64("amount difference total", amountDiffTotal, d)
		if err != nil {
			return Summary{}, err
		}
	}
	for _, gd := range result.GroupedAmountDiff {
		d, err := CheckedAbsInt64("amount difference", gd.DiffMinor)
		if err != nil {
			return Summary{}, err
		}
		amountDiffTotal, err = CheckedAddInt64("amount difference total", amountDiffTotal, d)
		if err != nil {
			return Summary{}, err
		}
	}
	for _, md := range result.ManyToManyAmountDiff {
		d, err := CheckedAbsInt64("amount difference", md.DiffMinor)
		if err != nil {
			return Summary{}, err
		}
		amountDiffTotal, err = CheckedAddInt64("amount difference total", amountDiffTotal, d)
		if err != nil {
			return Summary{}, err
		}
	}
	// Ambiguous groups require manual reconciliation — their amounts count toward
	// TotalDiscrepancy separately from unmatched rows.
	var ambiguousAmtLeft, ambiguousAmtRight int64
	for _, ag := range result.AmbiguousGroups {
		for _, tx := range ag.LeftRows {
			ambiguousAmtLeft, err = CheckedAddInt64("ambiguous left amount", ambiguousAmtLeft, tx.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
		for _, tx := range ag.Rights {
			ambiguousAmtRight, err = CheckedAddInt64("ambiguous right amount", ambiguousAmtRight, tx.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
	}

	// Subset-sum matched pairs contribute to matched monetary totals.
	for _, sm := range result.SubsetSumMatched {
		matchedAmtLeft, err = CheckedAddInt64("matched left amount", matchedAmtLeft, sm.Left.Amount)
		if err != nil {
			return Summary{}, err
		}
		for _, r := range sm.Rights {
			matchedAmtRight, err = CheckedAddInt64("matched right amount", matchedAmtRight, r.Amount)
			if err != nil {
				return Summary{}, err
			}
		}
	}

	// Subset-sum ambiguous pairs consume their involved right rows (deduplicated
	// across alternatives, matching MatchBySubsetSum's consumption logic) without
	// matching them. Track those amounts as ambiguous so they aren't silently
	// dropped from both row output and TotalDiscrepancy.
	for _, sa := range result.SubsetSumAmbiguous {
		seen := make(map[string]bool)
		for _, alt := range sa.Alternatives {
			for _, r := range alt {
				if seen[r.ID] {
					continue
				}
				seen[r.ID] = true
				ambiguousAmtRight, err = CheckedAddInt64("ambiguous right amount", ambiguousAmtRight, r.Amount)
				if err != nil {
					return Summary{}, err
				}
			}
		}
	}

	totalDiscrepancy, err := CheckedAddInt64("total discrepancy", unmatchedAmtLeft, unmatchedAmtRight, amountDiffTotal, ambiguousAmtLeft, ambiguousAmtRight)
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
		SubsetSumMatchedCount:     len(result.SubsetSumMatched),
		SubsetSumAmbiguousCount:   len(result.SubsetSumAmbiguous),
		SubsetSumSkippedCount:     len(result.SubsetSumSkipped),
		FinancialEffectMatchCount: len(result.FinancialEffectMatches),
		FinancialEffectDiffCount:  len(result.FinancialEffectDiffs),
		FinancialUncheckedCount:   len(result.FinancialUnchecked),
		SettlementMatchCount:      len(result.SettlementMatches),
		SettlementDiffCount:       len(result.SettlementDiffs),
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
