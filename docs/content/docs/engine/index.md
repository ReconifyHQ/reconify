---
title: Reconciliation Engine
description: Transaction normalization, CSV parsing, duplicate detection, and matching internals.
icon: GitCompareArrows
---

# Reconciliation Engine

The engine is a Go package at `engine/`. It is the single source of truth for all reconciliation logic.

## Transaction model

Every record from every source is normalised into a `Transaction`:

```go
type Transaction struct {
    ID        string            // "{source}-{row}" e.g. "bank-1"
    Date      time.Time
    Amount    int64             // always minor units (kobo, cents)
    Currency  string
    Reference string            // payment reference, order ID, etc. — the matching key
    GroupKey  string            // duplicate-detection key, independent of Reference; see group_col below
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
| `ref_col` | no | reference / ID column — the matching key used by Tier 1 |
| `group_col` | no | duplicate-detection key, independent of `ref_col`; falls back to `ref_col` when omitted. Use this when several rows legitimately share an identifier (e.g. an invoice paid in installments, all sharing an invoice number) but each has its own unique matching reference. |
| `name_col` | no | description / merchant name column |
| `currency_col` | no | currency code column |
| `tz` | no | timezone for date parsing, default `UTC` |

## Matching algorithm (`reconciler.go`)

The reconciler runs two matching tiers, then a separate, non-gating duplicate annotation pass. Every transaction always participates in matching — duplicate rows are never dropped or skipped.

### Tier 1 — Reference exact match

Transactions with matching `Reference` values are compared for amount and date. When more than one right-side candidate shares a reference, the engine picks the **best** candidate, not the first one encountered: an exact match (amount and date both within tolerance) always wins immediately; otherwise the candidate with the smallest amount difference is preferred over one with the smallest date difference, and ties are broken by the smallest diff regardless of where the candidate appears in the input. This best-candidate selection is shared (via `decideMatch`) between the batch and streaming matchers, so both behave identically.

- **Matched**: reference matches, amount within tolerance, date within window
- **Amount diff**: reference matches, date within window, amount outside tolerance
- **Timing diff**: reference matches, amount within tolerance, date outside window

Transactions without a reference are passed to tier 2 (or marked unmatched if name mode is `none`).

### Tier 2 — Name-token similarity (optional)

Activated when `name_mode: tokens` in the pair config. Uses Jaccard similarity on word tokens from the `Name` field. A score strictly greater than the configured threshold is considered a match, subject to the same amount and date tolerances.

The threshold is configurable per pair via `name_match_threshold` (`0 < x < 1`); it defaults to `0.5` when unset. `1.0` is rejected by validation rather than silently accepted: since the comparison is strict and a Jaccard score never exceeds `1.0`, that value would never match anything.

### Duplicate detection (annotation, not a gate)

Duplicate detection runs **after** matching and never filters anything out of it. Transactions in the same source sharing the same `GroupKey` (see `group_col`) are grouped and reported in `Result.Duplicates`, purely for visibility — every transaction, duplicate or not, still participates in Tier 1/Tier 2 matching. This means an invoice paid in three installments (same `group_col` value, three distinct `ref_col` values) shows up as a group of 3 in `Duplicates` while all three rows can independently match. `Summary.DuplicateCount` is a count of transactions across all duplicate groups, not a count of groups.

## Result shape

```go
type Result struct {
    PairName       string
    LeftSource     string
    RightSource    string
    Summary        Summary             // aggregate counts + match rate, across all counterparts
    Matched        []MatchedPair
    UnmatchedLeft  []Transaction
    UnmatchedRight []Transaction
    AmountDiff     []AmountDiffPair    // matched ref, amount outside tolerance
    TimingDiff     []TimingDiffPair    // matched ref+amount, date outside window
    Duplicates     []DuplicateGroup    // annotation only; see "Duplicate detection" above
    Warnings       []string            // non-fatal observations, e.g. empty-currency rows mixed with a non-empty base currency
    BySource       map[string]Summary  // per-counterpart breakdown for 1-N source pairs; nil for single-counterpart runs
}
```

`Summary` carries both `MatchRatePct` (exact Tier 1 matches only, unchanged for backward compatibility) and `ReconciledRatePct` (matched + amount-diff + timing-diff, i.e. every outcome where the two sides were reconciled to each other, just not perfectly). Both are percentages of `max(TotalLeft, TotalRight)`.

`BySource` is populated only when a pair has multiple counterparts (`rights`, see below); for an ordinary single-`right` pair it stays nil/empty and the top-level `Summary` is the only number that exists, so single-counterpart consumers are unaffected.

## Config file format

The CLI reads a YAML config file. The API generates this automatically from request body params.

```yaml
version: 1
timezone: Africa/Lagos

index:
  backend: auto                # memory | disk | auto | partitioned
  spill_dir: /tmp/reconify     # optional for disk/auto
  auto_max_right_file_mb: 2048 # optional threshold for auto
  partition_count: 32          # partitioned only; 0 uses the default

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

## 1-N source reconciliation

A pair can reconcile its `left` source against several counterparts in an explicit, ordered sequence instead of a single `right` source. Use `rights` instead of `right`:

