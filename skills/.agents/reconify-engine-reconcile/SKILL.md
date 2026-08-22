---
name: reconify-engine-reconcile
description: Reconcile unfamiliar financial files end to end. Use when one task spans input discovery, config creation or repair, Engine execution, and result explanation.
---

# Reconify Engine Reconcile

Produce a correct, reproducible reconciliation, not a high match rate. Complete the checkpoints in
order from the directory that will contain `reconify.yaml`.

## 1. Discover the build and inputs

Locate every input before inspecting it, then query the installed binary:

```bash
reconify capabilities
reconify inspect INPUT_LEFT
reconify inspect INPUT_RIGHT
```

Inspect every counterpart in a multi-source run. This checkpoint is complete when the actual input
paths are known and `capabilities` confirms the commands, formats, schemas, and passes needed by the
task.

## 2. Record the evidence and policy

From each profile, record the candidate date, amount, reference, name, currency, and grouping
columns; copy exact date layouts and note ambiguous inferences. Settle these business decisions with
the user or state an explicit assumption:

- the shared transaction identifier;
- the amount difference that is acceptable, expressed in minor units;
- the permitted date offset;
- whether repeated references represent duplicates, one-to-many groups, or many-to-many groups.

This checkpoint is complete when every mapping and tolerance has either file evidence, a user
decision, or a named assumption.

## 3. Build and prove the config

Read `../reconify-engine-config/SKILL.md` and follow its workflow. Keep each `file_pattern` relative
to the config file, then run:

```bash
reconify config validate --config reconify.yaml
reconify config check-source --config reconify.yaml --source SOURCE --file INPUT
```

Run `check-source` for every configured source. This checkpoint is complete only when validation and
all source checks pass against the real inputs.

## 4. Produce the retained result

Use explicit artifact settings so the result includes clean matches:

```bash
reconify reconcile --config reconify.yaml --pair PAIR \
  --format json --result-mode all --deterministic --out agent-result.json
```

For inputs too large to buffer, read `../reconify-engine-performance/SKILL.md` and choose a streaming
artifact deliberately. This checkpoint is complete when the command succeeds, the output parses in
its declared format, and its final summary is present.

## 5. Explain and challenge the result

```bash
reconify explain agent-result.json > agent-explanation.json
```

Compare the summary counters with the expected business scenario. A surprising counter is evidence
to investigate; read `../reconify-engine-debug/SKILL.md`, correct the earliest unsupported mapping or
policy decision, and rerun from config validation.

This checkpoint is complete when the explanation names the same counters as the result and every
surprising difference is either corrected or reported as unresolved.

## Recovery and completion

On any command failure, read its diagnostic, correct the current checkpoint, and rerun that command.
A missing guessed path is recoverable: discover the actual path and continue.

Finish with all three deliverables:

- validated `reconify.yaml`;
- retained result artifact;
- retained explanation artifact.

The final report states the result counters, the artifact paths, all assumptions, and any unresolved
error. Writing YAML or obtaining one successful command is an intermediate state, not completion.
