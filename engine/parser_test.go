package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func TestParseCSVEach_WideSchema_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.csv")
	content := strings.Join([]string{
		"date,description,amount,ref_id,currency,account_id,customer_id,branch_code,txn_type,status,channel,country,batch_id,line_no,value_date,booking_ts,balance_after_minor,processor_hint",
		`2024-01-31,"Invoice 123, Retail Segment","1,234.56",REF-001,USD,ACCT-1,CUST-1,NYC01,debit,posted,api,US,BATCH-1,1,2024-01-30,2024-01-31T09:31:00Z,100000,fixture`,
		`2024-02-01,"Chargeback, Card Present","(45.10)",REF-002,USD,ACCT-2,CUST-2,LON02,credit,cleared,mobile,GB,BATCH-1,2,2024-01-31,2024-02-01T09:31:00Z,99955,fixture`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Thousands:   ",",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
	}

	var got []Transaction
	err := ParseCSVEach(context.Background(), "bank", path, cfg, func(tx Transaction, _ int) error {
		got = append(got, tx)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseCSVEach error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(got)=%d, want 2", len(got))
	}
	if got[0].Amount != 123456 {
		t.Errorf("row1 amount=%d, want 123456", got[0].Amount)
	}
	if got[1].Amount != -4510 {
		t.Errorf("row2 amount=%d, want -4510", got[1].Amount)
	}
	if got[0].Reference != "REF-001" || got[1].Reference != "REF-002" {
		t.Errorf("unexpected refs: %q %q", got[0].Reference, got[1].Reference)
	}
	if len(got[0].Raw) < 10 {
		t.Errorf("expected raw map with many fields, got %+v", got[0].Raw)
	}
}

func TestParseCSVEach_SkipRaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.csv")
	content := "date,description,amount,ref_id,currency,account_id,customer_id,branch_code,txn_type,status,channel,country\n" +
		"2024-01-31,Payment A,100.00,REF-001,USD,ACCT-1,CUST-1,NYC01,debit,posted,api,US\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
		SkipRaw:     true,
	}

	var one Transaction
	err := ParseCSVEach(context.Background(), "bank", path, cfg, func(tx Transaction, _ int) error {
		one = tx
		return nil
	})
	if err != nil {
		t.Fatalf("ParseCSVEach error: %v", err)
	}
	if one.Raw != nil {
		t.Fatalf("expected Raw=nil when skip_raw=true, got %+v", one.Raw)
	}
}

func TestParseAmount_Precision(t *testing.T) {
	tests := []struct {
		name       string
		amount     string
		decimal    string
		thousands  string
		multiplier int64
		want       int64
	}{
		{
			name:       "large integer beyond float64 53-bit mantissa",
			amount:     "9007199254740993", // 2^53 + 1, not exactly representable as float64
			decimal:    ".",
			multiplier: 1,
			want:       9007199254740993,
		},
		{
			name:       "large amount with cents beyond float64 precision",
			amount:     "123456789012345.67",
			decimal:    ".",
			multiplier: 100,
			want:       12345678901234567,
		},
		{
			name:       "ordinary cents rounding",
			amount:     "19.995",
			decimal:    ".",
			multiplier: 100,
			want:       2000, // rounds half away from zero
		},
		{
			name:       "negative with parentheses-stripped sign",
			amount:     "-1234.56",
			decimal:    ".",
			multiplier: 100,
			want:       -123456,
		},
		{
			name:       "thousands separator removed",
			amount:     "1,234,567.89",
			decimal:    ".",
			thousands:  ",",
			multiplier: 100,
			want:       123456789,
		},
		{
			// fracVal*multiplier (999999999999999999 * 100 ≈ 1e20) overflows int64
			// (~9.22e18) if computed with plain int64 multiplication. The fix uses
			// math/bits 128-bit multiply/divide so this rounds correctly instead of
			// silently wrapping to a wrong (and wrong-signed) amount.
			name:       "18 fractional digits does not overflow int64 multiply",
			amount:     "0.999999999999999999",
			decimal:    ".",
			multiplier: 100,
			want:       100, // rounds up to 1.00 in minor units
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAmount(tc.amount, tc.decimal, tc.thousands, tc.multiplier)
			if err != nil {
				t.Fatalf("parseAmount(%q) error: %v", tc.amount, err)
			}
			if got != tc.want {
				t.Errorf("parseAmount(%q) = %d, want %d", tc.amount, got, tc.want)
			}
		})
	}
}

func TestParseCSVEach_DirtyDataErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "invalid date",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-99-99,Payment A,100.00,REF-1,USD\n",
			want: "invalid date",
		},
		{
			name: "empty date",
			content: "date,description,amount,ref_id,currency\n" +
				",Payment A,100.00,REF-1,USD\n",
			want: "date column",
		},
		{
			name: "bad amount",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-01-01,Payment A,12O0.50,REF-1,USD\n",
			want: "parse amount",
		},
		{
			name: "empty amount",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-01-01,Payment A,,REF-1,USD\n",
			want: "amount column",
		},
		{
			name: "malformed csv",
			content: "date,description,amount,ref_id,currency\n" +
				"2024-01-01,\"broken,100.00,REF-1,USD\n",
			want: "row",
		},
	}

	cfg := config.CSVParserCfg{
		Type:        "csv",
		DateCol:     "date",
		DateLayout:  "2006-01-02",
		AmountCol:   "amount",
		Decimal:     ".",
		Multiplier:  100,
		CurrencyCol: "currency",
		RefCol:      "ref_id",
		NameCol:     "description",
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "input.csv")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			err := ParseCSVEach(context.Background(), "bank", path, cfg, func(_ Transaction, _ int) error {
				return nil
			})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}
