//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package output

import (
	"fmt"
	"io"
	"text/tabwriter"

	. "github.com/reconifyhq/reconify/engine/domain"
)

// ---------------------------------------------------------------------------
// TableWriter — buffers all rows, renders ASCII table via text/tabwriter on Flush().
// Not suitable for large datasets: warns at tableWarnThreshold rows.
// TableWriter does not implement RunInfoSetter — it is a human display tool only.
// ---------------------------------------------------------------------------

const tableWarnThreshold = 10_000

type tableRow struct {
	typ      string
	leftID   string
	leftDt   string
	leftAmt  string
	leftRef  string
	rightID  string
	rightDt  string
	rightAmt string
	rightRef string
	note     string
}

type tableWriter struct {
	w    io.Writer
	rows []tableRow
	warn bool
}

func newTableWriter(w io.Writer) *tableWriter { return &tableWriter{w: w} }

func (t *tableWriter) maybeWarn() {
	if !t.warn && len(t.rows) > tableWarnThreshold {
		t.warn = true
		if _, err := fmt.Fprintf(t.w, "warning: table mode has buffered >%d rows; for large files use --format=ndjson or --format=csv\n", tableWarnThreshold); err != nil {
			return
		}
	}
}

func (t *tableWriter) WriteMatch(pair MatchedPair) error {
	t.rows = append(t.rows, tableRow{
		typ:      "match",
		leftID:   pair.Left.ID,
		leftDt:   pair.Left.Date.Format("2006-01-02"),
		leftAmt:  fmtI64(pair.Left.Amount),
		leftRef:  pair.Left.Reference,
		rightID:  pair.Right.ID,
		rightDt:  pair.Right.Date.Format("2006-01-02"),
		rightAmt: fmtI64(pair.Right.Amount),
		rightRef: pair.Right.Reference,
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteAmountDiff(pair AmountDiffPair) error {
	t.rows = append(t.rows, tableRow{
		typ:      "amount_diff",
		leftID:   pair.Left.ID,
		leftDt:   pair.Left.Date.Format("2006-01-02"),
		leftAmt:  fmtI64(pair.Left.Amount),
		leftRef:  pair.Left.Reference,
		rightID:  pair.Right.ID,
		rightDt:  pair.Right.Date.Format("2006-01-02"),
		rightAmt: fmtI64(pair.Right.Amount),
		rightRef: pair.Right.Reference,
		note:     "diff=" + fmtI64(pair.DiffMinor),
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteTimingDiff(pair TimingDiffPair) error {
	t.rows = append(t.rows, tableRow{
		typ:      "timing_diff",
		leftID:   pair.Left.ID,
		leftDt:   pair.Left.Date.Format("2006-01-02"),
		leftAmt:  fmtI64(pair.Left.Amount),
		leftRef:  pair.Left.Reference,
		rightID:  pair.Right.ID,
		rightDt:  pair.Right.Date.Format("2006-01-02"),
		rightAmt: fmtI64(pair.Right.Amount),
		rightRef: pair.Right.Reference,
		note:     "days=" + fmtInt(pair.DaysDiff),
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteUnmatched(tx Transaction, side string) error {
	row := tableRow{
		typ:  "unmatched_" + side,
		note: "",
	}
	if side == "left" {
		row.leftID = tx.ID
		row.leftDt = tx.Date.Format("2006-01-02")
		row.leftAmt = fmtI64(tx.Amount)
		row.leftRef = tx.Reference
	} else {
		row.rightID = tx.ID
		row.rightDt = tx.Date.Format("2006-01-02")
		row.rightAmt = fmtI64(tx.Amount)
		row.rightRef = tx.Reference
	}
	t.rows = append(t.rows, row)
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteDuplicate(group DuplicateGroup) error {
	t.rows = append(t.rows, tableRow{
		typ:     "duplicate",
		leftRef: group.Reference,
		note:    "source=" + group.Source + " count=" + fmtInt(len(group.Transactions)),
	})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) WriteSummary(s Summary) error {
	t.rows = append(t.rows, tableRow{
		typ: "summary",
		note: fmt.Sprintf("matched=%d unmatched_left=%d unmatched_right=%d rate=%.1f%% reconciled_rate=%.1f%%",
			s.MatchedCount, s.UnmatchedLeft, s.UnmatchedRight, s.MatchRatePct, s.ReconciledRatePct),
	})
	return nil
}

func (t *tableWriter) WriteFinancialEffectMatch(f FinancialEffectFinding) error {
	return t.writeFinancial(f, "financial_effect_match")
}
func (t *tableWriter) WriteFinancialEffectDiff(f FinancialEffectFinding) error {
	return t.writeFinancial(f, "financial_effect_diff")
}
func (t *tableWriter) WriteFinancialUnchecked(f FinancialEffectFinding) error {
	return t.writeFinancial(f, "financial_unchecked")
}
func (t *tableWriter) WriteSettlementMatch(f SettlementFinding) error {
	return t.writeFinancial(f, "settlement_match")
}
func (t *tableWriter) WriteSettlementDiff(f SettlementFinding) error {
	return t.writeFinancial(f, "settlement_diff")
}
func (t *tableWriter) writeFinancial(f FinancialEffectFinding, typ string) error {
	t.rows = append(t.rows, tableRow{typ: typ, leftID: f.Transaction.ID, leftDt: f.Transaction.Date.Format("2006-01-02"), leftAmt: fmtI64(f.Transaction.Amount), leftRef: f.Transaction.Reference, note: fmt.Sprintf("field=%s actual=%d expected=%d diff=%d status=%s", f.Check.Field, f.Check.Actual, f.Check.Expected, f.Check.DiffMinor, f.Check.Status)})
	t.maybeWarn()
	return nil
}

func (t *tableWriter) Flush() error {
	tw := tabwriter.NewWriter(t.w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "TYPE\tLEFT_ID\tLEFT_DATE\tLEFT_AMT\tLEFT_REF\tRIGHT_ID\tRIGHT_DATE\tRIGHT_AMT\tRIGHT_REF\tNOTE"); err != nil {
		return err
	}
	for _, row := range t.rows {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.typ, row.leftID, row.leftDt, row.leftAmt, row.leftRef,
			row.rightID, row.rightDt, row.rightAmt, row.rightRef, row.note); err != nil {
			return err
		}
	}
	return tw.Flush()
}
