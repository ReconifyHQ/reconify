---
name: reconify-engine-config
description: Write, validate, and correct a Reconify Engine reconify.yaml — source parsers, column mappings, pairs, tolerances, and matching passes.
---

# Reconify Engine Config

`reconify config schema` prints the authoritative, typed description of every configuration key,
including which are required, their defaults, and their enum values. Read it rather than recalling
an example. The shape below is a starting point, not the reference.

## Minimal working shape

```yaml
version: 1                        # required, must be 1
timezone: "UTC"                   # optional; overrides parser.tz

sources:
  left:
    file_pattern: "left.csv"      # required; glob, relative to THIS file's directory
    parser:
      type: csv                   # csv | json | xlsx | auto
      date_col: "date"            # required
      date_layout: "2006-01-02"   # required; Go layout, NOT strftime
      amount_col: "amount"        # required
      multiplier: 100             # required; 100 = dollars→cents, 1 = already minor units
      ref_col: "reference"        # the matching key
      currency_col: "currency"
      name_col: "description"     # required when the pair uses name_mode: tokens
  right:
    file_pattern: "right.csv"
    parser:
      type: csv
      date_col: "date"
      date_layout: "2006-01-02"
      amount_col: "amount"
      multiplier: 100
      ref_col: "reference"
      currency_col: "currency"
      name_col: "description"

pairs:
  left_vs_right:
    left: left                    # required
    right: right                  # one counterpart; use `rights: [a, b]` for 1-N
    date_window: "1d"             # "0d" requires an exact date match
    amount_tolerance_minor: 0     # MINOR UNITS: 200 means 2.00 when multiplier is 100
```

## Get the mappings from the file, not from memory

Run `reconify inspect FILE` on every input before writing YAML. Per column it reports
`inferred_type`, `ambiguous`, `sample_values`, and — for date columns — the exact `date_layout` to
copy. That removes the two most common config errors outright.

- `date_layout` is a Go reference layout (`2006-01-02`, `01/02/2006`), never `%Y-%m-%d`.
- `multiplier` is required. After it is applied, every amount is an integer in minor units, and so
  is `amount_tolerance_minor`.
- `ref_col` must be unique within a source. When it repeats, the Engine emits `duplicate` events
  instead of matching arbitrarily — that is a signal to fix the mapping, not to ignore.
- `thousands` strips a grouping separator before parsing (`","` for `1,234.56`); `decimal` sets the
  decimal point.
- `name_col` is required when the pair sets `name_mode: tokens`, and worth setting regardless:
  without it, counterpart names come back empty in every result row.
- `group_col` sets the duplicate/grouping key and falls back to `ref_col`.

## Pairs and matching

- `name_mode: none` matches on reference only. `tokens` also matches tokenized counterpart names
  above `name_match_threshold` (default `0.5`).
- `passes` sets the pipeline explicitly: `reference_one_to_one`, `name_tokens_one_to_one`,
  `one_to_many`, `many_to_many`. It is inferred from `name_mode` when absent, and `name_mode:
  tokens` is rejected when `passes` is set.
- `right` and `rights` are mutually exclusive. Use `rights: [stripe, paypal]` for one ledger
  against several counterparties.

## Validate, then prove each source

```bash
reconify config validate --config reconify.yaml
reconify config check-source --config reconify.yaml --source left --file left.csv
```

`validate` catches structural errors; `check-source` proves the mapping resolves against a real
file. Both exit `2` on a config error. Fix every error before reconciling: a config that validates
while pointing at the wrong column produces confident, wrong numbers.

Tolerances are policy, not tuning. Set `amount_tolerance_minor` and `date_window` from what the
user told you about their business — settlement fees, weekend banking delays — and never widen
them to make a match rate look better.
