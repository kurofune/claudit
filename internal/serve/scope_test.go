package serve

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
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
		ReloadIntervalSec:  30,
		MaxCachedRenders:   4,
	})
}

func TestApplyDefaults_NoURL_SetsServerDefaults(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	q, err := parseQuery(url.Values{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scope := srv.applyDefaults(&q)
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
	if !scope.IsDefault {
		t.Errorf("scope.IsDefault = false, want true")
	}
	if scope.WindowLabel == "" {
		t.Errorf("scope.WindowLabel empty, want a human label")
	}
	if scope.SessionsCap != 10 {
		t.Errorf("scope.SessionsCap = %d, want 10", scope.SessionsCap)
	}
	if scope.LiftURL == "" || !strings.Contains(scope.LiftURL, "scope=all") {
		t.Errorf("scope.LiftURL = %q, want a URL containing scope=all", scope.LiftURL)
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
	scope := srv.applyDefaults(&q)
	// URL set the window — server's 7d default should not narrow.
	wantSince := time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)
	if !q.Filter.Since.Equal(wantSince) {
		t.Errorf("Since = %s, want %s", q.Filter.Since, wantSince)
	}
	// SessionsTop still defaulted because URL didn't pin it.
	if q.SessionsTop != 10 {
		t.Errorf("SessionsTop = %d, want 10", q.SessionsTop)
	}
	// Pill: window comes from URL (not a server default), sessions
	// IS a server default, so the pill is still shown.
	if !scope.IsDefault {
		t.Errorf("scope.IsDefault = false, want true (sessions default still in effect)")
	}
	if scope.WindowLabel != "" {
		t.Errorf("scope.WindowLabel = %q, want empty (window came from URL)", scope.WindowLabel)
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
	scope := srv.applyDefaults(&q)
	if !q.Filter.Since.IsZero() {
		t.Errorf("scope=all should leave Since zero; got %s", q.Filter.Since)
	}
	if q.SessionsTop < 50 {
		// scope=all lifts the cap to a generous explicit number so
		// the view is still rendered, just much more permissive.
		t.Errorf("SessionsTop = %d, want a generous cap (>=50)", q.SessionsTop)
	}
	if scope.IsDefault {
		t.Errorf("scope.IsDefault = true, want false (scope=all)")
	}
}

func TestApplyDefaults_BadScopeRejected(t *testing.T) {
	v := url.Values{}
	v.Set("scope", "bogus")
	if _, err := parseQuery(v, time.Now()); err == nil {
		t.Errorf("expected error for ?scope=bogus")
	}
}

// scopeNoteDOMRegex matches the inline scope note inside the filter
// bar. The class name also lives in the always-emitted CSS, so a bare
// substring check is ambiguous — we anchor on the element opener.
var scopeNoteDOMRegex = regexp.MustCompile(`<span\s+class="scope-note"`)

func TestServer_ScopeNoteRendered(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !scopeNoteDOMRegex.MatchString(body) {
		t.Errorf("served report missing inline scope note")
	}
	if !strings.Contains(body, "show all") {
		t.Errorf("scope note missing 'show all' link text")
	}
	if !strings.Contains(body, "7 days") {
		t.Errorf("scope note missing window label '7 days'; body excerpt: %s",
			snippet(body, "scope-note", 400))
	}
}

func TestServer_ScopeNoteSuppressedWhenScopeAll(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if scopeNoteDOMRegex.MatchString(w.Body.String()) {
		t.Errorf("scope=all should suppress the scope note, but it's present")
	}
}

// snippet returns up to n chars of body starting at the first
// occurrence of needle, for diagnostic logging.
func snippet(body, needle string, n int) string {
	i := strings.Index(body, needle)
	if i < 0 {
		return "(needle not found)"
	}
	end := i + n
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}
