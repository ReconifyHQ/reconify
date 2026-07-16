---
title: Engine Performance
description: Streaming reconciliation design, benchmark results, memory behavior, and scaling guidance.
icon: Gauge
---

# Reconify Engine Performance

This document explains how the streaming reconciliation engine is designed, what
performance numbers it achieves, why it behaves the way it does at scale, and how
to select hardware for your workload.

---

## How the Engine Works

The reconciler processes two transaction files without loading both into memory
at the same time. The right-side file is indexed once into a hash map. The
left-side file is then streamed and matched against that index row by row. Results
are emitted as events and written to the output format immediately. Peak memory is
determined by the size of the right-side index, not the combined file sizes.

```mermaid
flowchart TD
    A[right.csv] -->|stream row by row| B[ParseCSVEach\nPass 1]
    B -->|Add compact bucket| C[(RightIndex\nmemoryIndex)]
    B --> D[rightSeen\nmap string uint8\nsaturating counter]
    D -->|any duplicates?| E{len > 0}
    E -->|yes| F[collectDuplicates\ntargeted right re-scan]
    E -->|no| G((skip\nno extra I/O))

    H[left.csv] -->|stream row by row| I[ParseCSVEach\nPass 2]
    C -->|Get buckets by ref| I
    I --> J{outcome per row}
    J -->|ref + amount + date match| K[WriteMatch]
    J -->|ref + date match, amount diff| L[WriteAmountDiff]
    J -->|ref + amount match, date diff| M[WriteTimingDiff]
    J -->|no matching bucket| N[WriteUnmatched left]
    I --> O[leftSeen\nmap string uint8]
    O -->|any duplicates?| P{len > 0}
    P -->|yes| Q[collectDuplicates\ntargeted left re-scan]
    P -->|no| R((skip))

    C -->|IterateUnused| S[WriteUnmatched right]

    K --> T[ResultWriter]
    L --> T
    M --> T
    N --> T
    S --> T
    F --> T
    Q --> T
    T -->|flush| U[output\nndjson / json / csv / table]
```

### The three stages

**Stage 1: index the right side.** `ParseCSVEach` streams through `right.csv`.
Each row is stored in the `RightIndex` as a compact `bucket` struct containing flat
integer fields (no `time.Time`, no duplicate reference string). Reference occurrence
counts are tracked using a saturating `uint8` counter capped at 2.

**Stage 2: match the left side.** `ParseCSVEach` streams through `left.csv`. For
each row the engine calls `idx.Get(ref)` for an O(1) bucket lookup. Matches, amount
diffs, timing diffs, and unmatched left rows are emitted immediately to the
`ResultWriter`. Left reference counts are tracked with the same saturating counter.

**Stage 3 (conditional): collect duplicates.** If any reference appeared more than
once on either side, the engine re-scans that file a second time and collects only
the rows whose reference was flagged. For datasets with no duplicates this stage is
never invoked and costs zero I/O.

The `ResultWriter` supports five output formats. `ndjson` and `csv` write one line
per event and use O(1) memory regardless of result size. `json` accumulates all
results in memory before flushing and is intended for smaller datasets or API
integration.

### Observing a live run

For unattended large-file jobs, write telemetry separately from the reconciliation
result:

```bash
reconify reconcile --config reconify.yaml --pair bank_vs_stripe \
  --format ndjson --out results.ndjson \
  --progress-out progress.ndjson --heartbeat-every 30s
```

`progress.ndjson` receives lifecycle events while `results.ndjson` keeps only
reconciliation data. The stream includes rows/sec and elapsed time; totals and
ETA remain absent when obtaining them would add a full scan. RSS/CPU are
best-effort platform metrics, while heap and GC counters are available from the
Go runtime. A telemetry sink failure does not interrupt the reconciliation.

### Bounded-memory partitioning

For a large CSV reconciliation, select the partitioned backend:

```yaml
index:
  backend: partitioned
  partition_count: 64
```

Reconify first hashes the configured matching/grouping key in both inputs and
writes each row to one of `partition_count` temporary files. For reference
passes it repeats the normal streaming reconciliation per partition. For
`one_to_many` and `many_to_many`, each partition is externally sorted into
bounded runs and merge-read by key, so only the current left/right groups are
materialized. Equal keys always select the same partition; peak grouped
working memory is therefore bounded by the largest active group plus fixed
sort/merge buffers rather than by the complete input or every group in a
partition. The temporary-disk requirement is higher because sort runs and
sorted outputs coexist briefly with the staged partitions.

