# Reconify Benchmarks

Reconify benchmarks are synthetic and publishable. They are designed to measure
correctness and performance against known reconciliation truth, not to claim that
every real-world row should match.

## Benchmark families

- `deterministic`: controlled 1-N reconciliation scenarios with exact expected
  counts. These are useful for regression checks and comparing single-source vs
  multi-source behavior.
- `realistic`: source-informed synthetic scenarios shaped by bank and PSP export
  behavior: payout grouping, fees, refunds, disputes, noisy descriptors, split
  files, multi-currency settlement fields, delayed settlement, and duplicate
  replay windows.

## Output directories

Benchmark scripts resolve generated output in this order:

1. `--output-dir <dir>`
2. `BENCH_OUTPUT_DIR`
3. `$RUNNER_TEMP/reconify-benchmarks` in GitHub Actions
4. `benchmarks/.out` locally

Generated CSV, config, NDJSON, and log files are not committed.

## Correctness policy

Correctness is a 100% match against each generated `expected.json` manifest.
That does not mean 100% of rows should reconcile as matches. Realistic data has
legitimate unmatched rows, amount differences, timing differences, refunds,
chargebacks, fees, and ambiguous rows. A benchmark passes when Reconify reports
those known cases exactly.

## Speed policy

Pull request CI runs smoke benchmarks and fails on correctness errors or command
timeouts. Runtime and RSS are reported but are not hard thresholds on shared CI
runners. Nightly and manual workflows run larger benchmarks and upload summary
artifacts for trend inspection.

## Commands

```bash
make bench-smoke
make bench-deterministic
make bench-realistic
make bench-full
```

Use `BENCH_OUTPUT_DIR=/path/to/fast-disk` when running large local benchmarks.
