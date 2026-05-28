package aggregate

import (
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// RollingTotals sums turn cost into four trailing buckets measured
// against now in local time. It is the single source of truth for the
// hour/today/week/month panel `claudit watch` renders — the same
// per-turn cost computation report/serve use, with one set of boundary
// conventions:
//
//   - hour:  trailing 60 minutes (rolling, not calendar-aligned)
//   - today: since local midnight
//   - week:  since the most recent Monday 00:00 local (ISO week)
//   - month: since the 1st of this month 00:00 local
//
// Boundaries are computed in now.Location(); a turn's timestamp counts
// when it is at or after the boundary instant. JSONL timestamps are
// usually UTC, which is fine — time comparisons are wall-clock-
// independent (they compare the absolute instant), so a UTC timestamp
// is correctly bucketed against a local-zone boundary.
func RollingTotals(turns []parse.Turn, prices *pricing.Table, now time.Time) (hour, today, week, month float64) {
	loc := now.Location()
	hourStart := now.Add(-time.Hour)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	// Weekday(): Sunday=0..Saturday=6. ISO week starts on Monday, so
	// treat Sunday as "7 days into the week" rather than day zero.
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := todayStart.AddDate(0, 0, -(weekday - 1))
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

	for _, t := range turns {
		cost, _ := prices.Cost(t.Model,
			t.Usage.InputTokens, t.Usage.OutputTokens,
			t.Usage.CacheCreate5mTokens, t.Usage.CacheCreate1hTokens,
			t.Usage.CacheReadTokens)
		if cost == 0 {
			continue
		}
		if !t.Timestamp.Before(monthStart) {
			month += cost
		}
		if !t.Timestamp.Before(weekStart) {
			week += cost
		}
		if !t.Timestamp.Before(todayStart) {
			today += cost
		}
		if !t.Timestamp.Before(hourStart) {
			hour += cost
		}
	}
	return hour, today, week, month
}
