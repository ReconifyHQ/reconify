//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package reconcile

import (
	"fmt"
	"strings"
)

func counterpartInputNames(counterparts []CounterpartInput) []string {
	names := make([]string, 0, len(counterparts))
	for _, counterpart := range counterparts {
		names = append(names, counterpart.SourceName)
	}
	return names
}

// validateCounterpartStreams performs all streaming preflight checks before any
// right file is indexed, preventing partial output for invalid multi-source input.
func validateCounterpartStreams(counterparts []CounterpartStream) error {
	if err := validateCounterpartNames(counterpartStreamNames(counterparts)); err != nil {
		return err
	}
	for index, counterpart := range counterparts {
		if counterpart.Index == nil {
			return fmt.Errorf("counterpart[%d] %q: right index cannot be nil", index, counterpart.SourceName)
		}
	}
	return nil
}

func counterpartStreamNames(counterparts []CounterpartStream) []string {
	names := make([]string, 0, len(counterparts))
	for _, counterpart := range counterparts {
		names = append(names, counterpart.SourceName)
	}
	return names
}

// validateCounterpartNames preserves configured order while rejecting names that
// would make per-source summaries ambiguous or unsafe to populate.
func validateCounterpartNames(names []string) error {
	seen := make(map[string]int, len(names))
	for index, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("counterpart[%d]: source name cannot be empty", index)
		}
		if previousIndex, exists := seen[trimmed]; exists {
			return fmt.Errorf("counterpart[%d] %q: duplicate source name (already configured at index %d)", index, name, previousIndex)
		}
		seen[trimmed] = index
	}
	return nil
}
