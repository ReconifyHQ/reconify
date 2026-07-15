# Reconify

[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> A developer-first, open-source reconciliation engine for finance, ops, and accounting teams.

Reconify ingests financial data from multiple sources (banks, PSPs, ledgers, spreadsheets), normalizes them, and compares transactions to detect:

- Matched entries
- Missing entries
- Timing differences
- Amount discrepancies
- Duplicates

## Installation

```bash
go install github.com/reconifyhq/reconify/cmd/reconify@latest
```

Or download a pre-built binary from [Releases](https://github.com/ReconifyHQ/reconify/releases).

Or build from source:

```bash
git clone https://github.com/ReconifyHQ/reconify.git
cd reconify
make build
```

## Quick Start

1. Create a configuration file `reconify.yaml`:

```bash
reconify config init
```

Or write one manually:

```yaml
version: 1
timezone: "UTC"

index:
  backend: auto                  # memory | disk | auto | partitioned
  spill_dir: "/tmp/reconify"     # optional, used by disk/auto/partitioned
  auto_max_right_file_mb: 2048   # optional legacy auto threshold
  max_memory_mb: 8192            # optional resource safeguard; 0 is uncapped
  max_temp_disk_mb: 16384        # optional resource safeguard; 0 is uncapped
  partition_count: 32            # partitioned backend; 0 uses the default

sources:
  bank:
    file_pattern: "data/bank/*.csv"
    parser:
      type: csv
      date_col: "Date"
      date_layout: "2006-01-02"
      tz: "UTC"
      amount_col: "Amount"
      decimal: "."
      thousands: ","
      multiplier: 100
      currency_col: "Currency"
      name_col: "Details"
      ref_col: "Reference"

  stripe:
    file_pattern: "data/stripe/*.csv"
    parser:
      type: csv
      date_col: "Date"
      date_layout: "2006-01-02"
      tz: "UTC"
      amount_col: "Amount"
      decimal: "."
      thousands: ","
      multiplier: 100
      currency_col: "Currency"
      name_col: "Description"
      ref_col: "Reference"

pairs:
  bank_vs_stripe:
    left: bank
    right: stripe
    date_window: "1d"
    amount_tolerance_minor: 0
    name_mode: "tokens"
```

2. Validate your configuration:

```bash
reconify config validate --config reconify.yaml
```

3. Run reconciliation:

```bash
reconify reconcile --config reconify.yaml --pair bank_vs_stripe --out results.json
```

4. Parse a single file (for debugging):

```bash
reconify parse --config reconify.yaml --source bank --file data/bank/jan.csv
```

### Input Formats

Sources can read CSV, JSON, NDJSON, and modern Excel workbooks:

```yaml
parser:
  type: auto                  # csv | json | xlsx | auto
  sheet: "Transactions"       # optional, xlsx only; first sheet is used by default
  date_col: "Date"
  date_layout: "2006-01-02"
  amount_col: "Amount"
  multiplier: 100
```

When `type` is omitted or set to `auto`, Reconify infers the parser from `.csv`,
`.json`, `.ndjson`, `.xlsx`, or `.xlsm`. Legacy `.xls` files are not supported;
save them as `.xlsx` or `.csv` first.

## Index Backend Configuration

Reconify supports multiple right-side index backends for `reconcile`:

- `memory` (default): fastest, highest RAM usage
- `disk`: lower RAM usage, slower lookups, uses SQLite temp files
- `auto`: preserves the file-size threshold without resource budgets, and uses
  memory, disk, then partitioned fallbacks when budgets are configured
- `partitioned`: bounded-memory CSV reconciliation for one counterpart or eligible ordered `rights` pairs, including grouped passes, with extra disk passes

Use this for large files where in-memory indexing causes GC pressure or OOM risk.

`max_memory_mb` and `max_temp_disk_mb` are safety budgets, not throughput
guarantees. Auto selection reports the chosen backend, reason, estimates, and
rejected fallback reasons in JSON-style output and on stderr for CSV/table.
Runs fail before completion when the selected budget or available temporary
storage cannot satisfy the estimate.

For a multi-counterpart pair, configure `rights: [stripe, paypal]`. The
partitioned backend hashes the common matching/grouping key once on the left
and once per counterpart, then carries unmatched left rows forward on disk.
Counterparts remain ordered, so an earlier source always consumes an eligible
left row first. This mode requires CSV inputs, a non-token matching pipeline,
compatible partition selectors, and duplicate groups that are safe to route
with that selector. Temporary partitions and carry-forward manifests are
removed on both successful and failed runs. Use `--format ndjson` or `--format
csv` for bounded output memory.

## Result emission modes

Use `--result-mode` to control which reconciliation events appear in the output:

```bash
# Suppress clean matches — only exceptions go to the output file.
reconify reconcile --config reconify.yaml --pair bank_vs_stripe \
  --format ndjson --out exceptions.ndjson \
  --result-mode exceptions_only

# Summary-only — useful for dashboard integrations.
reconify reconcile --config reconify.yaml --pair bank_vs_stripe \
  --format json --out summary.json \
  --result-mode summary_only
```

| Mode | Emits |
|---|---|
| `all` | Every event. **Default.** |
| `exceptions_only` | Unmatched, diffs, duplicates, ambiguous groups. Clean matches are suppressed. |
| `summary_only` | Only the final summary. All item events are suppressed. |

The mode can also be set per pair in the config with `result_mode: exceptions_only`. The CLI flag overrides the pair config when explicitly provided. Classification counts and monetary totals in the summary always reflect the full reconciliation — filtering is at the output boundary only.

## Progress telemetry

Use `--progress` for concise human-readable progress on stderr. For a separate
machine-readable event stream, add `--progress-out`; it writes live NDJSON and
never changes reconciliation data on stdout or `--out`.

```bash
reconify reconcile --config reconify.yaml --pair bank_vs_stripe \
  --format ndjson --out results.ndjson \
  --progress --progress-every 1000000 \
  --progress-out progress.ndjson --heartbeat-every 30s
```

Each telemetry line has a `type` (`progress` or `heartbeat`), `run_id`, RFC3339
timestamp, stage/status, row counts, rate/elapsed time, and best-effort process
metrics. Totals, percentage, and ETA are omitted when determining them would
require another full input scan. `--progress-out` must not be stdout or the same
path as `--out`; a telemetry write failure is reported once on stderr and does
not stop reconciliation.

## How It Works

The reconciliation engine:

1. **Parses** input files according to your source configuration (column mapping, date formats, amount normalization)
2. **Detects duplicates** within each source by reference field
3. **Matches transactions** across sources by reference, then validates amount tolerance and date window
4. **Optionally matches by name** using token-based Jaccard similarity
5. **Optionally reconciles grouped settlements** with explicit `one_to_many` or `many_to_many` passes
6. **Produces a JSON report** with matched pairs, grouped matches, unmatched entries, and summary statistics

See the [engine internals guide](docs/content/docs/engine/index.md) for algorithm details. See the [Configuration reference](docs/content/docs/configuration.md) for the full set of source parser and pair options.

## Using as a Go Library

The `engine` and `config` packages are public and can be imported by other Go modules:

```go
import (
    "github.com/reconifyhq/reconify/config"
    "github.com/reconifyhq/reconify/engine"
)

// Load config
cfg, _ := config.Load("reconify.yaml")

// Parse configured input files
left, _ := engine.Parse("bank", "bank.csv", cfg.Sources["bank"].Parser)
right, _ := engine.Parse("stripe", "stripe.xlsx", cfg.Sources["stripe"].Parser)

// Reconcile
result, _ := engine.Reconcile("bank_vs_stripe", "bank", "stripe", left, right, cfg.Pairs["bank_vs_stripe"])
```

## Enterprise / Private Deployment

Reconify is an open-source CLI and library. For private deployments with multi-user access, audit pipelines, or custom integrations, contact us at [reconifyhq.com](https://reconifyhq.com) to discuss an enterprise engagement or schedule a demo.

## Development

### Prerequisites

- Go 1.25.0+

### Agent Experience

Reconify includes agent-facing project context in [AGENTS.md](AGENTS.md), tool-specific adapters for Claude, Codex, Gemini, and Copilot, and reusable workflows under [.agents/skills/](.agents/skills/). Use [llms.txt](llms.txt) as the compact index for AI tools.

### Build

```bash
make build       # Build CLI binary
make build-all   # Cross-compile for all platforms
make install     # Install to $GOPATH/bin
```

### Test

```bash
make preflight   # Run the full pre-PR quality and security gate
make check       # Run the local equivalent of GitHub Actions checks
make test        # Run all tests with race detection and coverage
make lint        # Run linters
make security    # Run govulncheck and gosec
```

## License

MIT - see [LICENSE](LICENSE).

## Contributing

Contributions welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.
