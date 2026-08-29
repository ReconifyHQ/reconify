//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package output

import (
	"encoding/csv"
	"io"
	"strconv"
	"time"

	. "github.com/reconifyhq/reconify/engine/domain"
)

// ---------------------------------------------------------------------------
// CSVWriter — fixed schema, one row per event. O(1) memory. Versioned contract.
//
// Column order:
//
//	type, left_id, left_date, left_amount_minor, left_ref, left_name, left_currency,
//	right_id, right_date, right_amount_minor, right_ref, right_name, right_currency,
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
	"left_id", "left_date", "left_amount_minor", "left_ref", "left_name", "left_currency",
	"right_id", "right_date", "right_amount_minor", "right_ref", "right_name", "right_currency",
	"diff_minor", "days_diff",
	"source", "reference", "dup_count",
	"total_left", "total_right", "matched", "unmatched_left", "unmatched_right",
	"amount_diff_count", "timing_diff_count", "duplicate_count", "match_rate_pct",
	"matched_amount_left", "matched_amount_right", "unmatched_amount_left",
	"unmatched_amount_right", "amount_diff_total", "total_discrepancy", "reconciled_rate_pct",
	"financial_field", "financial_actual", "financial_expected", "financial_diff_minor", "financial_status",
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
	row[6] = SanitizeCSVField(tx.Currency)
}

func txRight(row []string, tx Transaction) {
	row[7] = tx.ID
	row[8] = fmtDate(tx.Date)
	row[9] = fmtI64(tx.Amount)
	row[10] = SanitizeCSVField(tx.Reference)
	row[11] = SanitizeCSVField(tx.Name)
	row[12] = SanitizeCSVField(tx.Currency)
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
	row[13] = fmtI64(pair.DiffMinor)
	return c.w.Write(row)
}

func (c *csvWriter) WriteTimingDiff(pair TimingDiffPair) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("timing_diff")
	txLeft(row, pair.Left)
	txRight(row, pair.Right)
	row[14] = fmtInt(pair.DaysDiff)
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
	row[15] = group.Source
	row[16] = SanitizeCSVField(group.Reference)
	row[17] = fmtInt(len(group.Transactions))
	return c.w.Write(row)
}

func (c *csvWriter) WriteSummary(s Summary) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow("summary")
	row[18] = fmtInt(s.TotalLeft)
	row[19] = fmtInt(s.TotalRight)
	row[20] = fmtInt(s.MatchedCount)
	row[21] = fmtInt(s.UnmatchedLeft)
	row[22] = fmtInt(s.UnmatchedRight)
	row[23] = fmtInt(s.AmountDiffCount)
	row[24] = fmtInt(s.TimingDiffCount)
	row[25] = fmtInt(s.DuplicateCount)
	row[26] = strconv.FormatFloat(s.MatchRatePct, 'f', 2, 64)
	row[27] = fmtI64(s.MatchedAmountLeft)
	row[28] = fmtI64(s.MatchedAmountRight)
	row[29] = fmtI64(s.UnmatchedAmountLeft)
	row[30] = fmtI64(s.UnmatchedAmountRight)
	row[31] = fmtI64(s.AmountDiffTotal)
	row[32] = fmtI64(s.TotalDiscrepancy)
	row[33] = strconv.FormatFloat(s.ReconciledRatePct, 'f', 2, 64)
	return c.w.Write(row)
}

func (c *csvWriter) WriteFinancialEffectMatch(f FinancialEffectFinding) error {
	return c.writeFinancial(f, "financial_effect_match")
}
func (c *csvWriter) WriteFinancialEffectDiff(f FinancialEffectFinding) error {
	return c.writeFinancial(f, "financial_effect_diff")
}
func (c *csvWriter) WriteFinancialUnchecked(f FinancialEffectFinding) error {
	return c.writeFinancial(f, "financial_unchecked")
}
func (c *csvWriter) WriteSettlementMatch(f SettlementFinding) error {
	return c.writeFinancial(f, "settlement_match")
}
func (c *csvWriter) WriteSettlementDiff(f SettlementFinding) error {
	return c.writeFinancial(f, "settlement_diff")
}

func (c *csvWriter) writeFinancial(f FinancialEffectFinding, typ string) error {
	if err := c.writeHeader(); err != nil {
		return err
	}
	row := emptyRow(typ)
	txLeft(row, f.Transaction)
	row[15] = SanitizeCSVField(f.Transaction.Source)
	row[34] = SanitizeCSVField(f.Check.Field)
	row[35] = fmtI64(f.Check.Actual)
	row[36] = fmtI64(f.Check.Expected)
	row[37] = fmtI64(f.Check.DiffMinor)
	row[38] = f.Check.Status
	return c.w.Write(row)
}

func (c *csvWriter) Flush() error {
	c.w.Flush()
	return c.w.Error()
}
