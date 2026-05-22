package serve

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

// errResponseWriter wraps an http.ResponseWriter but fails every
// Write with the configured error. Used to exercise handler paths
// that should log when the response body can't be written (typically
// because the client disconnected mid-response).
type errResponseWriter struct {
	http.ResponseWriter
	writeErr error
}

func (e *errResponseWriter) Write(p []byte) (int, error) {
	return 0, e.writeErr
}

// TestServer_Healthz_LogsWriteError asserts that when the response
// writer errors out, the handler does not silently swallow it. The
// existing handleHealthz used `_, _ = io.WriteString(...)`; that hid
// truncated writes from operators.
func TestServer_Healthz_LogsWriteError(t *testing.T) {
	var buf bytes.Buffer
	srv := newTestServer(t, t.TempDir())
	srv.opts.Logger = newSlogToBuf(&buf)

	r := httptest.NewRequest(http.MethodGet, "/_claudit/healthz", nil)
	w := &errResponseWriter{ResponseWriter: httptest.NewRecorder(), writeErr: errors.New("conn reset")}
	srv.Handler().ServeHTTP(w, r)

	out := buf.String()
	if !strings.Contains(out, "healthz") {
		t.Errorf("log = %q, want to contain %q", out, "healthz")
	}
	if !strings.Contains(out, "conn reset") {
		t.Errorf("log = %q, want to contain %q", out, "conn reset")
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
	allow := w.Header().Get("Allow")
	if !strings.Contains(allow, "GET") || !strings.Contains(allow, "HEAD") {
		t.Errorf("Allow = %q, want it to advertise GET and HEAD", allow)
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
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()
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

// TestServer_HardenedTimeouts asserts the http.Server is built with
// non-zero ReadTimeout, WriteTimeout, and IdleTimeout (in addition to
// ReadHeaderTimeout). A stalled or slow client should not be able to
// pin a connection open indefinitely; bare net/http defaults are all
// zero, so the only way these get set is if serve() wires them.
//
// Structural rather than behavioral because the production timeouts
// are large (30s/60s/120s) and a low-value behavioral test would
// require leaking the values into Options just to override them.
func TestServer_HardenedTimeouts(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	hs := srv.buildHTTPServer()
	if hs.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want > 0", hs.ReadHeaderTimeout)
	}
	if hs.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %v, want > 0", hs.ReadTimeout)
	}
	if hs.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout = %v, want > 0", hs.WriteTimeout)
	}
	if hs.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want > 0", hs.IdleTimeout)
	}
}

// TestWithBodyLimit_EnforcesCap verifies the body-size middleware caps
// reads from r.Body so a malicious or buggy client can't pin memory by
// streaming a large body. Builds a tiny test handler that drains
// r.Body, wraps it through the same middleware the server uses, and
// asserts that an over-cap body surfaces *http.MaxBytesError and an
// at-cap body reads fine.
func TestWithBodyLimit_EnforcesCap(t *testing.T) {
	var readErr error
	var readBytes int
	h := withBodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		readErr = err
		readBytes = len(b)
	}))

	// Over-cap: one byte past the limit must produce MaxBytesError.
	over := strings.Repeat("x", maxRequestBytes+1)
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(over))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var mbErr *http.MaxBytesError
	if !errors.As(readErr, &mbErr) {
		t.Fatalf("over-cap read err = %v (%T), want *http.MaxBytesError", readErr, readErr)
	}

	// At-cap: exactly maxRequestBytes must read cleanly.
	readErr = nil
	readBytes = 0
	atCap := strings.Repeat("y", maxRequestBytes)
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(atCap))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if readErr != nil {
		t.Fatalf("at-cap read err = %v, want nil", readErr)
	}
	if readBytes != maxRequestBytes {
		t.Errorf("at-cap read %d bytes, want %d", readBytes, maxRequestBytes)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestServer_ShutdownErrorIsLogged forces srv.Shutdown to return
// context.DeadlineExceeded by registering a hanging handler, putting
// it in-flight, then setting the server's drain timeout to a value
// shorter than the request will release in. Checks that the resulting
// error is written via s.opts.Logger. Without this, a stuck listener
// (e.g., the 3s drain timeout firing in production) would silently
// swallow the error and the next restart would have no diagnostic
// trail.
func TestServer_ShutdownErrorIsLogged(t *testing.T) {
	var buf bytes.Buffer
	srv := newTestServer(t, t.TempDir())
	srv.opts.Logger = newSlogToBuf(&buf)
	srv.shutdownTimeout = 50 * time.Millisecond

	started := make(chan struct{})
	release := make(chan struct{})
	srv.mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	addr := "http://" + ln.Addr().String()
	reqDone := make(chan struct{})
	go func() {
		defer close(reqDone)
		c := &http.Client{Timeout: 5 * time.Second}
		resp, err := c.Get(addr + "/hang")
		if err == nil {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Errorf("close body: %v", cerr)
			}
		}
	}()

	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("serve did not return")
	}

	out := buf.String()
	if !strings.Contains(out, "shutdown") {
		t.Errorf("log = %q, want to contain %q", out, "shutdown")
	}
	if !strings.Contains(out, "deadline exceeded") {
		t.Errorf("log = %q, want to contain %q", out, "deadline exceeded")
	}

	close(release)
	<-reqDone
}