`partition_count: 0` selects a power-of-two count from the input size and, when
configured, the memory budget; explicit values must be at least 2. The adaptive
selector targets roughly one million rows per active partition and increases
the count until the estimated working set fits `max_memory_mb`. More partitions
reduce memory but increase partition-file overhead and disk passes.

For non-grouped passes, duplicate handling uses a second disk-backed set of
hash buckets keyed by the effective `group_col`. Each bucket is externally
sorted and streamed one group at a time. `flag` emits duplicate groups from the
sorted stream; `merge` and `latest` write representative row IDs to per-match-
partition sidecars. The matching loop loads only the active partition's
sidecars, so duplicate metadata no longer grows with the complete input. This
uses additional temporary disk, which is included in resource estimates.

Use `ndjson` or `csv` output so results do not accumulate in memory.

Partition work can be enabled explicitly once row-level parity has been checked:

See the contributor-level [partitioned parallelism design guide](../../../architecture/partitioned-parallelism.md)
for the chunk lifecycle, invariants, metrics, and test workflow.

```bash
reconify reconcile --config reconify.yaml --pair bank_vs_stripe \
  --format ndjson --partition-workers 4 --partition-queue-capacity 2 \
  --partition-max-chunk-mb 256 --out results.ndjson
```

Workers never call the final `ResultWriter`. Each worker writes typed events to a
private, disk-backed chunk and places only its partition number, chunk path, and
summary in a bounded descriptor queue. A single writer replays chunks in partition
order and removes them after successful publication. A worker count of `0` (the
default) preserves serial processing; start with a small value and compare the
row-level output with serial partitioning before increasing it. Multi-source
counterpart passes remain ordered, while partitions inside each pass may run in
parallel.

The partitioned backend applies to CSV pairs using a consistent reference,
name, or group-key selector across all passes. Duplicate policies are supported
when each duplicate group is co-located with the partition key; otherwise the
CLI rejects partitioning to preserve global duplicate semantics. Grouped passes
are batch operations within each partition, not whole-file batch operations.

For `rights` pairs, the coordinator partitions the left file once, partitions
one counterpart at a time, and writes unmatched left rows to carry-forward
manifests before advancing to the next configured counterpart. Only one left
partition and one counterpart partition are active in memory. Right duplicate
and left disposition spill files preserve duplicate policies and annotations;
the private spill tree is removed on success and on failure. Multi-source
partitioning rejects non-CSV inputs, token-name matching, incompatible
selectors, and duplicate layouts that cannot be safely co-located.

### Resource-aware selection

Use resource budgets when file size alone is not a safe proxy for host capacity:

```yaml
index:
  backend: auto
  spill_dir: /var/tmp/reconify
  max_memory_mb: 8192
  max_temp_disk_mb: 16384
```

The selector estimates parsed index memory, SQLite storage, and partition
staging from streamed CSV row shape and file statistics. For a multi-source
partitioned run, the peak estimate includes full-file duplicate disposition
state, one active left and counterpart partition, and the carry-forward copy
being written. Consumed left/right partitions and grouped sort outputs are
removed during each pass, so completed counterparts do not accumulate in the
spill directory. With a budget set, `auto` tries memory, then disk, then
partitioned indexing when every counterpart is eligible. Without a budget,
multi-source `auto` keeps its existing memory/disk selection. It also checks
actual free space in `spill_dir`. The selected backend and rejected fallback
reasons are included in JSON-style metadata and reported on stderr for
CSV/table runs.
Budgets are resource safeguards, not throughput guarantees. If no candidate can
meet its estimate, the run fails explicitly and does not publish final output.

### Grouped settlement passes

The `one_to_many` and `many_to_many` passes stream complete groups from
externally sorted partition files. They need the current groups in memory before
they can sum amounts, compare group dates, and decide which rows to consume.
Other groups and sort runs are released as processing advances. A single
unusually large group can still require substantial memory because grouped output
contains every member of that group.

