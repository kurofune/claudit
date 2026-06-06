package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAPIAgents_OKShape(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	got := readJSONResponse(t, w)
	if _, ok := got["sessions"]; !ok {
		t.Errorf("response missing \"sessions\" key; got keys %v", got)
	}
}

// TestAPIAgents_MethodNotAllowed: like every other section endpoint,
// /agents rejects non-GET/HEAD verbs with 405 + an Allow header.
func TestAPIAgents_MethodNotAllowed(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodPost, "/_claudit/api/agents", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405; body=%s", w.Code, w.Body.String())
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow header = %q, want GET listed", allow)
	}
}

// TestAPIAgents_Head returns 200 with the ETag set but no body, so a
// browser can cheaply probe freshness.
func TestAPIAgents_Head(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodHead, "/_claudit/api/agents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("ETag"), `W/"gen-`) {
		t.Errorf("ETag = %q, want weak gen-prefixed", w.Header().Get("ETag"))
	}
	if w.Body.Len() != 0 {
		t.Errorf("HEAD response must have empty body; got %d bytes", w.Body.Len())
	}
}

// TestAPIAgents_EtagRevalidation: a fresh GET yields a 200 + ETag, and
// replaying that ETag returns 304 with no body and the ETag echoed.
func TestAPIAgents_EtagRevalidation(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag header on first response")
	}
	w2 := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents", http.Header{"If-None-Match": []string{etag}})
	if w2.Code != http.StatusNotModified {
		t.Errorf("If-None-Match replay: status = %d, want 304; body=%s", w2.Code, w2.Body.String())
	}
	if w2.Body.Len() != 0 {
		t.Errorf("304 response must have empty body; got %d bytes", w2.Body.Len())
	}
	if got := w2.Header().Get("ETag"); got != etag {
		t.Errorf("304 response should still echo ETag; got %q want %q", got, etag)
	}
}

// TestAPIAgents_Gzip: with Accept-Encoding: gzip the body is gzipped
// (Content-Encoding: gzip) and decodes to the same JSON shape.
func TestAPIAgents_Gzip(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents", http.Header{"Accept-Encoding": []string{"gzip"}})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	// readJSONResponse transparently gunzips.
	got := readJSONResponse(t, w)
	if _, ok := got["sessions"]; !ok {
		t.Errorf("gunzipped response missing \"sessions\" key; got keys %v", got)
	}
}

// TestAPIAgents_SessionsCap honors the ?sessions cap (same knob the
// Sessions tab uses), so a busy corpus doesn't ship every session. The
// fixture has two sessions; ?sessions=1 must return exactly one.
func TestAPIAgents_SessionsCap(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents?sessions=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	var p struct {
		Sessions []struct {
			SessionID string `json:"session_id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(body["sessions"], &p.Sessions); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(p.Sessions) != 1 {
		t.Errorf("?sessions=1 returned %d sessions, want 1", len(p.Sessions))
	}
}

// TestAPIAgents_BadQuery returns 400, not 500, for a malformed filter.
func TestAPIAgents_BadQuery(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents?since=garbage", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}
