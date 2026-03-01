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

// Summary holds aggregate counts and the match rate for a reconciliation run.
type Summary struct {
	TotalLeft       int     `json:"total_left"`
	TotalRight      int     `json:"total_right"`
	MatchedCount    int     `json:"matched"`
	UnmatchedLeft   int     `json:"unmatched_left"`
	UnmatchedRight  int     `json:"unmatched_right"`
	AmountDiffCount int     `json:"amount_diff_count"`
	TimingDiffCount int     `json:"timing_diff_count"`
	DuplicateCount  int     `json:"duplicate_count"`
	MatchRatePct    float64 `json:"match_rate_pct"`
}

// Result is the full output of a reconciliation run.
type Result struct {
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
}
