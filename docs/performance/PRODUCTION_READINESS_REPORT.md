# Reconify Production Readiness Report

Date: March 9, 2026

## Executive Summary

We validated the engine on multiple realistic dataset sizes and schemas, including:

- wide finance CSVs (18 columns per side)
- mixed reconciliation outcomes (match, amount diff, timing diff, unmatched)
- malformed and dirty data fixtures
- long-running runs with memory and GC tracing

The current engine is robust for large single-node batch reconciliation. The main scaling pressure is memory and GC when the right-side index grows. The system is not yet designed for an ultra-high-availability SaaS target.

Important correction:

- benchmark output previously reported a misleading `rows/op` value in one large benchmark.
- this has been fixed to report explicit `left_rows/op` and `total_rows/op`.

## What We Learned

### 1) Parser and ingestion behavior

- Parser handles wide schemas with mapped columns correctly.
- Parser catches bad date, empty required date, bad amount, empty required amount, and malformed CSV.
- Dirty-data fixtures are now generated automatically under `error-cases`.

### 2) Throughput on realistic workloads

From the latest 5M-per-side run (`testdata-5m`):

- reconcile wall time (RSS run): 57.76s for 10M total input rows (left + right)
- effective throughput: ~173,130 total rows/sec (both sides), ~86,565 left rows/sec
- peak RSS during reconcile run: ~2.39 GB
- GC cycles: 23

From the same run's benchmark phase:

- `BenchmarkReconcileStreaming_20M_Realistic`: 177.12s with 5M/side fixtures
- `BenchmarkParseCSVEach_20M_Left`: 18.42s with 5M-row left fixture

Interpretation:

- The reconciler remains memory-bound/GC-sensitive at scale.
- Wide rows increase parse and allocation pressure compared to narrow schemas.

### 3) Failure mode observed in practice

When using 20M/side wide files (~3.5 GB each), the realistic reconcile benchmark can exceed practical single-run time windows on a laptop-class environment. A terminated benchmark does not imply correctness failure, but it is an operational risk for unattended pipelines.

## Where the System Might Fail

### Application-level failure points

- Memory exhaustion for very large right-side files (in-memory index).
- Long GC phases reducing effective throughput and causing SLA misses.
- Fail-fast parser behavior can stop whole runs on one bad row.
- No resumable checkpointing in long reconciliations.
- Single-process execution means process/node restart loses progress.

### Operational failure points

- No distributed work queue or retry orchestration yet.
- No multi-region failover design for control plane.
- Limited runtime observability around per-phase timing, queue latency, and backpressure.
- No explicit SLO/error-budget process wired into deployment gates.

### Data and correctness risks

- Mixed-currency runs are rejected (correct), but upstream data quality must enforce this early.
- Large benchmark naming previously created interpretation risk; fixed in code, but historical logs remain.
- Dirty input handling is validated in tests, but policy for "skip bad rows" vs "hard fail" is still product-dependent.

## Reality Check on "99.99999% Uptime"

Seven nines means roughly 3.15 seconds of downtime per year. For a reconciliation platform, this is usually not a sensible first target unless you operate:

- active-active multi-region control plane
- no single datastore or queue dependency
- autonomous failover with verified RTO/RPO
- exhaustive chaos testing and dependency isolation

A realistic maturity path:

- Phase 1 target: 99.9% (pilot)
- Phase 2 target: 99.95% (early production)
- Phase 3 target: 99.99% (mature multi-AZ/multi-region controls)

For batch jobs, a better primary SLI is often job success and on-time completion, not raw API uptime.

## Production-Ready Plan

### Phase A: Correctness and observability hardening (now)

- Keep strict parser tests and dirty fixtures in CI.
- Add per-phase metrics:
  - right-index rows/sec
  - left-match rows/sec
  - total job duration
  - peak RSS
  - GC pause/mark durations
- Emit structured job audit envelope always in production mode.

### Phase B: Capacity and performance controls

- Add capacity guidance by dataset size bands (1M, 5M, 10M, 20M+).
- Add configurable hard limits:
  - max rows
  - max file size
  - max memory budget
- Introduce spill-to-disk or partitioned index strategy for very large right-side files.

### Phase C: Resilience architecture (infra)

- Move reconciliation execution behind a durable queue (idempotent job IDs).
- Persist job state transitions (`queued`, `running`, `checkpointed`, `completed`, `failed`).
- Add checkpoint/resume for long jobs.
- Run workers in at least multi-AZ setup with auto-retry and dead-letter queue.

### Phase D: High-availability controls

- Blue/green or canary deploys with automatic rollback gates.
- Health checks and SLO-backed alerting.
- Dependency timeouts, circuit breakers, and bulkheads.
- Regular disaster-recovery drills with measured RTO/RPO.

### Phase E: Governance for reliability

- Define SLOs and error budgets per surface:
  - control-plane API availability
  - job completion within SLA window
  - correctness invariants
- Block releases that violate error budget or regress benchmark baselines.

## Recommended SLIs/SLOs for Initial Production

- API availability: >= 99.95% monthly
- Job success rate (non-user-data errors): >= 99.9%
- P95 job latency (5M/side baseline): <= agreed target by environment
- Data correctness:
  - zero silent mismatches from parser failures
  - audit envelope present on all production runs

## Immediate Next Actions

1. Keep benchmark labels unambiguous (`left_rows/op`, `total_rows/op`) in all perf reports.
2. Add CI job that runs 1M and 5M performance checks with regression thresholds.
3. Define production run policy for dirty rows:
   fail-fast only, or quarantine-and-continue with explicit reconciliation error report.
4. Build queue-backed worker execution with idempotent retries and checkpointing.
