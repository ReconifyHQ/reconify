---
name: reconify-engine-reconcile
description: Run the complete non-interactive Reconify Engine workflow from capability discovery through explanation.
---

# Reconify Engine Reconcile

Use this ordered workflow whenever an agent must reconcile unfamiliar financial files:

1. Discover the installed surface with `reconify capabilities`.
2. Profile every input with `reconify inspect FILE`.
3. Produce a proposal with `reconify config infer`; resolve a `needs_input` proposal instead of guessing.
4. Validate the resulting `reconify.yaml` and check bounded source samples.
5. Reconcile with an explicit pair and a structured output format; use `--agent` for scripted callers.
6. Read the summary, investigate exceptions, and run `reconify explain RESULT`.

Persist the config and result together. Use exact references before tolerances or fuzzy matching, and make every non-default matching choice visible in the config.
