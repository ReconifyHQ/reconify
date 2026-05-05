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
go install github.com/reconify/reconify/cmd/reconify@latest
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

```yaml
version: 1
timezone: "UTC"

index:
  backend: auto                  # memory | disk | auto
  spill_dir: "/tmp/reconify"     # optional, used by disk/auto
  auto_max_right_file_mb: 2048   # optional, default 2048

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
- `auto`: picks `disk` when right file size is above `index.auto_max_right_file_mb`

Use this for large files where in-memory indexing causes GC pressure or OOM risk.

## How It Works

The reconciliation engine:

1. **Parses** input files according to your source configuration (column mapping, date formats, amount normalization)
2. **Detects duplicates** within each source by reference field
3. **Matches transactions** across sources by reference, then validates amount tolerance and date window
4. **Optionally matches by name** using token-based Jaccard similarity
5. **Produces a JSON report** with matched pairs, unmatched entries, and summary statistics

See [docs/engine/README.md](docs/engine/README.md) for algorithm details.

## Using as a Go Library

The `engine` and `config` packages are public and can be imported by other Go modules:

```go
import (
    "github.com/reconify/reconify/config"
    "github.com/reconify/reconify/engine"
)

// Load config
cfg, _ := config.Load("reconify.yaml")

// Parse configured input files
left, _ := engine.Parse("bank", "bank.csv", cfg.Sources["bank"].Parser)
right, _ := engine.Parse("stripe", "stripe.xlsx", cfg.Sources["stripe"].Parser)

// Reconcile
result, _ := engine.Reconcile("bank_vs_stripe", "bank", "stripe", left, right, cfg.Pairs["bank_vs_stripe"])
```

## Reconify Cloud

For a managed experience with a web dashboard, async processing, multi-user auth, and API access, check out [Reconify Cloud](https://reconify.com).

## Development

### Prerequisites

- Go 1.24+

### Build

```bash
make build       # Build CLI binary
make build-all   # Cross-compile for all platforms
make install     # Install to $GOPATH/bin
```

### Test

```bash
make test        # Run all tests
make lint        # Run linters
```

## License

MIT - see [LICENSE](LICENSE).

## Contributing

Contributions welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.
