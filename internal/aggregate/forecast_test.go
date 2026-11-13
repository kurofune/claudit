package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/v2/internal/pricing"
)

func TestMonthEndForecast_ZeroWhenNoPeriod(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices) // no WithPeriod

	// Add some current-month data so we're not also tripping the
	// "no data" gate. We want to confirm the period gate fires first.
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	d1 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", d1))
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", d2))

	assertNoProjection(t, agg.MonthEndForecast(now), "not_daily", now)
}

func TestMonthEndForecast_ZeroWhenWeeklyOrMonthly(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	d1 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)

	for _, p := range []Period{PeriodWeek, PeriodMonth} {
		agg := New(prices).WithPeriod(p)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", d1))
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", d2))
		t.Run(string(p), func(t *testing.T) {
			assertNoProjection(t, agg.MonthEndForecast(now), "not_daily", now)
		})
	}
}

func TestMonthEndForecast_ZeroWhenNoCurrentMonthData(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	// Data only in April; now is in May.
	apr1 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	apr2 := time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)
	apr3 := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", apr1))
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", apr2))
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", apr3))

	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	assertNoProjection(t, agg.MonthEndForecast(now), "too_few_days", now)
}

func TestMonthEndForecast_ZeroWhenOnlyOneDayOfCurrentMonthData(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	// Two turns but both on the same day in May.
	may5a := time.Date(2026, 5, 5, 9, 0, 0, 0, time.UTC)
	may5b := time.Date(2026, 5, 5, 18, 0, 0, 0, time.UTC)
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", may5a))
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", may5b))

	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	assertNoProjection(t, agg.MonthEndForecast(now), "too_few_days", now)
}

func TestMonthEndForecast_ZeroWhenDaysElapsedBelowTwo(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	// Prior-month data so it doesn't trip the "no current-month data" gate
	// before days_elapsed gets checked... but actually both gates being
	// independent is the point. Use current-month data on May 1 (multiple
	// distinct days would require pretending) — actually with now = May 1
	// 18:00 there's only one possible day, May 1, so we'd also trip the
	// distinct-day gate. The point of this test is days_elapsed gating
	// specifically; we set up data such that ONLY days_elapsed gate fires.
	// May 1 morning + May 1 evening: 1 distinct day → distinct-day gate
	// would also fire. To isolate days_elapsed, add data on two distinct
	// May days, but those days are May 1 and May 2... and now is May 1
	// 18:00, which is BEFORE May 2 — those wouldn't be in trendTotals at
	// the time of now. So days_elapsed gate is what we'd hit.
	may1a := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	may1b := time.Date(2026, 5, 1, 17, 0, 0, 0, time.UTC)
	may2 := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC)
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", may1a))
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", may1b))
	agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", may2))

	// 0.75 days elapsed.
	now := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC)
	assertNoProjection(t, agg.MonthEndForecast(now), "too_few_days", now)
}

