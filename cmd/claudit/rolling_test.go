package main

import (
	"testing"
	"time"
)

func TestRollingTotals_Buckets(t *testing.T) {
	// Fixed "now" so the test isn't time-of-day dependent.
	loc := time.FixedZone("test", 0)
	now := time.Date(2026, 5, 15, 10, 0, 0, 0, loc) // Friday
	rt := &rollingTotals{}

	rt.history = []turnSample{
		// Two months ago — outside every window.
		{at: now.AddDate(0, -2, 0), cost: 100.00},
		// 10 days ago — inside month, outside week.
		{at: now.AddDate(0, 0, -10), cost: 5.00},
		// 2 days ago (Wednesday) — inside month, inside week.
		{at: now.AddDate(0, 0, -2), cost: 2.00},
		// Today, 2 hours ago — inside today, outside hour.
		{at: now.Add(-2 * time.Hour), cost: 1.00},
		// 15 minutes ago — inside hour.
		{at: now.Add(-15 * time.Minute), cost: 0.25},
	}

	hour, today, week, month := rt.totals(now)
	if hour != 0.25 {
		t.Errorf("hour = %v, want 0.25 (15m-old only)", hour)
	}
	if today != 1.25 {
		t.Errorf("today = %v, want 1.25", today)
	}
	if week != 3.25 {
		t.Errorf("week = %v, want 3.25 (Wed + today)", week)
	}
	if month != 8.25 {
		t.Errorf("month = %v, want 8.25 (10-day + Wed + today)", month)
	}
}

func TestRollingTotals_HourIsTrailing60m(t *testing.T) {
	// Hour is a rolling 60-min window, not "since the top of this clock
	// hour" — verify the boundary directly.
	loc := time.FixedZone("test", 0)
	now := time.Date(2026, 5, 15, 10, 30, 0, 0, loc)
	rt := &rollingTotals{
		history: []turnSample{
			{at: now.Add(-61 * time.Minute), cost: 9.00}, // just outside
			{at: now.Add(-59 * time.Minute), cost: 1.00}, // just inside
			{at: now.Add(-time.Second), cost: 0.50},      // very recent
		},
	}
	hour, _, _, _ := rt.totals(now)
	if hour != 1.50 {
		t.Errorf("hour = %v, want 1.50 (61m-old must be excluded)", hour)
	}
}

func TestRollingTotals_WeekStartsOnMonday(t *testing.T) {
	loc := time.FixedZone("test", 0)
	// Sunday is the rollover edge case — ISO week ends Sunday inclusive,
	// new week starts Monday 00:00. Place "now" on Sunday and confirm
	// Saturday is still in the same ISO week.
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, loc) // Sunday
	rt := &rollingTotals{}
	rt.history = []turnSample{
		{at: time.Date(2026, 5, 11, 0, 0, 0, 0, loc), cost: 1}, // Monday
		{at: time.Date(2026, 5, 16, 0, 0, 0, 0, loc), cost: 2}, // Saturday
	}
	_, _, week, _ := rt.totals(now)
	if week != 3 {
		t.Errorf("week = %v, want 3 (Mon + Sat of same ISO week)", week)
	}
}

func TestRollingTotals_AddLiveExtendsHistory(t *testing.T) {
	loc := time.FixedZone("test", 0)
	now := time.Date(2026, 5, 15, 10, 0, 0, 0, loc)
	rt := &rollingTotals{}
	rt.addLive(now, 0.50, now)
	rt.addLive(time.Time{}, 9.99, now) // zero ts ignored
	_, today, _, _ := rt.totals(now)
	if today != 0.50 {
		t.Errorf("today = %v, want 0.50 (zero-ts addLive must be dropped)", today)
	}
}

func TestRollingTotals_BaselineHitRatio(t *testing.T) {
	rt := &rollingTotals{hitTokens: 3, hitDenom: 10}
	if got := rt.baselineHitRatio(); got != 0.3 {
		t.Errorf("got %v, want 0.3", got)
	}
	empty := &rollingTotals{}
	if got := empty.baselineHitRatio(); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
}
