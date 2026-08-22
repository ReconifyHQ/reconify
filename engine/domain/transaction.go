// Package domain defines the stable reconciliation data model.
package domain

import "time"

// ResultSchemaV1 is the stable schema identifier for structured reconciliation
// results emitted by the Engine.
const ResultSchemaV1 = "reconify.engine.result.v1"

// Transaction is a normalized financial record from any source.
type Transaction struct {
	ID        string            `json:"id"`
	Date      time.Time         `json:"date"`
	Amount    int64             `json:"amount"` // always in minor units (e.g. kobo, cents)
	Currency  string            `json:"currency"`
	Reference string            `json:"reference"`
	Name      string            `json:"name"`
	Source    string            `json:"source"` // source name from config
	Raw       map[string]string `json:"raw,omitempty"`
	// GroupKey is the duplicate-detection grouping key, independent of Reference
	// (the matching key). Populated from the parser's group_col, falling back to
	// Reference when group_col is not configured.
	GroupKey string `json:"group_key,omitempty"`
}

// MatchedPair is a left+right pair that reconciled cleanly.
type MatchedPair struct {
	Left  Transaction `json:"left"`
	Right Transaction `json:"right"`
}

// AmountDiffPair is a pair where reference matched but the amount differs beyond tolerance.
type AmountDiffPair struct {
	Left      Transaction `json:"left"`
	Right     Transaction `json:"right"`
	DiffMinor int64       `json:"diff_minor"`
}

// TimingDiffPair is a pair where reference+amount matched but the date is outside the window.
type TimingDiffPair struct {
	Left     Transaction `json:"left"`
	Right    Transaction `json:"right"`
	DaysDiff int         `json:"days_diff"`
}

// GroupedMatchedPair is a left transaction matched against N right transactions sharing
// the same reference. Used by the one_to_many pass when the sum of right amounts is
// within tolerance and all right dates are within the date window.
type GroupedMatchedPair struct {
	Left   Transaction   `json:"left"`
	Rights []Transaction `json:"rights"`
}

// GroupedAmountDiffPair is a grouped pair where the sum of right amounts falls outside
// the configured tolerance. DiffMinor = Left.Amount - sum(Rights.Amount).
type GroupedAmountDiffPair struct {
	Left      Transaction   `json:"left"`
	Rights    []Transaction `json:"rights"`
	DiffMinor int64         `json:"diff_minor"`
}

// GroupedTimingDiffPair is a grouped pair where amounts reconcile within tolerance but
// at least one right date falls outside the date window. DaysDiff is the maximum
// abs(daysBetween) across all rights in the group.
type GroupedTimingDiffPair struct {
	Left     Transaction   `json:"left"`
	Rights   []Transaction `json:"rights"`
	DaysDiff int           `json:"days_diff"`
}

// ManyToManyMatchedPair is a group-level match where M left transactions reconcile
// against N right transactions sharing the same grouping key.
type ManyToManyMatchedPair struct {
	Lefts  []Transaction `json:"lefts"`
	Rights []Transaction `json:"rights"`
}

// ManyToManyAmountDiffPair is a group-level match where summed left and right
// amounts differ beyond tolerance. DiffMinor = sum(Lefts.Amount) - sum(Rights.Amount).
type ManyToManyAmountDiffPair struct {
	Lefts     []Transaction `json:"lefts"`
	Rights    []Transaction `json:"rights"`
	DiffMinor int64         `json:"diff_minor"`
}

// ManyToManyTimingDiffPair is a group-level match where summed amounts reconcile
// within tolerance but at least one cross-side date distance is outside the window.
type ManyToManyTimingDiffPair struct {
	Lefts    []Transaction `json:"lefts"`
	Rights   []Transaction `json:"rights"`
	DaysDiff int           `json:"days_diff"`
}

