package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurofune/claudit/v2/internal/aggregate"
)

// mkCacheWriteLine is an assistant line with no tools whose usage writes
// cacheCreate tokens to the 5m cache and reads cacheRead.
func mkCacheWriteLine(sessionID, uuid string, cacheCreate, cacheRead int, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"timestamp":%q,"sessionId":%q,"cwd":"/p","message":{"id":%q,"model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":0}}}}`,
		uuid, ts.Format(time.RFC3339), sessionID, "msg-"+uuid, cacheCreate, cacheRead, cacheCreate)
}

// writeContextSession writes one session where a `go test` result is
// carried into cache by the next turn (writeTokens) and re-read once.
func writeContextSession(t *testing.T, dir, session string, writeTokens int, ts time.Time) {
	t.Helper()
	tool := "toolu_" + session
	writeJSONL(t, filepath.Join(dir, session+".jsonl"),
		mkToolUseLine(session, session+"-a", "/p", tool, "go test ./...", ts),
		mkToolResultLine(session, session+"-r", "/p", tool, "ok  pkg 0.1s", ts.Add(time.Second)),
		mkCacheWriteLine(session, session+"-b", writeTokens, 0, ts.Add(2*time.Second)),
		mkCacheWriteLine(session, session+"-c", 0, writeTokens, ts.Add(3*time.Second)),
	)
}

// TestOverviewHotspotsCarryContextCostAndTrend: the served overview ranks
// tool patterns by context cost (so the snapshot's tool results reach the
// aggregator) and compares each to the previous equal window.
func TestOverviewHotspotsCarryContextCostAndTrend(t *testing.T) {
	dir := t.TempDir()
	loc := time.Local
	writeContextSession(t, dir, "cur", 200_000, time.Date(2026, 5, 10, 12, 0, 0, 0, loc))
	writeContextSession(t, dir, "prev", 100_000, time.Date(2026, 5, 3, 12, 0, 0, 0, loc))
	srv := newTestServer(t, dir)

	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/overview?since=2026-05-08&until=2026-05-14", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body struct {
		Hotspots []struct {
			Title       string   `json:"title"`
			CostUSD     float64  `json:"cost_usd"`
			PrevCostUSD *float64 `json:"prev_cost_usd"`
			TrendPct    *float64 `json:"trend_pct"`
			Examples    []struct {
				SessionID  string `json:"session_id"`
				SourceFile string `json:"source_file"`
			} `json:"examples"`
		} `json:"hotspots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Hotspots) == 0 || body.Hotspots[0].Title != "Bash: `go test`" {
		t.Fatalf("hotspots = %+v, want Bash: `go test` first", body.Hotspots)
	}
	h := body.Hotspots[0]
	if h.CostUSD <= 0 || h.PrevCostUSD == nil || *h.PrevCostUSD <= 0 || h.TrendPct == nil {
		t.Fatalf("hotspot = %+v, want context cost and a trend against the previous window", h)
	}
	if *h.TrendPct < 99 || *h.TrendPct > 101 {
		t.Errorf("trend = %v%%, want ≈ +100%% (200k vs 100k tokens)", *h.TrendPct)
	}
	if len(h.Examples) != 1 || h.Examples[0].SessionID != "cur" || h.Examples[0].SourceFile != filepath.Join(dir, "cur.jsonl") {
		t.Errorf("examples = %+v, want the cur session", h.Examples)
	}
}

func TestPreviousWindow(t *testing.T) {
	since := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)

	pq, ok := previousWindow(Query{Filter: aggregate.Filter{Since: since, Until: until}, rawQuery: "q"}, now)
	if !ok || !pq.Filter.Since.Equal(since.AddDate(0, 0, -7)) || !pq.Filter.Until.Equal(since) {
		t.Errorf("bounded: %v..%v ok=%v, want %v..%v", pq.Filter.Since, pq.Filter.Until, ok, since.AddDate(0, 0, -7), since)
	}
	if pq.rawQuery == "q" {
		t.Error("previous window must not share the current window's cache key")
	}
	pq, ok = previousWindow(Query{Filter: aggregate.Filter{Since: since}}, now)
	if !ok || !pq.Filter.Since.Equal(since.AddDate(0, 0, -4)) || !pq.Filter.Until.Equal(since) {
		t.Errorf("open until: %v..%v, want 4 days before since", pq.Filter.Since, pq.Filter.Until)
	}
	if _, ok := previousWindow(Query{}, now); ok {
		t.Error("unbounded window has no previous window")
	}
}
