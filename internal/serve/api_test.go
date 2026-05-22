package serve

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// mkAssistantLineWithCWD is mkAssistantLine but with parameterized
// session, cwd, model, and token counts so a single fixture corpus
// can exercise multiple sessions, projects, and models.
func mkAssistantLineWithCWD(sessionID, uuid, parentUUID, cwd, model string, in, out int, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":%q,"cwd":%q,"message":{"model":%q,"role":"assistant","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":%d,"output_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}`,
		uuid, parentUUID, ts.Format(time.RFC3339), sessionID, cwd, model, in, out,
	)
}

// mkUserLineWithSession is mkUserLine but with parameterized session
// + cwd so the prompt-walk test covers a multi-session corpus.
func mkUserLineWithSession(sessionID, uuid, cwd, text string, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"user","uuid":%q,"timestamp":%q,"sessionId":%q,"cwd":%q,"message":{"role":"user","content":%q}}`,
		uuid, ts.Format(time.RFC3339), sessionID, cwd, text,
	)
}

// fixtureCorpus seeds a TempDir with two sessions across two
// projects and two models. Returns the dir path so the test can
// build a Cache rooted there. The corpus is intentionally tiny but
// covers every dimension the API exposes:
//   - 2 sessions (s-alpha in /p/alpha, s-beta in /p/beta)
//   - 2 models (opus + sonnet) so by_model has > 1 row
//   - User prompts so by_prompt + prompt_keys are non-empty
//   - Distinct timestamps so trends mode buckets non-trivially
//
// Filenames are <sessionID>.jsonl so a future per-session-mtime
// ETag test can stat them deterministically.
func fixtureCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

	writeJSONL(t, filepath.Join(dir, "s-alpha.jsonl"),
		mkUserLineWithSession("s-alpha", "u1", "/p/alpha", "fix the login bug", t0),
		mkAssistantLineWithCWD("s-alpha", "a1", "u1", "/p/alpha", "claude-opus-4-7", 50_000, 5_000, t0.Add(1*time.Second)),
		mkUserLineWithSession("s-alpha", "u2", "/p/alpha", "now write a test", t0.Add(time.Minute)),
		mkAssistantLineWithCWD("s-alpha", "a2", "u2", "/p/alpha", "claude-opus-4-7", 60_000, 6_000, t0.Add(time.Minute+1*time.Second)),
	)
	writeJSONL(t, filepath.Join(dir, "s-beta.jsonl"),
		mkUserLineWithSession("s-beta", "u3", "/p/beta", "refactor the cache", t0.Add(time.Hour)),
		mkAssistantLineWithCWD("s-beta", "a3", "u3", "/p/beta", "claude-sonnet-4-6", 30_000, 3_000, t0.Add(time.Hour+1*time.Second)),
	)
	return dir
}

// fixtureServer wraps newTestServer with the corpus above. Kept
// separate so individual tests can mix in extra files if they need
// to (e.g. a subagent test).
func fixtureServer(t *testing.T) *Server {
	t.Helper()
	return newTestServer(t, fixtureCorpus(t))
}

// readJSONResponse decodes the response body as JSON (handling gzip
// transparently) and returns the resulting map. Helper centralizes
// the decode dance so per-endpoint tests stay focused on shape.
func readJSONResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var r io.Reader = w.Body
	if w.Header().Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(w.Body)
		if err != nil {
			t.Fatalf("gzip.NewReader: %v", err)
		}
		defer func() { _ = zr.Close() }()
		r = zr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v; body: %s", err, string(b))
	}
	return m
}

// assertTopLevelKeys checks the decoded object's keys match exactly
// the expected set. Missing keys + extras both fail — catches the
// "added a field" and "removed a field" regressions in one check.
func assertTopLevelKeys(t *testing.T, got map[string]json.RawMessage, want []string) {
	t.Helper()
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	wantCopy := append([]string(nil), want...)
	sort.Strings(wantCopy)
	if strings.Join(gotKeys, ",") != strings.Join(wantCopy, ",") {
		t.Errorf("top-level keys mismatch\n got:  %v\n want: %v", gotKeys, wantCopy)
	}
}

// doAPI fires one request and returns the recorder.
func doAPI(t *testing.T, srv *Server, method, target string, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for k, vs := range headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w
}

