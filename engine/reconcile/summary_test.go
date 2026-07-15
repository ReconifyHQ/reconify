//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"math"
	"testing"

	. "github.com/reconifyhq/reconify/engine/domain"
)

func TestBuildSummaryRejectsMinimumDifference(t *testing.T) {
	_, err := buildSummary(1, 1, &Result{AmountDiff: []AmountDiffPair{{DiffMinor: math.MinInt64}}}, "USD")
	if err == nil {
		t.Fatal("expected summary overflow error")
	}
}
