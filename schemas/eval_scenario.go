package schemas

import _ "embed"

// EvalScenarioSchemaV1 is the stable identifier for a published Engine agent
// evaluation scenario, the machine-readable contract the cross-agent runner
// (AP-10) consumes.
const EvalScenarioSchemaV1 = "reconify.engine.eval-scenario.v1"

// EvalScenario is one graded fixture in the Engine agent evaluation corpus. It
// pairs a natural-language prompt with the inputs an agent is given, the
// reference config a correct answer is expected to behave like, and the
// reconciliation outcome that answer must produce.
//
// Paths are relative to the scenario directory. A runner materializes a
// working directory containing Inputs and the candidate config, placing the
// config at the working-directory root, because `file_pattern` resolves
// relative to the config file rather than the process working directory.
type EvalScenario struct {
	Schema          string         `json:"schema"`
	ID              string         `json:"id"`
	Prompt          string         `json:"prompt"`
	Inputs          []string       `json:"inputs"`
	ReferenceConfig string         `json:"reference_config"`
	ExpectedResult  string         `json:"expected_result"`
	Pair            string         `json:"pair"`
	Assertions      EvalAssertions `json:"assertions"`
	CounterExamples []string       `json:"counter_examples"`
}

// EvalAssertions is the subset of reconciliation summary counters that
// characterizes a scenario. A candidate config passes the summary gate when
// every counter here equals the value the run produced.
//
// Zero values are meaningful and always emitted: asserting duplicate_count is
// zero is how a grouped scenario proves it did not raise false duplicates.
type EvalAssertions struct {
	Matched                int `json:"matched"`
	UnmatchedLeft          int `json:"unmatched_left"`
	UnmatchedRight         int `json:"unmatched_right"`
	AmountDiffCount        int `json:"amount_diff_count"`
	TimingDiffCount        int `json:"timing_diff_count"`
	DuplicateCount         int `json:"duplicate_count"`
	GroupedMatchedCount    int `json:"grouped_matched_count"`
	ManyToManyMatchedCount int `json:"many_to_many_matched_count"`
	AmbiguousGroupCount    int `json:"ambiguous_group_count"`
}

//go:generate go run ../cmd/generate-eval-scenario-schema -output reconify.engine.eval-scenario.v1.json

// evalScenarioSchemaV1 is the checked-in schema served by the CLI.
//
//go:embed reconify.engine.eval-scenario.v1.json
var evalScenarioSchemaV1 []byte

// EvalScenarioV1 returns a copy of the published eval scenario schema document.
func EvalScenarioV1() []byte {
	return append([]byte(nil), evalScenarioSchemaV1...)
}
