package financial

import (
	"math"
	"math/big"
	"testing"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine/domain"
)

func TestEvaluatePercentageMatchesIndependentBigIntOracle(t *testing.T) {
	for i := 0; i < 1000; i++ {
		base := int64((i*7919)%2_000_000_000 - 1_000_000_000)
		rate := float64((i*3571)%10000) / 100
		got, err := Evaluate(config.ExpectationCfg{Percentage: &config.PercentageCfg{Base: "gross", Rate: rate}}, domain.Transaction{Financials: map[string]int64{"gross": base}})
		if err != nil {
			t.Fatalf("case %d: unexpected error: %v", i, err)
		}
		// Independent oracle: |base| * rate / 100, rounded half-up.
		rateRat := new(big.Rat).SetFloat64(rate)
		n := new(big.Int).Mul(big.NewInt(absForTest(base)), rateRat.Num())
		d := new(big.Int).Mul(rateRat.Denom(), big.NewInt(100))
		want, rem := new(big.Int), new(big.Int)
		want.QuoRem(n, d, rem)
		if new(big.Int).Lsh(new(big.Int).Set(rem), 1).Cmp(d) >= 0 {
			want.Add(want, big.NewInt(1))
		}
		if got != want.Int64() {
			t.Fatalf("case %d: base=%d rate=%v got=%d want=%d", i, base, rate, got, want.Int64())
		}
	}
}

func absForTest(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestEvaluateGoldenArithmetic(t *testing.T) {
	tests := []struct {
		name string
		rule config.ExpectationCfg
		want int64
	}{
		{"percentage", config.ExpectationCfg{Percentage: &config.PercentageCfg{Base: "gross", Rate: 1.5}}, 150},
		{"fixed plus percentage", config.ExpectationCfg{FixedPlusPercentage: &config.FixedPlusPercentageCfg{Fixed: 100, Percentage: config.PercentageCfg{Base: "gross", Rate: 1.5}}}, 250},
		{"half up", config.ExpectationCfg{Percentage: &config.PercentageCfg{Base: "gross", Rate: 1.5}}, 2},
		{"negative base magnitude", config.ExpectationCfg{Percentage: &config.PercentageCfg{Base: "gross", Rate: 0.5}}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := int64(10000)
			if tc.name == "half up" {
				base = 101
			}
			if tc.name == "negative base magnitude" {
				base = -100
			}
			got, err := Evaluate(tc.rule, domain.Transaction{Financials: map[string]int64{"gross": base}})
			if err != nil || got != tc.want {
				t.Fatalf("Evaluate() = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestEvaluateRejectsOverflow(t *testing.T) {
	_, err := Evaluate(config.ExpectationCfg{Percentage: &config.PercentageCfg{Base: "gross", Rate: 100}}, domain.Transaction{Financials: map[string]int64{"gross": math.MinInt64}})
	if err == nil {
		t.Fatal("expected contextual overflow error")
	}
}

func TestEvaluateFieldAndComponentSum(t *testing.T) {
	tx := domain.Transaction{Financials: map[string]int64{"fee": 150, "tax": 20, "scheme": 5}}
	field, err := Evaluate(config.ExpectationCfg{Field: "fee"}, tx)
	if err != nil || field != 150 {
		t.Fatalf("field expectation = %d, %v", field, err)
	}
	sum, err := Evaluate(config.ExpectationCfg{Components: []string{"fee", "tax", "scheme"}}, tx)
	if err != nil || sum != 175 {
		t.Fatalf("component sum = %d, %v; want 175", sum, err)
	}
}