func TestMonthEndForecast_HappyPath(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	// No filter: WindowStart should equal MonthStart, and the daily-rate /
	// projection math should be algebraically identical to the pre-filter
	// formulation (rate = MTD / (now - monthStart), projection scales over
	// full month).
	//
	// 2M opus input tokens = $10 at $5/Mtok. One turn per day, May 1..20.
	for day := 1; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	// now = May 20 12:00 UTC → 19.5 days elapsed.
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	const eps = 0.01

	if got.MTDCostUSD < 200.0-eps || got.MTDCostUSD > 200.0+eps {
		t.Errorf("MTDCostUSD: got %v, want ~200.0", got.MTDCostUSD)
	}
	// No filter, data entirely within May → WindowCost == MTD.
	if got.WindowCostUSD < 200.0-eps || got.WindowCostUSD > 200.0+eps {
		t.Errorf("WindowCostUSD: got %v, want ~200.0", got.WindowCostUSD)
	}
	if got.DaysInMonth != 31 {
		t.Errorf("DaysInMonth: got %d, want 31", got.DaysInMonth)
	}
	if got.DaysElapsed < 19.5-eps || got.DaysElapsed > 19.5+eps {
		t.Errorf("DaysElapsed: got %v, want ~19.5", got.DaysElapsed)
	}
	// 200 / 19.5 ≈ 10.2564
	const wantRate = 10.2564
	if got.DailyRateUSD < wantRate-eps || got.DailyRateUSD > wantRate+eps {
		t.Errorf("DailyRateUSD: got %v, want ~%v", got.DailyRateUSD, wantRate)
	}
	// 10.2564 * 31 ≈ 317.95
	const wantProj = 317.95
	if got.ProjectedMonthEnd < wantProj-0.5 || got.ProjectedMonthEnd > wantProj+0.5 {
		t.Errorf("ProjectedMonthEnd: got %v, want ~318.0", got.ProjectedMonthEnd)
	}

	wantMonthStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	wantMonthEnd := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !got.MonthStart.Equal(wantMonthStart) {
		t.Errorf("MonthStart: got %v, want %v", got.MonthStart, wantMonthStart)
	}
	if !got.MonthEnd.Equal(wantMonthEnd) {
		t.Errorf("MonthEnd: got %v, want %v", got.MonthEnd, wantMonthEnd)
	}
	if !got.AsOf.Equal(now.UTC()) {
		t.Errorf("AsOf: got %v, want %v", got.AsOf, now.UTC())
	}
	// No filter and data starting on the 1st → WindowStart == MonthStart.
	if !got.WindowStart.Equal(wantMonthStart) {
		t.Errorf("WindowStart: got %v, want %v", got.WindowStart, wantMonthStart)
	}
}

func TestMonthEndForecast_RespectsProjectFilter(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).
		WithPeriod(PeriodDay).
		WithFilter(Filter{ProjectSubstring: "/p/foo"})

	// Both /p/foo and /p/bar see daily traffic across May 1..20.
	// Only /p/foo turns should contribute to the forecast.
	for day := 1; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
		// /p/bar contributes $20/day → would inflate MTD if not filtered.
		agg.Add(turn("claude-opus-4-7", 4_000_000, 0, false, "/p/bar", ts))
	}

	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	// foo-only: 20 days × $10 = $200 MTD.
	const eps = 0.01
	if got.MTDCostUSD < 200.0-eps || got.MTDCostUSD > 200.0+eps {
		t.Errorf("MTDCostUSD: got %v, want ~200.0 (filter should exclude /p/bar)", got.MTDCostUSD)
	}
}

func TestMonthEndForecast_AsOfIsUTCAndMonthIsLocal(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load LA tz: %v", err)
	}
	pinLocal(t, la)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	for day := 1; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 5, 20, 5, 0, 0, 0, la) // 12:00 UTC

	got := agg.MonthEndForecast(now)

	if got.AsOf.Location() != time.UTC {
		t.Errorf("AsOf.Location: got %v, want UTC", got.AsOf.Location())
	}
	if !got.AsOf.Equal(now) {
		t.Errorf("AsOf: got %v, want %v", got.AsOf, now)
	}
	if want := time.Date(2026, 5, 1, 0, 0, 0, 0, la); !got.MonthStart.Equal(want) {
		t.Errorf("MonthStart: got %v, want %v", got.MonthStart, want)
	}
	if want := time.Date(2026, 6, 1, 0, 0, 0, 0, la); !got.MonthEnd.Equal(want) {
		t.Errorf("MonthEnd: got %v, want %v", got.MonthEnd, want)
	}
}

