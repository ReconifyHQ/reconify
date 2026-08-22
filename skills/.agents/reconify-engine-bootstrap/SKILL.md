---
name: reconify-engine-bootstrap
description: Set up a new Reconify Engine reconciliation from files to validated, explained results.
---

# Reconify Engine Bootstrap

1. Run `reconify capabilities` and `reconify inspect` for each input.
2. Create a configuration with `reconify config infer` or an explicit mapping.
3. Validate the config and each source before reconciling.
4. Run `reconify reconcile` using NDJSON for large jobs.
5. Read the summary and use `reconify explain` for exceptions.

Keep file patterns relative to the configuration file and retain the validated config with the result artifact for reproducibility.
