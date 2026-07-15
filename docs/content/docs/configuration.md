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
| `result_mode` | no | Controls which events are emitted. One of `all` (default), `exceptions_only`, or `summary_only`. See [Result emission modes](#result-emission-modes). |

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

Each `rights` entry must be non-empty and unique. Duplicate counterpart names are rejected during configuration validation rather than being processed twice.

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
- `--audit` is not yet supported for multi-counterpart runs. `--progress` and
  `--progress-out` emit lifecycle telemetry for each counterpart pass.
- `name_mode: tokens` is not yet supported in streaming multi-counterpart mode. CLI reconciliation uses the streaming path for multi-counterpart pairs, so omit `name_mode: tokens` for `rights` pairs in CLI runs.

### `one_to_many` pass (installment payments)

The `one_to_many` pass handles the case where one left transaction (e.g. an invoice) is settled by N right transactions sharing the same reference (e.g. installment payments). See the [engine documentation](/docs/engine/) for full semantics and configuration.

This pass is separate from the ordered `rights` multi-counterpart reconciliation. `rights` selects which counterpart *sources* to reconcile against in sequence; `one_to_many` is a matching *strategy* within a single counterpart.

### `many_to_many` pass (settlement groups)

The `many_to_many` pass handles grouped settlements where both sides split the same business event differently. For example, a store ledger might have separate rows for order sales, refunds, and gateway fees, while a Stripe payout export has separate payout rows for payments, refunds, and fees. If both sides share a payout reference such as `payout_123`, Reconify groups both sides by that key and compares the totals.

Use this for PSP payouts, partial payments, marketplace settlements, and fee/refund/adjustment rows that reconcile only at the settlement group level. It does not search arbitrary row combinations and does not use fuzzy matching; rows must share the configured group key.

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
| `one_to_many` | Matches one left row against N right rows sharing the same grouping key by summing their amounts. Accepts an optional `group_by` field (`reference` \| `name` \| `group_key`; defaults to `reference`). See "one_to_many pass" above. |
| `many_to_many` | Matches M left rows against N right rows sharing the same grouping key by summing both sides. Accepts an optional `group_by` field (`reference` \| `name` \| `group_key`; defaults to `reference`). See "many_to_many pass" above. |

**`passes` vs `rights`**: `rights` selects which counterpart *sources* to reconcile against in order. `passes` defines the matching *strategy* used within each counterpart. They are orthogonal — you can combine them.

With `rights: [stripe, paypal]`, `many_to_many` runs against `stripe` first, carries only unmatched left rows forward, then runs against `paypal`. It does not match one group across multiple right sources at once.

Omitting `passes` preserves the legacy behavior exactly. When `passes` is set, `name_mode: tokens` is rejected; add a `name_tokens_one_to_one` pass instead.

## Index Backend

Reconify supports multiple right-side index backends:

| Backend | Use when |
|---|---|
| `memory` | You want the fastest lookups and the right-side file fits comfortably in RAM. |
| `disk` | You need lower RAM usage and can accept slower lookups. |
| `auto` | You want threshold-compatible selection without budgets, or resource-aware memory/disk/partitioned fallback with budgets. |
| `partitioned` | You need bounded memory for a large single-counterpart CSV reconciliation, including grouped passes, and can accept extra sequential disk passes. |

```yaml
index:
  backend: auto
  spill_dir: "/tmp/reconify"
  auto_max_right_file_mb: 2048
  max_memory_mb: 8192
  max_temp_disk_mb: 16384
```

`max_memory_mb` and `max_temp_disk_mb` are optional safety budgets. A value of
zero leaves that configured budget uncapped, while the selector still checks
actual free temporary-disk space. With no budgets, `auto` keeps its existing
file-size threshold behavior. With either budget configured, `auto` prefers
memory, then disk, then partitioned indexing and records why candidates were
accepted or rejected. These limits protect resources; they are not throughput
guarantees.

### Partitioned backend

`partitioned` hashes the configured matching column in both CSV inputs and
writes rows into temporary partition files. Reconify then externally sorts and
merge-reads one partition at a time. Grouped passes materialize only the
current left/right key groups; reference passes use the normal per-partition
streaming index. This bounds grouped working memory by the largest active
group plus fixed sort/merge buffers rather than by the complete input.

```yaml
index:
  backend: partitioned
  partition_count: 32   # optional; 0 selects adaptively, explicit values must be >= 2
```

Use a streaming output format for large runs:

```bash
reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_provider \
  --format ndjson \
  --out results.ndjson
```

The partitioned backend supports single-counterpart CSV pairs with a consistent
reference, name, or group-key selector across all passes. `one_to_many` and
`many_to_many` passes run as bounded grouped streams one key at a time. External
sort runs use the configured `spill_dir` and are removed when the run finishes.
Duplicate groups must use the same effective key as partition routing; otherwise
the CLI rejects partitioning and recommends `memory` or `disk`. Multi-source
(`rights`) pairs should use `memory` or `disk` until a partition coordinator is
available. Key normalization should happen before partitioning.

## Result emission modes

`result_mode` controls which reconciliation events the writer emits. It can be set per pair in the config or overridden at runtime with `--result-mode`.

| Value | Emits |
|---|---|
| `all` | Every event: matches, diffs, unmatched, duplicates. **Default.** |
| `exceptions_only` | Unmatched, amount/timing diffs, duplicates, ambiguous groups, and grouped/N:M exception events. Clean matches are suppressed. |
| `summary_only` | Only the final summary. All item events are suppressed. |

The CLI flag `--result-mode` overrides the pair's `result_mode` when explicitly provided. Omitting the flag preserves the pair-level value (or the default `all`).

```yaml
pairs:
  bank_vs_stripe:
    left: bank
    right: stripe
    result_mode: exceptions_only  # suppress clean matches in output
```

```bash
# Override at runtime — flag wins over pair config.
reconify reconcile --pair bank_vs_stripe --result-mode summary_only
```

**Filtering is applied at the writer boundary.** Classification counts, monetary totals, and the `currency` field in the summary are always computed from the full reconciliation — they are not affected by which events are suppressed. The `result_mode` value is embedded in the `summary` output so consumers can identify which mode was in effect.

**Incomplete output semantics.** A writer error or flush failure prevents the output file from being committed and leaves any existing output file unchanged. A completed summary is never written after a writer or flush failure.

**Telemetry output** (`--progress-out`) is independent of `result_mode`. Lifecycle events are always emitted to the telemetry stream regardless of the result emission mode.

**Recommended patterns for high-volume jobs:**
- Use `exceptions_only` when downstream consumers only act on discrepancies.
- Use `summary_only` for dashboard-only integrations that only need aggregate counts and totals.
- Use `all` (default) for audit trails and when storing results for later replay.
