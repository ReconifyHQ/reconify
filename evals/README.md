# Reconify Engine agent evaluation corpus

Graded fixtures that measure how well an external coding agent (Claude Code, Codex,
Gemini CLI, or OpenCode) can configure the Reconify Engine from a
natural-language description of a reconciliation problem.

This directory is fixture data plus a machine-readable contract. The cross-agent runner
that consumes it is AP-10 (#110); the active contract is
`reconify.engine.eval-scenario.v2`, published in `schemas/` and printable with
`reconify schema eval-scenario-v2`. The original v1 contract remains published
for third-party fixture compatibility.

The v2 corpus exercises the non-interactive workflow: `capabilities`, `config validate`,
`reconcile`, and `explain`. It does not require `inspect` or `config infer`.

## Layout

```
evals/
  README.md
  003-settlement-fee/
    scenario.json                       # the contract: prompt, paths, assertions
    inputs/left.csv right.csv           # what the agent is given
    reference/reconify.yaml             # a config known to behave correctly
    expected/result.json                # the reconciliation outcome to reproduce
    expected/explanation.json           # deterministic explanation answer key
    counter_examples/                   # configs that must NOT reproduce it
      tolerance-too-wide.yaml
```

`file_pattern` resolves relative to the **config file**, not the process working
directory. A runner therefore materializes a working directory per candidate config —
the scenario `inputs/` plus the config at the working-directory root — so the reference
config and an agent's config run through an identical path. `internal/cli/evals_test.go`
does exactly this and is the reference implementation.

## Scoring

**Grade behavior, not text.** Many different configs are legitimately correct: date
windows can differ, passes can be explicit or implicit, YAML anchors are a style choice.
Diffing an agent's YAML against `reference/reconify.yaml` produces false negatives. Run
the agent's config and compare the *result*. `reference/reconify.yaml` is a worked answer
for human reviewers, not the grading key.

The evaluator grades five observable workflow dimensions per trial:

| Dimension | Check | What it catches |
|---|---|---|
| discovery | agent invokes `reconify capabilities` | unsupported assumptions about the installed Engine |
| configuration | agent validates a root `reconify.yaml` | missing required fields such as `multiplier` |
| execution | agent reconciles to `agent-result.json` | file patterns that resolve to nothing |
| classification | every counter in `assertions` equals the verified run's summary | the wrong matching strategy |
| explanation | agent writes the expected deterministic explanation | unexplained or incorrect result artifacts |

**The headline metric is `summary_match` pass rate across the corpus.** Report
`exact_match` separately rather than gating on it — it is stricter than "the agent
understood the problem." The evaluator reports both `pass@1` and `pass^k` for
classification and exact-result correctness.

Results are byte-stable. `reconcile --format json --deterministic` emits no `run_id` or
timestamp (those appear only under `--audit`), so `exact_match` is a plain file
comparison with no normalization.

Agents are stochastic, so a single run is not a measurement. Run k trials per scenario
and report both `pass^k` (every trial passed — reliability) and `pass@1` (any trial
passed — the capability ceiling). The gap between the two is itself the interesting
signal for a tool that has to be trusted with financial data.

### `assertions` and the characterizing counter

`matched` counts reference one-to-one matches only. Grouped outcomes land in
`grouped_matched_count` and `many_to_many_matched_count`. Each scenario asserts the
counter that actually characterizes it, and zero values are meaningful: `005` and `006`
assert `duplicate_count: 0` because grouped rows sharing a reference get flagged as
duplicates unless the source sets a per-row-unique `group_col`.

## Keeping the corpus honest

An evaluation that everything passes measures nothing. Two properties are enforced by
`internal/cli/evals_test.go` on every `make check`:

- **The answer key is correct** — every reference config still reproduces its
  `expected/result.json` and its asserted counters.
- **Every scenario discriminates** — each counter-example must fail a gate or produce
  different counters. A scenario with no counter-example fails the test.

Discrimination is what forces the fixtures to carry rows that punish laziness. Without
the extra row in `003` whose amounts differ by far more than a fee, `amount_tolerance_minor:
100000` scores identically to the correct answer. The counter-example is the thing that
makes that row necessary.

A third property is a review rule rather than a test, because it can't be automated:

- **No answer leakage** — a prompt describes the business situation, never the config key
  under test. "The customer settled this invoice across several smaller payments" is a
  scenario; "configure a `one_to_many` pass" is a typing exercise.

## Adding a scenario

1. Create `NNN-short-name/` with `inputs/`, and write the prompt as a business situation.
2. Write `reference/reconify.yaml` against `inputs/`, using `inputs/…` file patterns.
3. Generate the expected result from a materialized working directory:
   ```
   reconcile -c reconify.yaml --pair <pair> --format json --deterministic
   ```
   and save it as `expected/result.json`. Run
   `go run ./cmd/generate-eval-explanations` to refresh every deterministic
   `expected/explanation.json` fixture.
4. Add at least one counter-example: a config a competent agent might plausibly write
   that is nonetheless wrong. If you can't make one fail, the fixture is too permissive —
   add a row that distinguishes the correct answer.
5. Write `scenario.json` with the characterizing counters in `assertions`.
6. `make check`.
