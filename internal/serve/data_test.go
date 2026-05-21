package serve

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServer_DataEndpoint_ReturnsJSON asserts the new
// /_claudit/data.json endpoint exists, returns 200, and advertises
// application/json. This is the Phase-4 endpoint that the served-mode
// HTML fetches asynchronously instead of embedding the payload inline.
func TestServer_DataEndpoint_ReturnsJSON(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
}

// TestServer_DataEndpoint_PopulatesPromptKeys is a regression guard:
// when the corpus contains a user prompt with an assistant response,
// the data endpoint must include the prompt's normalized key in
// prompt_keys. Before this test, handleData was passing an empty
// HTMLOptions to BuildPayload, so SessionTimelines was nil and
// prompt_keys was always empty — silently disabling every "view
// session →" cross-link in the served HTML.
func TestServer_DataEndpoint_PopulatesPromptKeys(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	// One user prompt + one assistant turn chained off it. The chain
	// is what BuildSessionTimelines needs to attribute the turn to the
	// prompt (and thus surface the prompt's Key in prompt_keys).
	writeJSONL(t, filepath.Join(dir, "s.jsonl"),
		mkUserLine("u1", "hello world", t0),
		mkAssistantLine("a1", "u1", t0.Add(time.Second)),
	)
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var payload struct {
		PromptKeys []string `json:"prompt_keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(payload.PromptKeys) == 0 {
		t.Fatalf("prompt_keys is empty; want at least one key from the user prompt")
	}
}

// TestServer_DataEndpoint_PayloadHasExpectedKeys asserts the response
// body is a JSON object with the same top-level keys that BuildPayload
// produces — the page can't run without them.
func TestServer_DataEndpoint_PayloadHasExpectedKeys(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &obj); err != nil {
		t.Fatalf("body not JSON: %v; first 200: %s", err, w.Body.String()[:min(200, w.Body.Len())])
	}
	for _, k := range []string{"totals", "by_model", "period", "forecast"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("payload missing top-level key %q", k)
		}
	}
}

// TestServer_DataEndpoint_RespectsQueryFilters asserts the same query
// shaping that handleReport uses applies to the data endpoint. A
// non-matching project filter must produce a payload that reflects
// zero turns — proving the query plumbing is wired all the way through.
func TestServer_DataEndpoint_RespectsQueryFilters(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	// Unfiltered: totals should reflect the one turn.
	r1 := httptest.NewRequest(http.MethodGet, "/_claudit/data.json", nil)
	w1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("unfiltered status = %d", w1.Code)
	}
	var unf struct {
		Totals struct {
			Turns int `json:"turns"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &unf); err != nil {
		t.Fatalf("unfiltered body not JSON: %v", err)
	}
	if unf.Totals.Turns != 1 {
		t.Errorf("unfiltered totals.turns = %d, want 1", unf.Totals.Turns)
	}

	// Filtered to a non-matching project: zero turns.
	r2 := httptest.NewRequest(http.MethodGet, "/_claudit/data.json?project=does-not-exist", nil)
	w2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK {
		t.Fatalf("filtered status = %d", w2.Code)
	}
	var fil struct {
		Totals struct {
			Turns int `json:"turns"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &fil); err != nil {
		t.Fatalf("filtered body not JSON: %v", err)
	}
	if fil.Totals.Turns != 0 {
		t.Errorf("filtered totals.turns = %d, want 0 (project filter not honored)", fil.Totals.Turns)
	}
}

// TestServer_DataEndpoint_BadQueryRejected mirrors the report
// endpoint: malformed query keys should surface as 400, not as a
// successful response with silently-dropped filters.
func TestServer_DataEndpoint_BadQueryRejected(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json?since=not-a-date", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// TestServer_DataEndpoint_Gzip asserts the endpoint compresses when
// the client opts in via Accept-Encoding: gzip. The payload can be
// over a megabyte — gzip is the difference between snappy and slow.
func TestServer_DataEndpoint_Gzip(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if enc := w.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("content-encoding = %q, want gzip", enc)
	}
	// Decode and assert the payload is still valid JSON.
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() { _ = zr.Close() }()
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !json.Valid(plain) {
		t.Errorf("decompressed body not valid JSON; first 200: %s", string(plain[:min(200, len(plain))]))
	}
}

// TestServer_DataEndpoint_MethodNotAllowed asserts the endpoint is
// GET/HEAD only. Anything else should produce 405 with Allow advertising
// the supported methods.
func TestServer_DataEndpoint_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	r := httptest.NewRequest(http.MethodPost, "/_claudit/data.json", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /_claudit/data.json = %d, want 405", w.Code)
	}
	allow := w.Header().Get("Allow")
	if !strings.Contains(allow, "GET") || !strings.Contains(allow, "HEAD") {
		t.Errorf("Allow = %q, want it to advertise GET and HEAD", allow)
	}
}

// TestServer_DataEndpoint_HEAD asserts HEAD requests get 200 headers
// with no body, mirroring the report endpoint's HEAD support. Browsers
// and probes use this to check freshness without paying for the JSON.
func TestServer_DataEndpoint_HEAD(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodHead, "/_claudit/data.json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if w.Body.Len() != 0 {
		t.Errorf("body len = %d, want 0 for HEAD response", w.Body.Len())
	}
}

// TestServer_ReportHTML_DefersData asserts that the served-mode HTML
// at "/" does NOT inline the JSON island and instead carries the
// fetch preamble targeting /_claudit/data.json. This is the wiring
// test for Phase 4 — serve's renderHTML must set DeferData=true.
func TestServer_ReportHTML_DefersData(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, `<script id="claudit-data" type="application/json">`) {
		t.Errorf("served-mode HTML should NOT inline the data script tag")
	}
	if !strings.Contains(body, `"\/_claudit\/data.json"`) {
		t.Errorf("served-mode HTML should reference /_claudit/data.json in the fetch preamble")
	}
}

// TestServer_DataEndpoint_RepeatRequestReusesCache asserts that the
// JSON endpoint deduplicates work across requests with the same
// canonical query and same generation — the served-mode page hits
// /_claudit/data.json once per pageload and the auto-reload polls keep
// it warm.
func TestServer_DataEndpoint_RepeatRequestReusesCache(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	doReq := func() string {
		r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json?scope=all", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		return w.Body.String()
	}

	b1 := doReq()
	if got := srv.dataCacheLen(); got != 1 {
		t.Errorf("after first request: dataCacheLen = %d, want 1", got)
	}
	b2 := doReq()
	if got := srv.dataCacheLen(); got != 1 {
		t.Errorf("after second request: dataCacheLen = %d, want 1 (hit, not miss)", got)
	}
	if b1 != b2 {
		t.Errorf("cached JSON response differs from miss response (bytes diverged)")
	}
}

// TestServer_HTMLReport_PrewarmsJSONCache is a performance regression
// guard. Before this test, handleReport and handleData each
// independently built the aggregator + timelines, so a cold Cmd-R
// (server-restart + manual refresh) doubled the server-side work and
// produced a ~7s perceived pause on real corpora. handleReport already
// has the agg + timelines in hand — passing them through BuildPayload
// once and pre-populating the data cache means the page's imminent
// fetch of /_claudit/data.json is a cache hit (microseconds, no
// recompute).
func TestServer_HTMLReport_PrewarmsJSONCache(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	if got := srv.dataCacheLen(); got != 0 {
		t.Fatalf("baseline: dataCacheLen = %d, want 0 (no requests yet)", got)
	}

	r := httptest.NewRequest(http.MethodGet, "/?scope=all", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("HTML status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	if got := srv.dataCacheLen(); got != 1 {
		t.Errorf("after GET /: dataCacheLen = %d, want 1 (HTML render must pre-warm JSON cache)", got)
	}
}

// TestServer_DataEndpoint_NoStoreAndVary asserts the response carries
// Cache-Control: no-store (the payload reflects a live snapshot that
// can flip on any poll) and Vary: Accept-Encoding (so the response is
// not incorrectly cached across clients that don't ask for gzip).
func TestServer_DataEndpoint_NoStoreAndVary(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if v := w.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Errorf("Vary = %q, want Accept-Encoding present", v)
	}
}