// TestAPISnapshot_ShapeAndHeaders locks the snapshot endpoint's
// wire contract: cache-control no-store (it reflects a live
// counter the SPA polls / SSE pushes), no ETag (no-store means no
// revalidation flow), and the exact top-level key set.
func TestAPISnapshot_ShapeAndHeaders(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/snapshot", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := w.Header().Get("ETag"); got != "" {
		t.Errorf("snapshot must not emit ETag (no-store); got %q", got)
	}
	body := readJSONResponse(t, w)
	assertTopLevelKeys(t, body, []string{
		"generation",
		"last_updated",
		"file_count",
		"turn_count",
		"malformed",
		"host",
	})
}

// TestAPISections_ShapeAndKeys is the table-driven contract test
// for every static section endpoint. One row per endpoint, each
// expressing the exact set of top-level keys the SPA depends on.
func TestAPISections_ShapeAndKeys(t *testing.T) {
	srv := fixtureServer(t)
	cases := []struct {
		name     string
		path     string
		wantKeys []string
	}{
		{
			name:     "overview",
			path:     "/_claudit/api/overview",
			wantKeys: []string{"totals", "hotspots", "trend_totals", "forecast", "unknown_models"},
		},
		{
			name:     "cost",
			path:     "/_claudit/api/cost",
			wantKeys: []string{"by_model", "by_project", "by_skill", "by_prompt"},
		},
		{
			name:     "cache",
			path:     "/_claudit/api/cache",
			wantKeys: []string{"overall_hit_ratio", "cache_by_project", "cache_by_session", "cache_by_subagent", "cache_by_invocation"},
		},
		{
			name:     "tools",
			path:     "/_claudit/api/tools",
			wantKeys: []string{"by_tool", "by_tool_detail"},
		},
		{
			name:     "subagents",
			path:     "/_claudit/api/subagents",
			wantKeys: []string{"by_subagent", "agent_invocations", "main", "sidechain"},
		},
		{
			name:     "anomalies",
			path:     "/_claudit/api/anomalies",
			wantKeys: []string{"anomalies"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doAPI(t, srv, http.MethodGet, tc.path, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
				t.Errorf("Content-Type = %q, want application/json...", got)
			}
			if got := w.Header().Get("Cache-Control"); got != "no-cache, must-revalidate" {
				t.Errorf("Cache-Control = %q, want no-cache, must-revalidate", got)
			}
			if !strings.HasPrefix(w.Header().Get("ETag"), `W/"gen-`) {
				t.Errorf("ETag = %q, want weak gen-prefixed", w.Header().Get("ETag"))
			}
			body := readJSONResponse(t, w)
			assertTopLevelKeys(t, body, tc.wantKeys)
		})
	}
}

