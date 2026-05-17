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
| `type` | yes | Parser type. Currently `csv`. |
| `date_col` | yes | Column that contains the transaction date. |
| `date_layout` | yes | Go time layout, such as `2006-01-02`. |
| `amount_col` | yes | Column that contains the amount. |
| `decimal` | no | Decimal separator. Defaults to `.`. |
| `thousands` | no | Thousands separator. |
| `multiplier` | yes | Converts amounts to minor units, usually `100`. |
| `ref_col` | no | Reference or transaction ID column. |
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

`date_window` controls timing tolerance. `amount_tolerance_minor` is expressed in minor units, such as cents or kobo. `name_mode: tokens` enables token-based name matching for transactions without exact references.

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
