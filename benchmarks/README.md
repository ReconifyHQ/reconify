# Reconify Benchmarks

Reconify benchmarks are synthetic and publishable. They are designed to measure
correctness and performance against known reconciliation truth, not to claim that
every real-world row should match.

## Benchmark families

- `deterministic`: controlled 1-N reconciliation scenarios with exact expected
  counts. Useful for regression checks and comparing single-source vs
  multi-source behavior.
- `realistic`: source-informed synthetic scenarios shaped by bank and PSP export
  behavior: payout grouping, fees, refunds, disputes, noisy descriptors, split
  files, multi-currency settlement fields, delayed settlement, and duplicate
  replay windows.
- `adversarial`: deterministic fixtures that stress classification invariants and
  engine internals across six scenario types:
  - `uniform_unique_refs` — baseline reference one-to-one throughput (85% matched)
  - `hot_skewed_refs` — deep index buckets; each left ref maps to 5 right rows,
    only one of which matches (tests `decideMatch` bucket traversal)
  - `high_duplicate_pressure` — 20% of left rows in duplicate groups of 2; no
    right counterparts for dup rows (tests the duplicate-detection re-scan pass)
  - `high_result_emission` — 99% match rate; maximum `ResultWriter` call volume
  - `one_to_many_settlement` — one left row settled by 3 right rows sharing the
    same reference (`one_to_many` pass)
  - `many_to_many_settlement` — groups of 3 left matched to groups of 3 right
    sharing the same reference (`many_to_many` pass)

## Output directories

Benchmark scripts resolve generated output in this order:

1. `--output-dir <dir>`
2. `BENCH_OUTPUT_DIR`
3. `$RUNNER_TEMP/reconify-benchmarks` in GitHub Actions
4. `benchmarks/.out` locally

Generated CSV, config, NDJSON, and log files are not committed.

## Correctness and parity policy

Correctness is a 100% match against each generated `manifest.json`. That does
not mean 100% of rows reconcile as matches — realistic and adversarial fixtures
have known unmatched rows, diffs, duplicates, and grouped events. A run passes
when Reconify reports exactly those known cases.

### Row accounting

| Event type | Accounting rule |
|---|---|
| `matched`, `amount_diff`, `timing_diff` | Consume exactly one left and one right row. |
| `unmatched_left`, `unmatched_right` | Terminally classify exactly the emitted-side row. |
| Grouped one-to-many outcomes | Consume the left row and every listed right row. |
| Many-to-many outcomes | Consume every listed left and right row. |
| `ambiguous_group` | All listed rows reserved for manual review. |
| `duplicate` | Annotation only; never changes row classification or consumption. |

### Parity matrix

Reference one-to-one scenarios run full parity across all five output formats
(json, json-stream, ndjson, csv, table) and all streaming backends
(memory, disk, partitioned). Partitioned runs use four partitions to
exercise boundary behavior.

Grouped scenarios (`one_to_many_settlement`, `many_to_many_settlement`) skip
CSV and table formats (grouped match events are not representable in those
formats) and skip the `disk` and `partitioned` backends (they support
`reference_one_to_one` only). These skips are recorded in the manifest's
`skipped_matrix` field and reported as `SKIP` (exit 2) by the verifier —
never as parity failures.

### Monetary totals

Each fixture manifest stores expected per-currency monetary totals
(`matched_amount_left`, `matched_amount_right`, `unmatched_amount_left`,
`unmatched_amount_right`). All adversarial scenarios use a single currency
(USD) per run. Reconify rejects mixed-currency monetary totals, so each
currency must be run in isolation if multi-currency testing is added.

## Reproducibility

Fixture inputs and manifests are fully reproducible from the seed and row count.
Timing, RSS, GC metrics, and sampled peak temporary-disk use are best-effort
observations and are not reproducible across runs, hosts, or Go versions.

## Speed and CI policy

**PR/push CI** (`benchmark-smoke` job): runs the full adversarial semantic smoke
matrix (`--smoke`, 500 rows) plus the existing deterministic and realistic smoke
runs. Fails on any parity error or smoke timeout. Scale measurements are never
a PR requirement.

**Scheduled/manual CI** (`benchmark-scale` job): runs 100k-row deterministic,
realistic, and adversarial benchmarks. Parity failures are CI failures.
Each smoke run has a 30-second per-command timeout and fails on timeout.
Scale runs have a 300-second per-command timeout; timed-out runs are recorded
as warnings and do not fail the scale job.

Cold-cache measurements (`make bench-adversarial-cold`) are local/manual only.
Warm mode pre-reads inputs into the OS page cache. Cold mode attempts privileged
cache eviction and reports whether it succeeded in `report.json`. Cold
measurements are never a portable CI requirement.

## Commands

```bash
# PR equivalents (run as part of make check)
make bench-smoke

# Adversarial targets
make bench-adversarial-smoke   # 500-row semantic smoke matrix
make bench-adversarial          # 100k-row scale benchmark (warm cache)
make bench-adversarial-cold     # 100k-row cold-cache measurement (local/manual)

# Legacy scale targets
make bench-deterministic        # 100k deterministic
make bench-realistic            # 100k realistic
make bench-full                 # 1M deterministic + 1M realistic + 100k adversarial
```

Use `BENCH_OUTPUT_DIR=/path/to/fast-disk` when running large local benchmarks.

## Artifacts

Uploaded on every run: `summary.tsv`, `*.log`, `manifest.json`, `report.json`.
Generated CSV output streams and full reconciliation output files are NOT
uploaded (can be very large). All commands, cache limitations, host metadata,
and the non-capacity-claim policy are documented in this README.

## Host metadata in report.json

Each adversarial run emits a `report.json` containing schema and engine
metadata, host architecture/CPU/memory, scenario, format, backend, cache state,
input rows/sec, elapsed wall time, peak RSS (MB), sampled peak temporary-disk
use, GC count, output bytes, parity status, and timeout warnings. External CLI
processes do not expose allocation counters, so `alloc_bytes` is reported as
`null`. These are observations, not performance commitments. Shared CI runners
have variable timing; treat elapsed and RSS as trend indicators, not thresholds.

Checkpoint/resume coverage is intentionally absent because Reconify does not
currently expose checkpoint/resume execution.
