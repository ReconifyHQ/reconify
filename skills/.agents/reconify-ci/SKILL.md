---
name: reconify-ci
description: Run Reconify in CI/CD pipelines. Use when an agent needs to automate reconciliation in GitHub Actions, detect drift between runs, fail a job on unmatched rows, or produce byte-identical audit output.
---

# Reconify CI

## Overview

Use this skill for CI/CD automation patterns: scheduled reconciliation jobs, drift detection via deterministic output, and alerting on unmatched rows. Key flags: `--format ndjson`, `--deterministic`, `--audit`, `--fail-if-unmatched`.

## Exit Codes

| Code | Meaning |
|---|---|
| `0` | Success — reconciliation completed |
| `1` | Unexpected or internal error |
| `2` | Config or validation error (bad YAML, missing pair/source) |
| `3` | Reconcile completed with unmatched rows (only when `--fail-if-unmatched` is set) |

Scripts and CI jobs should branch on these codes, not parse stderr text.

## Basic GitHub Actions Job

```yaml
name: Reconcile

on:
  schedule:
    - cron: "0 6 * * *"   # daily at 06:00 UTC
  workflow_dispatch:

jobs:
  reconcile:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Download Reconify
        run: |
          curl -sSfL https://github.com/reconifyhq/reconify/releases/latest/download/reconify-linux-amd64 \
            -o /usr/local/bin/reconify
          chmod +x /usr/local/bin/reconify

      - name: Run reconciliation
        run: |
          reconify reconcile \
            --config reconify.yaml \
            --pair bank_vs_stripe \
            --format ndjson \
            --fail-if-unmatched \
            --out results.ndjson
        # Exit code 3 → unmatched rows → job fails automatically

      - name: Upload results
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: reconciliation-results
          path: results.ndjson
```

## Drift Detection with Deterministic Output

Use `--deterministic` + `--audit-fixed-timestamp` to produce byte-identical output across runs for the same input. Diff two runs to detect new discrepancies:

```bash
# Baseline run (store in CI artifact or commit)
reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --format json \
  --deterministic \
  --audit \
  --audit-fixed-timestamp "2025-01-01T00:00:00Z" \
  --out baseline.json

# Current run with same frozen timestamp
reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --format json \
  --deterministic \
  --audit \
  --audit-fixed-timestamp "2025-01-01T00:00:00Z" \
  --out current.json

# Diff — any output means new discrepancies
diff baseline.json current.json
```

Notes:
- `--deterministic` only takes effect with `--format json`
- `--audit-fixed-timestamp` must be RFC3339 or RFC3339Nano (e.g. `2025-01-01T00:00:00Z`)
- Without `--audit-fixed-timestamp`, `run_id` and `timestamp` vary per run, making diffs noisy

## Alerting on Unmatched Rows

Extract unmatched counts from NDJSON and fail the job if any exist:

```bash
reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --format ndjson \
  --out results.ndjson

# Check unmatched counts from the summary event
UNMATCHED=$(jq '[select(.type=="summary")] | .[0] | .unmatched_left + .unmatched_right' results.ndjson)
if [ "$UNMATCHED" -gt 0 ]; then
  echo "ERROR: $UNMATCHED unmatched transactions"
  jq 'select(.type=="unmatched")' results.ndjson  # show details
  exit 1
fi
```

Or use `--fail-if-unmatched` to let the CLI do the check (exit code 3 = unmatched rows):

```bash
reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --format ndjson \
  --fail-if-unmatched \
  --out results.ndjson
# $? == 3 if unmatched rows exist, 0 if fully matched
```

## Structured Error Parsing

In CI, use `--error-format json` to get machine-parseable errors on stderr:

```bash
reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --error-format json \
  --out results.ndjson 2>errors.json || {
    CODE=$?
    ERROR=$(jq -r '.error' errors.json)
    echo "Reconcile failed (exit $CODE): $ERROR"
    exit $CODE
  }
```

## Large File Recommendations

For files >500k rows, use streaming formats and disk-backed indexing:

```bash
reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --format ndjson \
  --progress \
  --out results.ndjson
```

Set `index.backend: auto` (or `disk`) in `reconify.yaml` when the right-side file is large:

```yaml
index:
  backend: auto
  auto_max_right_file_mb: 2048  # default; switch to disk above this threshold
```

## Critical Files

- `internal/cli/reconcile.go` — `--fail-if-unmatched`, `--deterministic`, `--audit`, `--progress` flag implementation
- `engine/format.go` — Format streaming behavior and memory characteristics
- `engine/transaction.go` — Summary struct fields (`unmatched_left`, `unmatched_right`, `match_rate_pct`)
