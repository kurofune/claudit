package aggregate

import (
	"sort"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// Period is a fixed-size time bucket for trend mode. PeriodNone disables
// trend tracking entirely — Add() then skips the per-bucket rollups.
type Period string

const (
	PeriodNone  Period = ""
	PeriodDay   Period = "day"
	PeriodWeek  Period = "week"
	PeriodMonth Period = "month"
)

// Valid reports whether p is a real period (PeriodNone is not).
func (p Period) Valid() bool {
	switch p {
	case PeriodDay, PeriodWeek, PeriodMonth:
		return true
	}
	return false
}

// Truncate returns the start of the period bucket containing t, in UTC.
// Weeks snap to Monday 00:00 UTC.
func (p Period) Truncate(t time.Time) time.Time {
	t = t.UTC()
	switch p {
	case PeriodDay:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	case PeriodWeek:
		// Weekday: Sun=0..Sat=6. Days back to Monday = (weekday+6)%7.
		back := (int(t.Weekday()) + 6) % 7
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return start.AddDate(0, 0, -back)
	case PeriodMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

// Step returns t advanced by one period.
func (p Period) Step(t time.Time) time.Time {
	switch p {
	case PeriodDay:
		return t.AddDate(0, 0, 1)
	case PeriodWeek:
		return t.AddDate(0, 0, 7)
	case PeriodMonth:
		return t.AddDate(0, 1, 0)
	}
	return t
}

// TrendPoint is one (period, key) cell. Time is the bucket start in UTC.
// Tokens is embedded so renderers can derive HitRatio() / MissTokens()
// per bucket — that's how "hit ratio over time" works.
//
// Sessions is the count of distinct session IDs active in this bucket.
// Only populated for the totals-level series (TrendTotals()); per-key
// series leave it zero. Used for the sessions period-over-period delta
// on the headline tiles.
type TrendPoint struct {
	Time     time.Time `json:"time"`
	CostUSD  float64   `json:"cost_usd"`
	Turns    int       `json:"turns"`
	Sessions int       `json:"sessions"`
	Tokens
}

// addTrend bumps the (key, bucket) cell. m must be non-nil.
func addTrend(m map[time.Time]*TrendPoint, bucket time.Time, cost float64, u parse.Usage) {
	tp := m[bucket]
	if tp == nil {
		tp = &TrendPoint{Time: bucket}
		m[bucket] = tp
	}
	tp.CostUSD += cost
	tp.Turns++
	tp.Tokens.addUsage(u)
}

// gapFill returns the points sorted ascending, with zero-cost cells
// inserted for every missing period between the first and last point.
// Returns nil for empty input.
func gapFill(p Period, m map[time.Time]*TrendPoint) []TrendPoint {
	if len(m) == 0 {
		return nil
	}
	keys := make([]time.Time, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	first, last := keys[0], keys[len(keys)-1]
	var out []TrendPoint
	for t := first; !t.After(last); t = p.Step(t) {
		if pt := m[t]; pt != nil {
			out = append(out, *pt)
		} else {
			out = append(out, TrendPoint{Time: t})
		}
	}
	return out
}

// TrendTotals returns gap-filled cost-by-period for the report. Empty
// slice when period is not set or no turns were counted. Sessions count
// is backfilled here from the per-bucket session sets recorded in Add().
func (a *Aggregator) TrendTotals() []TrendPoint {
	if !a.period.Valid() {
		return nil
	}
	pts := gapFill(a.period, a.trendTotals)
	for i := range pts {
		if s, ok := a.bucketSessions[pts[i].Time]; ok {
			pts[i].Sessions = len(s)
		}
	}
	return pts
}

// TrendByModel returns gap-filled per-model time series. Each series
// covers the global date range so cells line up across models.
func (a *Aggregator) TrendByModel() map[string][]TrendPoint {
	return a.trendByKey(a.trendByModel)
}

// TrendByProject returns gap-filled per-project time series.
func (a *Aggregator) TrendByProject() map[string][]TrendPoint {
	return a.trendByKey(a.trendByProject)
}

// TrendByTool returns gap-filled per-tool time series.
func (a *Aggregator) TrendByTool() map[string][]TrendPoint {
	return a.trendByKey(a.trendByTool)
}

// TrendBySession returns gap-filled per-session time series. Powers the
// hit-ratio sparkline column on the cache-by-session view.
func (a *Aggregator) TrendBySession() map[string][]TrendPoint {
	return a.trendByKey(a.trendBySession)
}

// TrendBySubagent returns gap-filled per-subagent-type time series.
// Powers the hit-ratio sparkline column on the cache-by-subagent view.
func (a *Aggregator) TrendBySubagent() map[string][]TrendPoint {
	return a.trendByKey(a.trendBySub)
}

// Period exposes the period the aggregator is bucketing on.
func (a *Aggregator) Period() Period { return a.period }

// trendByKey gap-fills each series, but only across that series' own
// observed range. For row sparklines we want shape, not absolute
// alignment — and a project that ran for one week shouldn't render as
// 200 zero cells just because another project ran longer.
func (a *Aggregator) trendByKey(src map[string]map[time.Time]*TrendPoint) map[string][]TrendPoint {
	if !a.period.Valid() || len(src) == 0 {
		return nil
	}
	out := make(map[string][]TrendPoint, len(src))
	for k, m := range src {
		out[k] = gapFill(a.period, m)
	}
	return out
}
