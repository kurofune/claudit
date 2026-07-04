package serve

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServer_CanceledRequest_NotLoggedAsError reproduces the startup
// noise seen in `claudit serve`: a browser fires per-section API
// requests, then disconnects (reload/navigation) before the aggregation
// finishes. The canceled request context surfaces as context.Canceled
// out of sharedAggregateData. That is a client hang-up, not a server
// failure, so it must NOT be logged at ERROR nor answered with a 500.
func TestServer_CanceledRequest_NotLoggedAsError(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)

	var buf bytes.Buffer
	srv.opts.Logger = newSlogToBuf(&buf)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // client is already gone before the handler runs
	r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("log = %q, want no ERROR record for a canceled request", buf.String())
	}
	if strings.Contains(buf.String(), "aggregate failed") {
		t.Errorf("log = %q, want no \"aggregate failed\" record for a canceled request", buf.String())
	}
	if w.Code == http.StatusInternalServerError {
		t.Errorf("status = %d, want no 500 written to a disconnected client", w.Code)
	}
}
