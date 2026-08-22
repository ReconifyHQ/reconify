---
name: reconify-engine-ci
description: Run Reconify Engine reconciliations in CI — deterministic artifacts, exit-code policy, and failing the build on the differences that matter.
---

# Reconify Engine CI

## Deterministic artifacts

```bash
reconify reconcile --config reconify.yaml --pair PAIR \
  --format json --deterministic --out result.json
```

`--deterministic` sorts output sections so results diff cleanly between runs; it applies to the
`json` format only. Add `--audit` when you need provenance (file hashes, tool version, config
snapshot), and `--audit-fixed-timestamp RFC3339` so repeated runs over identical inputs are
byte-identical — without it, the run timestamp changes every run and every diff is noise.

Do not use `--agent` for a CI artifact: it suppresses clean matches, so the file you archive is not
the full result.

## Failing the build deliberately

| Flag | Exit | Fails on |
|---|---|---|
| _(none)_ | `0` | nothing; unmatched rows are a normal result |
| `--fail-if-unmatched` | `3` | any unmatched row on either side |
| `--fail-if-exceptions` | `4` | any `amount_diff`, `timing_diff`, or unmatched row |

`--fail-if-exceptions` is a superset of `--fail-if-unmatched` and its code `4` takes precedence
when both are set. Exit `2` always means a config error — a red build from `2` is a broken pipeline,
not a broken ledger, and should be triaged differently.

Branch on the exit code, never on stderr text:

```bash
reconify reconcile --config reconify.yaml --pair PAIR --format json --deterministic \
  --out result.json --fail-if-exceptions
case $? in
  0) echo "clean" ;;
  2) echo "config error"; exit 1 ;;
  3|4) echo "differences found"; exit 1 ;;
esac
```

## Practicalities

Upload `result.json` as a build artifact **even when the job fails** — the failing run is the one
whose result someone needs to read. For large inputs use `--format ndjson` and an appropriate index
backend (see `reconify-engine-performance`). Keep the config in version control next to the
workflow so a result can always be reproduced from a commit.
