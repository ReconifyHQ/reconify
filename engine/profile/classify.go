package profile

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/engine/parser"
	"github.com/reconifyhq/reconify/schemas"
)

const (
	typeDate    = "date"
	typeAmount  = "amount"
	typeInteger = "integer"
	typeBoolean = "boolean"
	typeText    = "text"
	typeEmpty   = "empty"
)

// ambiguityThreshold mirrors the AP-6 confidence-lead language ("a >= 0.10
// confidence lead over the next-best candidate"), reused here only as a
// descriptive flag: AP-5 profiles facts, it never gates or decides a mapping.
const ambiguityThreshold = 0.10

// dateLayouts are tried in priority order; ties in match rate keep this order.
var dateLayouts = []string{
	"2006-01-02",
	time.RFC3339,
	"2006-01-02 15:04:05",
	"01/02/2006",
	"02/01/2006",
	"01/02/2006 15:04:05",
	"Jan 2, 2006",
	"02-Jan-2006",
	"20060102",
}

type amountFormatCombo struct {
	decimal   string
	thousands string
}

// amountCombos are tried in priority order; ties in match rate keep this order.
var amountCombos = []amountFormatCombo{
	{decimal: ".", thousands: ","},
	{decimal: ",", thousands: "."},
	{decimal: ",", thousands: " "},
	{decimal: ".", thousands: ""},
	{decimal: ",", thousands: ""},
}

var booleanTokens = map[string]struct{}{
	"true": {}, "false": {}, "yes": {}, "no": {}, "0": {}, "1": {},
}

var currencySymbols = []string{"$", "€", "£", "¥", "₹", "₩", "₦", "R$"}

type classification struct {
	inferredType string
	ambiguous    bool
	candidates   []schemas.TypeCandidate
	dateLayout   string
	amountFormat *schemas.AmountFormat
}

// observeValue applies every type-candidate probe to one non-empty value and
// updates the running counters. It is called once per value as rows stream
// past, so classify never needs to retain or re-scan raw values.
func (s *columnStats) observeValue(v string) {
	trimmed := strings.TrimSpace(v)

	for i, layout := range dateLayouts {
		if _, err := time.Parse(layout, trimmed); err == nil {
			s.dateMatches[i]++
		}
	}

	stripped := stripCurrencySymbol(trimmed)
	for i, combo := range amountCombos {
		if _, err := parser.ParseAmount(stripped, combo.decimal, combo.thousands, 1); err == nil {
			s.amountMatches[i]++
		}
	}

	if _, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		s.intMatches++
	}

	if _, ok := booleanTokens[strings.ToLower(trimmed)]; ok {
		s.boolMatches++
	}

	if strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		s.sawParenNegative = true
	}

	if s.currencySymbol == "" {
		s.currencySymbol = detectCurrencySymbol(trimmed)
	}
}

// classify ranks type candidates for a column from its accumulated match
// counters. s.nonEmpty must be > 0 (callers report the all-empty case as
// "empty" before calling classify).
func classify(s *columnStats) classification {
	dateConf, dateLayout := bestDateLayout(s.dateMatches, s.nonEmpty)
	amountConf, amountFmt := bestAmountFormat(s)
	intConf := float64(s.intMatches) / float64(s.nonEmpty)
	boolConf := float64(s.boolMatches) / float64(s.nonEmpty)

	best := dateConf
	for _, c := range []float64{amountConf, intConf, boolConf} {
		if c > best {
			best = c
		}
	}
	textConf := 1 - best
	if textConf < 0 {
		textConf = 0
	}

	candidates := []schemas.TypeCandidate{
		{Type: typeDate, Confidence: round2(dateConf)},
		{Type: typeAmount, Confidence: round2(amountConf)},
		{Type: typeInteger, Confidence: round2(intConf)},
		{Type: typeBoolean, Confidence: round2(boolConf)},
		{Type: typeText, Confidence: round2(textConf)},
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Confidence > candidates[j].Confidence
	})

	ambiguous := len(candidates) > 1 && candidates[0].Confidence-candidates[1].Confidence < ambiguityThreshold

	result := classification{
		inferredType: candidates[0].Type,
		ambiguous:    ambiguous,
		candidates:   candidates,
	}
	if result.inferredType == typeDate {
		result.dateLayout = dateLayout
	}
	if result.inferredType == typeAmount {
		result.amountFormat = amountFmt
	}
	return result
}

func bestDateLayout(matches []int, nonEmpty int) (float64, string) {
	bestConf := 0.0
	bestLayout := ""
	for i, layout := range dateLayouts {
		conf := float64(matches[i]) / float64(nonEmpty)
		if conf > bestConf {
			bestConf = conf
			bestLayout = layout
		}
	}
	return bestConf, bestLayout
}

func bestAmountFormat(s *columnStats) (float64, *schemas.AmountFormat) {
	bestConf := 0.0
	var bestCombo amountFormatCombo
	for i, combo := range amountCombos {
		conf := float64(s.amountMatches[i]) / float64(s.nonEmpty)
		if conf > bestConf {
			bestConf = conf
			bestCombo = combo
		}
	}
	if bestConf == 0 {
		return 0, nil
	}
	return bestConf, &schemas.AmountFormat{
		Decimal:               bestCombo.decimal,
		Thousands:             bestCombo.thousands,
		CurrencySymbol:        s.currencySymbol,
		ParenthesizedNegative: s.sawParenNegative,
	}
}

// stripCurrencySymbol removes a leading or trailing currency symbol before an
// amount-format parse attempt; ParseAmount itself only understands digits,
// separators, sign, and parentheses. v is expected to already be trimmed.
func stripCurrencySymbol(v string) string {
	for _, sym := range currencySymbols {
		if strings.HasPrefix(v, sym) {
			return strings.TrimSpace(strings.TrimPrefix(v, sym))
		}
		if strings.HasSuffix(v, sym) {
			return strings.TrimSpace(strings.TrimSuffix(v, sym))
		}
	}
	return v
}

// detectCurrencySymbol returns the currency symbol found in v (expected
// already trimmed), or "" if none of the known symbols appear.
func detectCurrencySymbol(v string) string {
	for _, sym := range currencySymbols {
		if strings.HasPrefix(v, sym) || strings.HasSuffix(v, sym) {
			return sym
		}
	}
	return ""
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
