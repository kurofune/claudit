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

func TestRenderCache_GzipAddedAfterPlainOnSameKey(t *testing.T) {
	// First request gets plain bytes cached (no gzip negotiated). A
	// later request for the same (query, gen) negotiates gzip and the
	// handler re-stores with both variants. The plain bytes should be
	// replaced and the gzip bytes added — both lookups satisfied.
	srv := cacheTestServer(4)
	const gen int64 = 7

	srv.storeCached(qWithKey("k"), gen, []byte("plain-v1"), nil)
	if _, ok := srv.lookupCached(qWithKey("k"), gen, true); ok {
		t.Errorf("gzip lookup hit before gzip was stored")
	}

	srv.storeCached(qWithKey("k"), gen, []byte("plain-v2"), []byte("gz-v2"))

	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (same key, in-place update)", got)
	}
	plain, ok := srv.lookupCached(qWithKey("k"), gen, false)
	if !ok || string(plain) != "plain-v2" {
		t.Errorf("plain lookup: ok=%v body=%q, want ok=true body=%q", ok, plain, "plain-v2")
	}
	gz, ok := srv.lookupCached(qWithKey("k"), gen, true)
	if !ok || string(gz) != "gz-v2" {
		t.Errorf("gzip lookup: ok=%v body=%q, want ok=true body=%q", ok, gz, "gz-v2")
	}
}

func TestRenderCache_PruneUsesStrictLessThan(t *testing.T) {
	// The prune loop's `<` operator is load-bearing: flipping to `<=`
	// would make every same-generation store silently purge its
	// predecessors, collapsing the cache to one entry per snapshot.
	// Production exercises many writes at the same generation between
	// snapshot refreshes, so this is a realistic regression to guard.
	srv := cacheTestServer(8)
	srv.storeCached(qWithKey("a"), 10, []byte("a"), nil)
	srv.storeCached(qWithKey("b"), 10, []byte("b"), nil)

	if got := srv.cacheLen(); got != 2 {
		t.Errorf("cacheLen = %d, want 2 (same-gen writes coexist)", got)
	}
	for _, k := range []string{"a", "b"} {
		if _, ok := srv.lookupCached(qWithKey(k), 10, false); !ok {
			t.Errorf("entry %q evicted; same-gen writes should coexist", k)
		}
	}
}

func TestRenderCache_OlderGenerationPruneIsCrossQuery(t *testing.T) {
	srv := cacheTestServer(8)
	const oldGen int64 = 5
	// Three entries at the old generation, distinct queries.
	for _, k := range []string{"x", "y", "z"} {
		srv.storeCached(qWithKey(k), oldGen, []byte(k), nil)
	}
	if got := srv.cacheLen(); got != 3 {
		t.Fatalf("setup: cacheLen = %d, want 3", got)
	}
	// One store at a newer generation — should sweep ALL older entries,
	// not just the same-query one.
	srv.storeCached(qWithKey("new"), oldGen+1, []byte("n"), nil)

	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (3 old-gen entries pruned + 1 new)", got)
	}
	for _, k := range []string{"x", "y", "z"} {
		if _, ok := srv.lookupCached(qWithKey(k), oldGen, false); ok {
			t.Errorf("old-gen entry %q still present; expected pruned", k)
		}
	}
	if _, ok := srv.lookupCached(qWithKey("new"), oldGen+1, false); !ok {
		t.Errorf("new-gen entry missing after store")
	}
}

func TestRenderCache_LookupPromotesEntry(t *testing.T) {
	srv := cacheTestServer(3)
	const gen int64 = 1
	// Insert oldest → newest: a, b, c. Without promotion, "a" is next to evict.
	for _, k := range []string{"a", "b", "c"} {
		srv.storeCached(qWithKey(k), gen, []byte(k), nil)
	}
	// Touch "a" — promotes it to MRU. Now "b" is the LRU.
	if _, ok := srv.lookupCached(qWithKey("a"), gen, false); !ok {
		t.Fatalf("setup: lookup(a) missed")
	}
	// Overflow: insert "d". Expect "b" evicted, "a" retained.
	srv.storeCached(qWithKey("d"), gen, []byte("d"), nil)

	if _, ok := srv.lookupCached(qWithKey("a"), gen, false); !ok {
		t.Errorf("entry %q evicted; expected retained (was promoted by lookup)", "a")
	}
	if _, ok := srv.lookupCached(qWithKey("b"), gen, false); ok {
		t.Errorf("entry %q still present; expected evicted (now LRU after a's promotion)", "b")
	}
}

func TestRenderCache_SameKeyStoreUpdatesInPlace(t *testing.T) {
	srv := cacheTestServer(4)
	const gen int64 = 1
	srv.storeCached(qWithKey("k"), gen, []byte("v1"), nil)
	srv.storeCached(qWithKey("k"), gen, []byte("v2"), nil)

	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (in-place update)", got)
	}
	body, ok := srv.lookupCached(qWithKey("k"), gen, false)
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
		srv.storeCached(qWithKey(k), gen, []byte(k), nil)
	}
	// Insert a 4th — cap=3, so "a" (oldest) should be evicted.
	srv.storeCached(qWithKey("d"), gen, []byte("d"), nil)

	if got := srv.cacheLen(); got != 3 {
		t.Errorf("cacheLen = %d, want 3 (cap)", got)
	}
	if _, ok := srv.lookupCached(qWithKey("a"), gen, false); ok {
		t.Errorf("entry %q still present; expected evicted", "a")
	}
	for _, k := range []string{"b", "c", "d"} {
		if _, ok := srv.lookupCached(qWithKey(k), gen, false); !ok {
			t.Errorf("entry %q missing; should be retained", k)
		}
	}
}
