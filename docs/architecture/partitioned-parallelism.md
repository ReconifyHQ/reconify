---
title: Partitioned parallel reconciliation
description: Design and operating guide for the disk-backed partition worker queue.
---

# Partitioned parallel reconciliation

This guide describes the implementation introduced for issue #64. It is for
contributors, agents, and operators who need to change or benchmark the
partitioned backend without weakening reconciliation correctness.

## Scope

Parallelism is opt-in. It applies to independent partitions inside a
single-source run and inside each ordered multi-source counterpart pass. The
counterpart passes themselves remain sequential because unmatched left rows are
carried forward to the next source in configured order.

The default is serial execution (Workers: 0 or --partition-workers 0). This
keeps existing callers conservative while allowing benchmark and production
operators to choose a worker count explicitly.

## Data flow

~~~mermaid
flowchart LR
    L[left partition i] --> W[partition worker]
    R[right partition i] --> W
    W --> C[(private gob result chunk)]
    W --> Q[bounded descriptor queue]
    Q --> O{ordered consumer}
    C --> O
    O -->|replay events| S[sole ResultWriter owner]
    O -->|add summary| A[aggregate summary]
    O -->|success| D[delete chunk]
    O -. failure/cancel .-> X[remove spill tree]
~~~

Workers never call the caller-owned ResultWriter. A worker serializes typed
events (match, amount/timing differences, unmatched rows, duplicate and
grouped events, and warnings) to a private gob file. The queue contains only a
partition number, chunk path, summary, and carry-forward metadata. It never
contains a transaction slice or a grouped payload.

The consumer keeps output deterministic by replaying partition 0, then 1, and
so on. A fast partition may finish first, but its descriptor waits in the
bounded queue/map until the next partition is ready. Once replay succeeds, the
chunk is deleted. A writer, filesystem, or context error cancels workers and
the private spill directory is removed by the reconciliation boundary.

## Public controls

Go callers use reconcile.PartitionedOptions:

| Field | Meaning |
|---|---|
| Workers | Concurrent partition workers. Values below 2 use the serial path. |
| QueueCapacity | Maximum completed descriptors waiting for the consumer. 0 derives a small capacity from Workers. |
| MaxChunkBytes | Optional per-chunk limit. 0 leaves the limit disabled; set it when temporary-disk policy requires a hard chunk ceiling. |
| SpillDir | Parent directory for private staging, carry manifests, sort runs, and result chunks. |
| Metrics | Optional callback receiving queue depth, queued spill bytes, completed chunks, writer bytes, and worker-block counts. The callback may run concurrently. |

The CLI exposes the same controls for partitioned runs:

~~~bash
reconify reconcile --config reconify.yaml --pair bank_vs_stripe \
  --format ndjson --partition-workers 4 \
  --partition-queue-capacity 2 \
  --partition-max-chunk-mb 256 \
  --out results.ndjson
~~~

Start with two or four workers. More workers increase concurrent indexes,
sort buffers, file descriptors, and temporary output, and can make a hot
partition or a slow filesystem the limiting factor.

## Correctness invariants

Before changing the queue or worker lifecycle, preserve these invariants:

1. The partition selector is derived from the normalized matching/grouping key,
   so candidates that can match are colocated.
2. A partition is consumed exactly once. A replayed chunk must not be replayed
   after a retry or cancellation.
3. Output order is partition order when deterministic output is promised.
4. Summary aggregation happens only in the consumer, never concurrently.
5. Multi-source pass order and carry-forward semantics are unchanged.
6. One-to-many, many-to-one (reverse orientation), and many-to-many members stay
   together in their group event.
7. A final output is published only by the CLI commit boundary after the engine
   completes and flushes successfully.

The parity suite compares row-level events, counterpart ownership, grouped
members, anomaly classifications, summaries, and duplicate annotations. Counts
alone are not sufficient evidence.

## Code map

- engine/reconcile/partitioned.go: single-source staging and partition queue
  integration.
- engine/reconcile/partitioned_multisource.go: ordered counterpart passes and
  per-pass partition queue integration.
- engine/reconcile/partitioned_queue.go: chunk event format, replay, bounded
  worker pool, cancellation, and queue metrics.
- engine/reconcile/partitioned_staging.go: CSV partition files and row
  sidecars.
- engine/reconcile/partitioned_grouped.go: external sort and group merge.
- engine/reconcile/partitioned_carry.go: multi-source unmatched carry-forward
  manifests.
- engine/reconcile/result_parity_test.go: batch/streaming/partitioned row
  parity, including parallel chunk replay.

Do not make ResultWriter thread-safe as a shortcut. Keeping one writer owner
is the design guarantee that avoids output races and preserves format-specific
ordering and filtering behavior.

## Verification workflow

Run focused parity tests first:

~~~bash
go test ./engine/reconcile -run 'TestExecutionPathParity|TestReconcilePartitioned'
go test -race ./engine/reconcile ./internal/cli
~~~

Then compare serial and parallel benchmarks with the same fixture, partition
count, output format, Go version, and host conditions:

~~~bash
go test -run '^$' -bench='BenchmarkReconcilePartitioned.*Balanced' \
  -benchtime=1x -benchmem ./engine/reconcile
~~~

For CLI runs, use ndjson or csv, capture /usr/bin/time -l (macOS) or an
equivalent RSS monitor, and retain the config and binary checksums. Always run a
correctness invocation for the same binary and dataset; a faster run without a
matching parity result is not publishable evidence.

