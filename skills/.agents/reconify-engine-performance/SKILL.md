---
name: reconify-engine-performance
description: Fit Reconify runs within resource limits. Use when inputs cause high memory, swapping, slow execution, large artifacts, or a need for progress telemetry.
---

# Reconify Engine Performance

Optimize against a measured baseline while preserving the result summary.

## 1. Define the budget and baseline

Record input sizes, wall time, peak memory, temporary disk, output size, config, Engine version, and
the complete summary counters. This checkpoint is complete when one repeatable command reproduces
the baseline and its correctness signature.

## 2. Load the available controls

```bash
reconify capabilities
reconify config schema
reconify reconcile --help
```

Read the result's `index_selection` block before choosing a change. This checkpoint is complete when
the backend that actually ran and the relevant limits are known.

## 3. Change one resource lever

| Pressure | First lever |
|---|---|
| Right-side index exceeds RAM | Select `disk`, `auto`, or `partitioned` from the installed schema |
| Result artifact grows beyond RAM | Use `ndjson` or `csv` with `--out` |
| Raw row maps dominate allocation | Set parser `skip_raw` when raw columns are not required |
| Long silent run | Enable progress or a separate telemetry output |
| Partition throughput or spool pressure | Tune workers, queue capacity, and chunk limits from command help |

Keep diagnostics on stderr and result data in the declared output file. Apply one lever per
measurement so its effect remains attributable.

## 4. Re-run and compare

Run the same inputs and compare resource measurements plus every summary counter. Performance work
is complete when the run fits the stated budget, repeated measurements support the improvement, and
the correctness signature is unchanged. A changed summary is a correctness regression and routes to
`../reconify-engine-debug/SKILL.md`.