`many_to_many` does not perform subset-sum or fuzzy combination search. It only
groups rows by an explicit key such as a payout ID, invoice ID, settlement ID, or
payment run ID, then compares the left and right group totals. This keeps the
runtime predictable and the output explainable.

---

## Benchmark Environment

The partitioned duplicate-disposition benchmarks are environment-gated so they
do not run during ordinary tests. Generate either fixture size and run the same
command before and after a change:

```bash
go run scripts/gen_bench_data.go --rows 5000000 --duplicate-groups 100000 --out /tmp/bench-5m
BENCH_DATA_DIR=/tmp/bench-5m go test -run '^$' \
  -bench='BenchmarkReconcilePartitioned_5M_DuplicatePreScan$' \
  -benchtime=1x -benchmem -timeout=600s ./engine
```

Use `--rows 20000000` and the corresponding `20M` benchmark for the larger
fixture. Record partition count, output format, Go version, elapsed time, peak
RSS, and temporary-disk usage with the result.

All numbers in this document were collected on an Apple M1 Pro (10-core, 32 GB
unified memory, NVMe SSD) running macOS with Go 1.26.4.

The dataset contains 20 million rows per side, split across two CSV files with
different column schemas (left uses `ref_id`/`description`; right uses
`txn_ref`/`merchant`). File sizes: left.csv ~1.23 GB, right.csv ~991 MB.

The outcome distribution reflects realistic financial reconciliation workloads:

| Outcome | Share |
|---|---|
| Perfect match (ref + amount + date) | 85% |
| Amount difference (1-50 minor units) | 5% |
| Timing difference (1-3 days) | 5% |
| Unmatched left only | 5% |
| Unmatched right only | 5% |

### Grouped partition benchmark

The grouped benchmark uses 5,000 groups with three right-side rows per group for
the balanced case, and one 20,000-row group for the skewed case. It was run with:

```bash
go test -run '^$' -bench='Benchmark(ReconcilePartitioned|ReconcileBatch)_OneToMany(Balanced|Skewed)$' -benchtime=1x -benchmem ./engine
```

For ordered multi-source measurements, use the dedicated balanced and skewed
fixtures and record the same wall time and allocation columns:

```bash
go test -run '^$' -bench='BenchmarkReconcilePartitionedMultiSource_OneToMany(Balanced|Skewed)$' -benchtime=1x -benchmem ./engine
```

To capture peak RSS and temporary-disk usage, wrap that command with the host's
resource monitor (for example `/usr/bin/time -l` on macOS), and record the Go
version, partition count, output format, and fixture shape with the result.

Results from one warm-cache run:

| Benchmark | Time | Allocated | Allocations |
|---|---:|---:|---:|
| Partitioned balanced | 145.6 ms | 78.4 MB | 760,475 |
| Batch balanced | 9.7 ms | 26.5 MB | 55,373 |
| Partitioned skewed | 138.5 ms | 109.8 MB | 621,823 |
| Batch skewed | 9.7 ms | 40.9 MB | 284 |

Peak RSS for the two partitioned cases was 208,355,328 bytes (~199 MiB), measured
with `/usr/bin/time -l` around the same benchmark command. These are comparison
measurements, not throughput guarantees; external sorting trades runtime and
temporary disk for a working set independent of the number of groups.

---

## Parse Throughput

Parsing is not the bottleneck. The 1 MB read buffer and a date-string cache (capped
at 1000 keys) keep allocation low and keep `encoding/csv` from dominating.

| Benchmark | Rows | Time | Throughput |
|---|---|---|---|
| `ParseCSVEach` 100k synthetic | 100,000 | ~58 ms | ~1.7 M rows/sec |
| `ParseCSVEach` 20M realistic (warm cache) | 20,000,000 | ~8.5 s | ~2.35 M rows/sec |

These numbers are I/O and CSV-decode bound. They will not improve further without
moving to zero-copy or SIMD-accelerated parsing (a Rust concern, not a Go one).

---

## Reconciliation Throughput Across Optimization Generations

```mermaid
xychart-beta
    title "Wall Clock Time: 20M x 20M Reconciliation"
    x-axis ["Baseline", "v5: no leftFirst", "v6: compact bucket"]
    y-axis "Seconds" 0 --> 600
    bar [537, 148, 171]
```

