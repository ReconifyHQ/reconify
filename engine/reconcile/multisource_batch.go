//nolint:revive // This package preserves stable compatibility names.
//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"fmt"

	//nolint:staticcheck // Domain types are deliberately imported into the implementation namespace.
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/matching"

	"strings"

	"github.com/reconifyhq/reconify/config"
	engineTelemetry "github.com/reconifyhq/reconify/engine/telemetry"
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
		Schema:     ResultSchemaV1,
		PairName:   pairName,
		LeftSource: leftSource,
		BySource:   make(map[string]Summary, len(counterparts)),
	}
	// Annotate left-side duplicates once (before any pass) for "flag" only.
	if leftPolicy == config.DuplicatePolicyFlag {
		result.Duplicates = matching.AnnotateDuplicates(left)
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
		result.FinancialEffectMatches = append(result.FinancialEffectMatches, passResult.FinancialEffectMatches...)
		result.FinancialEffectDiffs = append(result.FinancialEffectDiffs, passResult.FinancialEffectDiffs...)
		result.FinancialUnchecked = append(result.FinancialUnchecked, passResult.FinancialUnchecked...)
		result.SettlementMatches = append(result.SettlementMatches, passResult.SettlementMatches...)
		result.SettlementDiffs = append(result.SettlementDiffs, passResult.SettlementDiffs...)
		result.BySource[cp.SourceName] = passResult.Summary

		// Only keep this pass's right-side duplicate groups — its left-side
		// groups are a re-annotation of the carried-forward left set, already
		// covered by the single matching.AnnotateDuplicates(left) call above.
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
	reporter := engineTelemetry.NewReporter(telemetry)
	if reporter != nil {
		defer reporter.Close()
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
	reporter.Start(stage, leftSource, rightLabel, &leftTotal)
	result, err := ReconcileMultiSource(pairName, leftSource, left, counterparts, pair, opts...)
	if err != nil {
		reporter.Fail(leftTotal)
		return nil, err
	}
	reporter.Complete(leftTotal)
	reporter.Start("finalization", leftSource, rightLabel, nil)
	reporter.Complete(0)
	return result, nil
}

// CounterpartStream describes one streaming counterpart pass: its source name,
// right-side CSV file path + parser config, and a pre-built (empty) RightIndex
// that this pass populates. The caller owns the Index (e.g. for Close()), the
// same way callers of ReconcileStreaming own the single RightIndex they pass in.
