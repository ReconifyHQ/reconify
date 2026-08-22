---
name: reconify-engine-ci
description: Run deterministic Reconify Engine reconciliations in CI and fail safely on configured exceptions.
---

# Reconify Engine CI

Use explicit `--format`, `--out`, and exit-code handling. For reproducible JSON artifacts, use `--deterministic` and a fixed audit timestamp when auditing. `--fail-if-unmatched` returns 3; `--fail-if-exceptions` returns 4 and takes precedence. CI must branch on exit codes, not diagnostic text.

For large inputs, use a streaming output format and an appropriate index backend. Preserve results as CI artifacts even when the job fails.
