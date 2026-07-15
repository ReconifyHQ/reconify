package cli

import (
	"fmt"
	"os"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine"
)

func containsBatchOnlyGroupedPass(passes []config.PassConfig) bool {
	for _, p := range passes {
		if p.Type == config.PassTypeOneToMany || p.Type == config.PassTypeManyToMany {
			return true
		}
	}
	return false
}

// drainResultToWriter replays all events from a batch Result through a ResultWriter.
// Writers that implement GroupedEventWriter or ManyToManyEventWriter receive grouped
// events; for writers that do not (csv, table), a single warning is printed to stderr
// and those events are skipped — all standard events are still emitted.
// ambiguous_groups require manual reconciliation and are never silently dropped from
// the Summary that is always written last.
func drainResultToWriter(w engine.ResultWriter, res *engine.Result) error {
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
	if gw, ok := w.(engine.GroupedEventWriter); ok {
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
	} else if hasGrouped {
		fmt.Fprintln(os.Stderr,
			"warning: current --format does not support grouped or ambiguous match events; "+
				"use --format=json or --format=ndjson to capture all one_to_many output")
	}
	hasManyToMany := len(res.ManyToManyMatched)+len(res.ManyToManyAmountDiff)+
		len(res.ManyToManyTimingDiff) > 0
	if mw, ok := w.(engine.ManyToManyEventWriter); ok {
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
	} else if hasManyToMany {
		fmt.Fprintln(os.Stderr,
			"warning: current --format does not support many_to_many match events; "+
				"use --format=json or --format=ndjson to capture all many_to_many output")
	}
	// Emit warnings to stderr — mirrors what the streaming path does via cc.Warnings().
	for _, warning := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", warning)
	}
	if sbw, ok := w.(engine.SourceBreakdownWriter); ok {
		for name, summary := range res.BySource {
			if err := sbw.WriteSourceSummary(name, summary); err != nil {
				return err
			}
		}
	}
	if err := w.WriteSummary(res.Summary); err != nil {
		return err
	}
	return w.Flush()
}

// resolveFile returns an explicit path if provided, otherwise resolves the first glob match.
