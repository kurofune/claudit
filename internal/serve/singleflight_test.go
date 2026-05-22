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
// foundation perf assertion: N concurrent requests for the same
// (rawQuery, generation) collapse to a small handful of aggregate
// builds — the singleflight pools in-flight work so the per-section
// extraction cost dominates, not the multi-second aggregate pass.
//
// The render cache is disabled (MaxCachedRenders=0) so a cache hit
// cannot fake the collapse. With the cache off, every request must
// reach the build path, but most should attach to the first in-flight
// callback.
//
// Assertion is "< n" rather than "== 1" because Go's scheduler does
// not guarantee all N goroutines reach singleflight.Do() before the
// first build completes — especially with a small fixture where the
// build is sub-ms. The collapse property is what matters: the
// recorded count must be meaningfully less than n.
func TestServer_Singleflight_CollapsesConcurrentBuilds(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
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
			r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body=%s", w.Code, w.Body.String())
			}
		}()
	}
	close(barrier)
	wg.Wait()

	if got := srv.aggregateBuildCount(); got >= int64(n) {
		t.Errorf("aggregate build count = %d, want < %d (singleflight should collapse concurrent cold requests)", got, n)
	}
}
