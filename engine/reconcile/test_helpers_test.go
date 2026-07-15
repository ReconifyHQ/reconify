//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/xuri/excelize/v2"
)

type captureWriter struct {
	matches     []MatchedPair
	amountDiffs []AmountDiffPair
	timingDiffs []TimingDiffPair
	duplicates  []DuplicateGroup
	summaries   []Summary
}

func (c *captureWriter) WriteMatch(p MatchedPair) error { c.matches = append(c.matches, p); return nil }
func (c *captureWriter) WriteAmountDiff(p AmountDiffPair) error {
	c.amountDiffs = append(c.amountDiffs, p)
	return nil
}
func (c *captureWriter) WriteTimingDiff(p TimingDiffPair) error {
	c.timingDiffs = append(c.timingDiffs, p)
	return nil
}
func (c *captureWriter) WriteUnmatched(Transaction, string) error { return nil }
func (c *captureWriter) WriteDuplicate(p DuplicateGroup) error {
	c.duplicates = append(c.duplicates, p)
	return nil
}
func (c *captureWriter) WriteSummary(p Summary) error {
	c.summaries = append(c.summaries, p)
	return nil
}
func (c *captureWriter) Flush() error { return nil }

func writeSyntheticCSV(tb testing.TB, rows int) string {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "synthetic.csv")
	//nolint:gosec // path is derived from testing.TB.TempDir.
	f, err := os.Create(path) // #nosec G304 -- path is under testing.TB.TempDir()
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("date,amount,currency,reference,name\n"); err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if _, err := f.WriteString("2024-01-01,1.00,USD,REF-" + itoa(i) + ",Test\n"); err != nil {
			tb.Fatal(err)
		}
	}
	return path
}

func itoa(v int) string { return fmt.Sprintf("%d", v) }

func baseParserCfg(parserType string) config.ParserCfg {
	return config.ParserCfg{Type: parserType, DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Decimal: ".", Thousands: ",", Multiplier: 100, CurrencyCol: "currency", RefCol: "ref_id", NameCol: "description"}
}

func writeWorkbook(t *testing.T, path, sheet string, rows [][]string) {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if sheet != "Sheet1" {
		if _, err := f.NewSheet(sheet); err != nil {
			t.Fatal(err)
		}
	}
	for row, values := range rows {
		for col, value := range values {
			cell, err := excelize.CoordinatesToCellName(col+1, row+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue(sheet, cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}
