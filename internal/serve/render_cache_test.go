package serve

import (
	"bytes"
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
		r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	b1 := doReq()
	if got := srv.sectionCacheLen(apiSectionCost); got != 1 {
		t.Errorf("after first request: html entries = %d, want 1", got)
	}
	b2 := doReq()
	if got := srv.sectionCacheLen(apiSectionCost); got != 1 {
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

	for _, url := range []string{"/_claudit/api/cost?scope=all", "/_claudit/api/cost?project=p&scope=all", "/_claudit/api/cost?last=30d"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: status %d", url, w.Code)
		}
	}
	if got := srv.sectionCacheLen(apiSectionCost); got != 3 {
		t.Errorf("html entries = %d, want 3", got)
	}
}

// TestRenderCache_GenerationBumpPrunesOldEntries guards the eager
// generation sweep at the HTTP level: every cache lookup uses the
// CURRENT snapshot generation, and generation is one global monotonic
// counter, so once the snapshot bumps, the old-generation entry can
// never be hit again. Retaining it is pure dead weight (a single JSON
// payload can run to hundreds of MB), so the store at the new
// generation must sweep it.
func TestRenderCache_GenerationBumpPrunesOldEntries(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "s.jsonl")
	writeJSONL(t, path, mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 8)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if got := srv.sectionCacheLen(apiSectionCost); got != 1 {
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

	r2 := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, r2)
	// Only the new-generation entry may remain — the old-generation one
	// is unreachable (lookups always pass the current generation) and
	// must have been swept by the store at the new generation.
	if got := srv.sectionCacheLen(apiSectionCost); got != 1 {
		t.Errorf("after generation bump: cost entries = %d, want 1 (old swept, new added)", got)
	}
}

func TestRenderCache_QueryOrderingIsCanonical(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	for _, url := range []string{"/_claudit/api/cost?scope=all&project=foo", "/_claudit/api/cost?project=foo&scope=all"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: status %d", url, w.Code)
		}
	}
	// Both URLs collapse to the same canonical key.
	if got := srv.sectionCacheLen(apiSectionCost); got != 1 {
		t.Errorf("html entries = %d, want 1 (canonical query collapse)", got)
	}
}

