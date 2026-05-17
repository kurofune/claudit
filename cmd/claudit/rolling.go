package main

import (
	"time"

	"github.com/kurofune/claudit/internal/pricing"
)

// rollingTotals tracks today / week / month spend across the entire
// projects root, plus a 7-day baseline used by the end-of-session
// anomaly callout. Built once at watch startup by scanning the root,
// then incrementally updated as live events arrive.
//
// All time math runs in local time — the user thinks "today" in their
// own timezone, not UTC. The bucket boundaries are recomputed on
// every addLive call so a watch session that spans midnight still
// reports today's spend correctly after the clock rolls.
type rollingTotals struct {
	prices *pricing.Table

	// History holds turn (timestamp, cost) pairs from the startup
	// scan, restricted to the scan window. Live additions append to
	// this slice. We don't try to free old entries — a month of turns
	// at one-per-minute is ~43k records and fits comfortably in memory.
	history []turnSample

	// hitTokens / hitDenom are the cache-read and cache-eligible token
	// sums over the trailing 7 days (rolling). Used by the end-of-
	// session summary to compare this session's hit ratio to baseline.
	hitTokens int64
	hitDenom  int64
}

type turnSample struct {
	at   time.Time
	cost float64
}

// defaultScanDays is the rolling window when the user doesn't pass
// --scan-days. Thirty days covers the "month" bucket exactly.
const defaultScanDays = 30

// newRollingTotals scans root for every JSONL and seeds history with
// each assistant turn's (timestamp, cost) within the trailing 30
// days. now is injected for test determinism.
func newRollingTotals(root string, prices *pricing.Table, now time.Time) (*rollingTotals, error) {
	return newRollingTotalsWithDays(root, prices, now, defaultScanDays)
}

// newRollingTotalsWithDays is the parameterized variant — exposed so
// the watch CLI can pipe through --scan-days. scanDays <= 0 is
// clamped to 1; very large values are accepted as-is.
//
// File discovery uses listJSONL's mtime filter so files whose last
// modification predates the scan window are skipped without opening.
// Parsing is fanned out to GOMAXPROCS workers via the shared
// parseConcurrently helper — same primitive that report and diff use,
// so the worker-pool design lives in one place.
//
// Errors are aggregated and ignored per-file — a corrupt JSONL in
// the user's history should not prevent the live watch from starting.
// If we can't even walk the root, we return that error.
func newRollingTotalsWithDays(root string, prices *pricing.Table, now time.Time, scanDays int) (*rollingTotals, error) {
	if scanDays <= 0 {
		scanDays = 1
	}
	cutoff := now.AddDate(0, 0, -scanDays)
	hitCutoff := now.AddDate(0, 0, -7)

	rt := &rollingTotals{prices: prices}
	if root == "" {
		return rt, nil
	}

	files, err := listJSONL(root, cutoff)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return rt, nil
	}

	turns, _, _, _, _ := parseConcurrently(files)
	for _, t := range turns {
		if t.Timestamp.Before(cutoff) {
			continue
		}
		cost, _ := prices.Cost(t.Model,
			t.Usage.InputTokens, t.Usage.OutputTokens,
			t.Usage.CacheCreate5mTokens, t.Usage.CacheCreate1hTokens,
			t.Usage.CacheReadTokens)
		rt.history = append(rt.history, turnSample{at: t.Timestamp, cost: cost})
		if !t.Timestamp.Before(hitCutoff) {
			rt.hitTokens += int64(t.Usage.CacheReadTokens)
			rt.hitDenom += int64(t.Usage.CacheReadTokens) +
				int64(t.Usage.InputTokens) +
				int64(t.Usage.CacheCreate5mTokens) +
				int64(t.Usage.CacheCreate1hTokens)
		}
	}
	return rt, nil
}

// addLive appends a freshly-observed turn to the history. The
// timestamp can be either the turn's recorded wall-clock time or
// (when unset) the live observation time.
func (rt *rollingTotals) addLive(at time.Time, cost float64, _ time.Time) {
	if at.IsZero() {
		return
	}
	rt.history = append(rt.history, turnSample{at: at, cost: cost})
}

// totals returns (today, week, month) summed against now in local time.
// Boundaries: today = since midnight local; week = since most recent
// Monday 00:00 local (ISO week); month = since the 1st of this month
// 00:00 local.
func (rt *rollingTotals) totals(now time.Time) (today, week, month float64) {
	loc := now.Location()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	// Weekday(): Sunday=0..Saturday=6. ISO week starts on Monday, so
	// shift Sunday to "7 days back" rather than "0 days back."
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := todayStart.AddDate(0, 0, -(weekday - 1))
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	for _, s := range rt.history {
		// History timestamps usually come in UTC from the JSONL.
		// Comparing them against local-time boundaries works because
		// time.Time comparisons are wall-clock-independent (they compare
		// the absolute instant).
		if !s.at.Before(monthStart) {
			month += s.cost
		}
		if !s.at.Before(weekStart) {
			week += s.cost
		}
		if !s.at.Before(todayStart) {
			today += s.cost
		}
	}
	return today, week, month
}

// baselineHitRatio returns the trailing-7-day cache hit ratio (0..1),
// or 0 when there is no cache-eligible traffic in the window.
func (rt *rollingTotals) baselineHitRatio() float64 {
	if rt.hitDenom <= 0 {
		return 0
	}
	return float64(rt.hitTokens) / float64(rt.hitDenom)
}
