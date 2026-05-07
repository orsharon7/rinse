// Package demo provides small utility helpers used in throwaway examples.
package demo

import "errors"

// Average returns the arithmetic mean of the provided values.
// It returns an error when the input slice is empty.
func Average(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, errors.New("demo: cannot average empty slice")
	}
	var sum float64
	for i := 0; i < len(values); i++ {
		sum += values[i]
	}
	return sum / float64(len(values)), nil
}

// Contains reports whether target appears in items.
func Contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
