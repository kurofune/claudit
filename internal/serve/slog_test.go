package serve

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newSlogToBuf returns a *slog.Logger that writes the text-handler
// representation into buf. Tests assert on key=value pairs to confirm
// records are emitted with structured attributes rather than free-form
// Printf strings.
func newSlogToBuf(buf *bytes.Buffer) *slog.Logger {
	// Omit the time attribute to make assertions stable. We only care
	// about level, msg, and the user-supplied attrs.
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(h)
}

// TestServer_LogsRequestID_OnReportWriteError exercises the report
// handler's freshly-rendered write-error branch and asserts the log
// record carries a non-empty request_id attribute. This is the
// observability win the migration is for: ops can correlate a single
// failed render with all the events it produced.
func TestServer_LogsRequestID_OnReportWriteError(t *testing.T) {
	var buf bytes.Buffer
	srv := newTestServer(t, t.TempDir())
	srv.opts.Logger = newSlogToBuf(&buf)

	r := httptest.NewRequest(http.MethodGet, "/legacy", nil)
	w := &errResponseWriter{ResponseWriter: httptest.NewRecorder(), writeErr: errors.New("conn reset")}
	srv.Handler().ServeHTTP(w, r)

	out := buf.String()
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("log = %q, want level=ERROR", out)
	}
	if !strings.Contains(out, "conn reset") {
		t.Errorf("log = %q, want underlying error text", out)
	}
	if m := regexp.MustCompile(`request_id=(\S+)`).FindStringSubmatch(out); len(m) < 2 || m[1] == "" {
		t.Errorf("log = %q, want non-empty request_id= attribute", out)
	}
}

// TestServer_EchoesInboundRequestID verifies the middleware honors
// a client-supplied X-Request-ID (when reasonable) and surfaces the
// same value in (a) the response header and (b) any log record the
// request produces. Letting clients pin the ID lets a caller correlate
// across a chain of services.
func TestServer_EchoesInboundRequestID(t *testing.T) {
	var buf bytes.Buffer
	srv := newTestServer(t, t.TempDir())
	srv.opts.Logger = newSlogToBuf(&buf)

	const inbound = "abc-12345"
	// Targets /legacy because that handler emits a log line on the
	// write-error branch we exercise here — handleApp also logs, but
	// the legacy path has been the canonical observability surface
	// since the package was introduced. Either route would work; the
	// legacy route happens to be the one with the longest-stable log
	// shape.
	r := httptest.NewRequest(http.MethodGet, "/legacy", nil)
	r.Header.Set("X-Request-ID", inbound)
	w := &errResponseWriter{ResponseWriter: httptest.NewRecorder(), writeErr: errors.New("conn reset")}
	srv.Handler().ServeHTTP(w, r)

	if got := w.Header().Get("X-Request-ID"); got != inbound {
		t.Errorf("response X-Request-ID = %q, want %q", got, inbound)
	}
	if !strings.Contains(buf.String(), "request_id="+inbound) {
		t.Errorf("log = %q, want request_id=%s", buf.String(), inbound)
	}
}

// TestServer_RejectsHostileInboundRequestID covers the "client supplied
// junk" branch — control characters, very long strings, and other
// shapes that would mess up log lines or leak into downstream systems.
// Server discards the inbound value and generates a fresh one.
func TestServer_RejectsHostileInboundRequestID(t *testing.T) {
	var buf bytes.Buffer
	srv := newTestServer(t, t.TempDir())
	srv.opts.Logger = newSlogToBuf(&buf)

	hostile := "line1\nline2 with spaces and = signs"
	r := httptest.NewRequest(http.MethodGet, "/legacy", nil)
	r.Header.Set("X-Request-ID", hostile)
	w := &errResponseWriter{ResponseWriter: httptest.NewRecorder(), writeErr: errors.New("conn reset")}
	srv.Handler().ServeHTTP(w, r)

	got := w.Header().Get("X-Request-ID")
	if got == hostile || got == "" {
		t.Errorf("response X-Request-ID = %q, want a freshly generated id", got)
	}
	if strings.Contains(buf.String(), "line1") || strings.Contains(buf.String(), "line2") {
		t.Errorf("log = %q, must not contain hostile inbound id substrings", buf.String())
	}
}

// TestServer_LogsRequestID_OnReportCachedBranch is the cached-hit
// sibling of the freshly-rendered test above; warms the cache, then
// retries the same URL with a failing writer.
func TestServer_LogsRequestID_OnReportCachedBranch(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithCache(t, dir, 4)

	r1 := httptest.NewRequest(http.MethodGet, "/legacy?scope=all", nil)
	w1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w1, r1)
	if w1.Code != 200 {
		t.Fatalf("warmup status = %d", w1.Code)
	}

	var buf bytes.Buffer
	srv.opts.Logger = newSlogToBuf(&buf)
	r2 := httptest.NewRequest(http.MethodGet, "/legacy?scope=all", nil)
	w2 := &errResponseWriter{ResponseWriter: httptest.NewRecorder(), writeErr: errors.New("conn reset")}
	srv.Handler().ServeHTTP(w2, r2)

	if !regexp.MustCompile(`request_id=\S+`).MatchString(buf.String()) {
		t.Errorf("log = %q, want non-empty request_id= attribute", buf.String())
	}
}
