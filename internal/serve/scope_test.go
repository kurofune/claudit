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
	q, err := parseQuery(url.Values{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q)
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
	q, err := parseQuery(v, time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q)
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
	q, err := parseQuery(v, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&q)
	if !q.Filter.Since.IsZero() {
		t.Errorf("scope=all should leave Since zero; got %s", q.Filter.Since)
	}
	if q.SessionsTop < 50 {
		// scope=all lifts the cap to a generous explicit number so
		// the view is still rendered, just much more permissive.
		t.Errorf("SessionsTop = %d, want a generous cap (>=50)", q.SessionsTop)
	}
}

func TestApplyDefaults_BadScopeRejected(t *testing.T) {
	v := url.Values{}
	v.Set("scope", "bogus")
	if _, err := parseQuery(v, time.Now()); err == nil {
		t.Errorf("expected error for ?scope=bogus")
	}
}
