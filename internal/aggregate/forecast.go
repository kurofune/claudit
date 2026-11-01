package aggregate

import "time"

// Forecast is a linear month-to-date projection of cost for the local
// calendar month containing AsOf. ProjectedMonthEnd is zero when no
// projection is possible; Reason then says why, and AsOf, MonthStart,
// MonthEnd and DaysInMonth still name the month.
type Forecast struct {
	AsOf              time.Time `json:"as_of"`
	MonthStart        time.Time `json:"month_start"`  // local midnight on the 1st
	MonthEnd          time.Time `json:"month_end"`    // local midnight on the 1st of next month (exclusive)
	WindowStart       time.Time `json:"window_start"` // chart left edge; honors filter.Since across months
	DaysElapsed       float64   `json:"days_elapsed"` // length of the internal rate window (current month only)
	DaysInMonth       int       `json:"days_in_month"`
	MTDCostUSD        float64   `json:"mtd_cost_usd"`    // current month only
	WindowCostUSD     float64   `json:"window_cost_usd"` // total cost across filter (may span months)
	DailyRateUSD      float64   `json:"daily_rate_usd"`
	ProjectedMonthEnd float64   `json:"projected_month_end_usd"`
	Reason            string    `json:"reason"` // "" with a projection, else a ForecastReason* value
}

// Why a Forecast carries no projection.
const (
	ForecastReasonNotDaily   = "not_daily"    // trend is not bucketed by day
	ForecastReasonHistorical = "historical"   // filter.Until ends more than 24h before now
	ForecastReasonTooFewDays = "too_few_days" // under two costed days, or two days of rate window, this month
)

// MonthEndForecast returns a linear month-to-date cost projection for the
// local calendar month containing now — the same local-midnight calendar
// the ?since/?until filters use. Trend day buckets are UTC midnights; each
// counts toward the local month containing its start instant.
func (a *Aggregator) MonthEndForecast(now time.Time) Forecast {
	nowUTC := now.UTC()
	nowLocal := now.In(time.Local)
	monthStart := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, time.Local)
	monthEnd := time.Date(nowLocal.Year(), nowLocal.Month()+1, 1, 0, 0, 0, 0, time.Local)
	daysInMonth := time.Date(nowLocal.Year(), nowLocal.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
	none := func(reason string) Forecast {
		return Forecast{AsOf: nowUTC, MonthStart: monthStart, MonthEnd: monthEnd, DaysInMonth: daysInMonth, Reason: reason}
	}

	if a.period != PeriodDay {
		return none(ForecastReasonNotDaily)
	}

	// effectiveNow = min(now, filter.Until). A filter.Until more than 24h
	// before now is a historical view: nothing left to project.
	effectiveNow := nowLocal
	if !a.filter.Until.IsZero() && a.filter.Until.Before(effectiveNow) {
		effectiveNow = a.filter.Until.In(time.Local)
	}
	if nowUTC.Sub(effectiveNow) > 24*time.Hour {
		return none(ForecastReasonHistorical)
	}

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
		if b := bucket.In(time.Local); b.Year() != nowLocal.Year() || b.Month() != nowLocal.Month() {
			continue
		}
		mtd += tp.CostUSD
		distinctDays++
		if firstCurrentMonthBucket.IsZero() || bucket.Before(firstCurrentMonthBucket) {
			firstCurrentMonthBucket = bucket
		}
	}
	if distinctDays < 2 {
		return none(ForecastReasonTooFewDays)
	}

	// rateStart (internal): max(monthStart, filter.Since, firstCurrentMonthBucketWithCost).
	// This bounds the rate-window so DailyRateUSD reflects only current-month
	// activity even when Since is in a prior month.
	rateStart := monthStart
	if !a.filter.Since.IsZero() && a.filter.Since.After(rateStart) {
		rateStart = a.filter.Since.In(time.Local)
	}
	if firstCurrentMonthBucket.After(rateStart) {
		rateStart = firstCurrentMonthBucket.In(time.Local)
	}

	daysInRateWindow := effectiveNow.Sub(rateStart).Hours() / 24
	if daysInRateWindow < 2.0 {
		return none(ForecastReasonTooFewDays)
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
		return none(ForecastReasonTooFewDays)
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
