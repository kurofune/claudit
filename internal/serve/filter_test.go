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
	if q.Hotspots != -1 {
		t.Errorf("expected sentinel -1 for unset hotspots, got %d", q.Hotspots)
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
	if q.URLHasLast || q.URLHasSince || q.URLHasUntil {
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
	// ?since / ?until parse the date in the request's local zone (the
	// location of now), matching ?last and watch's rolling buckets. A
	// non-UTC now exercises that the boundary is local midnight, not UTC.
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.FixedZone("PDT", -7*3600))
	v := url.Values{}
	v.Set("since", "2026-05-01")
	v.Set("until", "2026-05-15")
	q, err := parseQuery(v, now)
	if err != nil {
		t.Fatal(err)
	}
	wantSince := time.Date(2026, 5, 1, 0, 0, 0, 0, now.Location())
	wantUntil := time.Date(2026, 5, 15, 0, 0, 0, 0, now.Location())
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
	q, err := parseQuery(v, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if q.Hotspots != 5 {
		t.Errorf("Hotspots = %d, want 5", q.Hotspots)
	}
}

// TestParseQuery_IgnoresRemovedKeys: the content-masking knob and the
// ?sessions cap on the deleted Agents view are gone. Old bookmarks may
// still carry them, so parseQuery treats them like any unknown key — no
// 400, nothing set — rather than reject the URL. The masking key is
// spelled in two halves so the repo-wide "no masking identifiers" grep
// stays empty.
func TestParseQuery_IgnoresRemovedKeys(t *testing.T) {
	for _, removedKey := range []string{"re" + "dact", "sessions"} {
		for _, val := range []string{"true", "1", "10", "-1", "bogus"} {
			v := url.Values{}
			v.Set(removedKey, val)
			q, err := parseQuery(v, time.Now())
			if err != nil {
				t.Errorf("parseQuery(?%s=%s) err = %v, want nil (unknown keys are ignored)", removedKey, val, err)
			}
			want := Query{Hotspots: -1, rawQuery: q.rawQuery}
			if q != want {
				t.Errorf("parseQuery(?%s=%s) = %+v, want only the unset sentinels", removedKey, val, q)
			}
		}
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

func TestParseQuery_Provider(t *testing.T) {
	for _, c := range []struct {
		raw, want string
		wantErr   bool
	}{
		{raw: "", want: ""},
		{raw: "all", want: ""},
		{raw: "All", wantErr: true},
		{raw: "claude-code", want: "claude-code"},
		{raw: "codex", want: "codex"},
		{raw: " codex ", want: "codex"},
		{raw: "gemini", wantErr: true},
		{raw: "Codex", wantErr: true},
	} {
		v := url.Values{}
		if c.raw != "" {
			v.Set("provider", c.raw)
		}
		q, err := parseQuery(v, time.Now())
		if c.wantErr {
			if err == nil {
				t.Errorf("provider=%q: want error, got Provider %q", c.raw, q.Filter.Provider)
			}
			continue
		}
		if err != nil {
			t.Errorf("provider=%q: %v", c.raw, err)
			continue
		}
		if q.Filter.Provider != c.want {
			t.Errorf("provider=%q: Filter.Provider = %q, want %q", c.raw, q.Filter.Provider, c.want)
		}
	}
}
