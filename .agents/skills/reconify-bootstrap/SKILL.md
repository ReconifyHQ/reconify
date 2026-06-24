---
name: reconify-bootstrap
description: Set up Reconify for a new reconciliation project from scratch. Use when an agent needs to create a reconify.yaml for new data sources, walk through the first reconciliation run, or help a user get from zero to working results.
---

# Reconify Bootstrap

## Overview

Use this skill for end-to-end setup of a new reconciliation project. The canonical config source of truth is `config/config.go`. Always validate before reconciling.

## Step-by-Step Workflow

### 1. Scaffold initial config

```bash
go run ./cmd/reconify config init
# Or with the installed binary:
reconify config init
```

The interactive `config init` wizard asks for source files, column mappings, and pair settings. It writes a `reconify.yaml` to the current directory. Answer every prompt — the wizard validates inputs as you go.

If running non-interactively, start from `examples/reconify.yaml` and edit manually.

### 2. Verify column mapping for each source

```bash
go run ./cmd/reconify config check-source \
  --config reconify.yaml \
  --source bank \
  --file data/bank/jan.csv
```

Repeat for every source. Fix any `[x]` column errors before proceeding. Common issues:
- Column names with leading/trailing spaces: trim them or use the exact name from the file header
- Case sensitivity: `config check-source` is case-insensitive, but document the actual casing in `date_col`, `amount_col`, etc.
- `ref_col` pointing to a non-unique column: the reconciler will emit `duplicate` events for every row

### 3. Validate the full config

```bash
go run ./cmd/reconify config validate --config reconify.yaml
```

Fix all `❌` errors. See required shape below. Exit code 2 means a config error.

### 4. Run the first reconciliation

```bash
go run ./cmd/reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --format ndjson \
  --out results.ndjson
```

Use `--format ndjson` for the first run — it streams O(1) memory and each event is on its own line for easy inspection.

### 5. Interpret the results

```bash
# Quick summary
jq 'select(.type == "summary")' results.ndjson

# Check match rate
jq 'select(.type == "summary") | {match_rate_pct, unmatched_left, unmatched_right}' results.ndjson
```

If match rate is low, use the `reconify-debug` skill to diagnose.

## Config Required Shape

```yaml
version: 1                        # required, must be 1

sources:
  bank:                           # source name (choose descriptive names)
    file_pattern: "data/bank/*.csv"
    parser:
      type: csv                   # csv | json | xlsx | auto
      date_col: "Date"            # exact column header for the date
      date_layout: "2006-01-02"  # Go time layout, NOT strftime
      amount_col: "Amount"
      multiplier: 100             # 100 for dollar→cents; 1 if already in minor units
      ref_col: "Reference"        # column with unique transaction ID (optional but recommended)

pairs:
  bank_vs_stripe:
    left: bank
    right: stripe                 # single counterpart; use rights: [a, b] for 1-N
    date_window: "1d"             # allow 1-day difference; "0d" = exact match
    amount_tolerance_minor: 0     # 0 = exact match; e.g. 50 = allow ±$0.50
    name_mode: none               # none | tokens
```

## Parser Decision Guide

| Situation | Setting |
|---|---|
| Dates formatted as `01/02/2006` | `date_layout: "01/02/2006"` |
| Amounts as `1,234.56` | `decimal: "."`, `thousands: ","` |
| Amounts already in cents | `multiplier: 1` |
| Excel file | `type: xlsx`; optionally `sheet: "Sheet1"` |
| JSON with nested fields | `type: json`; field paths use dot notation |
| Need fuzzy name matching | `name_mode: tokens`, add `name_col` to parser |
| Installment payments (1 left → N rights) | Add `passes: [{type: one_to_many}]` to pair; add `group_col` to right source parser |

## Common Blocking Errors

| Error | Cause | Fix |
|---|---|---|
| `date_col "Date" not found` | Column name mismatch | Run `config check-source` and copy the exact header name |
| `no files match pattern "data/*.csv"` | Wrong path or file not present | Check the path is relative to the config file directory |
| `multiplier must be positive` | Missing or zero multiplier | Set `multiplier: 100` (or `1` if already in minor units) |
| `date_layout is required` | Missing layout | Add `date_layout: "2006-01-02"` (use Go layout, not strftime) |
| `pair has no right or rights` | Missing counterpart | Add `right: <source_name>` or `rights: [...]` to the pair |
| `source "X" not found in config` | Typo in pair's left/right | Check source names match exactly between sources and pairs sections |

## Critical Files

- `config/config.go` — Config structs and validation rules (source of truth)
- `examples/reconify.yaml` — Full working example with all optional fields
- `internal/cli/config.go` — validate, check-source, init commands
- `internal/cli/config_init.go` — Interactive init wizard implementation