func TestServer_GzipWhenAccepted(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
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
	// Decompress and verify it's a JSON object (the /api/cost payload).
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("body is not valid gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(out)), "{") {
		t.Errorf("decompressed body is not a JSON object; got prefix: %q", string(out)[:min(60, len(out))])
	}
}

func TestServer_NoGzipWhenNotAccepted(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
	// No Accept-Encoding header.
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want empty", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(w.Body.String()), "{") {
		t.Errorf("uncompressed body is not a JSON object; got prefix: %q", w.Body.String()[:min(60, w.Body.Len())])
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
		c := newRenderLRU(in, 1<<30)
		if c.cap != 16 {
			t.Errorf("newRenderLRU(%d, 1<<30).cap = %d, want 16", in, c.cap)
		}
	}
}

func TestNewServer_MaxCachedRenderBytesDefaultAndPassthrough(t *testing.T) {
	cache := NewCache(t.TempDir())

	srv := NewServer(cache, Options{MaxCachedRenders: 4})
	if got := srv.renderCache.maxBytes; got != 256<<20 {
		t.Errorf("zero MaxCachedRenderBytes: maxBytes = %d, want %d (256 MiB default)", got, 256<<20)
	}

	srv2 := NewServer(cache, Options{MaxCachedRenders: 4, MaxCachedRenderBytes: 1 << 20})
	if got := srv2.renderCache.maxBytes; got != 1<<20 {
		t.Errorf("explicit MaxCachedRenderBytes: maxBytes = %d, want %d (passthrough)", got, 1<<20)
	}
}

func TestNewRenderLRU_NonPositiveMaxBytesDefaultsTo256MiB(t *testing.T) {
	for _, in := range []int64{0, -1} {
		c := newRenderLRU(4, in)
		if c.maxBytes != 256<<20 {
			t.Errorf("newRenderLRU(4, %d).maxBytes = %d, want %d (256 MiB)", in, c.maxBytes, 256<<20)
		}
	}
}

// Test-only section labels for the LRU unit tests below. The cache is
// section-agnostic — any string keys work — so these stand in for what
// used to be the production sectionHTML / sectionData constants. The
// LRU eviction / canonical-key / promotion behavior is the only thing
// under test here.
const (
	sectionHTML = "html"
	sectionData = "data"
)

// cacheTestServer is the smallest Server scaffolding the eviction unit
// tests need — just a renderLRU with no real cache/options. The store/
// lookup methods only touch s.renderCache, so the rest can stay nil.
// The byte budget is deliberately generous so the count-based eviction
// tests keep their semantics; byte-budget tests use
// cacheTestServerBytes to pin it.
func cacheTestServer(cap int) *Server {
	return cacheTestServerBytes(cap, 1<<30)
}

// cacheTestServerBytes is cacheTestServer with an explicit byte budget
// for the byte-eviction unit tests.
func cacheTestServerBytes(cap int, maxBytes int64) *Server {
	return &Server{renderCache: newRenderLRU(cap, maxBytes)}
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

// TestRenderCache_NewerGenerationStorePrunesAllOlder asserts the eager
// generation sweep: lookups always pass the CURRENT snapshot generation
// (see handleAPI / handleAPISessionDetail), and generation is a single
// global monotonic counter, so any entry with an older generation can
// never be hit again — it is unreachable dead weight. A store at
// generation G must therefore remove every entry with generation < G,
// across all queries AND all sections.
func TestRenderCache_NewerGenerationStorePrunesAllOlder(t *testing.T) {
	srv := cacheTestServer(8)
	const oldGen int64 = 5
	for _, k := range []string{"x", "y", "z"} {
		srv.storeCached(qWithKey(k), sectionHTML, oldGen, []byte(k), nil)
	}
	if got := srv.cacheLen(); got != 3 {
		t.Fatalf("setup: cacheLen = %d, want 3", got)
	}

	// Store under a DIFFERENT section at the newer generation — the
	// sweep must still take out the old-gen entries under sectionHTML
	// and other queries (cross-query, cross-section).
	srv.storeCached(qWithKey("new"), sectionData, oldGen+1, []byte("n"), nil)

	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (all old-gen entries swept, new one kept)", got)
	}
	for _, k := range []string{"x", "y", "z"} {
		if _, ok := srv.lookupCached(qWithKey(k), sectionHTML, oldGen, false); ok {
			t.Errorf("old-gen entry %q still present; unreachable entries must be swept eagerly", k)
		}
	}
	if _, ok := srv.lookupCached(qWithKey("new"), sectionData, oldGen+1, false); !ok {
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

func TestRenderCache_ByteBudgetEvictsLRU(t *testing.T) {
	// Budget of 100 bytes; three 40-byte plain bodies total 120. The
	// third store must evict the LRU entry ("a") to get back under
	// budget, even though the entry-count cap (8) is nowhere near hit.
	srv := cacheTestServerBytes(8, 100)
	const gen int64 = 1
	body := func(b byte) []byte { return bytes.Repeat([]byte{b}, 40) }
	for _, k := range []string{"a", "b", "c"} {
		srv.storeCached(qWithKey(k), sectionHTML, gen, body(k[0]), nil)
	}

	if _, ok := srv.lookupCached(qWithKey("a"), sectionHTML, gen, false); ok {
		t.Errorf("entry %q still present; expected byte-budget eviction of the LRU", "a")
	}
	for _, k := range []string{"b", "c"} {
		if _, ok := srv.lookupCached(qWithKey(k), sectionHTML, gen, false); !ok {
			t.Errorf("entry %q missing; 2×40 bytes fits the 100-byte budget", k)
		}
	}
	if got := srv.cacheLen(); got != 2 {
		t.Errorf("cacheLen = %d, want 2", got)
	}
}

func TestRenderCache_OversizedEntryIsStillCached(t *testing.T) {
	// An entry bigger than the whole budget must still be stored — the
	// huge Agents payload is exactly the body most worth caching — at
	// the price of evicting everything else.
	srv := cacheTestServerBytes(8, 100)
	const gen int64 = 1
	srv.storeCached(qWithKey("small"), sectionHTML, gen, []byte("tiny"), nil)
	srv.storeCached(qWithKey("huge"), sectionHTML, gen, bytes.Repeat([]byte{'h'}, 500), nil)

	got, ok := srv.lookupCached(qWithKey("huge"), sectionHTML, gen, false)
	if !ok || len(got) != 500 {
		t.Errorf("oversized entry: ok=%v len=%d, want ok=true len=500 (MRU never self-evicts)", ok, len(got))
	}
	if _, ok := srv.lookupCached(qWithKey("small"), sectionHTML, gen, false); ok {
		t.Errorf("entry %q still present; expected evicted to make room", "small")
	}
	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (only the oversized MRU remains)", got)
	}
}

func TestRenderCache_ByteAccountingCountsPlainPlusGzip(t *testing.T) {
	// "a" carries 30 plain + 30 gzip = 60 resident bytes. Adding "b"
	// (50 plain) totals 110 against a 100-byte budget, so "a" must be
	// evicted. If gzip bytes were ignored the total would read 80 and
	// "a" would survive.
	srv := cacheTestServerBytes(8, 100)
	const gen int64 = 1
	srv.storeCached(qWithKey("a"), sectionHTML, gen,
		bytes.Repeat([]byte{'a'}, 30), bytes.Repeat([]byte{'z'}, 30))
	srv.storeCached(qWithKey("b"), sectionHTML, gen, bytes.Repeat([]byte{'b'}, 50), nil)

	if _, ok := srv.lookupCached(qWithKey("a"), sectionHTML, gen, false); ok {
		t.Errorf("entry %q still present; gzip bytes must count toward the budget", "a")
	}
	if _, ok := srv.lookupCached(qWithKey("b"), sectionHTML, gen, false); !ok {
		t.Errorf("entry %q missing; MRU must survive", "b")
	}
}

func TestRenderCache_InPlaceUpdateAdjustsByteTotal(t *testing.T) {
	// Same key re-stored with a different plain body and a gzip body
	// added later (the gzip-on-second-request path): the byte total
	// must track the resident entry, not the sum of historical stores.
	srv := cacheTestServer(4)
	const gen int64 = 1
	srv.storeCached(qWithKey("k"), sectionHTML, gen, bytes.Repeat([]byte{'1'}, 10), nil)
	if got := srv.cacheBytes(); got != 10 {
		t.Fatalf("after first store: cacheBytes = %d, want 10", got)
	}

	srv.storeCached(qWithKey("k"), sectionHTML, gen,
		bytes.Repeat([]byte{'2'}, 30), bytes.Repeat([]byte{'z'}, 20))

	if got := srv.cacheBytes(); got != 50 {
		t.Errorf("after in-place update: cacheBytes = %d, want 50 (30 plain + 20 gzip)", got)
	}
	if got := srv.cacheLen(); got != 1 {
		t.Errorf("cacheLen = %d, want 1 (in-place update)", got)
	}
}

func TestRenderCache_InPlaceGrowthPastBudgetEvictsOthers(t *testing.T) {
	// Budget 100: "old" (40) + "k" (40) fit. Re-storing "k" in place
	// with 40 plain + 50 gzip grows the total to 130 — the update path
	// must enforce the budget too, evicting "old" (LRU) while keeping
	// the just-updated entry.
	srv := cacheTestServerBytes(8, 100)
	const gen int64 = 1
	srv.storeCached(qWithKey("old"), sectionHTML, gen, bytes.Repeat([]byte{'o'}, 40), nil)
	srv.storeCached(qWithKey("k"), sectionHTML, gen, bytes.Repeat([]byte{'k'}, 40), nil)

	srv.storeCached(qWithKey("k"), sectionHTML, gen,
		bytes.Repeat([]byte{'k'}, 40), bytes.Repeat([]byte{'z'}, 50))

	if _, ok := srv.lookupCached(qWithKey("old"), sectionHTML, gen, false); ok {
		t.Errorf("entry %q still present; in-place growth must trigger byte-budget eviction", "old")
	}
	if _, ok := srv.lookupCached(qWithKey("k"), sectionHTML, gen, true); !ok {
		t.Errorf("updated entry %q missing; the just-stored entry must never be evicted", "k")
	}
	if got := srv.cacheBytes(); got != 90 {
		t.Errorf("cacheBytes = %d, want 90 (40 plain + 50 gzip)", got)
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