// AmbiguousGroupPair is emitted by the one_to_many pass when more than one left row
// shares the same reference — grouping is undetermined and manual reconciliation is
// required. All rows in the group are excluded from matching.
type AmbiguousGroupPair struct {
	Reference string        `json:"reference"`
	LeftRows  []Transaction `json:"left_rows"`
	Rights    []Transaction `json:"rights"`
}

// SubsetSumMatchedPair is emitted by the subset_sum pass when exactly one subset of
// right-side rows sums to the left row's amount (within tolerance and date window).
// Rights are the specific rows that form the matching subset.
type SubsetSumMatchedPair struct {
	Left   Transaction   `json:"left"`
	Rights []Transaction `json:"rights"`
}

// SubsetSumAmbiguousPair is emitted by the subset_sum pass when multiple valid subsets
// satisfy the left row's constraints. Alternatives holds up to max_alternatives subsets;
// all involved right rows are consumed to prevent further conflicting matches.
type SubsetSumAmbiguousPair struct {
	Left         Transaction     `json:"left"`
	Alternatives [][]Transaction `json:"alternatives"`
}

// SubsetSumSkippedPair is emitted by the subset_sum pass when the search was not
// attempted or aborted. Reason is one of "candidate_limit_exceeded" or "timeout".
// The left row remains unmatched; right-side candidates are not consumed.
type SubsetSumSkippedPair struct {
	Left   Transaction `json:"left"`
	Reason string      `json:"reason"`
}

// DuplicateGroup is a set of transactions in the same source sharing the same reference.
type DuplicateGroup struct {
	Source       string        `json:"source"`
	Reference    string        `json:"reference"`
	Transactions []Transaction `json:"transactions"`
}

// Summary holds aggregate counts, match rate, and monetary totals for a reconciliation run.
type Summary struct {
	// ResultMode is the emission mode applied to this run: "all", "exceptions_only",
	// or "summary_only". Empty means "all" (the default, backward-compatible behavior).
	ResultMode string `json:"result_mode,omitempty"`
	// Currency is the base currency for all monetary totals in this summary.
	// Empty when all transactions had an empty currency field.
	Currency string `json:"currency,omitempty"`
	// RunID is the telemetry run identifier. Empty when telemetry was not active.
	RunID string `json:"run_id,omitempty"`

	// Row counts
	TotalLeft       int     `json:"total_left"`
	TotalRight      int     `json:"total_right"`
	MatchedCount    int     `json:"matched"`
	UnmatchedLeft   int     `json:"unmatched_left"`
	UnmatchedRight  int     `json:"unmatched_right"`
	AmountDiffCount int     `json:"amount_diff_count"`
	TimingDiffCount int     `json:"timing_diff_count"`
	DuplicateCount  int     `json:"duplicate_count"` // total transactions across all duplicate groups, not group count
	MatchRatePct    float64 `json:"match_rate_pct"`
	// ReconciledRatePct is (matched + amount_diff + timing_diff + grouped variants) / total.
	// MatchRatePct only counts exact 1-to-1 matches; use this field for the full picture.
	// Note: one_to_many passes inflate total_right (N rights per left), so a fully-reconciled
	// grouped dataset may report a sub-100% reconciled_rate_pct. many_to_many counts one
	// reconciled group event for M+N rows. These grouped rates are expected.
	ReconciledRatePct float64 `json:"reconciled_rate_pct"`

	// Grouped match counts (one_to_many pass). Omitted when zero.
	GroupedMatchedCount    int `json:"grouped_matched_count,omitempty"`
	GroupedAmountDiffCount int `json:"grouped_amount_diff_count,omitempty"`
	GroupedTimingDiffCount int `json:"grouped_timing_diff_count,omitempty"`
	// Many-to-many grouped match counts. Omitted when zero.
	ManyToManyMatchedCount    int `json:"many_to_many_matched_count,omitempty"`
	ManyToManyAmountDiffCount int `json:"many_to_many_amount_diff_count,omitempty"`
	ManyToManyTimingDiffCount int `json:"many_to_many_timing_diff_count,omitempty"`
	// AmbiguousGroupCount is the number of reference groups where >1 left row shared the
	// same reference, making grouping undetermined. These require manual reconciliation.
	AmbiguousGroupCount int `json:"ambiguous_group_count,omitempty"`
	// SubsetSum match counts (subset_sum pass). Omitted when zero.
	SubsetSumMatchedCount   int `json:"subset_sum_matched_count,omitempty"`
	SubsetSumAmbiguousCount int `json:"subset_sum_ambiguous_count,omitempty"`
	SubsetSumSkippedCount   int `json:"subset_sum_skipped_count,omitempty"`

	// Monetary totals (all values in minor units, e.g. cents).
	// These are always populated regardless of --audit mode.
	MatchedAmountLeft    int64 `json:"matched_amount_left"`    // sum of left.Amount for all matched pairs (1-to-1 and grouped)
	MatchedAmountRight   int64 `json:"matched_amount_right"`   // sum of right.Amount for all matched pairs (1-to-1 and grouped)
	UnmatchedAmountLeft  int64 `json:"unmatched_amount_left"`  // sum of Amount for unmatched left transactions
	UnmatchedAmountRight int64 `json:"unmatched_amount_right"` // sum of Amount for unmatched right transactions
	AmountDiffTotal      int64 `json:"amount_diff_total"`      // sum of abs(DiffMinor) across all amount_diff pairs (1-to-1 and grouped)
	// AmbiguousAmountLeft/Right are monetary totals for rows in ambiguous groups.
	// Included in TotalDiscrepancy — they represent value requiring manual review.
	AmbiguousAmountLeft  int64 `json:"ambiguous_amount_left,omitempty"`
	AmbiguousAmountRight int64 `json:"ambiguous_amount_right,omitempty"`
	// TotalDiscrepancy = UnmatchedAmountLeft + UnmatchedAmountRight + AmountDiffTotal
	//                  + AmbiguousAmountLeft + AmbiguousAmountRight
	TotalDiscrepancy int64 `json:"total_discrepancy"`
}

