package engine

import (
	"fmt"
	"math"
)

func checkedAddInt64(label string, values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value > 0 && total > math.MaxInt64-value || value < 0 && total < math.MinInt64-value {
			return 0, fmt.Errorf("%s overflows int64", label)
		}
		total += value
	}
	return total, nil
}

func checkedMulInt64(label string, left, right int64) (int64, error) {
	if left == 0 || right == 0 {
		return 0, nil
	}
	if left == -1 && right == math.MinInt64 || right == -1 && left == math.MinInt64 {
		return 0, fmt.Errorf("%s overflows int64", label)
	}
	value := left * right
	if value/right != left {
		return 0, fmt.Errorf("%s overflows int64", label)
	}
	return value, nil
}

func checkedAbsInt64(label string, value int64) (int64, error) {
	if value == math.MinInt64 {
		return 0, fmt.Errorf("%s overflows int64", label)
	}
	if value < 0 {
		return -value, nil
	}
	return value, nil
}

func checkedAddInt(label string, values ...int) (int, error) {
	var total int
	for _, value := range values {
		if value > 0 && total > math.MaxInt-value || value < 0 && total < math.MinInt-value {
			return 0, fmt.Errorf("%s overflows int", label)
		}
		total += value
	}
	return total, nil
}
