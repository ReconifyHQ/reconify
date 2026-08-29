//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/config"
	. "github.com/reconifyhq/reconify/engine/domain"
	"github.com/reconifyhq/reconify/engine/financial"
)

type rowNormalizer struct {
	sourceName   string
	filePath     string
	cfg          config.ParserCfg
	loc          *time.Location
	decimal      string
	multiplier   int64
	dateCache    map[string]time.Time
	cacheEnabled bool
}

func newRowNormalizer(sourceName, filePath string, cfg config.ParserCfg) *rowNormalizer {
	loc := time.UTC
	if cfg.TZ != "" {
		if l, err := time.LoadLocation(cfg.TZ); err == nil {
			loc = l
		}
	}

	decimal := "."
	if cfg.Decimal != "" {
		decimal = cfg.Decimal
	}

	multiplier := cfg.Multiplier
	if multiplier <= 0 {
		multiplier = 1
	}

	return &rowNormalizer{
		sourceName:   sourceName,
		filePath:     filePath,
		cfg:          cfg,
		loc:          loc,
		decimal:      decimal,
		multiplier:   multiplier,
		dateCache:    make(map[string]time.Time),
		cacheEnabled: true,
	}
}

func (n *rowNormalizer) fromRecord(headers, record []string, idRowNum, fileRowNum int) (Transaction, error) {
	values := make(map[string]string, len(headers))
	for i, h := range headers {
		if i < len(record) {
			values[h] = record[i]
		} else {
			values[h] = ""
		}
	}
	return n.fromMap(values, idRowNum, fileRowNum)
}

