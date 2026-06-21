---
title: Configuration
description: Configure CSV parsers, reconciliation pairs, tolerances, and index backend behavior.
icon: Settings
---

# Configuration

Reconify reads YAML configuration. A config defines sources, parser mappings, reconciliation pairs, and index backend behavior.

## Sources

Each source maps a CSV schema into Reconify's normalized transaction model.

```yaml
sources:
  bank:
    file_pattern: "data/bank/*.csv"
    parser:
      type: csv
      date_col: "Date"
      date_layout: "2006-01-02"
      amount_col: "Amount"
      decimal: "."
      thousands: ","
      multiplier: 100
      currency_col: "Currency"
      name_col: "Details"
      ref_col: "Reference"
```

| Field | Required | Description |
|---|---|---|
| `type` | yes | Parser type: `csv`, `json`, `xlsx`, or `auto`. |
| `date_col` | yes | Column that contains the transaction date. |
| `date_layout` | yes | Go time layout, such as `2006-01-02`. |
| `amount_col` | yes | Column that contains the amount. |
| `decimal` | no | Decimal separator. Defaults to `.`. |
| `thousands` | no | Thousands separator. |
| `multiplier` | yes | Converts amounts to minor units, usually `100`. |
| `ref_col` | no | Reference or transaction ID column. |
| `group_col` | no | Duplicate-detection key, independent of `ref_col`; falls back to `ref_col` when omitted. Use when rows share an identifier, such as an invoice number, but each has a unique matching reference. |
| `name_col` | no | Description or merchant column. |
| `currency_col` | no | Currency code column. |
| `tz` | no | Timezone for date parsing. Defaults to `UTC`. |

## Pairs

Pairs define which sources to reconcile and what tolerances to apply.

```yaml
pairs:
  bank_vs_stripe:
    left: bank
    right: stripe
    date_window: "1d"
    amount_tolerance_minor: 0
    name_mode: "tokens"
```

| Field | Required | Description |
|---|---|---|
| `left` | yes | Source name for the left side. |
| `right` | yes, if no `rights` | Single counterpart source name. Mutually exclusive with `rights`. |
| `rights` | yes, if no `right` | Ordered list of counterpart sources for multi-counterpart runs. Mutually exclusive with `right`. |
| `date_window` | no | Timing tolerance, such as `1d` or `2d`. |
| `amount_tolerance_minor` | no | Amount tolerance in minor units, such as cents or kobo. Defaults to `0`. |
| `name_mode` | no | `none` (default) or `tokens`. `tokens` enables Tier 2 name-token similarity matching. |
| `name_match_threshold` | no | Jaccard threshold for `name_mode: tokens`, where `0 < x < 1`. Defaults to `0.5`; `1.0` is rejected. |

`right` and `rights` are mutually exclusive. Set exactly one of them for each pair.

### Multi-counterpart reconciliation (`rights`)

Use `rights` when one left source should be reconciled against several counterpart sources in a fixed order. This is useful for settlement workflows where ledger rows may appear in one of several PSP settlement files.

```yaml
version: 1

sources:
  ledger:
    file_pattern: "data/ledger/*.csv"
    parser:
      type: csv
      date_col: "Date"
      date_layout: "2006-01-02"
      amount_col: "Amount"
      decimal: "."
      thousands: ","
      multiplier: 100
      currency_col: "Currency"
      name_col: "Memo"
      ref_col: "SettlementBatchID"

  stripe_settlements:
    file_pattern: "data/stripe-settlements/*.csv"
    parser:
      type: csv
      date_col: "settlement_date"
      date_layout: "2006-01-02"
      amount_col: "net_amount"
      decimal: "."
      thousands: ","
      multiplier: 100
      currency_col: "currency"
      name_col: "description"
      ref_col: "batch_id"

  paypal_settlements:
    file_pattern: "data/paypal-settlements/*.csv"
    parser:
      type: csv
      date_col: "settlement_date"
      date_layout: "2006-01-02"
      amount_col: "net_amount"
      decimal: "."
      thousands: ","
      multiplier: 100
      currency_col: "currency"
      name_col: "description"
      ref_col: "batch_id"

pairs:
  ledger_vs_settlements:
    left: ledger
    rights: [stripe_settlements, paypal_settlements]
    date_window: "2d"
    amount_tolerance_minor: 0
```

Passes run in the order listed in `rights`. Unmatched ledger rows from the `stripe_settlements` pass carry forward to the `paypal_settlements` pass. If two counterpart sources could match the same left row, the earlier source in `rights` wins.

