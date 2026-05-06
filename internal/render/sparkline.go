package render

import (
	"strings"

	"github.com/nategross/claudit/internal/aggregate"
)

// sparkBlocks: 9 levels — index 0 means "zero", 1..8 map to ascending
// block heights. Using 9 levels (rather than the usual 8) lets the eye
// distinguish "no data" from "tiny but nonzero," which matters when
// looking at a sparkline of cost-per-day with sparse activity.
var sparkBlocks = [...]rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders a series as a unicode block-char string. Empty input
// returns "—". The series is downsampled to at most maxCells by summing
// adjacent buckets so the line stays readable in markdown table cells.
func sparkline(points []aggregate.TrendPoint, maxCells int) string {
	if len(points) == 0 {
		return "—"
	}
	if maxCells < 1 {
		maxCells = len(points)
	}
	values := downsample(points, maxCells)
	var max float64
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	b.Grow(len(values) * 3) // block chars are multi-byte
	for _, v := range values {
		if max <= 0 {
			b.WriteRune(sparkBlocks[0])
			continue
		}
		if v <= 0 {
			b.WriteRune(sparkBlocks[0])
			continue
		}
		// Map (0, max] to bucket 1..8.
		idx := int(v/max*7.999) + 1
		if idx > 8 {
			idx = 8
		}
		b.WriteRune(sparkBlocks[idx])
	}
	return b.String()
}

// downsample groups adjacent points into at most maxCells cells by
// summing their costs. Returns one value per output cell. When the
// series is shorter than maxCells, it is returned 1:1.
func downsample(points []aggregate.TrendPoint, maxCells int) []float64 {
	n := len(points)
	if n <= maxCells {
		out := make([]float64, n)
		for i, p := range points {
			out[i] = p.CostUSD
		}
		return out
	}
	out := make([]float64, maxCells)
	// Distribute n points into maxCells buckets as evenly as possible.
	// Bucket i covers points [i*n/maxCells, (i+1)*n/maxCells).
	for i := 0; i < maxCells; i++ {
		start := i * n / maxCells
		end := (i + 1) * n / maxCells
		var sum float64
		for j := start; j < end; j++ {
			sum += points[j].CostUSD
		}
		out[i] = sum
	}
	return out
}
