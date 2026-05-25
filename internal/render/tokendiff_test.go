package render

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// mkTokenTurn builds a turn that exercises every token category so the
// composition logic (including the cache-write = 5m+1h sum) is covered.
func mkTokenTurn(model, cwd string, in, out, cw5m, cw1h, cr int, ts time.Time) parse.Turn {
	return parse.Turn{
		Model: model, CWD: cwd, Timestamp: ts,
		Usage: parse.Usage{
			InputTokens:         in,
			OutputTokens:        out,
			CacheCreate5mTokens: cw5m,
			CacheCreate1hTokens: cw1h,
			CacheReadTokens:     cr,
		},
	}
}

func TestBuildTokenDiff_RowsLabelsAndCounts(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	a := aggregate.New(prices)
	a.Add(mkTokenTurn("claude-opus-4-7", "/p", 100, 20, 30, 10, 1000, t0))

	b := aggregate.New(prices)
	b.Add(mkTokenTurn("claude-opus-4-7", "/p", 200, 50, 40, 20, 5000, t0))

	d := BuildTokenDiff(a, b)

	wantLabels := []string{"Input", "Output", "Cache write", "Cache read"}
	if len(d.Rows) != len(wantLabels) {
		t.Fatalf("row count = %d, want %d", len(d.Rows), len(wantLabels))
	}
	for i, want := range wantLabels {
		if d.Rows[i].Label != want {
			t.Errorf("row %d label = %q, want %q", i, d.Rows[i].Label, want)
		}
	}

	// Per-category A/B counts. Cache write = 5m + 1h.
	wantA := []int64{100, 20, 40, 1000}
	wantB := []int64{200, 50, 60, 5000}
	for i := range d.Rows {
		if d.Rows[i].A != wantA[i] {
			t.Errorf("row %q A = %d, want %d", d.Rows[i].Label, d.Rows[i].A, wantA[i])
		}
		if d.Rows[i].B != wantB[i] {
			t.Errorf("row %q B = %d, want %d", d.Rows[i].Label, d.Rows[i].B, wantB[i])
		}
	}
}

func TestBuildTokenDiff_TotalsAndShares(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	a := aggregate.New(prices)
	a.Add(mkTokenTurn("claude-opus-4-7", "/p", 100, 20, 30, 10, 1000, t0))
	b := aggregate.New(prices)
	b.Add(mkTokenTurn("claude-opus-4-7", "/p", 200, 50, 40, 20, 5000, t0))

	d := BuildTokenDiff(a, b)

	// Grand totals equal the sum of the category rows on each side.
	var sumA, sumB int64
	var pctA, pctB float64
	for _, r := range d.Rows {
		sumA += r.A
		sumB += r.B
		pctA += r.PctA
		pctB += r.PctB
	}
	if d.TotalA != sumA {
		t.Errorf("TotalA = %d, want %d (sum of rows)", d.TotalA, sumA)
	}
	if d.TotalB != sumB {
		t.Errorf("TotalB = %d, want %d (sum of rows)", d.TotalB, sumB)
	}
	// Shares of each side's own total partition to ~100%.
	for _, got := range []float64{pctA, pctB} {
		if diff := got - 100; diff < -0.01 || diff > 0.01 {
			t.Errorf("share sum = %v, want ~100", got)
		}
	}
}

// TestBuildTokenDiff_EmptySides: a zero corpus on either side must not
// divide by zero — shares fall back to 0 and totals are 0.
func TestBuildTokenDiff_EmptySides(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	a := aggregate.New(prices)
	b := aggregate.New(prices)

	d := BuildTokenDiff(a, b)
	if len(d.Rows) != 4 {
		t.Fatalf("row count = %d, want 4 even when empty", len(d.Rows))
	}
	if d.TotalA != 0 || d.TotalB != 0 {
		t.Errorf("empty totals = (%d, %d), want (0, 0)", d.TotalA, d.TotalB)
	}
	for _, r := range d.Rows {
		if r.PctA != 0 || r.PctB != 0 {
			t.Errorf("row %q shares = (%v, %v), want (0, 0) on empty corpus", r.Label, r.PctA, r.PctB)
		}
	}
}
