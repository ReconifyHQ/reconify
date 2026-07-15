package domain

import (
	"math"
	"testing"
)

func TestCheckedArithmeticRejectsOverflow(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"int64 addition", func() error { _, err := CheckedAddInt64("total", math.MaxInt64, 1); return err }},
		{"int addition", func() error { _, err := CheckedAddInt("count", math.MaxInt, 1); return err }},
		{"multiplication", func() error { _, err := CheckedMulInt64("estimate", math.MaxInt64, 2); return err }},
		{"absolute minimum", func() error { _, err := CheckedAbsInt64("difference", math.MinInt64); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); err == nil {
				t.Fatal("expected overflow error")
			}
		})
	}
}
