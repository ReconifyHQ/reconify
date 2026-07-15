package engine

import (
	"math"
	"testing"
)

func TestCheckedArithmeticRejectsOverflow(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"int64 addition", func() error { _, err := checkedAddInt64("total", math.MaxInt64, 1); return err }},
		{"int addition", func() error { _, err := checkedAddInt("count", math.MaxInt, 1); return err }},
		{"multiplication", func() error { _, err := checkedMulInt64("estimate", math.MaxInt64, 2); return err }},
		{"absolute minimum", func() error { _, err := checkedAbsInt64("difference", math.MinInt64); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); err == nil {
				t.Fatal("expected overflow error")
			}
		})
	}
}

func TestBuildSummaryRejectsMinimumDifference(t *testing.T) {
	_, err := buildSummary(1, 1, &Result{AmountDiff: []AmountDiffPair{{DiffMinor: math.MinInt64}}}, "USD")
	if err == nil {
		t.Fatal("expected summary overflow error")
	}
}
