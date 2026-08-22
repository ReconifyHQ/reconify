---
name: reconify-engine-performance
description: Run Reconify Engine reconciliations over large files within a memory budget — index backends, streaming formats, and the knobs that matter.
---

# Reconify Engine Performance

Defaults are tuned for files that fit comfortably in memory. Reach for this when inputs are large
enough that a run gets killed, swaps, or takes long enough to need progress output.

## Index backends

Set under `index` in `reconify.yaml`:

| Backend | Use when |
|---|---|
| `memory` (default) | The right-hand side fits in RAM. Fastest. |
| `disk` | Lower RAM via SQLite-backed index. |
| `auto` | Resource-aware selection between memory and disk. |
| `partitioned` | Very large inputs; bounded memory through partitioned processing. |

Related keys: `max_memory_mb` caps the estimated memory budget (`0` = uncapped),
`max_temp_disk_mb` caps temporary disk for `disk`/`partitioned`, `spill_dir` chooses where those
temp files live, and `auto_max_right_file_mb` (default `2048`) is the right-file size at which
`auto` switches to disk. Run `reconify config schema` for the current defaults on your build.

Every result reports an `index_selection` block naming the backend chosen and why — read it before
assuming which one ran.

## Streaming output

`--format ndjson`, `json-stream`, and `csv` stream; `json` and `table` buffer the whole result.
Use a streaming format for large runs and `--out FILE` rather than a shell redirect, so diagnostics
on stderr stay out of the data.

`--deterministic` sorts output sections and costs sort time and memory; it applies to `json` only,
so it is inherently for results small enough to buffer.

## Other knobs

- `skip_raw: true` on a parser skips allocating the raw field map — a straightforward memory win
  when you do not need original columns echoed back.
- `--progress` logs progress to stderr, `--progress-every N` sets the row interval, and
  `--progress-out FILE` writes live telemetry as NDJSON.
- `--partition-workers`, `--partition-max-chunk-mb`, and `--partition-queue-capacity` tune the
  `partitioned` backend. `0` workers means serial.
- `--max-token-buffer` bounds the unmatched buffer in token matching mode (`0` = unlimited).

Measure before and after on the same inputs. Because the Engine is deterministic, a performance
change that alters the summary is a correctness regression, not a speedup.
