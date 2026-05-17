package serve

import (
	"net/url"
	"testing"
	"time"
)

func TestParseQuery_Empty(t *testing.T) {
	q, err := parseQuery(url.Values{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if q.Filter.ProjectSubstring != "" {
		t.Errorf("project = %q, want empty", q.Filter.ProjectSubstring)
	}
	if !q.Filter.Since.IsZero() || !q.Filter.Until.IsZero() {
		t.Errorf("date filter set: %+v", q.Filter)
	}
	if q.Hotspots != -1 || q.SessionsTop != -1 {
		t.Errorf("expected sentinel -1 for unset counts, got hotspots=%d sessions=%d", q.Hotspots, q.SessionsTop)
	}
	// No ?by in URL: parseQuery leaves Period zero so applyDefaults
	// can overlay the server default. The server doesn't get to
	// learn the user's choice from the URL alone.
	if q.Period != "" {
		t.Errorf("Period = %q, want empty (URL didn't pin it)", q.Period)
	}
	if q.URLHasPeriod {
		t.Errorf("URLHasPeriod = true, want false")
	}
	if q.URLHasLast || q.URLHasSince || q.URLHasUntil || q.URLHasSess {
		t.Errorf("URL-set flags should all be false: %+v", q)
	}
}

func TestParseQuery_Last(t *testing.T) {
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	v := url.Values{}
	v.Set("last", "7d")
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC)
	if !q.Filter.Since.Equal(want) {
		t.Errorf("Since = %s, want %s", q.Filter.Since, want)
	}
	if !q.URLHasLast {
		t.Errorf("URLHasLast = false, want true")
	}
	if q.LastLabel != "7d" {
		t.Errorf("LastLabel = %q, want 7d", q.LastLabel)
	}
}

func TestParseQuery_SinceUntil(t *testing.T) {
	v := url.Values{}
	v.Set("since", "2026-05-01")
	v.Set("until", "2026-05-15")
	q, err := parseQuery(v, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	wantSince := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	wantUntil := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	if !q.Filter.Since.Equal(wantSince) {
		t.Errorf("Since = %s, want %s", q.Filter.Since, wantSince)
	}
	if !q.Filter.Until.Equal(wantUntil) {
		t.Errorf("Until = %s, want %s", q.Filter.Until, wantUntil)
	}
}

func TestParseQuery_LastAndSinceRejected(t *testing.T) {
	v := url.Values{}
	v.Set("last", "7d")
	v.Set("since", "2026-05-01")
	if _, err := parseQuery(v, time.Now()); err == nil {
		t.Errorf("expected error when both last and since are passed")
	}
}

func TestParseQuery_BadDate(t *testing.T) {
	v := url.Values{}
	v.Set("since", "not-a-date")
	if _, err := parseQuery(v, time.Now()); err == nil {
		t.Errorf("expected error for bad since")
	}
}

func TestParseQuery_By(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"day", "day", true},
		{"week", "week", true},
		{"month", "month", true},
		{"off", "", true},
		{"hourly", "", false},
	}
	for _, c := range cases {
		v := url.Values{}
		v.Set("by", c.in)
		q, err := parseQuery(v, time.Now())
		if c.ok && err != nil {
			t.Errorf("by=%q unexpected error: %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Errorf("by=%q: expected error", c.in)
		}
		if c.ok && string(q.Period) != c.want {
			t.Errorf("by=%q: Period=%q, want %q", c.in, q.Period, c.want)
		}
	}
}

func TestParseQuery_Counts(t *testing.T) {
	v := url.Values{}
	v.Set("hotspots", "5")
	v.Set("sessions", "0")
	v.Set("redact", "true")
	q, err := parseQuery(v, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if q.Hotspots != 5 {
		t.Errorf("Hotspots = %d, want 5", q.Hotspots)
	}
	if q.SessionsTop != 0 {
		t.Errorf("SessionsTop = %d, want 0", q.SessionsTop)
	}
	if !q.Redact {
		t.Errorf("Redact = false, want true")
	}
}

func TestParseQuery_NegativeCountsRejected(t *testing.T) {
	v := url.Values{}
	v.Set("hotspots", "-1")
	if _, err := parseQuery(v, time.Now()); err == nil {
		t.Errorf("expected error for negative hotspots (sentinel must come from sentinel default, not the URL)")
	}
}

func TestParseLastDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"7d", 7 * 24 * time.Hour, true},
		{"2w", 14 * 24 * time.Hour, true},
		{"0d", 0, false},
		{"-1d", 0, false},
		{"7", 0, false},
		{"d", 0, false},
		{"abcd", 0, false},
	}
	for _, c := range cases {
		got, err := parseLastDuration(c.in)
		if c.ok && err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%q: expected error", c.in)
		}
		if c.ok && got != c.want {
			t.Errorf("%q: got %s, want %s", c.in, got, c.want)
		}
	}
}