```mermaid
xychart-beta
    title "Peak RSS: 20M x 20M Reconciliation"
    x-axis ["Baseline", "v5: no leftFirst", "v6: compact bucket"]
    y-axis "RSS (GB)" 0 --> 10
    bar [7.6, 7.6, 6.2]
```

| Version | Wall clock | Peak RSS | Worst GC mark | Approx. rows/sec |
|---|---|---|---|---|
| Baseline | 537 s | 7.6 GB | ~48 s | ~29k |
| v5: eliminate `leftFirst`/`rightFirst` | 148 s | 7.6 GB | ~20 s | ~105k |
| v6: compact `bucket` struct | 171 s | 6.2 GB | ~15 s | ~91k |

The wall-clock numbers have run-to-run variance of roughly 15-20% on macOS due to
thermal throttling and OS scheduling. The RSS measurement is the stable signal.
The v5 to v6 regression in wall-clock time is within that variance band; the RSS
drop from 7.6 GB to 6.2 GB is real and reproducible.

---

## Why Throughput Collapses at 20M Scale

### The engine is not CPU-bound. It is GC-bound.

At 1M rows the right-side index fits largely within L3 cache (typically 8-32 MB on
modern processors). Hash lookups are fast, GC cycles are short, and throughput
reaches ~320k rows/sec.

At 20M rows three effects compound each other.

**Cache miss explosion.** The right-side index grows to 4-6 GB. Every reference
lookup on the left side now misses L3 cache and stalls the CPU waiting for main
memory. This alone reduces throughput by 2-3x compared to the cache-warm case.

**Heap scanning explosion.** Go's garbage collector uses a concurrent tricolor mark
algorithm. During each GC cycle the collector traces every live pointer in the heap.
The duration of the mark phase is proportional to the number of pointer fields in
live objects, not just total heap size. Maps with pointer-rich struct values are the
worst case for this algorithm.

**Mark phase duration.** At baseline the GC took up to 48 seconds for a single
concurrent mark phase at 20M scale. This is not stop-the-world time. The
application continues running while the collector marks. But marking consumes CPU
cores, taking them away from the reconciler and causing the 10x throughput drop from
1M to 20M rows (320k rows/sec down to 29k rows/sec).

### The root cause: `leftFirst map[string]Transaction`

Before any optimization the reconciler stored a full `Transaction` value for every
unique reference seen on the left side:

```
leftFirst  map[string]Transaction   // 20M entries x ~232 bytes = ~4.6 GB
rightFirst map[string]Transaction   // similar, depending on right-side duplication
```

Each `Transaction` held five string fields. Each string is a 16-byte header pointing
to a separate heap allocation. That is approximately 100 million live pointer fields
that the GC had to follow during every collection cycle. This is the 48-second mark
phase.

The critical detail: **both maps were used only for duplicate detection, never for
matching**. The matching algorithm accessed neither `leftFirst` nor `rightFirst`.

---

## Optimization History

### v5: Eliminate `leftFirst` and `rightFirst`

Duplicate detection only needs to know whether a reference appeared more than once,
not what the full transaction looked like. The v5 optimization replaces both large
maps with two small structures per side:

- `leftSeen map[string]uint8`: a saturating counter capped at 2 (0 = unseen, 1 = seen
  once, 2 = seen at least twice)
- `leftDupRefs map[string]bool`: the set of references flagged as duplicates

When duplicates are found, `collectDuplicates` re-scans the file and collects only
the rows whose reference is in `leftDupRefs`. This costs one extra sequential file
read. Sequential I/O on NVMe at 1-2 GB/sec makes this a small fraction of total run
time compared to the GC savings.

```mermaid
flowchart LR
    subgraph before ["Before v5: live heap at peak"]
        direction TB
        B1["rightIndex\n~4 GB"]
        B2["leftFirst\n~4.6 GB\nCULPRIT"]
        B3["rightFirst\n~1.5 GB"]
        B4["leftRefCount\n~640 MB"]
        B5["rightRefCount\n~640 MB"]
        B6["runtime + OS\n~1 GB"]
        B7["TOTAL: ~12 GB"]
    end

    subgraph after ["After v5: live heap at peak"]
        direction TB
        A1["rightIndex\n~4 GB"]
        A2["leftSeen\n~500 MB"]
        A3["rightSeen\n~500 MB"]
        A4["runtime + OS\n~1 GB"]
        A5["TOTAL: ~6 GB"]
    end

    before -->|"3.6x faster wall clock\n44% less total alloc"| after
```

