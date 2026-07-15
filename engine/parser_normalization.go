package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/config"
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
		ID:        fmt.Sprintf("%s-%d", n.sourceName, idRowNum),
		Date:      date,
		Amount:    amount,
		Source:    n.sourceName,
		Raw:       raw,
		Currency:  strings.TrimSpace(getMapCol(values, n.cfg.CurrencyCol)),
		Reference: reference,
		Name:      strings.TrimSpace(getMapCol(values, n.cfg.NameCol)),
		GroupKey:  groupKey,
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
