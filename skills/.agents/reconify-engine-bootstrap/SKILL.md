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

Classify each answer with the tiers in the end-to-end workflow: use proven values, record an
assumption for a safe default, and ask the user whenever plausible readings would produce different
matching outcomes. Use `inspect` evidence for column mappings and `config schema` for YAML shape;
examples and memory are not config sources.

Bootstrap is complete only when the end-to-end workflow has produced a validated `reconify.yaml`, a
retained `result.json`, and a retained `explanation.json`, and the final report covers every item in
that workflow's completion checklist.
