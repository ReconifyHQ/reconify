// Package financial evaluates source-local monetary expectations using checked,
// decimal arithmetic.
package financial

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/reconifyhq/reconify/config"
	"github.com/reconifyhq/reconify/engine/domain"
)

// Evaluate computes an expectation in minor units from a normalized
// transaction. Percentage rates are percentages (1.5 means 1.5%), and are
// rounded half-up on the magnitude of the base value.
func Evaluate(rule config.ExpectationCfg, tx domain.Transaction) (int64, error) {
	forms := 0
	if rule.Field != "" {
		forms++
	}
	if rule.Fixed != nil {
		forms++
	}
	if rule.Percentage != nil {
		forms++
	}
	if rule.FixedPlusPercentage != nil {
		forms++
	}
	if len(rule.Components) > 0 {
		forms++
	}
	if forms != 1 {
		return 0, fmt.Errorf("expectation must contain exactly one value form")
	}

	var value int64
	var err error
	switch {
	case rule.Field != "":
		var ok bool
		value, ok = tx.Financials[rule.Field]
		if !ok {
			return 0, fmt.Errorf("financial field %q is not present", rule.Field)
		}
	case rule.Fixed != nil:
		value = *rule.Fixed
	case rule.Percentage != nil:
		value, err = percentage(rule.Percentage.Base, rule.Percentage.Rate, tx)
	case rule.FixedPlusPercentage != nil:
		value, err = percentage(rule.FixedPlusPercentage.Percentage.Base, rule.FixedPlusPercentage.Percentage.Rate, tx)
		if err == nil {
			value, err = domain.CheckedAddInt64("fixed plus percentage", value, rule.FixedPlusPercentage.Fixed)
		}
	case len(rule.Components) > 0:
		for _, name := range rule.Components {
			part, ok := tx.Financials[name]
			if !ok {
				return 0, fmt.Errorf("financial component %q is not present", name)
			}
			value, err = domain.CheckedAddInt64("component sum", value, part)
			if err != nil {
				break
			}
		}
	}
	if err != nil {
		return 0, err
	}
	return value, nil
}

func percentage(baseName string, rate float64, tx domain.Transaction) (int64, error) {
	base, ok := tx.Financials[baseName]
	if !ok {
		return 0, fmt.Errorf("percentage base %q is not present", baseName)
	}
	if rate < 0 {
		return 0, fmt.Errorf("percentage rate must be non-negative")
	}
	baseAbs, err := domain.CheckedAbsInt64("percentage base", base)
	if err != nil {
		return 0, err
	}
	// Convert through the shortest decimal representation, then perform all
	// arithmetic as a rational. This avoids binary floating-point rounding.
	rateRat, ok := new(big.Rat).SetString(strconv.FormatFloat(rate, 'f', -1, 64))
	if !ok {
		return 0, fmt.Errorf("invalid percentage rate %v", rate)
	}
	rateRat.Quo(rateRat, big.NewRat(100, 1))
	n := new(big.Int).Mul(big.NewInt(baseAbs), rateRat.Num())
	d := rateRat.Denom()
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(n, d, rem)
	if new(big.Int).Lsh(new(big.Int).Set(rem), 1).Cmp(d) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("percentage result overflows int64")
	}
	return q.Int64(), nil
}
