# Reconify Documentation

Reconify is an open-source reconciliation engine for financial data. It parses CSV, JSON/NDJSON, and XLSX files from multiple sources, normalizes transactions, and matches them using configurable rules.

## Documentation

| Directory | What it covers |
|---|---|
| [engine/](./engine/) | Reconciliation engine internals — types, matching algorithm, CSV parsing |

## Quick Start

```bash
# Build the CLI
go build -o reconify ./cmd/reconify

# Create config interactively
./reconify config init

# Validate config
./reconify config validate --config reconify.yaml

# Run reconciliation
./reconify reconcile --config reconify.yaml --pair bank_vs_stripe --out results.json
```

See the [main README](../README.md) for full usage instructions.
