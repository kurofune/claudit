package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/pricing"
)

func TestPeriod_Truncate(t *testing.T) {
	// 2026-05-06 is a Wednesday.
	wed := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)

	cases := []struct {
		p    Period
		want time.Time
	}{
		{PeriodDay, time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)},
		{PeriodWeek, time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)}, // Monday
		{PeriodMonth, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := c.p.Truncate(wed); !got.Equal(c.want) {
			t.Errorf("%s.Truncate: got %v, want %v", c.p, got, c.want)
		}
	}

	// Sunday must snap *back* 6 days to the previous Monday.
	sun := time.Date(2026, 5, 10, 23, 59, 0, 0, time.UTC)
	wantMon := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	if got := PeriodWeek.Truncate(sun); !got.Equal(wantMon) {
		t.Errorf("Sunday week truncate: got %v, want %v", got, wantMon)
	}
}

func TestPeriod_Step(t *testing.T) {
	t0 := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC)
	if got := PeriodDay.Step(t0); !got.Equal(t0.AddDate(0, 0, 1)) {
		t.Errorf("day step: %v", got)
	}
	if got := PeriodWeek.Step(t0); !got.Equal(t0.AddDate(0, 0, 7)) {
		t.Errorf("week step: %v", got)
	}
	if got := PeriodMonth.Step(t0); !got.Equal(t0.AddDate(0, 1, 0)) {
		t.Errorf("month step: %v", got)
	}
}

func TestTrend_DailyGapFill(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	// Three turns: day1, day1, day3. Day2 must appear as a zero-cost cell.
	d1 := time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 4, 12, 9, 0, 0, 0, time.UTC)

	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", d1))
	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", d1))
	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", d3))

	pts := agg.TrendTotals()
	if len(pts) != 3 {
		t.Fatalf("want 3 cells (day1,day2,day3), got %d", len(pts))
	}
	// Day 2 is the gap-fill cell.
	if pts[1].CostUSD != 0 || pts[1].Turns != 0 {
		t.Errorf("day2 should be zero-fill, got cost=%v turns=%d", pts[1].CostUSD, pts[1].Turns)
	}
	if pts[0].Turns != 2 {
		t.Errorf("day1 turn count: %d", pts[0].Turns)
	}
	if pts[2].Turns != 1 {
		t.Errorf("day3 turn count: %d", pts[2].Turns)
	}
	// Times must be in ascending day-aligned order.
	for i, p := range pts {
		want := d1.AddDate(0, 0, i)
		want = time.Date(want.Year(), want.Month(), want.Day(), 0, 0, 0, 0, time.UTC)
		if !p.Time.Equal(want) {
			t.Errorf("pts[%d].Time = %v, want %v", i, p.Time, want)
		}
	}
}

func TestTrend_HitRatioPerBucket(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	d1 := time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)

	// d1: bad cache — input=1000, cache_read=0  → hit ratio 0%.
	agg.Add(fullTurn("s1", "/p/x", "claude-opus-4-7", 1000, 0, 0, 0, 0, d1))
	// d2: great cache — input=100, cache_read=900  → hit ratio 90%.
	agg.Add(fullTurn("s1", "/p/x", "claude-opus-4-7", 100, 0, 0, 0, 900, d2))

	pts := agg.TrendTotals()
	if len(pts) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(pts))
	}
	if got := pts[0].HitRatio(); got != 0 {
		t.Errorf("d1 hit ratio: %v, want 0", got)
	}
	if got := pts[1].HitRatio(); got != 0.9 {
		t.Errorf("d2 hit ratio: %v, want 0.9", got)
	}
}

func TestTrend_PerKey_NotEnabledByDefault(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices) // no WithPeriod
	d1 := time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC)
	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", d1))

	if got := agg.TrendTotals(); got != nil {
		t.Errorf("trend totals must be nil when period unset, got %v", got)
	}
	if got := agg.TrendByModel(); got != nil {
		t.Errorf("trend by-model must be nil when period unset, got %v", got)
	}
}

func TestTrend_ByModelAndProject(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	d1 := time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/a", d1))
	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/a", d2))
	agg.Add(turn("claude-sonnet-4-6", 0, 1_000_000, false, "/p/b", d2))

	bm := agg.TrendByModel()
	if len(bm["claude-opus-4-7"]) != 2 {
		t.Errorf("opus daily series: %d", len(bm["claude-opus-4-7"]))
	}
	// Sonnet only ran on d2 — single cell, no gap-fill needed for a 1-cell series.
	if got := bm["claude-sonnet-4-6"]; len(got) != 1 || got[0].Turns != 1 {
		t.Errorf("sonnet series: %#v", got)
	}

	bp := agg.TrendByProject()
	if len(bp["/p/a"]) != 2 {
		t.Errorf("/p/a daily series: %d", len(bp["/p/a"]))
	}
}
