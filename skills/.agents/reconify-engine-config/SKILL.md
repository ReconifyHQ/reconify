---
name: reconify-engine-config
description: Create, validate, review, or document Reconify Engine YAML mappings and source/pair settings.
---

# Reconify Engine Config

Treat `config/config.go` as the source of truth. Before changing a mapping, inspect the input with `reconify inspect`, then validate with `reconify config validate` and bounded source checks. Keep examples aligned with typed fields, defaults, and validation; do not invent YAML keys.

Use `reconify config infer` for non-interactive proposals. A proposal that needs input is not permission to guess: resolve ambiguous date, amount, and reference mappings before reconciliation.