// At 22:43 PDT on Oct 31 it is already Nov 1 in UTC; the forecast must
// still project October, the month the user's date filters live in.
func TestMonthEndForecast_LocalMonthOnLastEvening(t *testing.T) {
	pdt := time.FixedZone("PDT", -7*3600)
	pinLocal(t, pdt)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	for day := 5; day <= 30; day++ {
		ts := time.Date(2026, 10, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 10, 31, 22, 43, 0, 0, pdt)
	got := agg.MonthEndForecast(now)

	if !(got.ProjectedMonthEnd > got.MTDCostUSD && got.MTDCostUSD > 0) {
		t.Errorf("want ProjectedMonthEnd > MTDCostUSD > 0, got projected=%v mtd=%v (reason %q)",
			got.ProjectedMonthEnd, got.MTDCostUSD, got.Reason)
	}
	if got.Reason != "" {
		t.Errorf("Reason: got %q, want empty", got.Reason)
	}
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, pdt); !got.MonthStart.Equal(want) {
		t.Errorf("MonthStart: got %v, want %v", got.MonthStart, want)
	}
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, pdt); !got.MonthEnd.Equal(want) {
		t.Errorf("MonthEnd: got %v, want %v", got.MonthEnd, want)
	}
	if got.DaysInMonth != 31 {
		t.Errorf("DaysInMonth: got %d, want 31", got.DaysInMonth)
	}
}

// A zone whose month holds a DST shift still counts whole calendar days.
func TestMonthEndForecast_DaysInMonthAcrossDST(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load LA tz: %v", err)
	}
	pinLocal(t, la)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)
	for day := 1; day <= 10; day++ {
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", time.Date(2026, 3, day, 18, 0, 0, 0, time.UTC)))
	}
	got := agg.MonthEndForecast(time.Date(2026, 3, 11, 12, 0, 0, 0, la))
	if got.DaysInMonth != 31 {
		t.Errorf("DaysInMonth: got %d, want 31", got.DaysInMonth)
	}
}

func TestMonthEndForecast_SinceClampsWindowStart(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).
		WithPeriod(PeriodDay).
		WithFilter(Filter{Since: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)})

	// One turn per day, May 10..20, $10/day each = $110 total in the window.
	for day := 10; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	const eps = 0.01

	wantWindowStart := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	if !got.WindowStart.Equal(wantWindowStart) {
		t.Errorf("WindowStart: got %v, want %v", got.WindowStart, wantWindowStart)
	}
	if got.DaysElapsed < 10.5-eps || got.DaysElapsed > 10.5+eps {
		t.Errorf("DaysElapsed: got %v, want ~10.5", got.DaysElapsed)
	}
	if got.MTDCostUSD < 110.0-eps || got.MTDCostUSD > 110.0+eps {
		t.Errorf("MTDCostUSD: got %v, want ~110.0", got.MTDCostUSD)
	}
	// Filter stays within May → WindowCost == MTD.
	if got.WindowCostUSD < 110.0-eps || got.WindowCostUSD > 110.0+eps {
		t.Errorf("WindowCostUSD: got %v, want ~110.0", got.WindowCostUSD)
	}
	wantRate := 110.0 / 10.5
	if got.DailyRateUSD < wantRate-eps || got.DailyRateUSD > wantRate+eps {
		t.Errorf("DailyRateUSD: got %v, want ~%v", got.DailyRateUSD, wantRate)
	}
	// projected = MTD + dailyRate * remainingDays, where
	// remainingDays = monthEnd - effectiveNow = (Jun 1 00:00) - (May 20 12:00) = 11.5 days.
	wantProj := 110.0 + (110.0/10.5)*11.5
	if got.ProjectedMonthEnd < wantProj-0.5 || got.ProjectedMonthEnd > wantProj+0.5 {
		t.Errorf("ProjectedMonthEnd: got %v, want ~%v", got.ProjectedMonthEnd, wantProj)
	}
}

