---
name: reconify-engine-reconcile
description: Run the complete Reconify Engine workflow — discover the CLI, profile inputs, write and validate reconify.yaml, reconcile, and explain the result. Use whenever an agent must reconcile two sets of financial records it has not seen before.
---

# Reconify Engine Reconcile

You have an installed `reconify` binary and someone else's financial files. The Engine is
deterministic and local: the same config over the same inputs always produces the same result.
Your job is a config that is *correct*, not one that produces a high match rate.

## Ordered workflow

Run the workflow from the directory containing `reconify.yaml`. Input paths may be
nested (for example, `inputs/left.csv`); discover the actual paths before inspecting
them and keep every `file_pattern` relative to the config file.

```bash
reconify capabilities                         # 1. what this build supports
reconify inspect INPUT_LEFT                  # 2. profile EVERY input
reconify inspect INPUT_RIGHT
reconify config schema                        # 3. every config key, typed and documented
                                              # 4. write reconify.yaml (see reconify-engine-config)
reconify config validate --config reconify.yaml
reconify config check-source --config reconify.yaml --source left --file left.csv
reconify reconcile --config reconify.yaml --pair left_vs_right \
  --format json --deterministic --out agent-result.json
reconify explain agent-result.json > agent-explanation.json
```

Complete each checkpoint before advancing:

1. Confirm `capabilities` lists the commands and formats needed for the task.
2. Locate and inspect every input. If a guessed path fails, read the error, discover
   the real path, and rerun the inspection; a path error is recoverable, not a reason
   to stop.
3. Read `config schema` before writing YAML. Do not invent keys, pass names, or units.
4. Validate the config, then check every configured source against its real file. Fix
   the config and rerun both checks after any validation or source error.
5. Reconcile only after validation and source checks pass. Confirm that
   `agent-result.json` exists and is valid JSON.
6. Explain the exact result file and redirect the deterministic explanation to
   `agent-explanation.json`. Confirm that both artifacts exist before reporting.

Never stop after writing YAML or after a successful reconciliation. The deliverables
are the validated config, `agent-result.json`, and `agent-explanation.json`; the final
summary must state the result counters and any unresolved error.

`reconify config schema` is the authoritative description of every configuration key: type,
required flag, default, and enum values. Read it before writing YAML. Never guess a key name.

## The amount rule — read before writing any tolerance

**Amounts inside the Engine are integers in minor units.** `multiplier` converts the input into
them (`100` for dollars→cents, `1` when the file already holds cents). `amount_tolerance_minor` is
expressed in those same minor units.

A processor fee of "no more than 2.00" is `amount_tolerance_minor: 200`, not `2`.

- `2` tolerates two cents, so every ordinary fee is reported as a discrepancy and an analyst
  chases noise.
- `200000` silently absorbs a genuine break, and nobody ever sees it.

This one integer decides which differences reach a human. Derive it from the threshold the user
stated, scaled by the same `multiplier` you set on the sources.

## config infer is a starting point, not an answer

```bash
reconify config infer --left left.csv --right right.csv
```

It takes `--left` and `--right` flags, not positional paths.

It returns `"status": "needs_input"` whenever a mapping sits below its confidence gate — including
the ordinary case of *fewer than 100 sample rows*, which you cannot fix on a small file.
`needs_input` does not mean stop; it means the proposal is unverified. When you see it:

1. Read `reasons` to learn which mappings are unconfident.
2. Check each one against `reconify inspect` for that file — `inspect` reports `inferred_type`,
   `ambiguous`, `sample_values`, and the exact `date_layout` per column.
3. Write the config yourself, keeping only mappings you confirmed.
4. Never forward `proposed_yaml` unread. It defaults `amount_tolerance_minor` to `0` and invents a
   pair name.

## Do not use --agent for a result you intend to keep

`--agent` selects machine-readable defaults: streaming event output **and**
`result_mode: exceptions_only`, which suppresses clean matches. A result produced with `--agent`
has no `matched` array, so it cannot be diffed against a reference result or explained completely.

For a complete, reproducible artifact, be explicit:

```bash
reconify reconcile --config reconify.yaml --pair PAIR --format json --deterministic --out result.json
```

If you want agent defaults *and* a complete document, override the mode:
`--agent --format json --result-mode all`.

## Verify before you report

```bash
reconify explain result.json                  # stdout only; redirect to keep it
reconify explain result.json > explanation.json
```

State the numbers you are claiming and check them against what the user described. If the user said
one payment never arrived and `unmatched_left` is `0`, your config is wrong — usually a tolerance
or date window wide enough to absorb the break.

Never widen `amount_tolerance_minor`, `date_window`, or `name_match_threshold` to raise a match
rate. Each widening decides that a class of real differences will never be reported. Find the
specific rows first; if they are genuinely the same transaction, widen and say so in your summary.
