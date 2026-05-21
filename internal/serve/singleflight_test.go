package serve

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// TestServer_Singleflight_CollapsesConcurrentBuilds is the Phase-1
// foundation perf assertion: when N concurrent requests for the same
// (rawQuery, generation) arrive while the aggregate build is in
// flight, only ONE build runs — the rest share the in-flight result.
//
// The render cache is disabled (MaxCachedRenders=0) so a cache hit
// cannot fake the collapse. With the cache off, every concurrent
// request must reach the build path, and the recorded build count
// must still be 1.
//
// The fixture is sized at ~50 turns so the aggregate+timeline build
// takes long enough (hundreds of µs to a few ms on commodity hardware)
// that 10 goroutines released from a barrier all reach
// singleflight.Do() before the first build completes, even under CI
// scheduler jitter.
func TestServer_Singleflight_CollapsesConcurrentBuilds(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		lines = append(lines, mkAssistantLine(fmt.Sprintf("a%d", i), "", t0.Add(time.Duration(i)*time.Second)))
	}
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), lines...)

	cache := NewCache(dir)
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	srv := NewServer(cache, Options{
		Prices:             loadPricesForTest(t),
		DefaultLast:        7 * 24 * time.Hour,
		DefaultSessionsTop: 10,
		DefaultHotspots:    10,
		DefaultPeriod:      aggregate.Period("day"),
		MaxCachedRenders:   0,
	})

	const n = 10
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			r := httptest.NewRequest(http.MethodGet, "/_claudit/data.json?scope=all", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
		}()
	}
	close(barrier)
	wg.Wait()

	if got := srv.aggregateBuildCount(); got != 1 {
		t.Errorf("aggregate build count = %d, want 1 (singleflight should collapse %d concurrent cold requests)", got, n)
	}
}