func TestMonthEndForecast_FirstBucketAfterSince(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay) // no Since filter

	// Data starts May 14 — no current-month buckets before May 14.
	for day := 14; day <= 16; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	const eps = 0.01
	wantWindowStart := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	if !got.WindowStart.Equal(wantWindowStart) {
		t.Errorf("WindowStart: got %v, want %v", got.WindowStart, wantWindowStart)
	}
	if got.DaysElapsed < 7.5-eps || got.DaysElapsed > 7.5+eps {
		t.Errorf("DaysElapsed: got %v, want ~7.5", got.DaysElapsed)
	}
}

func TestMonthEndForecast_UntilCutsHistorical(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).
		WithPeriod(PeriodDay).
		WithFilter(Filter{Until: time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)})

	// Two distinct days of data before Until so distinct-day gate would otherwise pass.
	for day := 1; day <= 5; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	// now is May 21 — Until=May 10 cuts off > 24h before now → historical view.
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	assertNoProjection(t, agg.MonthEndForecast(now), "historical", now)
}

func TestMonthEndForecast_UntilWithinADay(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	// Until = May 21 00:00 UTC, now = May 21 12:00 UTC → cutoff is 12h before now (< 24h).
	agg := New(prices).
		WithPeriod(PeriodDay).
		WithFilter(Filter{Until: time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)})

	// Daily turns May 1..20 — all timestamps strictly before Until.
	for day := 1; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	if got.ProjectedMonthEnd <= 0 || got.Reason != "" {
		t.Fatalf("expected a projection (Until cutoff < 24h), got %+v", got)
	}
	// Sanity: forecast should compute against effectiveNow = Until = May 21 00:00.
	// daysInWindow = 21 - 1 = 20 days.
	const eps = 0.01
	if got.DaysElapsed < 20.0-eps || got.DaysElapsed > 20.0+eps {
		t.Errorf("DaysElapsed: got %v, want ~20.0", got.DaysElapsed)
	}
}

func TestMonthEndForecast_WindowStartHonorsSinceAcrossMonths(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).
		WithPeriod(PeriodDay).
		WithFilter(Filter{
			Since: time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC),
			// Until at noon May 21 matches `now` so rate window ends at `now`
			// (rather than being capped earlier by Until). Historical-view gate
			// requires effectiveNow within 24h of now.
			Until: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		})

	// April 19..30 = 12 days @ $10/day = $120.
	for day := 19; day <= 30; day++ {
		ts := time.Date(2026, 4, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}
	// May 1..20 = 20 days @ $10/day = $200.
	for day := 1; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	const eps = 0.01

	// WindowStart must honor Since, NOT be clamped to May 1.
	wantWindowStart := time.Date(2026, 4, 19, 0, 0, 0, 0, time.UTC)
	if !got.WindowStart.Equal(wantWindowStart) {
		t.Errorf("WindowStart: got %v, want %v", got.WindowStart, wantWindowStart)
	}
	wantMonthStart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !got.MonthStart.Equal(wantMonthStart) {
		t.Errorf("MonthStart: got %v, want %v", got.MonthStart, wantMonthStart)
	}
	// MTD is current-month-only.
	if got.MTDCostUSD < 200.0-eps || got.MTDCostUSD > 200.0+eps {
		t.Errorf("MTDCostUSD: got %v, want ~200.0", got.MTDCostUSD)
	}
	// WindowCost spans April + May within the filter.
	if got.WindowCostUSD < 320.0-eps || got.WindowCostUSD > 320.0+eps {
		t.Errorf("WindowCostUSD: got %v, want ~320.0", got.WindowCostUSD)
	}
	// Rate window = May 1 → May 21 12:00 = 20.5 days (April NOT counted).
	if got.DaysElapsed < 20.5-eps || got.DaysElapsed > 20.5+eps {
		t.Errorf("DaysElapsed: got %v, want ~20.5", got.DaysElapsed)
	}
	wantRate := 200.0 / 20.5
	if got.DailyRateUSD < wantRate-eps || got.DailyRateUSD > wantRate+eps {
		t.Errorf("DailyRateUSD: got %v, want ~%v", got.DailyRateUSD, wantRate)
	}
	// projected = WindowCost + dailyRate * remainingDays
	// remainingDays = Jun 1 00:00 - May 21 12:00 = 10.5 days
	wantProj := 320.0 + (200.0/20.5)*10.5
	if got.ProjectedMonthEnd < wantProj-0.5 || got.ProjectedMonthEnd > wantProj+0.5 {
		t.Errorf("ProjectedMonthEnd: got %v, want ~%v", got.ProjectedMonthEnd, wantProj)
	}
}

