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
	if got := srv.sectionCacheLen(sectionHTML); got != 1 {
		t.Errorf("after first request: html entries = %d, want 1", got)
	}
	b2 := doReq()
	if got := srv.sectionCacheLen(sectionHTML); got != 1 {
		t.Errorf("after second request: html entries = %d, want 1 (hit, not miss)", got)
	}
	if b1 != b2 {
		t.Errorf("cached response differs from miss response (bytes diverged)")
	}
}

func TestRenderCache_DifferentQueriesGetSeparateEntries(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 8)

	for _, url := range []string{"/?scope=all", "/?project=p&scope=all", "/?last=30d"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: status %d", url, w.Code)
		}
	}
	if got := srv.sectionCacheLen(sectionHTML); got != 3 {
		t.Errorf("html entries = %d, want 3", got)
	}
}

// TestRenderCache_GenerationBumpKeepsOldEntry guards the Phase-1
// behavior change: a snapshot generation bump must NOT prune older
// entries from the cache. With the section in the cache key, only the
// section whose inputs actually changed needs its entry replaced; the
// (q, section, oldGen) and (q, section, newGen) keys are distinct, so
// the new request stores a new entry and the old one lives until LRU
// capacity pushes it out. Pre-Phase-1 the prune-on-store loop did the
// opposite — collapsing the cache to one entry per snapshot — which
// churned per-section caches that hadn't changed.
func TestRenderCache_GenerationBumpKeepsOldEntry(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "s.jsonl")
	writeJSONL(t, path, mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 8)

	r := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if got := srv.sectionCacheLen(sectionHTML); got != 1 {
		t.Fatalf("after first req: html entries = %d, want 1", got)
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
	// Both the old-generation and new-generation HTML entries must be
	// present — distinct keys, no eager prune. LRU evicts them later.
	if got := srv.sectionCacheLen(sectionHTML); got != 2 {
		t.Errorf("after generation bump: html entries = %d, want 2 (old retained, new added)", got)
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
	if got := srv.sectionCacheLen(sectionHTML); got != 1 {
		t.Errorf("html entries = %d, want 1 (canonical query collapse)", got)
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

func TestNewRenderLRU_NonPositiveCapDefaultsTo16(t *testing.T) {
	for _, in := range []int{0, -1, -100} {
		c := newRenderLRU(in)
		if c.cap != 16 {
			t.Errorf("newRenderLRU(%d).cap = %d, want 16", in, c.cap)
		}
	}
}

// cacheTestServer is the smallest Server scaffolding the eviction unit
// tests need — just a renderLRU with no real cache/options. The store/
// lookup methods only touch s.renderCache, so the rest can stay nil.
func cacheTestServer(cap int) *Server {
	return &Server{renderCache: newRenderLRU(cap)}
}

func qWithKey(k string) Query {
	return Query{rawQuery: k}
}

// TestRenderCache_SectionInKey is the structural assertion behind
// Phase 1: entries with the same (query, generation) but different
// sections must coexist as independent cache slots. Pre-Phase-1, two
// separate LRUs achieved this physically; the unified LRU achieves it
// by carrying section in the key.
func TestRenderCache_SectionInKey(t *testing.T) {
	srv := cacheTestServer(4)
	const gen int64 = 7
	srv.storeCached(qWithKey("k"), sectionHTML, gen, []byte("html-bytes"), nil)
	srv.storeCached(qWithKey("k"), sectionData, gen, []byte("data-bytes"), nil)

	if got := srv.cacheLen(); got != 2 {
		t.Errorf("cacheLen = %d, want 2 (one entry per section)", got)
	}
	html, ok := srv.lookupCached(qWithKey("k"), sectionHTML, gen, false)
	if !ok || string(html) != "html-bytes" {
		t.Errorf("html lookup: ok=%v body=%q, want ok=true body=%q", ok, html, "html-bytes")
	}
	data, ok := srv.lookupCached(qWithKey("k"), sectionData, gen, false)
	if !ok || string(data) != "data-bytes" {
		t.Errorf("data lookup: ok=%v body=%q, want ok=true body=%q", ok, data, "data-bytes")
	}
}

func TestRenderCache_GzipAddedAfterPlainOnSameKey(t *testing.T) {
	// First request gets plain bytes cached (no gzip negotiated). A
	// later request for the same (query, section, gen) negotiates gzip
	// and the handler re-stores with both variants. The plain bytes
	// should be replaced and the gzip bytes added — both lookups
	// satisfied.
	srv := cacheTestServer(4)
	const gen int64 = 7

	srv.storeCached(qWithKey("k"), sectionHTML, gen, []byte("plain-v1"), nil)
	if _, ok := srv.lookupCached(qWithKey("k"), sectionHTML, gen, true); ok {
		t.Errorf("gzip lookup hit before gzip was stored")
	}

	srv.storeCached(qWithKey("k"), sectionHTML, gen, []byte("plain-v2"), []byte("gz-v2"))

	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (same key, in-place update)", got)
	}
	plain, ok := srv.lookupCached(qWithKey("k"), sectionHTML, gen, false)
	if !ok || string(plain) != "plain-v2" {
		t.Errorf("plain lookup: ok=%v body=%q, want ok=true body=%q", ok, plain, "plain-v2")
	}
	gz, ok := srv.lookupCached(qWithKey("k"), sectionHTML, gen, true)
	if !ok || string(gz) != "gz-v2" {
		t.Errorf("gzip lookup: ok=%v body=%q, want ok=true body=%q", ok, gz, "gz-v2")
	}
}

func TestRenderCache_SameGenWritesCoexist(t *testing.T) {
	// Distinct (query, section, gen) keys at the same generation must
	// coexist — they represent independent computations and one
	// shouldn't silently evict the other on store. Phase 1 dropped the
	// prune-on-store loop entirely; this is the regression guard.
	srv := cacheTestServer(8)
	srv.storeCached(qWithKey("a"), sectionHTML, 10, []byte("a"), nil)
	srv.storeCached(qWithKey("b"), sectionHTML, 10, []byte("b"), nil)

	if got := srv.cacheLen(); got != 2 {
		t.Errorf("cacheLen = %d, want 2 (same-gen writes coexist)", got)
	}
	for _, k := range []string{"a", "b"} {
		if _, ok := srv.lookupCached(qWithKey(k), sectionHTML, 10, false); !ok {
			t.Errorf("entry %q evicted; same-gen writes should coexist", k)
		}
	}
}

// TestRenderCache_OlderGenerationEntriesCoexist is the inverse of the
// pre-Phase-1 TestRenderCache_OlderGenerationPruneIsCrossQuery. With
// section in the key, a newer-gen store no longer implies the old-gen
// entries are stale — only the same-section's old-gen entry is
// definitely superseded, and even that one lives until LRU pushes it
// out. Old-gen entries from other queries/sections must NOT be swept.
func TestRenderCache_OlderGenerationEntriesCoexist(t *testing.T) {
	srv := cacheTestServer(8)
	const oldGen int64 = 5
	for _, k := range []string{"x", "y", "z"} {
		srv.storeCached(qWithKey(k), sectionHTML, oldGen, []byte(k), nil)
	}
	if got := srv.cacheLen(); got != 3 {
		t.Fatalf("setup: cacheLen = %d, want 3", got)
	}

	srv.storeCached(qWithKey("new"), sectionHTML, oldGen+1, []byte("n"), nil)

	if got := srv.cacheLen(); got != 4 {
		t.Errorf("cacheLen = %d, want 4 (3 old-gen entries retained + 1 new)", got)
	}
	for _, k := range []string{"x", "y", "z"} {
		if _, ok := srv.lookupCached(qWithKey(k), sectionHTML, oldGen, false); !ok {
			t.Errorf("old-gen entry %q evicted; expected retained until LRU pushes it out", k)
		}
	}
	if _, ok := srv.lookupCached(qWithKey("new"), sectionHTML, oldGen+1, false); !ok {
		t.Errorf("new-gen entry missing after store")
	}
}

func TestRenderCache_LookupPromotesEntry(t *testing.T) {
	srv := cacheTestServer(3)
	const gen int64 = 1
	// Insert oldest → newest: a, b, c. Without promotion, "a" is next to evict.
	for _, k := range []string{"a", "b", "c"} {
		srv.storeCached(qWithKey(k), sectionHTML, gen, []byte(k), nil)
	}
	// Touch "a" — promotes it to MRU. Now "b" is the LRU.
	if _, ok := srv.lookupCached(qWithKey("a"), sectionHTML, gen, false); !ok {
		t.Fatalf("setup: lookup(a) missed")
	}
	// Overflow: insert "d". Expect "b" evicted, "a" retained.
	srv.storeCached(qWithKey("d"), sectionHTML, gen, []byte("d"), nil)

	if _, ok := srv.lookupCached(qWithKey("a"), sectionHTML, gen, false); !ok {
		t.Errorf("entry %q evicted; expected retained (was promoted by lookup)", "a")
	}
	if _, ok := srv.lookupCached(qWithKey("b"), sectionHTML, gen, false); ok {
		t.Errorf("entry %q still present; expected evicted (now LRU after a's promotion)", "b")
	}
}

func TestRenderCache_SameKeyStoreUpdatesInPlace(t *testing.T) {
	srv := cacheTestServer(4)
	const gen int64 = 1
	srv.storeCached(qWithKey("k"), sectionHTML, gen, []byte("v1"), nil)
	srv.storeCached(qWithKey("k"), sectionHTML, gen, []byte("v2"), nil)

	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (in-place update)", got)
	}
	body, ok := srv.lookupCached(qWithKey("k"), sectionHTML, gen, false)
	if !ok {
		t.Fatalf("entry missing after update")
	}
	if string(body) != "v2" {
		t.Errorf("body = %q, want %q (newest write wins)", body, "v2")
	}
}

func TestRenderCache_StoringBeyondCapEvictsOldest(t *testing.T) {
	srv := cacheTestServer(3)
	const gen int64 = 1
	for _, k := range []string{"a", "b", "c"} {
		srv.storeCached(qWithKey(k), sectionHTML, gen, []byte(k), nil)
	}
	// Insert a 4th — cap=3, so "a" (oldest) should be evicted.
	srv.storeCached(qWithKey("d"), sectionHTML, gen, []byte("d"), nil)

	if got := srv.cacheLen(); got != 3 {
		t.Errorf("cacheLen = %d, want 3 (cap)", got)
	}
	if _, ok := srv.lookupCached(qWithKey("a"), sectionHTML, gen, false); ok {
		t.Errorf("entry %q still present; expected evicted", "a")
	}
	for _, k := range []string{"b", "c", "d"} {
		if _, ok := srv.lookupCached(qWithKey(k), sectionHTML, gen, false); !ok {
			t.Errorf("entry %q missing; should be retained", k)
		}
	}
}
