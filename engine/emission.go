package engine

import (
	"fmt"
	"os"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

// PartitionKeyColumns resolves which column to use as the partition routing key
// for each side of the pair. All passes must agree on the same logical selector
// (reference, name, or group_key); mixed selectors or an empty key column
// return ok=false with a descriptive reason.
func PartitionKeyColumns(pair config.Pair, leftCfg, rightCfg config.ParserCfg) (leftCol, rightCol string, ok bool, reason string) {
	if pair.NameMode == "tokens" {
		return "", "", false, "partitioned backend does not support name-token matching"
	}
	if len(pair.Passes) == 0 {
		if leftCfg.RefCol == "" || rightCfg.RefCol == "" {
			return "", "", false, "partitioned backend requires ref_col on both sources"
		}
		return leftCfg.RefCol, rightCfg.RefCol, true, ""
	}

	var resolvedLeft, resolvedRight, resolvedSelector string
	for _, pass := range pair.Passes {
		var lc, rc, sel string
		switch pass.Type {
		case config.PassTypeReferenceOneToOne:
			lc, rc, sel = leftCfg.RefCol, rightCfg.RefCol, "reference"
		case config.PassTypeOneToMany, config.PassTypeManyToMany:
			switch pass.ResolvedGroupBy() {
			case config.GroupByReference:
				lc, rc, sel = leftCfg.RefCol, rightCfg.RefCol, "reference"
			case config.GroupByName:
				lc, rc, sel = leftCfg.NameCol, rightCfg.NameCol, "name"
			case config.GroupByGroupKey:
				lc, rc, sel = leftCfg.GroupCol, rightCfg.GroupCol, "group_key"
			default:
				return "", "", false, fmt.Sprintf("unknown group_by %q", pass.ResolvedGroupBy())
			}
		default:
			return "", "", false, fmt.Sprintf("pass type %q is not supported by the partitioned backend", pass.Type)
		}
		if lc == "" || rc == "" {
			return "", "", false, fmt.Sprintf("partition key column for selector %q is empty", sel)
		}
		if resolvedSelector == "" {
			resolvedLeft, resolvedRight, resolvedSelector = lc, rc, sel
		} else if resolvedLeft != lc || resolvedRight != rc {
			return "", "", false, fmt.Sprintf("passes resolve to different partition key columns (%q vs %q)", resolvedSelector, sel)
		}
	}
	if resolvedSelector == "" {
		return "", "", false, "no passes configured"
	}
	return resolvedLeft, resolvedRight, true, ""
}

// containsGroupedPass reports whether any pass requires grouped matching.
func containsGroupedPass(passes []config.PassConfig) bool {
	for _, p := range passes {
		if p.Type == config.PassTypeOneToMany || p.Type == config.PassTypeManyToMany {
			return true
		}
	}
	return false
}

// PartitionDuplicateSafe reports whether duplicate handling preserves its
// whole-input semantics when rows are routed by the pair's partition key.
// Keep never collapses or reports groups. The other policies need every row
// sharing a GroupKey to remain in one partition.
func PartitionDuplicateSafe(pair config.Pair, leftCfg, rightCfg config.ParserCfg) (bool, string) {
	if !containsGroupedPass(pair.Passes) {
		// The streaming partition path performs a global duplicate pre-scan for
		// non-grouped passes, so duplicate groups may safely span partitions.
		return true, ""
	}
	leftKey, rightKey, ok, reason := PartitionKeyColumns(pair, leftCfg, rightCfg)
	if !ok {
		return false, reason
	}
	if reason := duplicatePartitionReason(leftCfg, leftKey, "left"); reason != "" {
		return false, reason
	}
	if reason := duplicatePartitionReason(rightCfg, rightKey, "right"); reason != "" {
		return false, reason
	}
	return true, ""
}

func duplicatePartitionReason(cfg config.ParserCfg, partitionKeyCol, side string) string {
	if cfg.ResolvedDuplicatePolicy() == config.DuplicatePolicyKeep {
		return ""
	}
	groupCol := cfg.GroupCol
	if groupCol == "" {
		groupCol = cfg.RefCol
	}
	if groupCol == "" || strings.EqualFold(strings.TrimSpace(groupCol), strings.TrimSpace(partitionKeyCol)) {
		return ""
	}
	return fmt.Sprintf("%s duplicate groups use %q but partitioning uses %q; use backend: memory or backend: disk", side, groupCol, partitionKeyCol)
}

// WriteResultEvents writes all match events from res to w without calling
// WriteSummary or Flush. suppressWarnings silences the "format doesn't support
// grouped events" warning so callers that loop over partitions can emit it once.
func WriteResultEvents(w ResultWriter, res *Result, suppressWarnings bool) error {
	for _, mp := range res.Matched {
		if err := w.WriteMatch(mp); err != nil {
			return err
		}
	}
	for _, ap := range res.AmountDiff {
		if err := w.WriteAmountDiff(ap); err != nil {
			return err
		}
	}
	for _, tp := range res.TimingDiff {
		if err := w.WriteTimingDiff(tp); err != nil {
			return err
		}
	}
	for _, tx := range res.UnmatchedLeft {
		if err := w.WriteUnmatched(tx, "left"); err != nil {
			return err
		}
	}
	for _, tx := range res.UnmatchedRight {
		if err := w.WriteUnmatched(tx, "right"); err != nil {
			return err
		}
	}
	for _, dg := range res.Duplicates {
		if err := w.WriteDuplicate(dg); err != nil {
			return err
		}
	}
	hasGrouped := len(res.GroupedMatched)+len(res.GroupedAmountDiff)+
		len(res.GroupedTimingDiff)+len(res.AmbiguousGroups) > 0
	if gw, ok := w.(GroupedEventWriter); ok {
		for _, gm := range res.GroupedMatched {
			if err := gw.WriteGroupedMatch(gm); err != nil {
				return err
			}
		}
		for _, gd := range res.GroupedAmountDiff {
			if err := gw.WriteGroupedAmountDiff(gd); err != nil {
				return err
			}
		}
		for _, gt := range res.GroupedTimingDiff {
			if err := gw.WriteGroupedTimingDiff(gt); err != nil {
				return err
			}
		}
		for _, ag := range res.AmbiguousGroups {
			if err := gw.WriteAmbiguousGroup(ag); err != nil {
				return err
			}
		}
	} else if hasGrouped && !suppressWarnings {
		fmt.Fprintln(os.Stderr,
			"warning: current output format does not support grouped or ambiguous match events; "+
				"use --format=json or --format=ndjson to capture all one_to_many output")
	}
	hasManyToMany := len(res.ManyToManyMatched)+len(res.ManyToManyAmountDiff)+
		len(res.ManyToManyTimingDiff) > 0
	if mw, ok := w.(ManyToManyEventWriter); ok {
		for _, mm := range res.ManyToManyMatched {
			if err := mw.WriteManyToManyMatch(mm); err != nil {
				return err
			}
		}
		for _, md := range res.ManyToManyAmountDiff {
			if err := mw.WriteManyToManyAmountDiff(md); err != nil {
				return err
			}
		}
		for _, mt := range res.ManyToManyTimingDiff {
			if err := mw.WriteManyToManyTimingDiff(mt); err != nil {
				return err
			}
		}
	} else if hasManyToMany && !suppressWarnings {
		fmt.Fprintln(os.Stderr,
			"warning: current output format does not support many_to_many match events; "+
				"use --format=json or --format=ndjson to capture all many_to_many output")
	}
	for _, warning := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}
	if sbw, ok := w.(SourceBreakdownWriter); ok {
		for name, summary := range res.BySource {
			if err := sbw.WriteSourceSummary(name, summary); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteResult writes all events, the summary, and flushes w.
func WriteResult(w ResultWriter, res *Result) error {
	if err := WriteResultEvents(w, res, false); err != nil {
		return err
	}
	if err := w.WriteSummary(res.Summary); err != nil {
		return err
	}
	return w.Flush()
}
