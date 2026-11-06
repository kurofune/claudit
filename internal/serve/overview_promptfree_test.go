package serve

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/render"
)

// TestAPIOverview_BuildsNoPromptIndex: the overview reads no per-prompt
// figure, so serving it builds no PromptIndex, and its payload equals the
// one prompt-indexed Aggregators give; the Cost tab still builds one.
func TestAPIOverview_BuildsNoPromptIndex(t *testing.T) {
	dir := fixtureCorpus(t)
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	writeContextSession(t, dir, "s-ctx", 10_000, t0)
	srv := newTestServerWithCache(t, dir, 0)
	const q = "?since=2026-04-28&until=2026-05-05"

	w := doAPI(t, srv, http.MethodGet, apiPathOverview+q, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("overview: status %d; body=%s", w.Code, w.Body.String())
	}
	if got := srv.promptIndexBuildCount(); got != 0 {
		t.Errorf("prompt-index builds after the overview = %d, want 0", got)
	}

	now := srv.clock()
	parsed, err := parseQuery(mustQuery(t, q), now)
	if err != nil {
		t.Fatal(err)
	}
	srv.applyDefaults(&parsed, now)
	snap := srv.cache.Snapshot()
	cur := srv.buildAggregator(snap, parsed)
	pq, ok := previousWindow(parsed, now)
	if !ok {
		t.Fatal("the window has no previous one")
	}
	want, _ := json.Marshal(render.BuildOverview(cur, srv.buildAggregator(snap, pq)))
	var got, wantV map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	_ = json.Unmarshal(want, &wantV)
	delete(got, "forecast")
	delete(wantV, "forecast")
	g, _ := json.Marshal(got)
	wv, _ := json.Marshal(wantV)
	if string(g) != string(wv) {
		t.Errorf("prompt-free overview differs:\n got %s\nwant %s", g, wv)
	}

	if w := doAPI(t, srv, http.MethodGet, "/_claudit/api/cost"+q, nil); w.Code != http.StatusOK {
		t.Fatalf("cost: status %d", w.Code)
	}
	if got := srv.promptIndexBuildCount(); got != 1 {
		t.Errorf("prompt-index builds after the cost tab = %d, want 1", got)
	}
}

func mustQuery(t *testing.T, q string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(strings.TrimPrefix(q, "?"))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
