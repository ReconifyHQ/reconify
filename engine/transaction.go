// Package engine provides the core reconciliation types and logic.
package engine

import "time"

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

// DuplicateGroup is a set of transactions in the same source sharing the same reference.
type DuplicateGroup struct {
	Source       string        `json:"source"`
	Reference    string        `json:"reference"`
	Transactions []Transaction `json:"transactions"`
}

// Summary holds aggregate counts, match rate, and monetary totals for a reconciliation run.
type Summary struct {
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
	// ReconciledRatePct is (matched + amount_diff + timing_diff) / total. MatchRatePct
	// only counts exact matches, so a run that is 100% reconciled but entirely within
	// AmountDiff/TimingDiff tolerance still reports MatchRatePct=0; use this field instead.
	ReconciledRatePct float64 `json:"reconciled_rate_pct"`

	// Monetary totals (all values in minor units, e.g. cents).
	// These are always populated regardless of --audit mode.
	MatchedAmountLeft    int64 `json:"matched_amount_left"`    // sum of left.Amount for all matched pairs
	MatchedAmountRight   int64 `json:"matched_amount_right"`   // sum of right.Amount for all matched pairs
	UnmatchedAmountLeft  int64 `json:"unmatched_amount_left"`  // sum of Amount for unmatched left transactions
	UnmatchedAmountRight int64 `json:"unmatched_amount_right"` // sum of Amount for unmatched right transactions
	AmountDiffTotal      int64 `json:"amount_diff_total"`      // sum of abs(DiffMinor) across all amount_diff pairs
	TotalDiscrepancy     int64 `json:"total_discrepancy"`      // UnmatchedAmountLeft + UnmatchedAmountRight + AmountDiffTotal
}

// Result is the full output of a reconciliation run.
type Result struct {
	RunInfo        *RunInfo         `json:"run_info,omitempty"` // nil unless --audit mode
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
// the --audit flag is set. It is never populated in the default path.
type RunInfo struct {
	RunID       string         `json:"run_id"`       // 16 hex chars derived from file hashes + timestamp
	Timestamp   time.Time      `json:"timestamp"`    // UTC wall-clock time captured before parsing began
	ToolVersion string         `json:"tool_version"` // set from build -ldflags Version variable
	LeftFile    FileInfo       `json:"left_file"`
	RightFile   FileInfo       `json:"right_file"`
	PairConfig  PairConfigSnap `json:"pair_config"`
}

// FileInfo holds the resolved path and SHA-256 digest of an input file.
type FileInfo struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"` // lowercase hex, 64 characters
}

// PairConfigSnap is a read-only snapshot of the pair's matching rules as they were
// applied during this run. Embedding it in the output means an auditor reading the
// file later knows exactly what tolerance and window were in effect.
type PairConfigSnap struct {
	DateWindow           string `json:"date_window"`
	AmountToleranceMinor int64  `json:"amount_tolerance_minor"`
	NameMode             string `json:"name_mode"`
}
