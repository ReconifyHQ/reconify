---
name: reconify-engine-debug
description: Diagnose a Reconify Engine reconciliation that produced unexpected numbers — read exception events, trace them to a config cause, and fix the mapping rather than the tolerance.
---

# Reconify Engine Debug

Start from the result, not from the config. `reconify explain RESULT.json` gives a deterministic,
bounded summary; `--format ndjson` on the original run gives one event per line for inspection.

## Event types

| Event | Meaning |
|---|---|
| `match` | Left+right pair reconciled cleanly |
| `unmatched_left` / `unmatched_right` | A row with no counterpart |
| `amount_diff` | Reference matched, amount differs beyond `amount_tolerance_minor` |
| `timing_diff` | Reference and amount matched, date outside `date_window` |
| `duplicate` | Two rows in the *same* source share a reference |
| `ambiguous_group` | Several left rows share one reference; needs manual review |
| `grouped_match` / `grouped_amount_diff` / `grouped_timing_diff` | 1-N results from a `one_to_many` pass |
| `many_to_many_match` / `many_to_many_amount_diff` / `many_to_many_timing_diff` | N:M results |
| `source_summary` | Per-counterpart summary in a 1-N run |
| `summary` | Aggregate counts; always the last event |

## From symptom to cause

| Symptom | Look at |
|---|---|
| Everything unmatched | `ref_col` maps to different identifiers on each side, or `date_layout` is wrong and no date parsed |
| Many `amount_diff` at a similar magnitude | A real fee or FX spread — set `amount_tolerance_minor` from the stated threshold, in minor units |
| Many `amount_diff` off by a factor of 100 | `multiplier` is wrong on one side |
| Many `timing_diff` of one or two days | Settlement delay — widen `date_window`, not the amount tolerance |
| `duplicate` events | `ref_col` is not unique in that source; consider `group_col` or a `one_to_many` pass |
| `ambiguous_group` | Reference repeats on the left; the data cannot be matched 1:1 as configured |
| Counterpart names empty | `name_col` is not set on the parser |

Reproduce with the smallest input that still shows the behaviour, then re-run and compare the
summary. Because the Engine is deterministic, an unchanged config over unchanged inputs cannot
produce a different result — if the numbers moved, something in the config or the files moved.

## The rule that matters

A lower match rate is not a problem to be tuned away. Before widening any tolerance, identify the
specific rows it would newly absorb and confirm they are the same transaction. Widening
`amount_tolerance_minor`, `date_window`, or `name_match_threshold` to raise a match rate converts
real financial differences into silence.