// SourceSummary is the per-counterpart summary payload used by multi-source
// NDJSON results.
type SourceSummary struct {
	Source  string  `json:"source"`
	Summary Summary `json:"summary"`
}

// Result is the full output of a reconciliation run.
type Result struct {
	Schema         string           `json:"schema"`
	RunInfo        *RunInfo         `json:"run_info,omitempty"` // populated by --audit or --auto
	IndexSelection *IndexSelection  `json:"index_selection,omitempty"`
	PairName       string           `json:"pair"`
	LeftSource     string           `json:"left_source"`
	RightSource    string           `json:"right_source"`
	Summary        Summary          `json:"summary"`
	Matched        []MatchedPair    `json:"matched"`
	UnmatchedLeft  []Transaction    `json:"unmatched_left"`
	UnmatchedRight []Transaction    `json:"unmatched_right"`
	AmountDiff     []AmountDiffPair `json:"amount_diff"`
	TimingDiff     []TimingDiffPair `json:"timing_diff"`
	Duplicates     []DuplicateGroup `json:"duplicates"`
	// Grouped slices are populated by the one_to_many pass. Omitted when empty
	// so output remains backwards-compatible for runs without one_to_many.
	GroupedMatched    []GroupedMatchedPair    `json:"grouped_matched,omitempty"`
	GroupedAmountDiff []GroupedAmountDiffPair `json:"grouped_amount_diff,omitempty"`
	GroupedTimingDiff []GroupedTimingDiffPair `json:"grouped_timing_diff,omitempty"`
	// Many-to-many slices are populated by the many_to_many pass. Omitted when empty
	// so output remains backwards-compatible for runs without many_to_many.
	ManyToManyMatched    []ManyToManyMatchedPair    `json:"many_to_many_matched,omitempty"`
	ManyToManyAmountDiff []ManyToManyAmountDiffPair `json:"many_to_many_amount_diff,omitempty"`
	ManyToManyTimingDiff []ManyToManyTimingDiffPair `json:"many_to_many_timing_diff,omitempty"`
	// AmbiguousGroups holds reference groups where >1 left row shares a reference,
	// making grouping undetermined. These rows require manual reconciliation.
	AmbiguousGroups []AmbiguousGroupPair `json:"ambiguous_groups,omitempty"`
	// SubsetSum slices are populated by the subset_sum pass. Omitted when empty
	// so output remains backwards-compatible for runs without subset_sum.
	SubsetSumMatched   []SubsetSumMatchedPair   `json:"subset_sum_matched,omitempty"`
	SubsetSumAmbiguous []SubsetSumAmbiguousPair `json:"subset_sum_ambiguous,omitempty"`
	SubsetSumSkipped   []SubsetSumSkippedPair   `json:"subset_sum_skipped,omitempty"`
	// Warnings are non-fatal observations about the run (e.g. empty-currency rows
	// mixed with a non-empty base currency). They never affect matching or totals.
	Warnings []string `json:"warnings,omitempty"`
	// BySource gives a per-counterpart breakdown for 1-N source runs (see
	// ReconcileMultiSource), keyed by counterpart source name. Nil/empty for
	// ordinary single-counterpart runs. Summary above remains the aggregate
	// across all counterparts so single-number consumers are unaffected.
	BySource map[string]Summary `json:"by_source,omitempty"`
}

