package profile

import "github.com/reconifyhq/reconify/schemas"

// columnCollector accumulates per-column type-candidate match counts while
// streaming rows, so a --full scan of an arbitrarily large file costs O(1)
// memory per column instead of retaining every cell value for a final pass.
type columnCollector struct {
	sampleValues int
	rowsScanned  int
	stats        map[string]*columnStats
}

// columnStats holds running counters for one column. It never retains raw
// values beyond the bounded sample; every classification probe is applied to
// each value as it streams past in observeRow/observeValue.
type columnStats struct {
	nonEmpty int

	dateMatches   []int // parallel to dateLayouts
	amountMatches []int // parallel to amountCombos
	intMatches    int
	boolMatches   int

	sawParenNegative bool
	currencySymbol   string

	sampleValues []string
	sampleSeen   map[string]struct{}
}

func newColumnStats() *columnStats {
	return &columnStats{
		dateMatches:   make([]int, len(dateLayouts)),
		amountMatches: make([]int, len(amountCombos)),
		sampleSeen:    make(map[string]struct{}),
	}
}

func newColumnCollector(sampleValues int) *columnCollector {
	return &columnCollector{
		sampleValues: sampleValues,
		stats:        make(map[string]*columnStats),
	}
}

func (c *columnCollector) observeRow(row map[string]string) {
	c.rowsScanned++
	for name, v := range row {
		s := c.stats[name]
		if s == nil {
			s = newColumnStats()
			c.stats[name] = s
		}
		if v == "" {
			continue
		}
		s.nonEmpty++
		s.observeValue(v)
		if c.sampleValues > 0 && len(s.sampleValues) < c.sampleValues {
			if _, seen := s.sampleSeen[v]; !seen {
				s.sampleSeen[v] = struct{}{}
				s.sampleValues = append(s.sampleValues, v)
			}
		}
	}
}

func (c *columnCollector) buildColumnProfile(name string) schemas.ColumnProfile {
	s := c.stats[name]
	if s == nil {
		s = newColumnStats()
	}

	col := schemas.ColumnProfile{
		Name:          name,
		NonEmptyCount: s.nonEmpty,
		NullCount:     c.rowsScanned - s.nonEmpty,
	}

	if s.nonEmpty == 0 {
		col.InferredType = typeEmpty
		col.Candidates = []schemas.TypeCandidate{{Type: typeEmpty, Confidence: 1}}
		return col
	}

	classification := classify(s)
	col.InferredType = classification.inferredType
	col.Ambiguous = classification.ambiguous
	col.Candidates = classification.candidates
	col.DateLayout = classification.dateLayout
	col.AmountFormat = classification.amountFormat
	if c.sampleValues > 0 {
		col.SampleValues = s.sampleValues
	}

	return col
}
