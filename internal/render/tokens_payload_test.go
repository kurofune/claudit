package render

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/pricing"
)

// TestBuildTokens_ShapeAndKeys: the tokens tab payload carries the
// grand total, the category composition breakdown, the per-period
// token trend, and the by-model table.
func TestBuildTokens_ShapeAndKeys(t *testing.T) {
	a := htmlSetup(t)
	p := BuildTokens(a)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal TokensPayload: %v", err)
	}
	wantTopLevel(t, got, []string{
		"total",
		"composition",
		"trend",
		"by_model",
		"period",
	})
}

// TestBuildTokens_CarriesPeriod: like the overview, the tokens payload
// ships the bucket granularity so the SPA can label the trend axis
// (HH:MM for the single-day hourly view).
func TestBuildTokens_CarriesPeriod(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	a := aggregate.New(prices).WithPeriod(aggregate.PeriodHour)
	a.Add(mkTurn("claude-opus-4-7", "/p/x", 1_000_000, 200_000, time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)))
	if got := BuildTokens(a).Period; got != aggregate.PeriodHour {
		t.Errorf("Period = %q, want hour", got)
	}
}

// TestBuildTokens_CompositionSumsToGrandTotal: the 4 composition rows
// must partition the grand total exactly, and (when non-empty) their
// percentages must add up to ~100.
func TestBuildTokens_CompositionSumsToGrandTotal(t *testing.T) {
	a := htmlSetup(t)
	p := BuildTokens(a)

	var sumTokens int64
	var sumPct float64
	for _, c := range p.Composition {
		sumTokens += c.Tokens
		sumPct += c.Pct
	}
	if sumTokens != p.Total {
		t.Errorf("composition token sum = %d, want Total %d", sumTokens, p.Total)
	}
	if p.Total > 0 {
		if diff := sumPct - 100; diff < -0.01 || diff > 0.01 {
			t.Errorf("composition pct sum = %v, want ~100 (diff %v)", sumPct, diff)
		}
	}
}

// TestBuildTokens_ByModelMatchesAggregator: each by_model row's
// per-category counts equal the matching aggregator bucket's fields,
// Total equals the bucket's Total(), and summing all rows' per-category
// counts reconstructs the corpus totals (by-model partitions the data).
func TestBuildTokens_ByModelMatchesAggregator(t *testing.T) {
	a := htmlSetup(t)
	p := BuildTokens(a)

	buckets := a.ByModel()
	if len(p.ByModel) != len(buckets) {
		t.Fatalf("by_model row count = %d, want %d", len(p.ByModel), len(buckets))
	}

	var sumInput, sumOutput, sumCacheRead int64
	for i, row := range p.ByModel {
		b := buckets[i]
		if row.Model != b.Model {
			t.Errorf("row %d model = %q, want %q (ordering must match)", i, row.Model, b.Model)
		}
		if row.Input != b.InputTokens {
			t.Errorf("row %d input = %d, want %d", i, row.Input, b.InputTokens)
		}
		if row.Output != b.OutputTokens {
			t.Errorf("row %d output = %d, want %d", i, row.Output, b.OutputTokens)
		}
		if want := b.CacheCreate5mTokens + b.CacheCreate1hTokens; row.CacheWrite != want {
			t.Errorf("row %d cache_write = %d, want %d", i, row.CacheWrite, want)
		}
		if row.CacheRead != b.CacheReadTokens {
			t.Errorf("row %d cache_read = %d, want %d", i, row.CacheRead, b.CacheReadTokens)
		}
		if row.Total != b.Total() {
			t.Errorf("row %d total = %d, want %d", i, row.Total, b.Total())
		}
		sumInput += row.Input
		sumOutput += row.Output
		sumCacheRead += row.CacheRead
	}

	totals := a.Totals().Tokens
	if sumInput != totals.InputTokens {
		t.Errorf("sum of row inputs = %d, want corpus input %d", sumInput, totals.InputTokens)
	}
	if sumOutput != totals.OutputTokens {
		t.Errorf("sum of row outputs = %d, want corpus output %d", sumOutput, totals.OutputTokens)
	}
	if sumCacheRead != totals.CacheReadTokens {
		t.Errorf("sum of row cache_read = %d, want corpus cache_read %d", sumCacheRead, totals.CacheReadTokens)
	}
}
