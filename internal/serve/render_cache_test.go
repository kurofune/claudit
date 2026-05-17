package serve

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

func newTestServerWithCache(t *testing.T, dir string, cacheSize int) *Server {
	t.Helper()
	cache := NewCache(dir)
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	return NewServer(cache, Options{
		Prices:             loadPricesForTest(t),
		DefaultLast:        7 * 24 * time.Hour,
		DefaultSessionsTop: 10,
		DefaultHotspots:    10,
		DefaultPeriod:      aggregate.Period("day"),
		MaxCachedRenders:   cacheSize,
	})
}

func TestRenderCache_RepeatRequestReusesEntry(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	doReq := func() string {
		r := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	b1 := doReq()
	if got := srv.cacheLen(); got != 1 {
		t.Errorf("after first request: cacheLen = %d, want 1", got)
	}
	b2 := doReq()
	if got := srv.cacheLen(); got != 1 {
		t.Errorf("after second request: cacheLen = %d, want 1 (hit, not miss)", got)
	}
	if b1 != b2 {
		t.Errorf("cached response differs from miss response (bytes diverged)")
	}
}

func TestRenderCache_DifferentQueriesGetSeparateEntries(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	for _, url := range []string{"/?scope=all", "/?project=p&scope=all", "/?last=30d"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: status %d", url, w.Code)
		}
	}
	if got := srv.cacheLen(); got != 3 {
		t.Errorf("cacheLen = %d, want 3", got)
	}
}

func TestRenderCache_GenerationBumpEvictsOldEntries(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "s.jsonl")
	writeJSONL(t, path, mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	r := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if got := srv.cacheLen(); got != 1 {
		t.Fatalf("after first req: cacheLen = %d, want 1", got)
	}

	// Append a turn + bump mtime + refresh cache → generation goes up.
	writeJSONL(t, path,
		mkAssistantLine("a1", "", t0),
		mkAssistantLine("a2", "a1", t0.Add(time.Second)),
	)
	future := time.Now().Add(2 * time.Second)
	if err := mustChtime(path, future); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.cache.Refresh(); err != nil {
		t.Fatal(err)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, r2)
	// Old-generation entry must be gone; new-generation entry replaces it.
	if got := srv.cacheLen(); got != 1 {
		t.Errorf("after generation bump: cacheLen = %d, want 1 (old evicted, new stored)", got)
	}
}

func TestRenderCache_QueryOrderingIsCanonical(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	for _, url := range []string{"/?scope=all&project=foo", "/?project=foo&scope=all"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: status %d", url, w.Code)
		}
	}
	// Both URLs collapse to the same canonical key.
	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (canonical query collapse)", got)
	}
}

func TestServer_GzipWhenAccepted(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	r := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
	r.Header.Set("Accept-Encoding", "gzip, deflate")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding present", got)
	}
	// Decompress and verify it's the report.
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("body is not valid gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<!DOCTYPE html>") {
		t.Errorf("decompressed body missing doctype")
	}
}

func TestServer_NoGzipWhenNotAccepted(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	r := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
	// No Accept-Encoding header.
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want empty", got)
	}
	if !strings.Contains(w.Body.String(), "<!DOCTYPE html>") {
		t.Errorf("uncompressed body missing doctype")
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := []struct {
		hdr  string
		want bool
	}{
		{"", false},
		{"gzip", true},
		{"gzip, deflate", true},
		{"deflate, gzip", true},
		{"deflate", false},
		{"gzip;q=0.5", true},
		{"identity", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.hdr != "" {
			r.Header.Set("Accept-Encoding", c.hdr)
		}
		if got := acceptsGzip(r); got != c.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", c.hdr, got, c.want)
		}
	}
}

// mustChtime is a thin wrapper that lets the test return the error
// without importing os in the test header (already pulled in by the
// cache test fixtures).
func mustChtime(path string, t time.Time) error {
	return chtimes(path, t)
}
