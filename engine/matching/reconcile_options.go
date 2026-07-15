//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package matching

import (
	"fmt"
	"strings"
	"time"

	"github.com/reconifyhq/reconify/config"
)

// ReconcileOptions carries optional overrides for a Reconcile or ReconcileMultiSource call.
// Pass as the last variadic argument; omit entirely to preserve existing default behavior.
type ReconcileOptions struct {
	// LeftPolicy overrides the left source's duplicate handling policy.
	// Defaults to DuplicatePolicyFlag (current behavior) when zero.
	LeftPolicy config.DuplicatePolicy
	// RightPolicy overrides the right source's duplicate handling policy.
	// Defaults to DuplicatePolicyFlag (current behavior) when zero.
	RightPolicy config.DuplicatePolicy
	// ResultMode is embedded in Summary.ResultMode. Empty means "all" (the default).
	ResultMode config.ResultMode
	// RunID is embedded in Summary.RunID when provided. Matches the telemetry RunID.
	RunID string
}

// ResolveNameMatchThreshold returns t if it is a valid Jaccard threshold (0 < t <= 1),
// else the default of 0.5. Config validation already rejects out-of-range values when
// set, so this only needs to handle the unset (zero) case.
func ResolveNameMatchThreshold(t float64) float64 {
	if t <= 0 {
		return 0.5
	}
	return t
}

// ParseDateWindow parses a date window string like "1d", "3d" to a number of days.
// Returns 0 if empty (meaning no window limit).
func ParseDateWindow(window string) (int, error) {
	if window == "" {
		return 0, nil
	}
	var days int
	var unit string
	if _, err := fmt.Sscanf(window, "%d%s", &days, &unit); err != nil {
		return 0, fmt.Errorf("expected format like '1d', got %q", window)
	}
	if unit != "d" && unit != "D" {
		return 0, fmt.Errorf("unit must be 'd', got %q", unit)
	}
	return days, nil
}

// daysBetween returns the absolute number of days between two times.
func daysBetween(a, b time.Time) int {
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return int(diff.Hours() / 24)
}

// Tokenize splits a string into lower-case word tokens.
func Tokenize(s string) map[string]bool {
	tokens := make(map[string]bool)
	for _, word := range strings.Fields(strings.ToLower(s)) {
		if len(word) > 1 {
			tokens[word] = true
		}
	}
	return tokens
}

// TokenOverlap returns the Jaccard similarity between two token sets.
func TokenOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for tok := range a {
		if b[tok] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
