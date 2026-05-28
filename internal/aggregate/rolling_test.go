package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// opusTurn is one opus-4-7 turn billing 1M input tokens = $5 exactly,
// so the rolling sums are trivial multiples of $5.
func opusTurn(ts time.Time) parse.Turn {
	return parse.Turn{
		Model:     "claude-opus-4-7",
		Timestamp: ts,
		Usage:     parse.Usage{InputTokens: 1_000_000},
	}
}

func TestRollingTotals_Buckets(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	// Wednesday noon. monthStart=May 1, weekStart=Mon May 18,
	// todayStart=May 20 00:00, hourStart=May 20 11:00.
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)

	turns := []parse.Turn{
		opusTurn(time.Date(2026, 5, 20, 11, 30, 0, 0, time.UTC)), // hour+today+week+month
		opusTurn(time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)),   // today+week+month
		opusTurn(time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)),  // week+month
		opusTurn(time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)),   // month only
		opusTurn(time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)),  // before month: none
	}

	hour, today, week, month := RollingTotals(turns, prices, now)

	const eps = 1e-9
	check := func(name string, got, want float64) {
		if got < want-eps || got > want+eps {
			t.Errorf("%s = %.4f, want %.4f", name, got, want)
		}
	}
	check("hour", hour, 5)
	check("today", today, 10)
	check("week", week, 15)
	check("month", month, 20)
}

func TestRollingTotals_BoundaryInclusive(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// A turn exactly on the month boundary must count (>= boundary).
	month := func() float64 {
		_, _, _, m := RollingTotals([]parse.Turn{opusTurn(monthStart)}, prices, now)
		return m
	}()
	if month < 5-1e-9 {
		t.Errorf("turn exactly at monthStart should count; month = %.4f, want 5", month)
	}

	// One nanosecond before the boundary must not.
	monthBefore := func() float64 {
		_, _, _, m := RollingTotals([]parse.Turn{opusTurn(monthStart.Add(-time.Nanosecond))}, prices, now)
		return m
	}()
	if monthBefore > 1e-9 {
		t.Errorf("turn just before monthStart should not count; month = %.4f, want 0", monthBefore)
	}
}

// TestRollingTotals_MatchesFilteredAggregator is the invariant the whole
// unification rests on: watch's rolling month must equal what report/serve
// produce for the same month window. Both run over the same turns; the
// rolling month bucket must equal an aggregator filtered to month-start.
func TestRollingTotals_MatchesFilteredAggregator(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	monthStart := time.Date(2026, 5, 1, 0, 0, 0, 0, now.Location())

	turns := []parse.Turn{
		opusTurn(time.Date(2026, 5, 19, 10, 0, 0, 0, time.UTC)),
		opusTurn(time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)),
		opusTurn(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)),
		opusTurn(time.Date(2026, 4, 28, 10, 0, 0, 0, time.UTC)), // prior month
	}

	_, _, _, month := RollingTotals(turns, prices, now)

	agg := New(prices).WithFilter(Filter{Since: monthStart})
	for _, tn := range turns {
		agg.Add(tn)
	}
	want := agg.Totals().CostUSD

	if month < want-1e-9 || month > want+1e-9 {
		t.Errorf("rolling month = %.6f, filtered aggregator = %.6f; they must match", month, want)
	}
}

func TestRollingTotals_HourIsTrailing60m(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	turns := []parse.Turn{
		opusTurn(now.Add(-30 * time.Minute)), // in hour
		opusTurn(now.Add(-59 * time.Minute)), // in hour
		opusTurn(now.Add(-61 * time.Minute)), // just outside
	}
	hour, _, _, _ := RollingTotals(turns, prices, now)
	if hour < 10-1e-9 || hour > 10+1e-9 {
		t.Errorf("hour = %.4f, want 10 (trailing 60m, not calendar hour)", hour)
	}
}

func TestRollingTotals_WeekStartsOnMonday(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	// Sunday 2026-05-24 noon: the ISO week began Monday 2026-05-18.
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	turns := []parse.Turn{
		opusTurn(time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)),  // Monday 00:00 — in week
		opusTurn(time.Date(2026, 5, 17, 23, 0, 0, 0, time.UTC)), // Sunday prior — out
	}
	_, _, week, _ := RollingTotals(turns, prices, now)
	if week < 5-1e-9 || week > 5+1e-9 {
		t.Errorf("week = %.4f, want 5 (week starts Monday)", week)
	}
}

func TestRollingTotals_LocalBoundary(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	// now is in a -07:00 zone. monthStart is local May 1 00:00 (= May 1
	// 07:00 UTC). A turn at May 1 03:00 UTC is before local monthStart,
	// so it must NOT count toward the month.
	pdt := time.FixedZone("PDT", -7*3600)
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, pdt)
	turn := opusTurn(time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC))

	_, _, _, month := RollingTotals([]parse.Turn{turn}, prices, now)
	if month > 1e-9 {
		t.Errorf("UTC-May-1-03:00 turn precedes local monthStart; month = %.4f, want 0", month)
	}
}
