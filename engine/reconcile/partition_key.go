//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"fmt"
	"strings"

	"github.com/reconifyhq/reconify/config"
)

// PartitionKeyColumns resolves which column to use as the partition routing key
// for each side of the pair. All passes must agree on the same logical selector
// (reference, name, or group_key); mixed selectors or an empty key column
// return ok=false with a descriptive reason.
func PartitionKeyColumns(pair config.Pair, leftCfg, rightCfg config.ParserCfg) (leftCol, rightCol string, ok bool, reason string) {
	leftCol, rightCol, _, ok, reason = partitionKeyColumns(pair, leftCfg, rightCfg)
	return leftCol, rightCol, ok, reason
}

// partitionKeyColumns resolves the common logical selector as well as the
// concrete input columns. The selector is useful to multi-source callers,
// where each counterpart may use a different physical header for the same
// matching key.
func partitionKeyColumns(pair config.Pair, leftCfg, rightCfg config.ParserCfg) (leftCol, rightCol, selector string, ok bool, reason string) {
	if pair.NameMode == "tokens" {
		return "", "", "", false, "partitioned backend does not support name-token matching"
	}
	if len(pair.Passes) == 0 {
		if leftCfg.RefCol == "" || rightCfg.RefCol == "" {
			return "", "", "", false, "partitioned backend requires ref_col on both sources"
		}
		return leftCfg.RefCol, rightCfg.RefCol, "reference", true, ""
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
				return "", "", "", false, fmt.Sprintf("unknown group_by %q", pass.ResolvedGroupBy())
			}
		default:
			return "", "", "", false, fmt.Sprintf("pass type %q is not supported by the partitioned backend", pass.Type)
		}
		if lc == "" || rc == "" {
			return "", "", "", false, fmt.Sprintf("partition key column for selector %q is empty", sel)
		}
		if resolvedSelector == "" {
			resolvedLeft, resolvedRight, resolvedSelector = lc, rc, sel
		} else if resolvedLeft != lc || resolvedRight != rc {
			return "", "", "", false, fmt.Sprintf("passes resolve to different partition key columns (%q vs %q)", resolvedSelector, sel)
		}
	}
	if resolvedSelector == "" {
		return "", "", "", false, "no passes configured"
	}
	return resolvedLeft, resolvedRight, resolvedSelector, true, ""
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