func (n *rowNormalizer) fromMap(values map[string]string, idRowNum, fileRowNum int) (Transaction, error) {
	dateStr := strings.TrimSpace(getMapCol(values, n.cfg.DateCol))
	if dateStr == "" {
		return Transaction{}, fmt.Errorf("%s: row %d: source %q: date column %q is empty",
			n.filePath, fileRowNum, n.sourceName, n.cfg.DateCol)
	}

	var date time.Time
	if n.cacheEnabled {
		if t, ok := n.dateCache[dateStr]; ok {
			date = t
		} else {
			t, parseErr := time.ParseInLocation(n.cfg.DateLayout, dateStr, n.loc)
			if parseErr != nil {
				return Transaction{}, fmt.Errorf("%s: row %d: source %q: invalid date %q with layout %q: %w",
					n.filePath, fileRowNum, n.sourceName, dateStr, n.cfg.DateLayout, parseErr)
			}
			date = t
			if len(n.dateCache) >= 1000 {
				n.cacheEnabled = false
				n.dateCache = nil
			} else {
				n.dateCache[dateStr] = t
			}
		}
	} else {
		t, parseErr := time.ParseInLocation(n.cfg.DateLayout, dateStr, n.loc)
		if parseErr != nil {
			return Transaction{}, fmt.Errorf("%s: row %d: source %q: invalid date %q with layout %q: %w",
				n.filePath, fileRowNum, n.sourceName, dateStr, n.cfg.DateLayout, parseErr)
		}
		date = t
	}

	amtStr := getMapCol(values, n.cfg.AmountCol)
	if amtStr == "" {
		return Transaction{}, fmt.Errorf("%s: row %d: source %q: amount column %q is empty",
			n.filePath, fileRowNum, n.sourceName, n.cfg.AmountCol)
	}
	amount, err := parseAmount(amtStr, n.decimal, n.cfg.Thousands, n.multiplier)
	if err != nil {
		return Transaction{}, fmt.Errorf("%s: row %d: source %q: parse amount %q: %w",
			n.filePath, fileRowNum, n.sourceName, amtStr, err)
	}
	var financials map[string]int64
	if n.cfg.Financials != nil {
		financials = make(map[string]int64, len(n.cfg.Financials.Fields)+2)
		fields := make(map[string]string, len(n.cfg.Financials.Fields)+2)
		for name, col := range n.cfg.Financials.Fields {
			fields[name] = col
		}
		if n.cfg.Financials.GrossCol != "" {
			fields["gross"] = n.cfg.Financials.GrossCol
		}
		if n.cfg.Financials.NetCol != "" {
			fields["net"] = n.cfg.Financials.NetCol
		}
		for name, col := range fields {
			value := strings.TrimSpace(getMapCol(values, col))
			if value == "" {
				return Transaction{}, fmt.Errorf("%s: row %d: source %q: financial column %q (%s) is empty", n.filePath, fileRowNum, n.sourceName, col, name)
			}
			parsed, parseErr := parseAmount(value, n.decimal, n.cfg.Thousands, n.multiplier)
			if parseErr != nil {
				return Transaction{}, fmt.Errorf("%s: row %d: source %q: parse financial %q (%s): %w", n.filePath, fileRowNum, n.sourceName, value, name, parseErr)
			}
			financials[name] = parsed
		}
	}
	var financialChecks []FinancialCheck
	if n.cfg.Financials != nil {
		fields := make([]string, 0, len(financials))
		for field := range financials {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			actual := financials[field]
			rule, configured := n.cfg.Financials.Expectations[field]
			if !configured {
				financialChecks = append(financialChecks, FinancialCheck{Field: field, Actual: actual, Status: "unchecked"})
				continue
			}
			candidate := Transaction{Financials: financials}
			expected, evalErr := financial.Evaluate(rule, candidate)
			if evalErr != nil {
				return Transaction{}, fmt.Errorf("%s: row %d: source %q: financial expectation %q: %w", n.filePath, fileRowNum, n.sourceName, field, evalErr)
			}
			diff, diffErr := CheckedAddInt64("financial difference", actual, -expected)
			if diffErr != nil {
				return Transaction{}, fmt.Errorf("%s: row %d: financial expectation %q: %w", n.filePath, fileRowNum, field, diffErr)
			}
			status := "diff"
			if diff <= rule.ToleranceMinor && diff >= -rule.ToleranceMinor {
				status = "match"
			}
			financialChecks = append(financialChecks, FinancialCheck{Field: field, Actual: actual, Expected: expected, DiffMinor: diff, ToleranceMinor: rule.ToleranceMinor, Status: status})
		}
		if _, hasGross := financials["gross"]; hasGross {
			if net, hasNet := financials["net"]; hasNet {
				expectedNet := financials["gross"]
				settlementTolerance := int64(0)
				for field, rule := range n.cfg.Financials.Expectations {
					if field == "gross" || field == "net" {
						continue
					}
					fee, exists := financials[field]
					if !exists {
						continue
					}
					if rule.Operation == "add" {
						expectedNet, err = CheckedAddInt64("settlement expected net", expectedNet, fee)
					} else {
						expectedNet, err = CheckedAddInt64("settlement expected net", expectedNet, -fee)
					}
					if err != nil {
						return Transaction{}, fmt.Errorf("%s: row %d: source %q: settlement: %w", n.filePath, fileRowNum, n.sourceName, err)
					}
					if rule.ToleranceMinor > settlementTolerance {
						settlementTolerance = rule.ToleranceMinor
					}
				}
				diff, diffErr := CheckedAddInt64("settlement difference", net, -expectedNet)
				if diffErr != nil {
					return Transaction{}, fmt.Errorf("%s: row %d: source %q: settlement: %w", n.filePath, fileRowNum, n.sourceName, diffErr)
				}
				status := "diff"
				if diff <= settlementTolerance && diff >= -settlementTolerance {
					status = "match"
				}
				financialChecks = append(financialChecks, FinancialCheck{Field: "settlement", Actual: net, Expected: expectedNet, DiffMinor: diff, ToleranceMinor: settlementTolerance, Status: status})
			}
		}
	}

	var raw map[string]string
	if !n.cfg.SkipRaw {
		raw = make(map[string]string, len(values))
		for k, v := range values {
			raw[k] = v
		}
	}

	reference := strings.TrimSpace(getMapCol(values, n.cfg.RefCol))
	groupKey := reference
	if n.cfg.GroupCol != "" {
		groupKey = strings.TrimSpace(getMapCol(values, n.cfg.GroupCol))
	}

	return Transaction{
		ID:              fmt.Sprintf("%s-%d", n.sourceName, idRowNum),
		Date:            date,
		Amount:          amount,
		Source:          n.sourceName,
		Raw:             raw,
		Currency:        strings.TrimSpace(getMapCol(values, n.cfg.CurrencyCol)),
		Reference:       reference,
		Name:            strings.TrimSpace(getMapCol(values, n.cfg.NameCol)),
		GroupKey:        groupKey,
		Financials:      financials,
		FinancialChecks: financialChecks,
	}, nil
}
func getMapCol(values map[string]string, colName string) string {
	if colName == "" {
		return ""
	}
	want := strings.ToLower(strings.TrimSpace(colName))
	for k, v := range values {
		if strings.ToLower(strings.TrimSpace(k)) == want {
			return v
		}
	}
	return ""
}
