---
name: reconify-debug
description: Interpret Reconify reconciliation output and diagnose mismatches. Use when an agent needs to understand what NDJSON or JSON output means, trace why transactions are unmatched, or determine which config field to adjust.
---

# Reconify Debug

## Overview

Use this skill when the task involves understanding or diagnosing reconciliation results. Read `engine/transaction.go` for the canonical type definitions and `engine/format.go` for writer logic.

## Quick Inspection

Run a small reconciliation with NDJSON to get one-event-per-line output:

```bash
go run ./cmd/reconify reconcile \
  --config reconify.yaml \
  --pair <pair_name> \
  --format ndjson \
  --out results.ndjson
```

Inspect event distribution:

```bash
# Count events by type
jq -r '.type' results.ndjson | sort | uniq -c | sort -rn

# Show all unmatched left transactions
jq 'select(.type == "unmatched" and .side == "left")' results.ndjson

# Show the summary
jq 'select(.type == "summary")' results.ndjson
```

## NDJSON Event Types

Each line in NDJSON output is a JSON object with a `type` field:

Each line is `{"type":"<name>","data":{...}}`. The `data` payload differs per event type.

| Type | When emitted | `data` payload |
|---|---|---|
| `run_info` | First line, only with `--audit` | RunInfo: `run_id`, `timestamp`, `tool_version`, `files`, `pair_config` |
| `match` | Clean 1:1 match | `{left: Transaction, right: Transaction}` |
| `amount_diff` | Reference matched, amount outside tolerance | `{left, right, diff_minor}` |
| `timing_diff` | Reference+amount matched, date outside window | `{left, right, days_diff}` |
| `unmatched_left` | Left transaction with no match | Transaction object |
| `unmatched_right` | Right transaction with no match | Transaction object |
| `grouped_match` | One left matched N rights (one_to_many pass) | `{left, rights: []Transaction}` |
| `grouped_amount_diff` | Grouped, amount differs | `{left, rights, diff_minor}` |
| `grouped_timing_diff` | Grouped, date outside window | `{left, rights, days_diff}` |
| `ambiguous_group` | Multiple lefts share same reference | `{reference, left_rows, rights}` |
| `duplicate` | Transactions in same source share same reference | `{source, reference, transactions}` |
| `source_summary` | Per-counterpart summary (1-N pairs only) | `{source: string, summary: Summary}` |
| `summary` | Always last line | Summary: `total_left`, `total_right`, `matched`, `unmatched_left`, `unmatched_right`, `amount_diff_count`, `timing_diff_count`, `duplicate_count`, `match_rate_pct`, `reconciled_rate_pct` |

A `Transaction` object: `id`, `date` (RFC3339), `amount` (minor units integer), `currency`, `reference`, `name`, `source`, `raw` (original columns, omitted when `skip_raw: true`).

## Diagnostic Decision Tree

**High `unmatched_left` or `unmatched_right`**
1. Check column mapping with `config check-source`: `go run ./cmd/reconify config check-source --config reconify.yaml --source <name> --file <file>`
2. Verify `ref_col` points to the correct unique identifier column in each source
3. If references are formatted differently (e.g. `TXN-001` vs `001`), add normalization or align source data
4. Check `date_layout` matches the actual date format in the file

**`amount_diff` events**
- Amount matches by reference but the numeric value differs beyond `amount_tolerance_minor`
- Increase `amount_tolerance_minor` if the difference is expected (e.g. rounding, fees)
- Check `multiplier` — if one source is in major units (dollars) and another in minor units (cents), the multiplier is wrong
- Verify `decimal` and `thousands` separators are set correctly for each source

**`timing_diff` events**
- Reference+amount matched, but settlement date falls outside `date_window`
- Increase `date_window` (e.g. from `1d` to `3d`) to accommodate settlement lag
- Check `tz` and top-level `timezone` — timezone mismatches shift dates by hours, which can cross midnight

**`ambiguous_group` events (one_to_many pass)**
- Multiple left rows share the same reference value
- Add or correct `group_col` on the left source to group installments correctly
- If references are truly non-unique, use `ref_col` pointing to a more specific field

**`duplicate` events**
- Transactions within a single source share the same reference
- Expected for installment payments — add a `one_to_many` pass in the pair config
- If unexpected, investigate data quality in the source file

**`match_rate_pct` below expectation but `reconciled_rate_pct` higher**
- `match_rate_pct` counts only exact 1:1 matches; `reconciled_rate_pct` includes amount/timing diffs and grouped matches
- A fully reconciled one_to_many dataset can show sub-100% `match_rate_pct` — this is expected

## Critical Files

- `engine/transaction.go` — Transaction, Summary, all pair/group types
- `engine/format.go` — ResultWriter interfaces, ndjsonWriter, jsonWriter implementations
- `internal/cli/reconcile.go` — CLI flags affecting output (`--audit`, `--deterministic`, `--fail-if-unmatched`)