Results at 20M x 20M: wall clock 537s to 148s (3.6x), TotalAlloc 43.5 GB to 24.2
GB, worst GC mark 48s to 20s. Peak RSS unchanged at 7.6 GB because the right-side
index still dominated.

### v6: Compact the `bucket` struct

After v5 the right-side index held a full `Transaction` inside each bucket. The
`Transaction` type contained a `time.Time` field (which internally stores a
`*Location` pointer) and a `Reference` string that duplicated the map key. At 20M
entries that was 40 million extra pointer fields in the GC scan graph.

The `bucket` struct was refactored to store flat integer fields only:

```go
type bucket struct {
    id       string
    dateUnix int64   // time.UnixNano() -- no *Location pointer
    amount   int64
    currency string
    name     string
    source   string
    used     bool
}
```

`Reference` is injected from the map key when constructing a `Transaction` for
output via `b.toTransaction(ref)`. `Date` is stored as nanoseconds and reconstructed
via `time.Unix(0, b.dateUnix).UTC()` only at output time. The matching loop caches
`ltx.Date.UnixNano()` once per left row and compares integers:

```go
ltxDateNano := ltx.Date.UnixNano()
daysDiff := daysBetweenNano(ltxDateNano, b.dateUnix)
```

This avoids allocating `time.Duration` and calling `.Hours()` for every bucket
comparison on the hot path.

Results at 20M x 20M: Peak RSS 7.6 GB to 6.2 GB (18% reduction), worst GC mark
20s to 15s.

---

## Memory at Peak During Reconciliation

```mermaid
flowchart TD
    subgraph ram ["RAM contents during Pass 2 - peak usage point"]
        direction LR
        I["RightIndex\n20M compact buckets\n4-6 GB\n(dominant)"]
        LS["leftSeen\n20M string keys\n~500 MB"]
        RS["rightSeen\n20M string keys\n~500 MB"]
        RT["Go runtime\ngoroutines, GC metadata\n~300 MB"]
        BUF["I/O buffers\nCSV read buffer 1 MB\nnegligible"]
        OUT["Output buffer\nndjson: O(1)\njson: O(results)"]
    end
```

The right index is the ceiling. The left and right `Seen` maps are substantial but
secondary. Everything else is negligible at 20M scale.

The right index size depends on the average string content in your data. Each bucket
stores four string fields (`id`, `currency`, `name`, `source`). Short identifiers
and three-character currency codes bring the per-bucket cost toward 150 bytes; long
UUIDs and verbose merchant names push it toward 300 bytes. The map structure itself
adds roughly 50-60 bytes per entry regardless.

---

## Hardware Requirements

### RAM formula

The right-side index dominates peak memory. The formula below gives a conservative
estimate for hardware planning:

```
RAM_required_GB = ceil( (R x B_right + L x 60) / 1_000_000_000 + 2.0 )
```

Where:

| Symbol | Meaning |
|---|---|
| `R` | Number of rows in the right-side file |
| `B_right` | Bytes per right-side row in the index (see table below) |
| `L` | Number of rows in the left-side file |
| `60` | Bytes per left-side entry in the `leftSeen` tracking map |
| `2.0 GB` | Fixed overhead: Go runtime, OS buffers, output, safety margin |

`B_right` depends on the average length of the string fields in your data:

| Field profile | `B_right` |
|---|---|
| Short IDs (under 12 chars), 3-char currency codes | 150 |
| Typical (20-char IDs, short descriptions) | 220 |
| Long (UUID IDs, verbose merchant names) | 300 |

For conservative hardware planning use `B_right = 300`.

```mermaid
flowchart LR
    R["R = right-side rows"] --> RCALC["R x B_right bytes"]
    L["L = left-side rows"] --> LCALC["L x 60 bytes"]
    RCALC --> SUM["sum both sides"]
    LCALC --> SUM
    SUM --> DIV["divide by 1,000,000,000\nconvert to GB"]
    DIV --> ADD["add 2 GB fixed overhead"]
    ADD --> CEIL["round up to next integer"]
    CEIL --> OUT["RAM required in GB"]
```

