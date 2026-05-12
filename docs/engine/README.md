# Reconciliation Engine

The engine is a Go package at `internal/engine/`. It is the single source of truth for all reconciliation logic — the CLI and the API both use it.

## Transaction model

Every record from every source is normalised into a `Transaction`:

```go
type Transaction struct {
    ID        string            // "{source}-{row}" e.g. "bank-1"
    Date      time.Time
    Amount    int64             // always minor units (kobo, cents)
    Currency  string
    Reference string            // payment reference, order ID, etc.
    Name      string            // description / merchant name
    Source    string            // source name from config
    Raw       map[string]string // original input row/object fields
}
```

**Amounts are always stored in minor units.** A value of `150000` means NGN 1,500.00. The multiplier in the parser config controls the conversion (typically `100` for 2 decimal places).

## Input parser (`parser.go`)

`Parse(sourceName, filePath string, cfg ParserCfg) ([]Transaction, error)`

`ParseEach(ctx, sourceName, filePath string, cfg ParserCfg, fn func(Transaction, int) error) error`

`ParseCSV(sourceName, filePath string, cfg CSVParserCfg) ([]Transaction, error)`

- Column lookup is **case-insensitive**
- CSV and XLSX use the first row as headers
- JSON supports top-level arrays and NDJSON objects
- Decimal and thousands separators are configurable
- Parenthetical negatives are supported: `(1,234.56)` → `-123456`
- Missing optional columns (reference, name, currency) are silently ignored

### Parser config fields

| Field | Required | Description |
|---|---|---|
| `type` | no | `csv`, `json`, `xlsx`, or `auto`; empty defaults to extension inference |
| `sheet` | no | XLSX sheet name; defaults to the first sheet |
| `date_col` | yes | column name for the transaction date |
| `date_layout` | yes | Go time layout, e.g. `2006-01-02` |
| `amount_col` | yes | column name for the amount |
| `decimal` | no | decimal separator, default `.` |
| `thousands` | no | thousands separator, default empty |
| `multiplier` | yes | converts amount to minor units, typically `100` |
| `ref_col` | no | reference / ID column |
| `name_col` | no | description / merchant name column |
| `currency_col` | no | currency code column |
| `tz` | no | timezone for date parsing, default `UTC` |

## Matching algorithm (`reconciler.go`)

The reconciler runs three tiers in sequence. Each tier removes matched transactions from the pool before the next tier runs.

### Tier 1 — Reference exact match

Transactions with matching `Reference` values are compared for amount and date:
- **Matched**: reference matches, amount within tolerance, date within window
- **Amount diff**: reference matches, date within window, amount outside tolerance
- **Timing diff**: reference matches, amount within tolerance, date outside window

Transactions without a reference are passed to tier 2 (or marked unmatched if name mode is `none`).

### Tier 2 — Name-token similarity (optional)

Activated when `name_mode: tokens` in the pair config. Uses Jaccard similarity on word tokens from the `Name` field. A score > 0.5 is considered a match, subject to the same amount and date tolerances.

### Tier 3 — Duplicate detection

Runs before tiers 1 and 2. Transactions in the same source sharing the same reference are grouped as duplicates. Only the first occurrence participates in matching.

## Result shape

```go
type Result struct {
    PairName       string
    LeftSource     string
    RightSource    string
    Summary        Summary          // counts + match rate
    Matched        []MatchedPair
    UnmatchedLeft  []Transaction
    UnmatchedRight []Transaction
    AmountDiff     []AmountDiffPair // matched ref, amount outside tolerance
    TimingDiff     []TimingDiffPair // matched ref+amount, date outside window
    Duplicates     []DuplicateGroup
}
```

## Config file format

The CLI reads a YAML config file. The API generates this automatically from request body params.

```yaml
version: 1
timezone: Africa/Lagos

index:
  backend: auto                # memory | disk | auto
  spill_dir: /tmp/reconify     # optional for disk/auto
  auto_max_right_file_mb: 2048 # optional threshold for auto

sources:
  bank:
    file_pattern: ./data/bank-statement.csv
    parser:
      type: csv
      date_col: Date
      date_layout: "02/01/2006"
      amount_col: Amount
      decimal: "."
      thousands: ","
      multiplier: 100
      ref_col: Reference
      name_col: Description

  stripe:
    file_pattern: ./data/stripe-export.csv
    parser:
      type: csv
      date_col: created
      date_layout: "2006-01-02"
      amount_col: amount
      multiplier: 1        # Stripe already uses minor units
      ref_col: id
      name_col: description

pairs:
  default:
    left: bank
    right: stripe
    date_window: 2d
    amount_tolerance_minor: 0
    name_mode: none
```

### Index backend options

- `memory` (default): highest throughput, right-side index must fit in RAM.
- `disk`: stores right-side index in a temporary SQLite file; lower RAM, slower point lookups.
- `auto`: picks `disk` when right file size is above `auto_max_right_file_mb`.