```yaml
pairs:
  bank_vs_multiple:
    left: bank
    rights: [stripe, paypal]
    date_window: 1d
    amount_tolerance_minor: 0
```

`right` and `rights` are mutually exclusive; config validation requires exactly one of them. `Pair.Counterparts()` is the single helper every consumer (CLI and engine) uses to read the resolved list — for an ordinary `right: stripe` pair it returns `["stripe"]`, so single-counterpart configs are completely unaffected by this feature.

**Passes run in the order listed, and order is a deliberate configuration choice.** Each counterpart is matched against the still-unmatched left transactions left over from the previous pass: `bank` rows that match in `stripe` are consumed there; only the remainder is checked against `paypal`. If two counterparts could both match the same left row, the earlier one in `rights` always wins. Left-side duplicate annotation is computed once on the original left set (not once per pass), so a row that survives unmatched across multiple passes is never double-counted in `Duplicates`.

Engine entry points mirror the single-counterpart ones:
- `ReconcileMultiSource` (non-streaming) is the `rights` equivalent of `Reconcile`.
- `ReconcileStreamingMultiSource` (streaming) is the `rights` equivalent of `ReconcileStreaming`. Its first pass streams the left file from disk exactly like `ReconcileStreaming` does; unmatched-left rows are buffered in memory and replayed against each subsequent counterpart's freshly built index. It currently does not support `name_mode: tokens`.

The CLI (`internal/cli/reconcile.go`) calls the existing single-counterpart path unchanged when `len(pair.Counterparts()) == 1`, and only switches to the multi-source path for `rights` pairs with more than one counterpart — `--audit` and `--progress` are not yet supported in that path, and `--right-file` doesn't apply since each counterpart resolves its own file via its source's `file_pattern`.

Output gets one additive field, `BySource`, giving a per-counterpart breakdown alongside the aggregate `Summary` (see "Result shape" above). Writers that support a breakdown (`json`, `json-stream`, `ndjson`) implement an optional `SourceBreakdownWriter` interface; `csv` and `table` silently omit it, same pattern as the existing optional `RunInfoSetter` interface for `--audit`.

This `rights` behavior is ordered multi-counterpart reconciliation across sources; it is separate from grouped transaction matching within a single source (see `one_to_many` and `many_to_many` below).

## Explicit pass pipelines and grouped passes

By default the engine runs reference matching then optionally name-token matching (via `name_mode: tokens`). For more control, a pair can declare an explicit ordered `passes` list instead:

```yaml
pairs:
  invoices:
    left: ledger
    right: bank
    date_window: 3d
    amount_tolerance_minor: 0
    passes:
      - type: one_to_many
```

Valid pass types:
- `reference_one_to_one` — best-candidate reference matching (same as Tier 1 above).
- `name_tokens_one_to_one` — Jaccard name-token matching (same as Tier 2 above). Cannot be combined with `name_mode: tokens`.
- `one_to_many` — one left transaction settled by N right transactions sharing the same reference (installment payments). See below.
- `many_to_many` — M left transactions reconciled against N right transactions sharing the same grouping key (settlement or payout groups). See below.