Run the pair with a readable structured output format:

```bash
reconify reconcile --config reconify.yaml --pair ledger_vs_settlements --format json --out results.json
```

For large jobs, prefer `ndjson`, `csv`, or `json-stream` over `table` or buffered `json`:

```bash
reconify reconcile --config reconify.yaml --pair ledger_vs_settlements --format ndjson --out results.ndjson
reconify reconcile --config reconify.yaml --pair ledger_vs_settlements --format json-stream --out results.json
```

`json` and `json-stream` include a per-counterpart `by_source` field alongside the aggregate `summary`:

```json
{
  "pair": "ledger_vs_settlements",
  "left_source": "ledger",
  "right_source": "stripe_settlements,paypal_settlements",
  "summary": {
    "total_left": 1200,
    "total_right": 1198,
    "matched": 1180,
    "unmatched_left": 20,
    "unmatched_right": 18,
    "reconciled_rate_pct": 98.33
  },
  "by_source": {
    "stripe_settlements": {
      "total_left": 1200,
      "total_right": 900,
      "matched": 890,
      "unmatched_left": 310,
      "unmatched_right": 10,
      "reconciled_rate_pct": 74.17
    },
    "paypal_settlements": {
      "total_left": 310,
      "total_right": 298,
      "matched": 290,
      "unmatched_left": 20,
      "unmatched_right": 8,
      "reconciled_rate_pct": 93.55
    }
  }
}
```

The Go result field is named `BySource`; JSON object output uses `by_source`. `ndjson` emits the same breakdown as one `source_summary` line per counterpart:

```json
{"type":"source_summary","data":{"source":"stripe_settlements","summary":{"total_left":1200,"total_right":900,"matched":890,"unmatched_left":310,"unmatched_right":10,"reconciled_rate_pct":74.17}}}
{"type":"summary","data":{"total_left":1200,"total_right":1198,"matched":1180,"unmatched_left":20,"unmatched_right":18,"reconciled_rate_pct":98.33}}
```

`csv` and `table` still include aggregate reconciliation output, but they omit the per-counterpart breakdown.

Caveats:

- `right` and `rights` are mutually exclusive.
- `--audit` and `--progress` are not yet supported for multi-counterpart runs.
- `name_mode: tokens` is not yet supported in streaming multi-counterpart mode. CLI reconciliation uses the streaming path for multi-counterpart pairs, so omit `name_mode: tokens` for `rights` pairs in CLI runs.

### Future grouped matching

`one_to_many`, `passes`, and `group_by` are roadmap items, not implemented configuration keys. They describe future grouped transaction matching and should not be treated as the same feature as today's ordered `rights` reconciliation.

## Reconciliation Passes

By default, Reconify runs reference matching first, then optional name-token matching when `name_mode: tokens` is set. You can make the matching pipeline explicit with `passes`:

```yaml
pairs:
  bank_vs_stripe:
    left: bank
    right: stripe
    date_window: "1d"
    amount_tolerance_minor: 0
    passes:
      - type: reference_one_to_one
      - type: name_tokens_one_to_one
```

Passes run in configured order. Each pass only sees rows that were not matched by earlier passes.

| Pass type | Description |
|---|---|
| `reference_one_to_one` | Matches one left row to one right row by reference. This is the default first tier. |
| `name_tokens_one_to_one` | Matches unmatched rows by Jaccard token similarity on the name field. Equivalent to `name_mode: tokens` in the legacy model. |
| `one_to_many` | Matches one left row against a group of right rows whose amounts sum to the left amount. |

**`passes` vs `rights`**: `rights` selects which counterpart *sources* to reconcile against in order. `passes` defines the matching *strategy* used within each counterpart. They are orthogonal — you can combine them.

Omitting `passes` preserves the legacy behavior exactly. When `passes` is set, `name_mode: tokens` is rejected; add a `name_tokens_one_to_one` pass instead.

## Index Backend

Reconify supports multiple right-side index backends:

| Backend | Use when |
|---|---|
| `memory` | You want the fastest lookups and the right-side file fits comfortably in RAM. |
| `disk` | You need lower RAM usage and can accept slower lookups. |
| `auto` | You want Reconify to choose disk indexing above a file-size threshold. |

```yaml
index:
  backend: auto
  spill_dir: "/tmp/reconify"
  auto_max_right_file_mb: 2048
```
