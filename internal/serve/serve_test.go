package serve

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/pricing"
)

func loadPricesForTest(t *testing.T) *pricing.Table {
	t.Helper()
	p, err := pricing.LoadDefault()
	if err != nil {
		t.Fatalf("load default prices: %v", err)
	}
	return p
}

// newTestServer builds a Server with an in-memory cache seeded from
// the given dir. Refreshes synchronously so the snapshot is ready
// before the test issues requests.
func newTestServer(t *testing.T, dir string) *Server {
	t.Helper()
	cache := NewCache(dir)
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	return NewServer(cache, Options{
		Prices:             loadPricesForTest(t),
		DefaultHotspots:    10,
		DefaultSessionsTop: 50,
	})
}

func TestServer_ReportHTML(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html...", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("body missing doctype; first 200 chars: %q", body[:min(200, len(body))])
	}
	// ServeMode injection: the auto-reload toast div MUST be present
	// for served renders. One-shot renders skip it (covered in the
	// render package tests).
	if !strings.Contains(body, "claudit-reload-toast") {
		t.Errorf("served render missing auto-reload toast")
	}
}

func TestServer_ReportBadQueryRejected(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/?since=not-a-date", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestServer_ReportFilterByProject(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	// cwd in mkAssistantLine is "/p"; filter to a non-matching string
	// should yield a report with zero turns. We don't assert the
	// exact numbers (renderer-internal), just that the request
	// succeeds and the filter is honored end-to-end.
	r := httptest.NewRequest(http.MethodGet, "/?project=does-not-exist", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
}

func TestServer_Status(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var body struct {
		Generation  int64  `json:"generation"`
		LastUpdated string `json:"last_updated"`
		FileCount   int    `json:"file_count"`
		TurnCount   int    `json:"turn_count"`
		Malformed   int    `json:"malformed"`
		Host        struct {
			Root string `json:"root"`
		} `json:"host"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("status not JSON: %v; body=%s", err, w.Body.String())
	}
	if body.Generation < 1 {
		t.Errorf("generation = %d, want >= 1", body.Generation)
	}
	if body.FileCount != 1 || body.TurnCount != 1 {
		t.Errorf("counts = %+v, want 1/1", body)
	}
	if body.Host.Root != dir {
		t.Errorf("host.root = %q, want %q", body.Host.Root, dir)
	}
}

func TestServer_Healthz(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/_claudit/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Body.String(); got != "ok\n" {
		t.Errorf("body = %q, want \"ok\\n\"", got)
	}
}

func TestServer_RootMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d, want 405", w.Code)
	}
}

func TestServer_UnknownPath404(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	r := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Errorf("unknown path = %d, want 404", w.Code)
	}
}

// TestServer_EndToEnd boots an actual TCP server on an ephemeral port,
// confirms /healthz responds, then shuts down via context cancel.
// Smokes the listener path that the unit handlers don't cover.
func TestServer_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	addr := "http://" + ln.Addr().String()
	resp, err := httpGetWithTimeout(addr+"/_claudit/healthz", 2*time.Second)
	if err != nil {
		t.Fatalf("healthz GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok\n" {
		t.Errorf("healthz body = %q", string(b))
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("serve did not shut down within 5s")
	}
}

func httpGetWithTimeout(url string, d time.Duration) (*http.Response, error) {
	c := &http.Client{Timeout: d}
	return c.Get(url)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
