package engine

import (
	"encoding/csv"
	"io"
	"strconv"
	"time"
)

// ---------------------------------------------------------------------------
// CSVWriter — fixed schema, one row per event. O(1) memory. Versioned contract.
//
// Column order:
//
//	type, left_id, left_date, left_amount_minor, left_ref, left_name,
//	right_id, right_date, right_amount_minor, right_ref, right_name,
//	diff_minor, days_diff,
//	source, reference, dup_count,
//	total_left, total_right, matched, unmatched_left, unmatched_right,
//	amount_diff_count, timing_diff_count, duplicate_count, match_rate_pct,
//	matched_amount_left, matched_amount_right, unmatched_amount_left,
//	unmatched_amount_right, amount_diff_total, total_discrepancy, reconciled_rate_pct
//
// Unused columns for a given event type are empty string.
// CSVWriter does not implement RunInfoSetter — audit users should use json formats.
// ---------------------------------------------------------------------------

var csvHeader = []string{
	"type",
	"left_id", "left_date", "left_amount_minor", "left_ref", "left_name",
	"right_id", "right_date", "right_amount_minor", "right_ref", "right_name",
	"diff_minor", "days_diff",
	"source", "reference", "dup_count",
	"total_left", "total_right", "matched", "unmatched_left", "unmatched_right",
	"amount_diff_count", "timing_diff_count", "duplicate_count", "match_rate_pct",
	"matched_amount_left", "matched_amount_right", "unmatched_amount_left",
	"unmatched_amount_right", "amount_diff_total", "total_discrepancy", "reconciled_rate_pct",
}

type csvWriter struct {
	w       *csv.Writer
	started bool
}

func newCSVWriter(w io.Writer) *csvWriter {
	return &csvWriter{w: csv.NewWriter(w)}
}

func (c *csvWriter) writeHeader() error {
	if c.started {
		return nil
	}
	c.started = true
	return c.w.Write(csvHeader)
}

// emptyRow returns a slice of empty strings with len == len(csvHeader).
func emptyRow(typ string) []string {
	row := make([]string, len(csvHeader))
	row[0] = typ
	return row
}

func fmtDate(t time.Time) string { return t.Format(time.RFC3339) }
func fmtI64(v int64) string      { return strconv.FormatInt(v, 10) }
func fmtInt(v int) string        { return strconv.Itoa(v) }

func firstTransactionID(txns []Transaction) string {
	if len(txns) == 0 {
		return ""
	}
	return txns[0].ID
}

// SanitizeCSVField prevents spreadsheet formula injection by prefixing cells
// that start with a formula trigger character with a single quote.
func SanitizeCSVField(s string) string {
	if len(s) == 0 {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func txLeft(row []string, tx Transaction) {
	row[1] = tx.ID
	row[2] = fmtDate(tx.Date)
	row[3] = fmtI64(tx.Amount)
	row[4] = SanitizeCSVField(tx.Reference)
	row[5] = SanitizeCSVField(tx.Name)
}

func txRight(row []string, tx Transaction) {
	row[6] = tx.ID
	row[7] = fmtDate(tx.Date)
	row[8] = fmtI64(tx.Amount)
	row[9] = SanitizeCSVField(tx.Reference)
	row[10] = SanitizeCSVField(tx.Name)
}

func (c *csvWriter) WriteMatch(pair MatchedPair) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("match")
	txLeft(row, pair.Left)
	txRight(row, pair.Right)
	return c.w.Write(row)
}

func (c *csvWriter) WriteAmountDiff(pair AmountDiffPair) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("amount_diff")
	txLeft(row, pair.Left)
	txRight(row, pair.Right)
	row[11] = fmtI64(pair.DiffMinor)
	return c.w.Write(row)
}

func (c *csvWriter) WriteTimingDiff(pair TimingDiffPair) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("timing_diff")
	txLeft(row, pair.Left)
	txRight(row, pair.Right)
	row[12] = fmtInt(pair.DaysDiff)
	return c.w.Write(row)
}

func (c *csvWriter) WriteUnmatched(tx Transaction, side string) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	typ := "unmatched_" + side
	row := emptyRow(typ)
	if side == "left" {
		txLeft(row, tx)
	} else {
		txRight(row, tx)
	}
	return c.w.Write(row)
}

func (c *csvWriter) WriteDuplicate(group DuplicateGroup) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("duplicate")
	row[13] = group.Source
	row[14] = SanitizeCSVField(group.Reference)
	row[15] = fmtInt(len(group.Transactions))
	return c.w.Write(row)
}

func (c *csvWriter) WriteSummary(s Summary) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("summary")
	row[16] = fmtInt(s.TotalLeft)
	row[17] = fmtInt(s.TotalRight)
	row[18] = fmtInt(s.MatchedCount)
	row[19] = fmtInt(s.UnmatchedLeft)
	row[20] = fmtInt(s.UnmatchedRight)
	row[21] = fmtInt(s.AmountDiffCount)
	row[22] = fmtInt(s.TimingDiffCount)
	row[23] = fmtInt(s.DuplicateCount)
	row[24] = strconv.FormatFloat(s.MatchRatePct, 'f', 2, 64)
	row[25] = fmtI64(s.MatchedAmountLeft)
	row[26] = fmtI64(s.MatchedAmountRight)
	row[27] = fmtI64(s.UnmatchedAmountLeft)
	row[28] = fmtI64(s.UnmatchedAmountRight)
	row[29] = fmtI64(s.AmountDiffTotal)
	row[30] = fmtI64(s.TotalDiscrepancy)
	row[31] = strconv.FormatFloat(s.ReconciledRatePct, 'f', 2, 64)
	return c.w.Write(row)
}

func (c *csvWriter) Flush() error {
	c.w.Flush()
	return c.w.Error()
}
