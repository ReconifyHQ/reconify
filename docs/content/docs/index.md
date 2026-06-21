---
title: Introduction
description: Documentation for Reconify, an open-source reconciliation engine for financial data.
icon: BookOpen
---

# Reconify Documentation

Reconify is an open-source reconciliation engine for financial data. It parses CSV files from multiple sources, normalizes transactions, and matches them using configurable rules.

## What You Can Build

| Area | What it covers |
|---|---|
| [Getting Started](./getting-started) | Install the CLI, validate a config, and run your first reconciliation |
| [Configuration](./configuration) | Source parsers, matching pairs, and index backend options |
| [Engine](./engine/) | Reconciliation internals, types, matching algorithm, and CSV parsing |
| [Performance](./performance/) | Streaming design, benchmarks, capacity guidance, and production notes |

## Quick Start

```bash
# Build the CLI
go build -o reconify ./cmd/reconify

# Validate config
./reconify config validate --config reconify.yaml

# Run reconciliation
./reconify reconcile --config reconify.yaml --pair bank_vs_stripe --out results.json
```

See [Getting Started](./getting-started) for full usage instructions.

---

Reconify is open source. View the source, report issues, and contribute on [GitHub](https://github.com/ReconifyHQ/reconify). For private deployments or enterprise engagements, visit [reconifyhq.com](https://reconifyhq.com).
