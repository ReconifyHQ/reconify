---
name: reconify-engine-bootstrap
description: Bootstrap a Reconify workspace. Use when no reconify.yaml exists and the requested outcome is a validated config plus the first explained result.
---

# Reconify Engine Bootstrap

Read `../reconify-engine-reconcile/SKILL.md` and execute its complete workflow. This skill supplies
the bootstrap decisions required at its evidence-and-policy checkpoint.

## Bootstrap intake

Settle these questions from the files and the user:

- Which system-issued value identifies the same transaction on both sides?
- What amount difference is acceptable, in minor units?
- How many calendar days may settlement shift?
- What does a repeated reference mean in each source?
- Which files and pair name should become the stable workspace interface?

Record an explicit assumption for any answer the files and user cannot supply. Use `inspect` evidence
for column mappings and `config schema` for YAML shape; examples and memory are not config sources.

Bootstrap is complete only when the end-to-end workflow has produced a validated config, a retained
result, and a retained explanation, and the final report names every assumption.