**Example: 20M right rows, 20M left rows, typical string lengths (B_right = 220)**

```
RAM = ceil( (20_000_000 x 220 + 20_000_000 x 60) / 1_000_000_000 + 2.0 )
    = ceil( (4_400_000_000 + 1_200_000_000) / 1_000_000_000 + 2.0 )
    = ceil( 5.6 + 2.0 )
    = ceil( 7.6 )
    = 8 GB
```

Observed peak RSS in benchmarks: 6.2 GB. The formula over-estimates slightly to
account for Go allocator overhead and GC live-set bookkeeping.

### Reference table

```mermaid
flowchart TD
    A["How many rows on the right side?"] --> B{"R <= 5M"}
    B -->|yes| C["4 GB RAM\ncomfortable headroom"]
    B -->|no| D{"R <= 15M"}
    D -->|yes| E["8 GB RAM"]
    D -->|no| F{"R <= 30M"}
    F -->|yes| G["16 GB RAM"]
    F -->|no| H{"R <= 60M"}
    H -->|yes| I["32 GB RAM"]
    H -->|no| J["64+ GB RAM\nor use a disk-backed RightIndex\n(SQLite, mmap)"]
```

| Right rows | Left rows | B_right=150 | B_right=220 | B_right=300 | Recommended RAM |
|---|---|---|---|---|---|
| 1M | 1M | 0.3 GB | 0.3 GB | 0.4 GB | 4 GB |
| 5M | 5M | 1.1 GB | 1.4 GB | 1.8 GB | 4 GB |
| 10M | 10M | 2.1 GB | 2.8 GB | 3.6 GB | 8 GB |
| 20M | 20M | 4.2 GB | 5.6 GB | 7.2 GB | 16 GB |
| 50M | 50M | 10.5 GB | 14 GB | 18 GB | 32 GB |
| 100M | 100M | 21 GB | 28 GB | 36 GB | 64 GB |

Recommended RAM adds a 2x safety margin over the formula output to account for GC
live-set overhead and OS memory pressure. If the right side does not fit in RAM,
implement `RightIndex` backed by SQLite or a memory-mapped file and pass it to
`ReconcileStreaming`; the reconciler is decoupled from the index implementation.

### CPU

Parsing and reconciliation are single-threaded. A single fast core matters more
than core count. Any modern processor running at 2.5 GHz or above is sufficient.
The current engine does not benefit from additional cores.

### Disk

Input files are read sequentially. The right file is read once per run (twice in
the rare case where duplicate references are detected). No intermediate files are
written.

Sequential read bandwidth at 20M rows (approximately 2.2 GB combined) with a warm
OS page cache is negligible. Cold cache adds approximately:

| Storage type | Additional time |
|---|---|
| NVMe SSD | 0.5 to 2 seconds |
| SATA SSD | 2 to 5 seconds |
| Spinning disk (HDD) | 10 to 30 seconds |

---

## What Comes Next

The remaining pointer density in the right-side index comes from four string fields
per bucket (`id`, `currency`, `name`, `source`). The next optimization levers, in
order of impact, are:

**Currency interning.** Typical datasets contain 3 to 5 distinct currency codes.
Replacing the `currency string` field with a `uint8` enum eliminates one pointer
field per bucket. At 20M rows this saves roughly 200 MB of RSS and removes 20M
pointer fields from the GC scan graph.

**ID lazy reconstruction.** Store only a `rowNum uint32` in the bucket instead of
the `id string`. Reconstruct the ID by seeking to the source row only when
generating output events. This eliminates one more pointer field per bucket but
adds I/O on the output path, so it is only beneficial when the match rate is high
(few unmatched events per total rows processed).

**Hard ceiling in Go.** The practical ceiling for this design in Go is approximately
80 to 120k rows/sec at 20M scale with peak RSS around 4 to 5 GB. Below that floor,
the fundamental cost of 20M hash map entries with string keys and Go's
pointer-tracing GC sets a hard limit that no further struct compaction will break
through. A Rust implementation with arena-allocated structs and no GC would provide
a step-change improvement at that point.