// TestAPISections_EtagRevalidation asserts every section honors
// If-None-Match: a fresh request gets a 200 + ETag, and replaying
// that ETag returns 304 with no body. Keep both behaviors locked —
// drift here would break browser caching silently (304 → broken
// page).
func TestAPISections_EtagRevalidation(t *testing.T) {
	srv := fixtureServer(t)
	endpoints := []string{
		"/_claudit/api/overview",
		"/_claudit/api/cost",
		"/_claudit/api/cache",
		"/_claudit/api/tools",
		"/_claudit/api/subagents",
		"/_claudit/api/anomalies",
		"/_claudit/api/trends?dim=model",
		"/_claudit/api/sessions",
	}
	for _, path := range endpoints {
		t.Run(strings.ReplaceAll(strings.TrimPrefix(path, "/_claudit/api/"), "?", "_"), func(t *testing.T) {
			w := doAPI(t, srv, http.MethodGet, path, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("first request: status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
			etag := w.Header().Get("ETag")
			if etag == "" {
				t.Fatalf("missing ETag header on first response")
			}
			w2 := doAPI(t, srv, http.MethodGet, path, http.Header{"If-None-Match": []string{etag}})
			if w2.Code != http.StatusNotModified {
				t.Errorf("If-None-Match replay: status = %d, want 304; body=%s", w2.Code, w2.Body.String())
			}
			if w2.Body.Len() != 0 {
				t.Errorf("304 response must have empty body; got %d bytes", w2.Body.Len())
			}
			if got := w2.Header().Get("ETag"); got != etag {
				t.Errorf("304 response should still echo ETag; got %q want %q", got, etag)
			}
		})
	}
}

// TestAPISections_Gzip asserts the API endpoints honor Accept-
// Encoding: gzip. The plain and gzipped paths must produce the
// same decoded JSON — the response body is just transport-layer
// compressed, not a different payload.
func TestAPISections_Gzip(t *testing.T) {
	srv := fixtureServer(t)
	plain := doAPI(t, srv, http.MethodGet, "/_claudit/api/cost", nil)
	gz := doAPI(t, srv, http.MethodGet, "/_claudit/api/cost", http.Header{"Accept-Encoding": []string{"gzip"}})

	if gz.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip request: Content-Encoding = %q, want gzip", gz.Header().Get("Content-Encoding"))
	}
	if plain.Header().Get("Content-Encoding") == "gzip" {
		t.Fatalf("plain request shouldn't carry Content-Encoding: gzip")
	}

	// Both decoded payloads should equal each other byte-for-byte.
	plainBody := plain.Body.Bytes()
	zr, err := gzip.NewReader(bytes.NewReader(gz.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	gzBody, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzipped body: %v", err)
	}
	if !bytes.Equal(plainBody, gzBody) {
		t.Errorf("gzipped vs plain payload mismatch\n plain:   %s\n gzipped: %s", string(plainBody), string(gzBody))
	}
}

// TestAPISections_FilterCombinations runs the table of common
// filter strings through one endpoint as a smoke test. Each row
// must respond 200 and return a JSON object — content correctness
// is enforced by the render-package payload tests; this check is
// integration-level (does the filter pipeline plumb through?).
func TestAPISections_FilterCombinations(t *testing.T) {
	srv := fixtureServer(t)
	cases := []struct {
		name  string
		query string
	}{
		{"empty", ""},
		{"project_alpha", "?project=alpha"},
		{"project_beta", "?project=beta"},
		{"since", "?since=2026-05-01"},
		{"until", "?until=2026-05-02"},
		{"last_7d", "?last=7d"},
		{"redact", "?redact=true"},
		{"scope_all", "?scope=all"},
		{"by_week", "?by=week"},
		{"sessions_cap", "?sessions=10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doAPI(t, srv, http.MethodGet, "/_claudit/api/cost"+tc.query, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
			// JSON decodes (basic structural sanity).
			body := readJSONResponse(t, w)
			if _, ok := body["by_model"]; !ok {
				t.Errorf("missing by_model key in response")
			}
		})
	}
}

// TestAPISections_BadQuery returns 400, not 500, for syntactically
// invalid filter values.
func TestAPISections_BadQuery(t *testing.T) {
	srv := fixtureServer(t)
	cases := []string{
		"/_claudit/api/cost?since=not-a-date",
		"/_claudit/api/cost?by=bogus",
		"/_claudit/api/cost?hotspots=-1",
		"/_claudit/api/cost?sessions=abc",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			w := doAPI(t, srv, http.MethodGet, target, nil)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestAPISections_MethodNotAllowed asserts non-GET/HEAD verbs are
// rejected with 405 and an Allow header that names the supported
// verbs.
func TestAPISections_MethodNotAllowed(t *testing.T) {
	srv := fixtureServer(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			w := doAPI(t, srv, method, "/_claudit/api/overview", nil)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", w.Code)
			}
			if allow := w.Header().Get("Allow"); !strings.Contains(allow, "GET") {
				t.Errorf("Allow header = %q, want GET listed", allow)
			}
		})
	}
}

