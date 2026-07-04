package serve

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// TestPromptMemo_DifferentQueriesSameGenerationBuildOnce guards the
// per-generation memoization of the query-independent corpus structures
// (PromptIndex + ReplaySet): switching filters at the same snapshot
// generation must not rebuild them, even though each distinct query
// runs its own aggregate build.
func TestPromptMemo_DifferentQueriesSameGenerationBuildOnce(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServerWithDefaults(t, dir)

	for _, url := range []string{"/_claudit/api/cost?scope=all", "/_claudit/api/cost?last=30d"} {
		r := httptest.NewRequest(http.MethodGet, url, nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: status %d", url, w.Code)
		}
	}

	if got := srv.aggregateBuildCount(); got != 2 {
		t.Fatalf("aggregate builds = %d, want 2 (distinct queries must each build)", got)
	}
	if got := srv.promptIndexBuildCount(); got != 1 {
		t.Errorf("prompt-index builds = %d, want 1 (same generation must reuse memo)", got)
	}
}

// TestPromptMemo_GenerationBumpRebuilds guards the invalidation side:
// once the corpus changes (new snapshot generation), the memoized
// PromptIndex/ReplaySet are stale and the next request must rebuild.
func TestPromptMemo_GenerationBumpRebuilds(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, "s.jsonl")
	writeJSONL(t, path, mkAssistantLine("a1", "", t0))
	srv := newTestServerWithDefaults(t, dir)

	doReq := func() {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
	}

	doReq()
	if got := srv.promptIndexBuildCount(); got != 1 {
		t.Fatalf("after first request: prompt-index builds = %d, want 1", got)
	}

	// Append a turn + bump mtime + refresh cache → generation goes up.
	writeJSONL(t, path,
		mkAssistantLine("a1", "", t0),
		mkAssistantLine("a2", "a1", t0.Add(time.Second)),
	)
	if err := chtimes(path, time.Now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.cache.Refresh(); err != nil {
		t.Fatal(err)
	}

	doReq()
	if got := srv.promptIndexBuildCount(); got != 2 {
		t.Errorf("after generation bump: prompt-index builds = %d, want 2 (stale memo must be replaced)", got)
	}
}

// TestPromptMemo_HitProducesIdenticalPayload guards behavioral
// equivalence: an aggregate build that consumes the memoized
// PromptIndex/ReplaySet must produce the same response bytes as the
// build that populated the memo. The render cache is disabled
// (MaxCachedRenders 0) so the second request genuinely re-runs
// buildAggregator instead of replaying a cached body. The fixture
// includes a user prompt so the payload's by_prompt attribution
// actually depends on the PromptIndex — a corrupted memo hit would
// collapse it to "(no prompt)" and diverge the bodies.
func TestPromptMemo_HitProducesIdenticalPayload(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"),
		mkUserLineWithSession("s1", "u1", "/p", "fix the login bug", t0),
		mkAssistantLineWithCWD("s1", "a1", "u1", "/p", "claude-opus-4-7", 50_000, 5_000, t0.Add(time.Second)),
	)
	srv := newTestServerWithCache(t, dir, 0) // render cache off

	doReq := func() string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/_claudit/api/cost?scope=all", nil)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		return w.Body.String()
	}

	b1 := doReq()
	b2 := doReq()
	if got := srv.aggregateBuildCount(); got != 2 {
		t.Fatalf("aggregate builds = %d, want 2 (render cache off must rebuild)", got)
	}
	if got := srv.promptIndexBuildCount(); got != 1 {
		t.Fatalf("prompt-index builds = %d, want 1 (second build must hit the memo)", got)
	}
	if b1 != b2 {
		t.Errorf("memo-hit response differs from memo-miss response:\nmiss: %s\nhit:  %s", b1, b2)
	}
}
