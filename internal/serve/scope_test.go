package serve

import (
	"net/url"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// newTestServerWithDefaults builds a Server with the same defaults
// the CLI ships (last=7d, sessions=10). The cache is empty by default;
// pass a seed dir via newTestServer when you want real data.
func newTestServerWithDefaults(t *testing.T, dir string) *Server {
	t.Helper()
	cache := NewCache(dir)
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	return NewServer(cache, Options{
		Prices:             loadPricesForTest(t),
		DefaultLast:        7 * 24 * time.Hour,
		DefaultHotspots:    10,
		DefaultSessionsTop: 10,
		DefaultPeriod:      aggregate.Period("day"),
		MaxCachedRenders:   4,
	})
}

func TestApplyDefaults_NoURL_SetsServerDefaults(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	now := time.Now()
	q, err := parseQuery(url.Values{}, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q, now)
	if q.Filter.Since.IsZero() {
		t.Errorf("server default did not apply: Since still zero")
	}
	if q.SessionsTop != 10 {
		t.Errorf("SessionsTop = %d, want 10", q.SessionsTop)
	}
	if q.Hotspots != 10 {
		t.Errorf("Hotspots = %d, want 10", q.Hotspots)
	}
	if string(q.Period) != "day" {
		t.Errorf("Period = %q, want day", q.Period)
	}
}

func TestApplyDefaults_URLLast_SkipsServerWindow(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	v := url.Values{}
	v.Set("last", "30d")
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q, now)
	// URL set the window — server's 7d default should not narrow.
	wantSince := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	if !q.Filter.Since.Equal(wantSince) {
		t.Errorf("Since = %s, want %s", q.Filter.Since, wantSince)
	}
	// SessionsTop still defaulted because URL didn't pin it.
	if q.SessionsTop != 10 {
		t.Errorf("SessionsTop = %d, want 10", q.SessionsTop)
	}
}

func TestApplyDefaults_ScopeAll_LiftsEverything(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	v := url.Values{}
	v.Set("scope", "all")
	now := time.Now()
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q, now)
	if !q.Filter.Since.IsZero() {
		t.Errorf("scope=all should leave Since zero; got %s", q.Filter.Since)
	}
	if q.SessionsTop < 50 {
		// scope=all lifts the cap to a generous explicit number so
		// the view is still rendered, just much more permissive.
		t.Errorf("SessionsTop = %d, want a generous cap (>=50)", q.SessionsTop)
	}
}

func TestApplyDefaults_SingleDay_SwitchesToHourly(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	v := url.Values{}
	v.Set("since", "2026-05-06")
	v.Set("until", "2026-05-07") // date-picker's exclusive next-day
	now := time.Date(2026, 5, 6, 13, 30, 0, 0, time.Local)
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q, now)

	if string(q.Period) != "hour" {
		t.Fatalf("Period = %q, want hour for same-day window", q.Period)
	}
	wantStart := time.Date(2026, 5, 6, 0, 0, 0, 0, time.Local)
	if !q.TrendFillStart.Equal(wantStart) {
		t.Errorf("TrendFillStart = %s, want %s", q.TrendFillStart, wantStart)
	}
	// Today: the chart caps at now rather than the day's end.
	if !q.TrendFillEnd.Equal(now) {
		t.Errorf("TrendFillEnd = %s, want now %s", q.TrendFillEnd, now)
	}
}

func TestApplyDefaults_SingleDayPast_FillsFullDay(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	v := url.Values{}
	v.Set("since", "2026-05-06")
	v.Set("until", "2026-05-07")
	now := time.Date(2026, 5, 20, 9, 0, 0, 0, time.Local) // well after the day
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q, now)

	if string(q.Period) != "hour" {
		t.Fatalf("Period = %q, want hour", q.Period)
	}
	// A past day caps at the last instant of the selected day.
	wantEnd := time.Date(2026, 5, 7, 0, 0, 0, 0, time.Local).Add(-time.Nanosecond)
	if !q.TrendFillEnd.Equal(wantEnd) {
		t.Errorf("TrendFillEnd = %s, want %s", q.TrendFillEnd, wantEnd)
	}
}

func TestApplyDefaults_MultiDay_StaysDaily(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	v := url.Values{}
	v.Set("since", "2026-05-06")
	v.Set("until", "2026-05-09")
	now := time.Date(2026, 5, 20, 9, 0, 0, 0, time.Local)
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q, now)

	if string(q.Period) != "day" {
		t.Errorf("multi-day window should stay daily, got %q", q.Period)
	}
	if !q.TrendFillStart.IsZero() {
		t.Errorf("multi-day should not set a trend-fill window, got %s", q.TrendFillStart)
	}
}

func TestApplyDefaults_SingleDay_ExplicitByWins(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	v := url.Values{}
	v.Set("since", "2026-05-06")
	v.Set("until", "2026-05-07")
	v.Set("by", "day") // user explicitly forced daily
	now := time.Date(2026, 5, 6, 13, 0, 0, 0, time.Local)
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q, now)

	if string(q.Period) != "day" {
		t.Errorf("explicit ?by=day should win over single-day auto-hour, got %q", q.Period)
	}
	if !q.TrendFillStart.IsZero() {
		t.Errorf("explicit ?by should not set a trend-fill window")
	}
}

func TestApplyDefaults_BadScopeRejected(t *testing.T) {
	v := url.Values{}
	v.Set("scope", "bogus")
	if _, err := parseQuery(v, time.Now()); err == nil {
		t.Errorf("expected error for ?scope=bogus")
	}
}