// ---------------------------------------------------------------------------
// Audit envelope types
// ---------------------------------------------------------------------------

// RunInfo carries provenance metadata for a reconciliation run.
// It is embedded in structured output formats (json, json-stream, ndjson) when
// --audit or --auto is set. It is not populated in the default path.
type RunInfo struct {
	RunID       string         `json:"run_id"`       // 16 hex chars derived from file hashes + timestamp
	Timestamp   time.Time      `json:"timestamp"`    // UTC wall-clock time captured before parsing began
	ToolVersion string         `json:"tool_version"` // set from build -ldflags Version variable
	LeftFile    FileInfo       `json:"left_file"`
	RightFile   FileInfo       `json:"right_file"`
	PairConfig  PairConfigSnap `json:"pair_config"`
	// InferredConfig and InferenceConfidence are populated by --auto so a
	// structured result contains everything needed to reproduce the run.
	InferredConfig      string                      `json:"inferred_config,omitempty"`
	InferenceConfidence []InferenceConfidenceSource `json:"inference_confidence,omitempty"`
}

// InferenceConfidenceSource records the confidence and lead for each mapping
// selected while building an inferred config.
type InferenceConfidenceSource struct {
	Source   string                `json:"source"`
	Mappings []InferenceConfidence `json:"mappings"`
}

// InferenceConfidence records one inferred mapping's selected column and gate metrics.
type InferenceConfidence struct {
	Role       string  `json:"role"`
	Column     string  `json:"column"`
	Confidence float64 `json:"confidence"`
	Lead       float64 `json:"lead"`
}

// FileInfo holds the resolved path, SHA-256 digest, and stat metadata of an input file.
type FileInfo struct {
	Path    string    `json:"path"`
	SHA256  string    `json:"sha256"` // lowercase hex, 64 characters
	Size    int64     `json:"size_bytes"`
	ModTime time.Time `json:"mod_time"`
}

// PairConfigSnap is a read-only snapshot of the pair's matching rules as they were
// applied during this run. Embedding it in the output means an auditor reading the
// file later knows exactly what tolerance and window were in effect.
type PairConfigSnap struct {
	DateWindow           string `json:"date_window"`
	AmountToleranceMinor int64  `json:"amount_tolerance_minor"`
	NameMode             string `json:"name_mode"`
}
