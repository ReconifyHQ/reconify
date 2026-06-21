---
title: Getting Started
description: Install Reconify, create a config file, and run reconciliation from the CLI.
icon: Rocket
---

# Getting Started

Reconify is a developer-first reconciliation engine for finance, ops, and accounting teams. It ingests financial data from multiple sources, normalizes transactions, and reports matches, missing entries, timing differences, amount discrepancies, and duplicates.

## Install

Install the CLI with Go:

```bash
go install github.com/reconifyhq/reconify/cmd/reconify@latest
```

Or build from source:

```bash
git clone https://github.com/reconifyhq/reconify.git
cd reconify
make build
```

## Create a Config

Create a `reconify.yaml` file:

```yaml
version: 1
timezone: "UTC"

index:
  backend: auto
  spill_dir: "/tmp/reconify"
  auto_max_right_file_mb: 2048

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

  stripe:
    file_pattern: "data/stripe/*.csv"
    parser:
      type: csv
      date_col: "Date"
      date_layout: "2006-01-02"
      amount_col: "Amount"
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

## Run

Validate the configuration:

```bash
reconify config validate --config reconify.yaml
```

Run reconciliation:

```bash
reconify reconcile --config reconify.yaml --pair bank_vs_stripe --out results.json
```

Parse one file while debugging mappings:

```bash
reconify parse --config reconify.yaml --source bank --file data/bank/jan.csv
```

## Next Steps

- [Configuration](./configuration) — full reference for source parsers, pairs, and index backend options.
- [Engine](./engine/) — matching algorithm and result shape.
- [Performance](./performance/) — streaming design, benchmarks, and production guidance.
- [GitHub](https://github.com/ReconifyHQ/reconify) — source, releases, and issue tracker.
