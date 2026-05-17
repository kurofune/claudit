package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/parse"
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
	// scan, restricted to the last 30 days. Live additions append to
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

// newRollingTotals scans root for every JSONL, parses it, and seeds
// the history slice with each assistant turn's (timestamp, cost)
// captured over the last 30 days. now is injected for test
// determinism. Returns the populated state.
//
// Errors are aggregated and ignored per-file — a corrupt JSONL in
// the user's history should not prevent the live watch from starting.
// If we can't even walk the root, we return that error.
func newRollingTotals(root string, prices *pricing.Table, now time.Time) (*rollingTotals, error) {
	cutoff := now.AddDate(0, 0, -30)
	hitCutoff := now.AddDate(0, 0, -7)

	rt := &rollingTotals{prices: prices}
	if root == "" {
		return rt, nil
	}
	if _, err := os.Stat(root); err != nil {
		// A missing root just means no history yet — the live tail will
		// still work. Don't treat it as fatal.
		if os.IsNotExist(err) {
			return rt, nil
		}
		return nil, err
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		res, _ := parse.ParseFile(f, path)
		for _, t := range res.Turns {
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
		return nil
	})
	if err != nil {
		return nil, err
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
