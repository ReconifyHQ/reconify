---
name: reconify-performance
description: Benchmark, tune, or explain Reconify performance. Use when an agent works on large-file reconciliation, parser allocation behavior, index backends, streaming output formats, benchmark scripts, memory usage, or docs under docs/performance.
---

# Reconify Performance

## Overview

Use this skill for performance-sensitive work in the parser, reconciler, output writers, and index backends. Keep quick correctness tests separate from heavy benchmarks.

## Performance Map

- Parser streaming and allocation behavior: `engine/parser.go`
- Reconciliation streaming and progress: `engine/reconciler.go`
- Output writer memory behavior: `engine/format.go`
- Right-side indexes: `engine/index.go`, `engine/index_disk.go`
- Benchmark data and runners: `scripts/gen_bench_data.go`, `scripts/bench_full.sh`, `scripts/bench_rss.sh`
- Performance docs: `docs/performance/README.md`

## Quick Verification

Use these before running heavier benchmarks:

```bash
go test ./engine ./config ./internal/cli
go test -run Test -bench=BenchmarkParse -benchmem ./engine
go test -run Test -bench=BenchmarkReconcile -benchmem ./engine
```

`make test` runs race detection and coverage across the repo. It is a correctness gate, not a benchmark.

## Benchmark Workflow

1. Establish a baseline before changing hot paths.
2. Record the command, dataset size, output format, index backend, and Go version.
3. Prefer streaming formats for large jobs: `ndjson`, `csv`, or `json-stream`.
4. Use `index.backend: auto` or `disk` when the right-side file is large enough to risk memory pressure.
5. Re-run the same benchmark after changes and compare allocations, ns/op, and peak RSS when available.
6. Update `docs/performance/README.md` only with reproducible numbers and the exact command used.

## Large-File CLI Patterns

```bash
go run ./cmd/reconify reconcile \
  --config reconify.yaml \
  --pair bank_vs_stripe \
  --format ndjson \
  --progress \
  --out results.ndjson
```

Use `--deterministic` only with `--format=json` when stable diff output matters more than sort overhead. Use `--audit --audit-fixed-timestamp ...` when comparing byte-identical audit output.

## Guardrails

- Do not replace streaming paths with whole-file reads.
- Do not benchmark `table` or default `json` for large jobs unless the task is specifically about buffered output.
- Do not commit generated benchmark datasets or local result files unless the user asks for fixtures.
