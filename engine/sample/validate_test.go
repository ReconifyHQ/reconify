package sample

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/reconifyhq/reconify/config"
)

func TestValidateCollectsSampleFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.csv")
	if err := os.WriteFile(path, []byte("date,amount\n2024-01-01,10.00\n01/02/2024,nope\n2024-01-03,12.00\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Validate(context.Background(), path, config.ParserCfg{Type: "csv", DateCol: "date", DateLayout: "2006-01-02", AmountCol: "amount", Multiplier: 100}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.RowsScanned != 2 || got.SuccessfulRows != 1 || len(got.Errors) != 1 || got.Errors[0].Row != 2 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if !got.Truncated {
		t.Fatal("expected truncated scan")
	}
}
