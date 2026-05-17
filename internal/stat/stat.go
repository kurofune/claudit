// Package stat provides small statistical helpers shared between the
// aggregator (anomaly detection over trend buckets) and the watch loop
// (rolling spike detection over recent turn costs).
package stat

import "sort"

// Median returns the sample median of xs. Sorts in place — callers pass
// throwaway slices so the side effect is invisible. Returns 0 for an
// empty input rather than panicking; callers that distinguish "no data"
// from "median is 0" should check len(xs) themselves.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	mid := len(xs) / 2
	if len(xs)%2 == 1 {
		return xs[mid]
	}
	return (xs[mid-1] + xs[mid]) / 2
}
