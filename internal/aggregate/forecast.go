package aggregate

import "time"

// Forecast is a linear month-to-date projection of cost for the calendar
// month containing AsOf. Zero value means "no forecast available" — the
// renderer skips display when ProjectedMonthEnd is zero.
type Forecast struct {
	AsOf              time.Time `json:"as_of"`
	MonthStart        time.Time `json:"month_start"`
	MonthEnd          time.Time `json:"month_end"`    // first of next month (exclusive)
	WindowStart       time.Time `json:"window_start"` // chart left edge (UTC); honors filter.Since across months
	DaysElapsed       float64   `json:"days_elapsed"` // length of the internal rate window (current month only)
	DaysInMonth       int       `json:"days_in_month"`
	MTDCostUSD        float64   `json:"mtd_cost_usd"`    // current month only
	WindowCostUSD     float64   `json:"window_cost_usd"` // total cost across filter (may span months)
	DailyRateUSD      float64   `json:"daily_rate_usd"`
	ProjectedMonthEnd float64   `json:"projected_month_end_usd"`
}

// MonthEndForecast returns a linear month-to-date cost projection for the
// calendar month containing now. Returns a zero Forecast when the period
// is not PeriodDay, when there's insufficient current-month data, or
// when too little of the month has elapsed.
func (a *Aggregator) MonthEndForecast(now time.Time) Forecast {
	if a.period != PeriodDay {
		return Forecast{}
	}
	nowUTC := now.UTC()
	monthStart := time.Date(nowUTC.Year(), nowUTC.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Sum MTD cost (current month only), find the earliest current-month
	// bucket with cost (used for the internal rate-window start clamp), and
	// find the earliest bucket overall (used as the chart's WindowStart
	// fallback when no Since filter is set).
	var mtd float64
	distinctDays := 0
	var firstCurrentMonthBucket time.Time
	var firstBucketOverall time.Time
	for bucket, tp := range a.trendTotals {
		if tp.CostUSD <= 0 {
			continue
		}
		if firstBucketOverall.IsZero() || bucket.Before(firstBucketOverall) {
			firstBucketOverall = bucket
		}
		if bucket.Year() != nowUTC.Year() || bucket.Month() != nowUTC.Month() {
			continue
		}
		mtd += tp.CostUSD
		distinctDays++
		if firstCurrentMonthBucket.IsZero() || bucket.Before(firstCurrentMonthBucket) {
			firstCurrentMonthBucket = bucket
		}
	}
	if distinctDays < 2 {
		return Forecast{}
	}

	// rateStart (internal): max(monthStart, filter.Since, firstCurrentMonthBucketWithCost).
	// This bounds the rate-window so DailyRateUSD reflects only current-month
	// activity even when Since is in a prior month.
	rateStart := monthStart
	if !a.filter.Since.IsZero() && a.filter.Since.After(rateStart) {
		rateStart = a.filter.Since.UTC()
	}
	if !firstCurrentMonthBucket.IsZero() && firstCurrentMonthBucket.After(rateStart) {
		rateStart = firstCurrentMonthBucket
	}

	// effectiveNow = min(now, filter.Until). Skip forecast if filter.Until cuts
	// off more than 24h before now — that's a historical view.
	effectiveNow := nowUTC
	if !a.filter.Until.IsZero() && a.filter.Until.Before(effectiveNow) {
		effectiveNow = a.filter.Until.UTC()
	}
	if nowUTC.Sub(effectiveNow) > 24*time.Hour {
		return Forecast{}
	}

	daysInMonth := int(monthEnd.Sub(monthStart).Hours() / 24)
	daysInRateWindow := effectiveNow.Sub(rateStart).Hours() / 24
	if daysInRateWindow < 2.0 {
		return Forecast{}
	}

	// WindowStart (chart left edge): honor Since when set; else fall back to
	// the earliest bucket overall; else monthStart. NO clamping to monthStart —
	// the chart is allowed to span before the current month.
	var windowStart time.Time
	switch {
	case !a.filter.Since.IsZero():
		windowStart = a.filter.Since.UTC()
	case !firstBucketOverall.IsZero():
		windowStart = firstBucketOverall
	default:
		windowStart = monthStart
	}
	if windowStart.After(monthEnd) {
		return Forecast{}
	}

	windowCost := a.Totals().CostUSD
	dailyRate := mtd / daysInRateWindow
	remainingDays := monthEnd.Sub(effectiveNow).Hours() / 24
	if remainingDays < 0 {
		remainingDays = 0
	}
	projected := windowCost + dailyRate*remainingDays

	return Forecast{
		AsOf:              nowUTC,
		MonthStart:        monthStart,
		MonthEnd:          monthEnd,
		WindowStart:       windowStart,
		DaysElapsed:       daysInRateWindow,
		DaysInMonth:       daysInMonth,
		MTDCostUSD:        mtd,
		WindowCostUSD:     windowCost,
		DailyRateUSD:      dailyRate,
		ProjectedMonthEnd: projected,
	}
}
