package explain

import (
	"encoding/json"

	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/schemas"
)

type exception struct {
	typ  string
	data any
}

func buildExplanation(result domain.Result, topN int) (schemas.Explanation, error) {
	findings := []schemas.Finding{
		{Category: "matched", Count: result.Summary.MatchedCount},
		{Category: "unmatched_left", Count: result.Summary.UnmatchedLeft},
		{Category: "unmatched_right", Count: result.Summary.UnmatchedRight},
		{Category: "amount_diff", Count: result.Summary.AmountDiffCount},
		{Category: "timing_diff", Count: result.Summary.TimingDiffCount},
		{Category: "duplicates", Count: result.Summary.DuplicateCount},
		{Category: "grouped_matched", Count: result.Summary.GroupedMatchedCount},
		{Category: "grouped_amount_diff", Count: result.Summary.GroupedAmountDiffCount},
		{Category: "grouped_timing_diff", Count: result.Summary.GroupedTimingDiffCount},
		{Category: "many_to_many_matched", Count: result.Summary.ManyToManyMatchedCount},
		{Category: "many_to_many_amount_diff", Count: result.Summary.ManyToManyAmountDiffCount},
		{Category: "many_to_many_timing_diff", Count: result.Summary.ManyToManyTimingDiffCount},
		{Category: "ambiguous_group", Count: result.Summary.AmbiguousGroupCount},
	}

	exceptions := collectExceptions(result)
	if result.Summary.FinancialEffectMatchCount != 0 {
		findings = append(findings, schemas.Finding{Category: "financial_effect_match", Count: result.Summary.FinancialEffectMatchCount})
	}
	if result.Summary.FinancialEffectDiffCount != 0 {
		findings = append(findings, schemas.Finding{Category: "financial_effect_diff", Count: result.Summary.FinancialEffectDiffCount})
	}
	if result.Summary.FinancialUncheckedCount != 0 {
		findings = append(findings, schemas.Finding{Category: "financial_unchecked", Count: result.Summary.FinancialUncheckedCount})
	}
	if result.Summary.SettlementMatchCount != 0 {
		findings = append(findings, schemas.Finding{Category: "settlement_match", Count: result.Summary.SettlementMatchCount})
	}
	if result.Summary.SettlementDiffCount != 0 {
		findings = append(findings, schemas.Finding{Category: "settlement_diff", Count: result.Summary.SettlementDiffCount})
	}
	total := exceptionCount(result, exceptions)
	if topN > len(exceptions) {
		topN = len(exceptions)
	}
	top := make([]schemas.Exception, 0, topN)
	for _, item := range exceptions[:topN] {
		data, err := objectData(item.data)
		if err != nil {
			return schemas.Explanation{}, err
		}
		top = append(top, schemas.Exception{Type: item.typ, Data: data})
	}
	return schemas.Explanation{
		Schema:          schemas.ExplanationSchemaV1,
		Summary:         result.Summary,
		BySource:        result.BySource,
		Findings:        findings,
		TopExceptions:   top,
		ExceptionsTotal: total,
		Truncated:       total > len(top),
	}, nil
}

func collectExceptions(result domain.Result) []exception {
	items := make([]exception, 0)
	for _, value := range result.UnmatchedLeft {
		items = append(items, exception{"unmatched_left", value})
	}
	for _, value := range result.UnmatchedRight {
		items = append(items, exception{"unmatched_right", value})
	}
	for _, value := range result.AmountDiff {
		items = append(items, exception{"amount_diff", value})
	}
	for _, value := range result.TimingDiff {
		items = append(items, exception{"timing_diff", value})
	}
	for _, value := range result.Duplicates {
		items = append(items, exception{"duplicate", value})
	}
	for _, value := range result.GroupedAmountDiff {
		items = append(items, exception{"grouped_amount_diff", value})
	}
	for _, value := range result.GroupedTimingDiff {
		items = append(items, exception{"grouped_timing_diff", value})
	}
	for _, value := range result.ManyToManyAmountDiff {
		items = append(items, exception{"many_to_many_amount_diff", value})
	}
	for _, value := range result.ManyToManyTimingDiff {
		items = append(items, exception{"many_to_many_timing_diff", value})
	}
	for _, value := range result.AmbiguousGroups {
		items = append(items, exception{"ambiguous_group", value})
	}
	for _, value := range result.FinancialEffectDiffs {
		items = append(items, exception{"financial_effect_diff", value})
	}
	for _, value := range result.SettlementDiffs {
		items = append(items, exception{"settlement_diff", value})
	}
	return items
}

func exceptionCount(result domain.Result, events []exception) int {
	// Summary counts remain authoritative when result_mode suppressed event
	// payloads. For categories whose summary count is a row count (rather than
	// an event count), use the payload count when it is present.
	total := max(result.Summary.UnmatchedLeft, countType(events, "unmatched_left"))
	total += max(result.Summary.UnmatchedRight, countType(events, "unmatched_right"))
	total += max(result.Summary.AmountDiffCount, countType(events, "amount_diff"))
	total += max(result.Summary.TimingDiffCount, countType(events, "timing_diff"))
	total += max(result.Summary.GroupedAmountDiffCount, countType(events, "grouped_amount_diff"))
	total += max(result.Summary.GroupedTimingDiffCount, countType(events, "grouped_timing_diff"))
	total += max(result.Summary.ManyToManyAmountDiffCount, countType(events, "many_to_many_amount_diff"))
	total += max(result.Summary.ManyToManyTimingDiffCount, countType(events, "many_to_many_timing_diff"))
	duplicateEvents := countType(events, "duplicate")
	if duplicateEvents > 0 {
		total += duplicateEvents
	} else {
		total += boolCount(result.Summary.DuplicateCount)
	}
	total += max(result.Summary.AmbiguousGroupCount, countType(events, "ambiguous_group"))
	total += max(result.Summary.FinancialEffectDiffCount, countType(events, "financial_effect_diff"))
	total += max(result.Summary.SettlementDiffCount, countType(events, "settlement_diff"))
	if total == 0 {
		return len(events)
	}
	return total
}

func countType(events []exception, typ string) int {
	count := 0
	for _, event := range events {
		if event.typ == typ {
			count++
		}
	}
	return count
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func boolCount(value int) int {
	if value > 0 {
		return 1
	}
	return 0
}

func objectData(value any) (map[string]interface{}, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var data map[string]interface{}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return nil, err
	}
	return data, nil
}
