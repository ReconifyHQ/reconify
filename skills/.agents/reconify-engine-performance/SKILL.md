---
name: reconify-engine-performance
description: Benchmark, tune, or explain Reconify Engine streaming, indexes, and large-file behavior.
---

# Reconify Engine Performance

Read `docs/content/docs/cli/performance/index.md` and `docs/architecture/partitioned-parallelism.md` before changing streaming, indexes, or worker behavior. Preserve bounded-memory paths and measure with the existing benchmark scripts. Prefer `ndjson` or CSV for large output and make disk/partitioned fallback behavior observable and tested.