**`[reference_one_to_one, one_to_many]` pipeline caveat.** The `reference_one_to_one` pass greedily picks the best 1-to-1 candidate for any left row whose reference appears on the right. For same-date installments this means the pass classifies them as `amount_diff` (each installment's amount differs from the invoice total), consuming the left row before `one_to_many` can see it. Use `[reference_one_to_one, one_to_many]` only when some rows are genuine 1-to-1 matches *and* the installments fail **both** amount and date in the ref pass for each individual right row — otherwise use `one_to_many` alone.

When `passes` is set, `name_mode: tokens` is rejected by config validation — add a `name_tokens_one_to_one` pass explicitly instead.

### `one_to_many` pass

The `one_to_many` pass handles the case where a single left transaction (e.g. an invoice) is settled by several right transactions sharing the same reference (e.g. installment payments). Instead of 1-to-1 candidate selection, it sums all right rows for a reference and compares the total to the left amount.

**`group_by` key.** By default the pass groups right rows by the `reference` field. Use `group_by` to change the grouping field:

```yaml
passes:
  - type: one_to_many
    group_by: reference   # default — omit to get the same behavior
```

Built-in values: `reference`, `name`, `group_key`. Omitting `group_by` is equivalent to `group_by: reference`. Unknown values are rejected by config validation.

**Outcomes:**
- **`grouped_matched`** — sum of right amounts within tolerance, all right dates within window.
- **`grouped_amount_diff`** — sum falls outside tolerance (but dates OK). `DiffMinor = left.Amount - sum(rights)`.
- **`grouped_timing_diff`** — amounts reconcile within tolerance, but at least one right date is outside the window. `DaysDiff` is the max `abs(daysBetween)` across all rights in the group.
- **Both fail** — left stays unmatched, rights are NOT consumed (available for later passes or carry-forward).
- **`ambiguous_groups`** — when more than one left row shares the same reference, grouping is undetermined. All involved rows are emitted as an `AmbiguousGroupPair` and excluded from matching entirely. Resolving ambiguous groups requires manual reconciliation.

**`group_col` / `ref_col` separation for installments.** The duplicate detector uses `group_col` (falling back to `ref_col` when unset). When right-side rows are intentional installments, each has a unique per-row ID but they share the matching reference (`ref_col`). If you leave `group_col` unset, the duplicate detector will flag all installments as a duplicate group. Set `group_col` to a per-row-unique column (e.g. `payment_id`) to avoid this false-positive:

```yaml
sources:
  bank_installments:
    file_pattern: ./data/installments.csv
    parser:
      type: csv
      date_col: date
      date_layout: "2006-01-02"
      amount_col: amount
      multiplier: 100
      ref_col: invoice_number   # shared by all installments for one invoice
      group_col: payment_id     # unique per row — prevents false-positive duplicates
```

**Extended monetary invariant.** Ambiguous amounts count toward `TotalDiscrepancy` separately from ordinary unmatched amounts:

```
TotalDiscrepancy = UnmatchedAmountLeft + UnmatchedAmountRight + AmountDiffTotal
                 + AmbiguousAmountLeft + AmbiguousAmountRight
```

**`reconciled_rate_pct` denominator caveat.** The `one_to_many` pass inflates `total_right` because N right rows correspond to one left row. A fully reconciled grouped dataset can report a sub-100% `reconciled_rate_pct` — this is expected. `match_rate_pct` counts only exact 1-to-1 matches (unchanged); `reconciled_rate_pct` includes grouped matched, grouped amount diff, and grouped timing diff outcomes.

**Batch-only.** The `one_to_many` pass requires all right rows for a reference to be in memory simultaneously (to sum amounts and detect ambiguous left references). This fundamentally breaks the streaming memory contract. The CLI detects `one_to_many` in a pair's passes and automatically routes to the in-memory batch path (`engine.Reconcile` / `engine.ReconcileMultiSource`). `--audit` is not yet supported with `one_to_many` passes; `--progress` is silently skipped. Writers that do not support grouped events (`csv`, `table`) receive a warning on stderr; use `--format=json` or `--format=ndjson` to capture the full output including `grouped_matched`, `grouped_amount_diff`, `grouped_timing_diff`, and `ambiguous_groups`.

### `many_to_many` pass

The `many_to_many` pass handles grouped settlements where both systems split the same business event differently. For example, a store ledger may contain separate rows for order sales, refunds, and gateway fees, while a Stripe payout export contains separate payout rows for card payments, refunds, and fees. If all rows carry the same payout reference, the pass reconciles the two groups by comparing totals:

| Store ledger rows | Reference | Amount |
|---|---|---:|
| Order A sale | `payout_123` | 10000 |
| Order B sale | `payout_123` | 8000 |
| Order C refund | `payout_123` | -3000 |
| Stripe fee | `payout_123` | -500 |

| Stripe payout rows | Reference | Amount |
|---|---|---:|
| Card payments total | `payout_123` | 18000 |
| Refunds total | `payout_123` | -3000 |
| Stripe fees | `payout_123` | -500 |

Both sides sum to `14500`, so the output contains one explainable `many_to_many_matched` event with `lefts` and `rights` arrays.

Use it for PSP payout reconciliation, partial payments, marketplace settlements, and fees/refunds/adjustments that are split differently across systems. Good grouping keys include payout IDs, invoice IDs, settlement IDs, payment run IDs, and remittance references.

Configuration mirrors `one_to_many`:

```yaml
passes:
  - type: many_to_many
    group_by: reference   # default; also supports name or group_key
```

**Outcomes:**
- **`many_to_many_matched`** — summed left and right amounts are within tolerance, and group dates are within window.
- **`many_to_many_amount_diff`** — dates are OK, but summed amounts differ beyond tolerance. `DiffMinor = sum(lefts) - sum(rights)`.
- **`many_to_many_timing_diff`** — amounts reconcile within tolerance, but at least one cross-side row date is outside the window. `DaysDiff` is the max `abs(daysBetween)` across the group.
- **Both fail** — all rows stay unmatched and available for later passes.

**What it does not do.** The pass does not search arbitrary combinations of rows that happen to sum to the same amount, and it does not use fuzzy matching. Rows must share the configured group key. With `rights: [stripe, paypal]`, the pass runs against each counterpart in order and carries forward unmatched left rows; it does not reconcile one group across multiple right sources at once.

**Pass-order caveat.** If `reference_one_to_one` runs before `many_to_many` and the grouped rows share the same reference, the reference pass can consume individual rows as matches, amount diffs, or timing diffs before the group pass sees them. Use `many_to_many` alone for settlement groups where the reference is the group key, or use a different `group_by` key such as `name` or `group_key` when the reference pass should run first.

**Performance.** The pass is linear in the number of rows for grouping and summing, but it must hold both side groups in memory. It is batch-only by design. The CLI routes pairs containing `one_to_many` or `many_to_many` to the batch path; `--audit` is not yet supported with grouped passes. Writers that do not support grouped detail (`csv`, `table`) receive a warning on stderr; use `--format=json`, `--format=json-stream`, or `--format=ndjson` to capture N:M event detail.