func TestMonthEndForecast_WindowStartFallsBackToFirstBucketWhenSinceUnset(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay) // no Since filter

	// Data only on May 5..20.
	for day := 5; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	if got.ProjectedMonthEnd <= 0 || got.Reason != "" {
		t.Fatalf("expected a projection, got %+v", got)
	}
	wantWindowStart := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	if !got.WindowStart.Equal(wantWindowStart) {
		t.Errorf("WindowStart: got %v, want %v", got.WindowStart, wantWindowStart)
	}
}

func TestMonthEndForecast_WindowCostMatchesMTDWhenFilterStaysInMonth(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay) // no filter

	// Data May 1..20.
	for day := 1; day <= 20; day++ {
		ts := time.Date(2026, 5, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	if got.ProjectedMonthEnd <= 0 || got.Reason != "" {
		t.Fatalf("expected a projection, got %+v", got)
	}
	const eps = 0.01
	if diff := got.WindowCostUSD - got.MTDCostUSD; diff < -eps || diff > eps {
		t.Errorf("WindowCostUSD (%v) != MTDCostUSD (%v) for in-month data", got.WindowCostUSD, got.MTDCostUSD)
	}
}

func TestMonthEndForecast_February(t *testing.T) {
	pinLocal(t, time.UTC)
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)

	// Multi-day February 2026 traffic (2026 is not a leap year).
	for day := 1; day <= 10; day++ {
		ts := time.Date(2026, 2, day, 9, 0, 0, 0, time.UTC)
		agg.Add(turn("claude-opus-4-7", 2_000_000, 0, false, "/p/foo", ts))
	}

	now := time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC)
	got := agg.MonthEndForecast(now)

	if got.DaysInMonth != 28 {
		t.Errorf("DaysInMonth: got %d, want 28", got.DaysInMonth)
	}
}

// pinLocal sets time.Local for one test, since MonthEndForecast reads the
// local calendar month. Tests that call it must not run in parallel.
func pinLocal(t *testing.T, loc *time.Location) {
	t.Helper()
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
}

// assertNoProjection checks a no-projection Forecast still names its
// local month so the UI can say which month lacks data.
func assertNoProjection(t *testing.T, got Forecast, reason string, now time.Time) {
	t.Helper()
	if got.ProjectedMonthEnd != 0 {
		t.Errorf("ProjectedMonthEnd: got %v, want 0", got.ProjectedMonthEnd)
	}
	if got.Reason != reason {
		t.Errorf("Reason: got %q, want %q", got.Reason, reason)
	}
	nl := now.In(time.Local)
	wantStart := time.Date(nl.Year(), nl.Month(), 1, 0, 0, 0, 0, time.Local)
	if !got.MonthStart.Equal(wantStart) {
		t.Errorf("MonthStart: got %v, want %v", got.MonthStart, wantStart)
	}
	if want := wantStart.AddDate(0, 1, 0); !got.MonthEnd.Equal(want) {
		t.Errorf("MonthEnd: got %v, want %v", got.MonthEnd, want)
	}
	if !got.AsOf.Equal(now) {
		t.Errorf("AsOf: got %v, want %v", got.AsOf, now)
	}
	if want := time.Date(nl.Year(), nl.Month()+1, 0, 0, 0, 0, 0, time.Local).Day(); got.DaysInMonth != want {
		t.Errorf("DaysInMonth: got %d, want %d", got.DaysInMonth, want)
	}
}
