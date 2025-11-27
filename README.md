# Reconify

> A developer-first, open-source reconciliation engine for finance, ops, and accounting teams.

Reconify ingests financial data from multiple sources (banks, PSPs, ledgers, spreadsheets), normalizes them, and compares transactions to detect:

- ✅ Matched entries
- ❌ Missing entries
- ⏰ Timing differences
- 💰 Amount discrepancies
- 🔄 Duplicates
- ⚠️ Anomalies

## Features

- **Fast, stream-based parser** (CSV → normalized records)
- **Deterministic reconciliation engine**
- **Clear reports and error handling**
- **CLI-first design** for automation
- **Zero data retention** - privacy-focused
- **Self-hostable** - no cloud dependencies

## Installation

```bash
go install github.com/reconify/reconify@latest
```

Or build from source:

```bash
git clone https://github.com/reconify/reconify.git
cd reconify
make build
```

## Quick Start

1. Create a configuration file `reconify.yaml`:

```yaml
version: 1
timezone: "UTC"

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

## Project Status

🚧 **Under Active Development** - This is a PoC (Proof of Concept) in development.

## Architecture

Reconify consists of:

1. **Core Engine (CLI)**: Fast, deterministic reconciliation binary
2. **Web Dashboard** (Coming Soon): Simple web interface for ad-hoc reconciliations

## Development

```bash
# Run tests
make test

# Build binary
make build

# Run linter
make lint

# Format code
make fmt
```

## License

[To be determined]

## Contributing

Contributions welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