// TestAPITrends_RequiresDim returns 400 (not 500, not 200 with an
// empty series) when ?dim is missing. The handler validates early
// so the aggregation pipeline doesn't run for nothing.
func TestAPITrends_RequiresDim(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/trends", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// TestAPITrends_UnknownDim returns 400 — same input-validation
// behavior, but bubbled up from BuildTrends.
func TestAPITrends_UnknownDim(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/trends?dim=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

// TestAPITrends_DimsCoverPlanTable hits every dim the API surface
// table lists. Each one must return 200 with the correct shape.
func TestAPITrends_DimsCoverPlanTable(t *testing.T) {
	srv := fixtureServer(t)
	for _, dim := range []string{"model", "project", "session", "tool", "subagent"} {
		t.Run(dim, func(t *testing.T) {
			w := doAPI(t, srv, http.MethodGet, "/_claudit/api/trends?dim="+dim, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
			body := readJSONResponse(t, w)
			assertTopLevelKeys(t, body, []string{"period", "dim", "series"})
		})
	}
}

// TestAPISessions_ListShape returns one row per session, totals
// only, no per-prompt timeline blob.
func TestAPISessions_ListShape(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/sessions", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	assertTopLevelKeys(t, body, []string{"sessions"})

	// One session-summary per fixture session, no per-prompt data.
	var p struct {
		Sessions []struct {
			SessionID string  `json:"session_id"`
			CWD       string  `json:"cwd"`
			CostUSD   float64 `json:"cost_usd"`
			Turns     int     `json:"turns"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(body["sessions"], &p.Sessions); err != nil {
		t.Fatalf("unmarshal sessions array: %v", err)
	}
	if len(p.Sessions) < 2 {
		t.Errorf("want at least 2 sessions in fixture; got %d", len(p.Sessions))
	}
	// Defensive: serialized body must not carry per-prompt timeline
	// data (drives the SPA's lazy-fetch design).
	if bytes.Contains(body["sessions"], []byte(`"prompts":`)) {
		t.Errorf("/api/sessions response carries per-prompt timeline data; should be lazy-fetched")
	}
}

// TestAPISessionTimeline_GetExisting fetches one session's timeline
// and asserts the response shape carries prompts + turns.
func TestAPISessionTimeline_GetExisting(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/sessions/s-alpha/timeline", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache, must-revalidate" {
		t.Errorf("Cache-Control = %q, want no-cache, must-revalidate", got)
	}
	// ETag should be mtime-derived (sess-{id}-mt-{nanos}) because
	// fixture files exist on disk and are stattable.
	etag := w.Header().Get("ETag")
	if !strings.HasPrefix(etag, `W/"sess-s-alpha-mt-`) {
		t.Errorf("ETag = %q, want W/\"sess-s-alpha-mt-...\"", etag)
	}

	body := readJSONResponse(t, w)
	if _, ok := body["session_id"]; !ok {
		t.Errorf("timeline response missing session_id key; keys=%v", keysOf(body))
	}
	if _, ok := body["prompts"]; !ok {
		t.Errorf("timeline response missing prompts key; keys=%v", keysOf(body))
	}
}

// TestAPISessionTimeline_UnknownSession returns 404, not a 200 with
// an empty body — a SPA should see a clean error path.
func TestAPISessionTimeline_UnknownSession(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/sessions/does-not-exist/timeline", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// TestAPISessionTimeline_EtagRevalidation: same If-None-Match flow
// as the section endpoints, but the ETag here is per-session-mtime
// so adding turns to a *different* session doesn't invalidate this
// one. Replaying the ETag must still return 304.
func TestAPISessionTimeline_EtagRevalidation(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/sessions/s-alpha/timeline", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("first: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag on first response")
	}
	w2 := doAPI(t, srv, http.MethodGet, "/_claudit/api/sessions/s-alpha/timeline", http.Header{"If-None-Match": []string{etag}})
	if w2.Code != http.StatusNotModified {
		t.Errorf("replay: status = %d, want 304", w2.Code)
	}
}

// TestAPISessionsTree_UnknownSubpath: anything under /sessions/
// other than /timeline is a 404, not a 500.
func TestAPISessionsTree_UnknownSubpath(t *testing.T) {
	srv := fixtureServer(t)
	cases := []string{
		"/_claudit/api/sessions/s-alpha",            // no subpath
		"/_claudit/api/sessions/s-alpha/bogus",      // unknown subpath
		"/_claudit/api/sessions/s-alpha/timeline/x", // extra segment
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			w := doAPI(t, srv, http.MethodGet, target, nil)
			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
			}
		})
	}
}

// TestAPI_DataJSONStillWorks: the old /_claudit/data.json endpoint
// keeps working after Phase 3. Locks the "additive only" promise
// the plan makes — the SPA migration can land progressively
// because the static-report fetch path doesn't change.
func TestAPI_DataJSONStillWorks(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, dataPath, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	// The legacy data.json carries the union shape; spot-check one key.
	if _, ok := body["totals"]; !ok {
		t.Errorf("data.json missing totals key")
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
