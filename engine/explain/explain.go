// Package explain summarizes completed reconciliation result documents.
package explain

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/schemas"
)

// Options controls explanation output.
type Options struct {
	// TopN bounds the number of exception payloads copied into the explanation.
	TopN int
}

// Explain reads a JSON, JSON-stream, or NDJSON result and returns a deterministic
// explanation. Unknown future event types are ignored for forward compatibility.
func Explain(input io.Reader, options Options) (schemas.Explanation, error) {
	if options.TopN < 0 {
		return schemas.Explanation{}, fmt.Errorf("top exception count must be non-negative")
	}
	data, err := io.ReadAll(input)
	if err != nil {
		return schemas.Explanation{}, fmt.Errorf("read result: %w", err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return schemas.Explanation{}, fmt.Errorf("result is empty")
	}

	var result domain.Result
	if isNDJSON(trimmed) {
		result, err = parseNDJSON(trimmed)
	} else {
		err = json.Unmarshal(trimmed, &result)
		if err != nil {
			err = fmt.Errorf("decode JSON result: %w", err)
		}
	}
	if err != nil {
		return schemas.Explanation{}, err
	}
	return buildExplanation(result, options.TopN)
}

func isNDJSON(data []byte) bool {
	lineEnd := bytes.IndexByte(data, '\n')
	first := data
	if lineEnd >= 0 {
		first = data[:lineEnd]
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(first, &object) != nil {
		return false
	}
	_, hasType := object["type"]
	return hasType
}

type envelope struct {
	Schema string          `json:"schema"`
	Type   string          `json:"type"`
	Data   json.RawMessage `json:"data"`
}

func parseNDJSON(data []byte) (domain.Result, error) {
	var result domain.Result
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var event envelope
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return domain.Result{}, fmt.Errorf("decode NDJSON line %d: %w", line, err)
		}
		if event.Type == "" {
			return domain.Result{}, fmt.Errorf("decode NDJSON line %d: missing event type", line)
		}
		if err := appendEvent(&result, event); err != nil {
			return domain.Result{}, fmt.Errorf("decode NDJSON line %d (%s): %w", line, event.Type, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return domain.Result{}, fmt.Errorf("read NDJSON: %w", err)
	}
	return result, nil
}

func appendEvent(result *domain.Result, event envelope) error {
	decode := func(target any) error { return json.Unmarshal(event.Data, target) }
	switch event.Type {
	case "run_info":
		var value domain.RunInfo
		if err := decode(&value); err != nil {
			return err
		}
		result.RunInfo = &value
	case "index_selection":
		var value domain.IndexSelection
		if err := decode(&value); err != nil {
			return err
		}
		result.IndexSelection = &value
	case "match":
		var value domain.MatchedPair
		if err := decode(&value); err != nil {
			return err
		}
		result.Matched = append(result.Matched, value)
	case "amount_diff":
		var value domain.AmountDiffPair
		if err := decode(&value); err != nil {
			return err
		}
		result.AmountDiff = append(result.AmountDiff, value)
	case "timing_diff":
		var value domain.TimingDiffPair
		if err := decode(&value); err != nil {
			return err
		}
		result.TimingDiff = append(result.TimingDiff, value)
	case "unmatched_left":
		var value domain.Transaction
		if err := decode(&value); err != nil {
			return err
		}
		result.UnmatchedLeft = append(result.UnmatchedLeft, value)
	case "unmatched_right":
		var value domain.Transaction
		if err := decode(&value); err != nil {
			return err
		}
		result.UnmatchedRight = append(result.UnmatchedRight, value)
	case "duplicate":
		var value domain.DuplicateGroup
		if err := decode(&value); err != nil {
			return err
		}
		result.Duplicates = append(result.Duplicates, value)
	case "grouped_match":
		var value domain.GroupedMatchedPair
		if err := decode(&value); err != nil {
			return err
		}
		result.GroupedMatched = append(result.GroupedMatched, value)
	case "grouped_amount_diff":
		var value domain.GroupedAmountDiffPair
		if err := decode(&value); err != nil {
			return err
		}
		result.GroupedAmountDiff = append(result.GroupedAmountDiff, value)
	case "grouped_timing_diff":
		var value domain.GroupedTimingDiffPair
		if err := decode(&value); err != nil {
			return err
		}
		result.GroupedTimingDiff = append(result.GroupedTimingDiff, value)
	case "many_to_many_match":
		var value domain.ManyToManyMatchedPair
		if err := decode(&value); err != nil {
			return err
		}
		result.ManyToManyMatched = append(result.ManyToManyMatched, value)
	case "many_to_many_amount_diff":
		var value domain.ManyToManyAmountDiffPair
		if err := decode(&value); err != nil {
			return err
		}
		result.ManyToManyAmountDiff = append(result.ManyToManyAmountDiff, value)
	case "many_to_many_timing_diff":
		var value domain.ManyToManyTimingDiffPair
		if err := decode(&value); err != nil {
			return err
		}
		result.ManyToManyTimingDiff = append(result.ManyToManyTimingDiff, value)
	case "ambiguous_group":
		var value domain.AmbiguousGroupPair
		if err := decode(&value); err != nil {
			return err
		}
		result.AmbiguousGroups = append(result.AmbiguousGroups, value)
	case "financial_effect_match":
		var value domain.FinancialEffectFinding
		if err := decode(&value); err != nil {
			return err
		}
		result.FinancialEffectMatches = append(result.FinancialEffectMatches, value)
	case "financial_effect_diff":
		var value domain.FinancialEffectFinding
		if err := decode(&value); err != nil {
			return err
		}
		result.FinancialEffectDiffs = append(result.FinancialEffectDiffs, value)
	case "financial_unchecked":
		var value domain.FinancialEffectFinding
		if err := decode(&value); err != nil {
			return err
		}
		result.FinancialUnchecked = append(result.FinancialUnchecked, value)
	case "settlement_match":
		var value domain.SettlementFinding
		if err := decode(&value); err != nil {
			return err
		}
		result.SettlementMatches = append(result.SettlementMatches, value)
	case "settlement_diff":
		var value domain.SettlementFinding
		if err := decode(&value); err != nil {
			return err
		}
		result.SettlementDiffs = append(result.SettlementDiffs, value)
	case "source_summary":
		var value domain.SourceSummary
		if err := decode(&value); err != nil {
			return err
		}
		if result.BySource == nil {
			result.BySource = map[string]domain.Summary{}
		}
		result.BySource[value.Source] = value.Summary
	case "summary":
		return decode(&result.Summary)
	}
	return nil
}
