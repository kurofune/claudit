package render

import (
	"github.com/kurofune/claudit/internal/aggregate"
)

// TokenCompositionRow is one category's before/after token counts for the
// diff's Tokens section. PctA / PctB are each side's share of its own
// grand total (0..100) — they size the A and B mix bars, which show how
// the composition shifted independent of the absolute change.
type TokenCompositionRow struct {
	Label string  `json:"label"`
	A     int64   `json:"a"`
	B     int64   `json:"b"`
	PctA  float64 `json:"pct_a"`
	PctB  float64 `json:"pct_b"`
}

// TokenDiff is the diff's token-composition comparison: the 4 category
// rows (in the fixed Input / Output / Cache write / Cache read order) plus
// each side's grand total. Backs the markdown table, the JSON `tokens`
// block, and the HTML Tokens section.
type TokenDiff struct {
	Rows   []TokenCompositionRow `json:"rows"`
	TotalA int64                 `json:"total_a"`
	TotalB int64                 `json:"total_b"`
}

// BuildTokenDiff pairs A's token composition against B's, category by
// category. It reuses tokenComposition (the same split BuildTokens uses
// for the report) so the diff's categories never drift from the report's.
func BuildTokenDiff(a, b *aggregate.Aggregator) TokenDiff {
	ta := a.Totals().Tokens
	tb := b.Totals().Tokens
	ca := tokenComposition(ta, ta.Total())
	cb := tokenComposition(tb, tb.Total())

	rows := make([]TokenCompositionRow, len(ca))
	for i := range ca {
		rows[i] = TokenCompositionRow{
			Label: ca[i].Label,
			A:     ca[i].Tokens,
			B:     cb[i].Tokens,
			PctA:  ca[i].Pct,
			PctB:  cb[i].Pct,
		}
	}
	return TokenDiff{Rows: rows, TotalA: ta.Total(), TotalB: tb.Total()}
}

// tokenAxisMax returns the largest single per-category count across both
// sides of a TokenDiff. It's the right-edge axis for the Tokens section's
// dumbbell rows, so dot positions stay comparable across categories — the
// dominant band (usually cache-read) defines the scale, exactly as a mover
// section's Max does. Excludes the grand totals on purpose: a total exceeds
// any single band and would peg every dot to 100%. Returns 0 for an empty
// diff so dotPct's max<=0 guard short-circuits.
func tokenAxisMax(td TokenDiff) float64 {
	var max int64
	for _, r := range td.Rows {
		if r.A > max {
			max = r.A
		}
		if r.B > max {
			max = r.B
		}
	}
	return float64(max)
}
