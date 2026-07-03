package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPprofIndex_Returns200WithGoroutineListing(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())

	r := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /debug/pprof/ status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, "goroutine") {
		t.Errorf("GET /debug/pprof/ body does not contain %q; got %q", "goroutine", body)
	}
}

func TestPprofHeap_Returns200(t *testing.T) {
	srv := newTestServerWithDefaults(t, t.TempDir())

	r := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /debug/pprof/heap status = %d, want %d", w.Code, http.StatusOK)
	}
}
