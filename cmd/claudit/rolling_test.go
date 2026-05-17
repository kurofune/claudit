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
		// Today.
		{at: now.Add(-time.Hour), cost: 1.00},
	}

	today, week, month := rt.totals(now)
	if today != 1.00 {
		t.Errorf("today = %v, want 1.00", today)
	}
	if week != 3.00 {
		t.Errorf("week = %v, want 3.00 (Wed + today)", week)
	}
	if month != 8.00 {
		t.Errorf("month = %v, want 8.00 (10-day + Wed + today)", month)
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
	_, week, _ := rt.totals(now)
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
	today, _, _ := rt.totals(now)
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
