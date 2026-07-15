//nolint:staticcheck // Domain aliases keep package-internal signatures readable.
package parser

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// parseAmount parses an amount string to int64 minor units.
// It removes the thousands separator, normalizes the decimal separator to ".",
// then parses using integer/string arithmetic (no float64 round-trip) to avoid
// precision loss on large or high-precision amounts.
func parseAmount(s string, decimal string, thousands string, multiplier int64) (int64, error) {
	s = strings.TrimSpace(s)

	// Remove thousands separator
	if thousands != "" {
		s = strings.ReplaceAll(s, thousands, "")
	}

	// Normalize decimal separator
	if decimal != "." && decimal != "" {
		s = strings.ReplaceAll(s, decimal, ".")
	}

	// Handle parentheses for negative amounts: (1234.56) -> -1234.56
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = "-" + s[1:len(s)-1]
	}

	// Remove any currency symbols or spaces that might remain
	s = strings.TrimSpace(s)

	if s == "" {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	negative := false
	switch s[0] {
	case '-':
		negative = true
		s = s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
		if strings.ContainsRune(fracPart, '.') {
			return 0, fmt.Errorf("not a number: %q", s)
		}
	}
	if intPart == "" {
		intPart = "0"
	}
	if len(fracPart) > 18 {
		return 0, fmt.Errorf("not a number: %q", s)
	}

	if multiplier <= 0 {
		return 0, fmt.Errorf("invalid multiplier: %d", multiplier)
	}

	intVal, err := strconv.ParseUint(intPart, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, fmt.Errorf("amount overflow: %q", s)
		}
		return 0, fmt.Errorf("not a number: %q", s)
	}

	var fracScaled uint64
	if fracPart != "" {
		fracVal, err := strconv.ParseUint(fracPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		denom := uint64(1)
		for i := 0; i < len(fracPart); i++ {
			denom *= 10
		}
		// fracVal*multiplier can exceed int64 (e.g. 18 fractional digits with a
		// multiplier >= 100), so do the multiply/divide as a 128-bit operation via
		// math/bits rather than int64 arithmetic that would silently wrap. The true
		// quotient is always < multiplier (fracVal < denom by construction), so it
		// fits safely back into int64/uint64 once divided.
		hi, lo := bits.Mul64(fracVal, uint64(multiplier))
		q, r := bits.Div64(hi, lo, denom)
		if 2*r >= denom {
			q++
		}
		if q > uint64(math.MaxInt64) {
			return 0, fmt.Errorf("amount overflow: %q", s)
		}
		fracScaled = q
	}

	// Keep the magnitude unsigned until the final conversion. This allows the
	// negative side to represent exactly one more unit than the positive side
	// (math.MinInt64) without ever overflowing a signed intermediate.
	limit := uint64(math.MaxInt64)
	if negative {
		limit++
	}
	hi, lo := bits.Mul64(intVal, uint64(multiplier))
	if hi != 0 || lo > limit-fracScaled {
		return 0, fmt.Errorf("amount overflow: %q", s)
	}
	magnitude := lo + fracScaled
	if negative {
		if magnitude == uint64(math.MaxInt64)+1 {
			return math.MinInt64, nil
		}
		return -int64(magnitude), nil // #nosec G115 -- magnitude is <= MaxInt64 after the MinInt64 case above.
	}
	return int64(magnitude), nil // #nosec G115 -- magnitude is bounded by MaxInt64 above.
}
