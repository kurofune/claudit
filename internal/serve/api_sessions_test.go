package serve

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/agentflow"
	"github.com/kurofune/claudit/internal/aggregate"
)

type indexRowWire struct {
	SessionID       string    `json:"session_id"`
	FamilyKey       string    `json:"family_key"`
	Remainder       string    `json:"remainder"`
	Title           string    `json:"title"`
	Outcome         string    `json:"outcome"`
	Project         string    `json:"project"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	CostUSD         float64   `json:"cost_usd"`
	DurationMs      int64     `json:"duration_ms"`
	Turns           int       `json:"turns"`
	Commands        int       `json:"commands"`
	Failed          int       `json:"failed"`
	FirstFailedTurn string    `json:"first_failed_turn"`
	Compactions     int       `json:"compactions"`
	LastActivity    time.Time `json:"last_activity"`
	Active          bool      `json:"active"`
	StartedAt       time.Time `json:"started_at"`
}

func getSessions(t *testing.T, srv *Server, query string) []indexRowWire {
	t.Helper()
	w := doAPI(t, srv, http.MethodGet, apiPathSessions+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET sessions%s: status %d; body=%s", query, w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	assertTopLevelKeys(t, body, []string{"cursor", "sessions"})
	var rows []indexRowWire
	if err := json.Unmarshal(body["sessions"], &rows); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	return rows
}

func sessionIDs[T any](rows []T, id func(T) string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, id(r))
	}
	return out
}

// TestAPISessions_IndexRows asserts every field of both rows of the
// two-session fixture, newest last_activity first.
func TestAPISessions_IndexRows(t *testing.T) {
	srv := fixtureServer(t)
	rows := getSessions(t, srv, "")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2: %+v", len(rows), rows)
	}
	var raw struct {
		Sessions []map[string]json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(doAPI(t, srv, http.MethodGet, apiPathSessions, nil).Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw sessions: %v", err)
	}
	for _, r := range raw.Sessions {
		if string(r["compactions"]) != "0" || string(r["first_failed_turn"]) != `""` {
			t.Errorf("row %s: compactions = %s, first_failed_turn = %s, want 0 and \"\"", r["session_id"], r["compactions"], r["first_failed_turn"])
		}
	}
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	prices := loadPricesForTest(t)
	opus := func(in, out int) float64 {
		c, _ := prices.Cost("claude-opus-4-7", in, out, 0, 0, 0)
		return c
	}
	sonnet, _ := prices.Cost("claude-sonnet-4-6", 30_000, 3_000, 0, 0, 0)
	want := []indexRowWire{
		{
			SessionID: "s-beta", FamilyKey: "refactor the cache", Remainder: "", Title: "refactor the cache",
			Outcome: "x", Project: "beta", Provider: "claude-code", Model: "claude-sonnet-4-6",
			CostUSD: sonnet, DurationMs: 0, Turns: 1, Commands: 0, Failed: 0,
			LastActivity: t0.Add(time.Hour + time.Second), Active: false, StartedAt: t0.Add(time.Hour + time.Second),
		},
		{
			SessionID: "s-alpha", FamilyKey: "fix the login bug", Remainder: "", Title: "fix the login bug",
			Outcome: "x", Project: "alpha", Provider: "claude-code", Model: "claude-opus-4-7",
			CostUSD: opus(50_000, 5_000) + opus(60_000, 6_000), DurationMs: 60_000, Turns: 2, Commands: 0, Failed: 0,
			LastActivity: t0.Add(time.Minute + time.Second), Active: false, StartedAt: t0.Add(time.Second),
		},
	}
	for i, w := range want {
		g := rows[i]
		if d := g.CostUSD - w.CostUSD; d > 1e-9 || d < -1e-9 {
			t.Errorf("%s cost_usd = %v, want %v", w.SessionID, g.CostUSD, w.CostUSD)
		}
		g.CostUSD = w.CostUSD
		if !g.LastActivity.Equal(w.LastActivity) || !g.StartedAt.Equal(w.StartedAt) {
			t.Errorf("%s times = %v / %v, want %v / %v", w.SessionID, g.LastActivity, g.StartedAt, w.LastActivity, w.StartedAt)
		}
		g.LastActivity, g.StartedAt = w.LastActivity, w.StartedAt
		if g != w {
			t.Errorf("row %d:\n got  %+v\n want %+v", i, g, w)
		}
	}
}

// TestAPISessions_FiltersNarrow: ?since/?until/?project/?provider narrow
// the index to the sessions with a turn inside them.
func TestAPISessions_FiltersNarrow(t *testing.T) {
	dir := fixtureCorpus(t)
	later := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s-gamma.jsonl"),
		mkUserLineWithSession("s-gamma", "u9", "/p/gamma", "ship it", later),
		mkAssistantLineWithCWD("s-gamma", "a9", "u9", "/p/gamma", "claude-opus-4-7", 1_000, 100, later.Add(time.Second)),
	)
	srv := newTestServer(t, dir)
	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"s-gamma", "s-beta", "s-alpha"}},
		{"?since=2026-05-02", []string{"s-gamma"}},
		{"?until=2026-05-02", []string{"s-beta", "s-alpha"}},
		{"?project=alpha", []string{"s-alpha"}},
		{"?provider=claude-code", []string{"s-gamma", "s-beta", "s-alpha"}},
		{"?provider=codex", []string{}},
		{"?since=2026-05-01&until=2026-05-02&project=beta", []string{"s-beta"}},
	}
	for _, c := range cases {
		got := sessionIDs(getSessions(t, srv, c.query), func(r indexRowWire) string { return r.SessionID })
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("sessions%s = %v, want %v", c.query, got, c.want)
		}
	}
}

func TestAPISessions_GzipEtagAndBadQuery(t *testing.T) {
	srv := fixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, apiPathSessions, http.Header{"Accept-Encoding": []string{"gzip"}})
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d encoding %q, want 200 gzip", w.Code, w.Header().Get("Content-Encoding"))
	}
	etag := w.Header().Get("ETag")
	if w2 := doAPI(t, srv, http.MethodGet, apiPathSessions, http.Header{"If-None-Match": []string{etag}}); w2.Code != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", w2.Code)
	}
	if w3 := doAPI(t, srv, http.MethodGet, apiPathSessions+"?since=garbage", nil); w3.Code != http.StatusBadRequest {
		t.Errorf("bad query status = %d, want 400", w3.Code)
	}
}

// TestAPISessions_ActiveExpiresWithoutANewGeneration: with the snapshot
// standing still, a row fetched while active reads inactive once its
// window runs out, on an ordinary GET and on a conditional GET whose ETag
// was issued while it was active; inside the window that ETag still
// revalidates.
func TestAPISessions_ActiveExpiresWithoutANewGeneration(t *testing.T) {
	srv := fixtureServer(t)
	betaLast := time.Date(2026, 5, 1, 11, 0, 1, 0, time.UTC)
	now := betaLast.Add(30 * time.Second)
	srv.now = func() time.Time { return now }
	gen := srv.cache.Snapshot().Generation

	active := func(w *httptest.ResponseRecorder) map[string]bool {
		t.Helper()
		var body struct {
			Sessions []indexRowWire `json:"sessions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
		}
		out := map[string]bool{}
		for _, r := range body.Sessions {
			out[r.SessionID] = r.Active
		}
		return out
	}

	w := doAPI(t, srv, http.MethodGet, apiPathSessions, nil)
	if got := active(w); !got["s-beta"] || got["s-alpha"] {
		t.Fatalf("inside the window: active = %v, want only s-beta", got)
	}
	activeTag := w.Header().Get("ETag")

	now = betaLast.Add(59 * time.Second)
	if w := doAPI(t, srv, http.MethodGet, apiPathSessions, http.Header{"If-None-Match": []string{activeTag}}); w.Code != http.StatusNotModified {
		t.Errorf("conditional GET inside the window: status %d, want 304", w.Code)
	}

	now = betaLast.Add(61 * time.Second)
	if g := srv.cache.Snapshot().Generation; g != gen {
		t.Fatalf("generation moved %d -> %d; the test needs it unchanged", gen, g)
	}
	w = doAPI(t, srv, http.MethodGet, apiPathSessions, nil)
	if got := active(w); got["s-beta"] {
		t.Errorf("ordinary GET past the window: active = %v, want none", got)
	}
	if tag := w.Header().Get("ETag"); tag == activeTag {
		t.Errorf("ETag %s unchanged across the expiry", tag)
	}
	w = doAPI(t, srv, http.MethodGet, apiPathSessions, http.Header{"If-None-Match": []string{activeTag}, "Accept-Encoding": []string{"gzip"}})
	if w.Code != http.StatusOK {
		t.Fatalf("conditional GET past the window with the active ETag: status %d, want 200", w.Code)
	}
	plain := doAPI(t, srv, http.MethodGet, apiPathSessions, nil)
	if got := active(plain); got["s-beta"] {
		t.Errorf("GET after the conditional one: active = %v, want none", got)
	}
	if w2 := doAPI(t, srv, http.MethodGet, apiPathSessions, http.Header{"If-None-Match": []string{plain.Header().Get("ETag")}}); w2.Code != http.StatusNotModified {
		t.Errorf("conditional GET with the post-expiry ETag: status %d, want 304", w2.Code)
	}
}

// storyCorpus holds s-story: prompt u1 on 2026-05-01 driving a1 and a2 and
// spawning one Explore sub-agent, and prompt u2 on 2026-05-03 driving a3.
func storyCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	d1 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s-story.jsonl"),
		mkUserLineWithSession("s-story", "u1", "/p/story", "fix the login bug", d1),
		mkAssistantLineWithCWD("s-story", "a1", "u1", "/p/story", "claude-opus-4-7", 1_000, 100, d1.Add(time.Second)),
		mkAssistantLineWithCWD("s-story", "a2", "a1", "/p/story", "claude-opus-4-7", 1_000, 100, d1.Add(2*time.Second)),
		mkUserLineWithSession("s-story", "u2", "/p/story", "now write a test", d3),
		mkAssistantLineWithCWD("s-story", "a3", "u2", "/p/story", "claude-opus-4-7", 1_000, 100, d3.Add(time.Second)),
	)
	subDir := filepath.Join(dir, "s-story", "subagents")
	writeJSONL(t, filepath.Join(subDir, "agent-k1.jsonl"),
		mkAssistantLineWithCWD("s-story", "k1", "", "/p/story", "claude-haiku-4-5", 500, 50, d1.Add(1500*time.Millisecond)),
	)
	if err := os.WriteFile(filepath.Join(subDir, "agent-k1.meta.json"), []byte(`{"agentType":"Explore","description":"find callers","toolUseId":"toolu_k1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func getStory(t *testing.T, srv *Server, query string) map[string]json.RawMessage {
	t.Helper()
	w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET story%s: status %d; body=%s", query, w.Code, w.Body.String())
	}
	return readJSONResponse(t, w)
}

// wholeStoryTools lists every tool call — the main agent's and its
// sub-agents' — of sessionID's whole story under the extra query
// ("&provider=codex", or "").
func wholeStoryTools(t *testing.T, srv *Server, sessionID, extra string) []storyToolWire {
	t.Helper()
	body := getStory(t, srv, "?scope=all&whole=1&session="+url.QueryEscape(sessionID)+extra)
	type stepWire struct{ Tools []storyToolWire }
	var steps []stepWire
	var children []struct{ Steps []stepWire }
	if err := json.Unmarshal(body["steps"], &steps); err != nil {
		t.Fatalf("story steps: %v", err)
	}
	if err := json.Unmarshal(body["children"], &children); err != nil {
		t.Fatalf("story children: %v", err)
	}
	var out []storyToolWire
	for _, c := range children {
		steps = append(steps, c.Steps...)
	}
	for _, st := range steps {
		out = append(out, st.Tools...)
	}
	return out
}

func keysOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// TestAPISessionsStory_Shape walks the story payload of a two-prompt
// session: prompts, page, steps carrying every AgentStep field, children
// and hidden.
func TestAPISessionsStory_Shape(t *testing.T) {
	srv := newTestServer(t, storyCorpus(t))
	body := getStory(t, srv, "?session=s-story")
	assertTopLevelKeys(t, body, []string{"session_id", "prompts", "page", "steps", "children", "hidden", "source_files"})

	var p struct {
		SessionID string `json:"session_id"`
		Prompts   []struct {
			UUID      string    `json:"uuid"`
			Text      string    `json:"text"`
			Timestamp time.Time `json:"timestamp"`
			Turns     int       `json:"turns"`
		} `json:"prompts"`
		Page     map[string]any    `json:"page"`
		Steps    []json.RawMessage `json:"steps"`
		Children []json.RawMessage `json:"children"`
		Hidden   map[string]int    `json:"hidden"`
	}
	raw, _ := json.Marshal(body)
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.SessionID != "s-story" {
		t.Errorf("session_id = %q", p.SessionID)
	}
	if len(p.Prompts) != 2 || p.Prompts[0].UUID != "u1" || p.Prompts[0].Text != "fix the login bug" || p.Prompts[0].Turns != 2 ||
		p.Prompts[1].UUID != "u2" || p.Prompts[1].Turns != 1 {
		t.Errorf("prompts = %+v", p.Prompts)
	}
	if !p.Prompts[0].Timestamp.Equal(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("prompt 0 timestamp = %v", p.Prompts[0].Timestamp)
	}
	wantPage := map[string]any{"prompt_index": 0.0, "turn_offset": 0.0, "turn_limit": 200.0, "has_more": false}
	if fmt.Sprint(p.Page) != fmt.Sprint(wantPage) {
		t.Errorf("page = %v, want %v", p.Page, wantPage)
	}
	if len(p.Steps) != 2 {
		t.Fatalf("steps = %d, want prompt 0's 2", len(p.Steps))
	}
	if got, want := keysOf(t, p.Steps[0]), "context_cost_usd,context_tokens,cost_usd,duration_ms,gen_ms,model,text,timestamp,tokens,tools,uuid"; got != want {
		t.Errorf("step keys = %s\nwant        %s", got, want)
	}
	var step struct {
		UUID string `json:"uuid"`
	}
	_ = json.Unmarshal(p.Steps[0], &step)
	if step.UUID != "a1" {
		t.Errorf("step 0 uuid = %q, want the turn uuid a1", step.UUID)
	}
	if len(p.Children) != 1 {
		t.Fatalf("children = %d, want the Explore sub-agent", len(p.Children))
	}
	if got, want := keysOf(t, p.Children[0]), "agent_type,cost_usd,description,ended_at,error_count,kind,parent_tool_use_id,started_at,steps"; got != want {
		t.Errorf("child keys = %s\nwant         %s", got, want)
	}
	var child struct {
		AgentType       string `json:"agent_type"`
		ParentToolUseID string `json:"parent_tool_use_id"`
	}
	_ = json.Unmarshal(p.Children[0], &child)
	if child.AgentType != "Explore" || child.ParentToolUseID != "toolu_k1" {
		t.Errorf("child = %+v", child)
	}
	if p.Hidden["replays"] != 0 || p.Hidden["outside_filter"] != 0 || len(p.Hidden) != 2 {
		t.Errorf("hidden = %v, want replays 0 outside_filter 0", p.Hidden)
	}

	second := getStory(t, srv, "?session=s-story&prompt=1")
	var s2 struct {
		Steps    []struct{ UUID string } `json:"steps"`
		Children []json.RawMessage       `json:"children"`
	}
	raw, _ = json.Marshal(second)
	_ = json.Unmarshal(raw, &s2)
	if len(s2.Steps) != 1 || s2.Steps[0].UUID != "a3" || len(s2.Children) != 0 {
		t.Errorf("prompt 1 page = %+v", s2)
	}
}

// TestAPISessionsStory_SinceHidesFirstPrompt: a window starting after the
// first prompt leaves only the second, and hidden.outside_filter counts the
// excluded turns (a1, a2 and the sub-agent's k1).
func TestAPISessionsStory_SinceHidesFirstPrompt(t *testing.T) {
	srv := newTestServer(t, storyCorpus(t))
	body := getStory(t, srv, "?session=s-story&since=2026-05-02")
	var p struct {
		Prompts []struct{ UUID string } `json:"prompts"`
		Hidden  struct {
			OutsideFilter int `json:"outside_filter"`
		} `json:"hidden"`
	}
	raw, _ := json.Marshal(body)
	_ = json.Unmarshal(raw, &p)
	if len(p.Prompts) != 1 || p.Prompts[0].UUID != "u2" {
		t.Errorf("prompts = %+v, want only u2", p.Prompts)
	}
	if p.Hidden.OutsideFilter != 3 {
		t.Errorf("hidden.outside_filter = %d, want 3", p.Hidden.OutsideFilter)
	}
}

// longCorpus is one single-prompt session of 450 turns.
func longCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	lines := []string{mkUserLineWithSession("s-long", "u1", "/p/long", "keep going", t0)}
	parent := "u1"
	for i := 0; i < 450; i++ {
		id := fmt.Sprintf("a%03d", i)
		lines = append(lines, mkAssistantLineWithCWD("s-long", id, parent, "/p/long", "claude-opus-4-7", 100, 10, t0.Add(time.Duration(i+1)*time.Second)))
		parent = id
	}
	writeJSONL(t, filepath.Join(dir, "s-long.jsonl"), lines...)
	return dir
}

func TestAPISessionsStory_Paging(t *testing.T) {
	srv := newTestServer(t, longCorpus(t))
	for _, c := range []struct {
		query string
		n     int
		more  bool
		first string
	}{
		{"?session=s-long&prompt=0", 200, true, "a000"},
		{"?session=s-long&prompt=0&offset=200", 200, true, "a200"},
		{"?session=s-long&prompt=0&offset=400", 50, false, "a400"},
	} {
		body := getStory(t, srv, c.query)
		var p struct {
			Steps []struct{ UUID string } `json:"steps"`
			Page  struct {
				HasMore    bool `json:"has_more"`
				TurnOffset int  `json:"turn_offset"`
			} `json:"page"`
		}
		raw, _ := json.Marshal(body)
		_ = json.Unmarshal(raw, &p)
		if len(p.Steps) != c.n || p.Page.HasMore != c.more || p.Steps[0].UUID != c.first {
			t.Errorf("%s: %d steps, has_more %v, first %s; want %d %v %s", c.query, len(p.Steps), p.Page.HasMore, p.Steps[0].UUID, c.n, c.more, c.first)
		}
	}
	for _, q := range []string{"?session=s-long&prompt=1", "?session=unknown", "?session=s-long&offset=450"} {
		if w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+q, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", q, w.Code)
		}
	}
	for _, q := range []string{"", "?prompt=0", "?session=s-long&prompt=-1", "?session=s-long&offset=x", "?session=s-long&since=garbage"} {
		if w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+q, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, w.Code)
		}
	}
}

// TestAPISessionsStory_PagesShareOneStory: a session's later pages reuse
// the story the first page built, and no page builds a whole-corpus
// Aggregator.
func TestAPISessionsStory_PagesShareOneStory(t *testing.T) {
	srv := newTestServer(t, longCorpus(t))
	getStory(t, srv, "?session=s-long")
	getStory(t, srv, "?session=s-long&offset=200")
	getStory(t, srv, "?session=s-long&offset=400&prompt=0")
	if got := srv.storyBuildCount(); got != 1 {
		t.Errorf("story builds after three pages = %d, want 1", got)
	}
	if got := srv.aggregateBuildCount(); got != 0 {
		t.Errorf("aggregate builds after three pages = %d, want 0", got)
	}
}

// TestAPISessionsStory_TurnLocatesItsPage: ?turn=<uuid> answers the page
// that carries the turn; an unknown turn answers 404, and ?turn with
// ?prompt or ?offset is a bad request.
func TestAPISessionsStory_TurnLocatesItsPage(t *testing.T) {
	srv := newTestServer(t, longCorpus(t))
	for _, c := range []struct {
		turn   string
		offset int
		first  string
	}{{"a000", 0, "a000"}, {"a250", 200, "a200"}, {"a449", 400, "a400"}} {
		body := getStory(t, srv, "?session=s-long&turn="+c.turn)
		var p struct {
			Steps []struct{ UUID string } `json:"steps"`
			Page  struct {
				PromptIndex int `json:"prompt_index"`
				TurnOffset  int `json:"turn_offset"`
			} `json:"page"`
		}
		raw, _ := json.Marshal(body)
		_ = json.Unmarshal(raw, &p)
		first := ""
		if len(p.Steps) > 0 {
			first = p.Steps[0].UUID
		}
		if p.Page.PromptIndex != 0 || p.Page.TurnOffset != c.offset || first != c.first {
			t.Errorf("turn %s: page %+v first %q; want offset %d first %s", c.turn, p.Page, first, c.offset, c.first)
		}
	}
	if w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+"?session=s-long&turn=zzz", nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown turn: status %d, want 404", w.Code)
	}
	for _, q := range []string{"?session=s-long&turn=a000&prompt=0", "?session=s-long&turn=a000&offset=0"} {
		if w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+q, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, w.Code)
		}
	}
}

// TestAPISessionsStory_ContextCostAndSourceFiles: over the story corpus
// plus a session whose `go test` result the next turn writes to cache,
// that carrying turn's context_cost_usd is the result's write cost (the
// session's whole WriteCostUSD), the calling and re-reading turns carry 0,
// and source_files names every log the session's turns came from.
func TestAPISessionsStory_ContextCostAndSourceFiles(t *testing.T) {
	dir := storyCorpus(t)
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	writeContextSession(t, dir, "s-ctx", 10_000, t0)
	srv := newTestServer(t, dir)

	var p struct {
		Steps []struct {
			UUID           string  `json:"uuid"`
			ContextCostUSD float64 `json:"context_cost_usd"`
		} `json:"steps"`
		SourceFiles []string `json:"source_files"`
	}
	raw, _ := json.Marshal(getStory(t, srv, "?scope=all&session=s-ctx"))
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	by := map[string]float64{}
	for _, s := range p.Steps {
		by[s.UUID] = s.ContextCostUSD
	}
	agg := aggregate.New(srv.opts.Prices)
	snap := srv.cache.Snapshot()
	for _, turn := range snap.Turns {
		agg.Add(turn)
	}
	agg.AddToolResults(snap.ToolResults)
	var want float64
	for _, row := range agg.ContextCostBySession() {
		if row.SessionID == "s-ctx" {
			want = row.WriteCostUSD
		}
	}
	if want <= 0 {
		t.Fatalf("fixture session write cost = %v, want > 0", want)
	}
	if math.Abs(by["s-ctx-b"]-want) > 1e-12 || by["s-ctx-a"] != 0 || by["s-ctx-c"] != 0 {
		t.Errorf("context_cost_usd by turn = %v, want s-ctx-b %v and 0 elsewhere", by, want)
	}
	if got := strings.Join(p.SourceFiles, ","); got != filepath.Join(dir, "s-ctx.jsonl") {
		t.Errorf("source_files = %v, want the one session log", p.SourceFiles)
	}

	raw, _ = json.Marshal(getStory(t, srv, "?scope=all&session=s-story"))
	p.SourceFiles = nil
	_ = json.Unmarshal(raw, &p)
	if got, want := strings.Join(p.SourceFiles, ","), filepath.Join(dir, "s-story.jsonl")+","+filepath.Join(dir, "s-story", "subagents", "agent-k1.jsonl"); got != want {
		t.Errorf("source_files = %s\nwant           %s", got, want)
	}
}

// TestAPISessionsStory_Whole: ?whole=1 answers every turn of every prompt
// and every sub-agent in one payload, with the session's title, project,
// provider, model and time span from its index row.
func TestAPISessionsStory_Whole(t *testing.T) {
	srv := newTestServer(t, storyCorpus(t))
	body := getStory(t, srv, "?scope=all&session=s-story&whole=1")
	assertTopLevelKeys(t, body, []string{"session_id", "prompts", "page", "steps", "children", "hidden", "source_files",
		"title", "project", "provider", "model", "started_at", "ended_at"})
	var p struct {
		Steps    []struct{ UUID string } `json:"steps"`
		Children []json.RawMessage       `json:"children"`
		Title    string                  `json:"title"`
		Project  string                  `json:"project"`
		Provider string                  `json:"provider"`
		Model    string                  `json:"model"`
		Started  time.Time               `json:"started_at"`
		Ended    time.Time               `json:"ended_at"`
	}
	raw, _ := json.Marshal(body)
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if got := len(p.Steps); got != 3 || p.Steps[2].UUID != "a3" || len(p.Children) != 1 {
		t.Errorf("whole steps = %+v children = %d, want a1 a2 a3 and the sub-agent", p.Steps, len(p.Children))
	}
	if p.Title != "fix the login bug" || p.Provider != "claude-code" || p.Model != "claude-opus-4-7" || p.Project == "" {
		t.Errorf("whole header = %q %q %q %q", p.Title, p.Project, p.Provider, p.Model)
	}
	if !p.Started.Equal(time.Date(2026, 5, 1, 12, 0, 1, 0, time.UTC)) || !p.Ended.Equal(time.Date(2026, 5, 3, 12, 0, 1, 0, time.UTC)) {
		t.Errorf("whole span = %v .. %v", p.Started, p.Ended)
	}
	if w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+"?session=s-story&whole=1&prompt=1", nil); w.Code != http.StatusBadRequest {
		t.Errorf("whole with prompt: status %d, want 400", w.Code)
	}
}

// sessionsBody is the /sessions body, full or delta.
type sessionsBody struct {
	Cursor   string                      `json:"cursor"`
	Sessions []agentflow.SessionIndexRow `json:"sessions"`
	Changed  []agentflow.SessionIndexRow `json:"changed"`
	Removed  []string                    `json:"removed"`
}

// getSessionsBody GETs /sessions with query and asserts its top-level keys.
func getSessionsBody(t *testing.T, srv *Server, query string, keys ...string) sessionsBody {
	t.Helper()
	w := doAPI(t, srv, http.MethodGet, apiPathSessions+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET sessions%s: status %d; body=%s", query, w.Code, w.Body.String())
	}
	raw := w.Body.Bytes()
	m := readJSONResponse(t, w)
	assertTopLevelKeys(t, m, keys)
	var b sessionsBody
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, raw)
	}
	if b.Cursor == "" {
		t.Fatalf("GET sessions%s: no cursor", query)
	}
	return b
}

var fullKeys, deltaKeys = []string{"cursor", "sessions"}, []string{"cursor", "changed", "removed"}

// TestAPISessions_SinceCursorAnswersOnlyTheGrownRow: after one session's
// log grows, ?since=<cursor> answers that session's row alone, its turns
// one higher, and removes nothing.
func TestAPISessions_SinceCursorAnswersOnlyTheGrownRow(t *testing.T) {
	dir := t.TempDir()
	_, b := twoSessionCorpus(t, dir)
	srv := newTestServer(t, dir)

	full := getSessionsBody(t, srv, "?scope=all", fullKeys...)
	before := map[string]agentflow.SessionIndexRow{}
	for _, r := range full.Sessions {
		before[r.SessionID] = r
	}
	appendTurn(t, srv, b, "s-b", "b3", "b2", time.Date(2026, 5, 1, 12, 10, 0, 0, time.UTC))

	d := getSessionsBody(t, srv, "?scope=all&since="+url.QueryEscape(full.Cursor), deltaKeys...)
	if len(d.Changed) != 1 || d.Changed[0].SessionID != "s-b" {
		t.Fatalf("changed = %v, want only s-b", sessionIDs(d.Changed, func(r agentflow.SessionIndexRow) string { return r.SessionID }))
	}
	if got, want := d.Changed[0].Turns, before["s-b"].Turns+1; got != want {
		t.Errorf("s-b turns = %d, want %d", got, want)
	}
	if d.Removed == nil || len(d.Removed) != 0 {
		t.Errorf("removed = %#v, want []", d.Removed)
	}
	if d.Cursor == full.Cursor {
		t.Errorf("delta cursor %q did not move", d.Cursor)
	}

	again := getSessionsBody(t, srv, "?scope=all&since="+url.QueryEscape(d.Cursor), deltaKeys...)
	if len(again.Changed) != 0 || len(again.Removed) != 0 {
		t.Errorf("delta from the current cursor = %+v, want empty", again)
	}
}

// TestAPISessions_UnknownOrExpiredCursorAnswersTheFullIndex: no ?since, a
// cursor naming a generation never built, one from another filter, and
// one older than the history all answer the full index.
func TestAPISessions_UnknownOrExpiredCursorAnswersTheFullIndex(t *testing.T) {
	dir := t.TempDir()
	_, b := twoSessionCorpus(t, dir)
	srv := newTestServer(t, dir)

	first := getSessionsBody(t, srv, "?scope=all", fullKeys...)
	if len(first.Sessions) != 2 {
		t.Fatalf("full index rows = %d, want 2", len(first.Sessions))
	}
	c, ok := parseSessionCursor(first.Cursor)
	if !ok {
		t.Fatalf("cursor %q does not parse", first.Cursor)
	}
	unknown := c
	unknown.gen += 1000
	other := c
	other.filter = "00000000"
	if other.filter == c.filter {
		other.filter = "11111111"
	}
	for _, cur := range []string{unknown.String(), other.String(), "c1-0-abcdef12"} {
		got := getSessionsBody(t, srv, "?scope=all&since="+url.QueryEscape(cur), fullKeys...)
		if len(got.Sessions) != 2 {
			t.Errorf("since=%s: %d rows, want the full 2", cur, len(got.Sessions))
		}
	}

	parent := "b2"
	ts := time.Date(2026, 5, 1, 12, 10, 0, 0, time.UTC)
	for i := 0; i <= indexHistoryMax; i++ {
		id := fmt.Sprintf("bx%03d", i)
		appendTurn(t, srv, b, "s-b", id, parent, ts.Add(time.Duration(i)*time.Second))
		parent = id
		getSessionsBody(t, srv, "?scope=all", fullKeys...)
	}
	expired := getSessionsBody(t, srv, "?scope=all&since="+url.QueryEscape(first.Cursor), fullKeys...)
	if len(expired.Sessions) != 2 {
		t.Errorf("expired cursor: %d rows, want the full 2", len(expired.Sessions))
	}
	if w := doAPI(t, srv, http.MethodGet, apiPathOverview+"?since="+url.QueryEscape(first.Cursor), nil); w.Code != http.StatusBadRequest {
		t.Errorf("cursor on /overview: status %d, want 400", w.Code)
	}
}

// TestAPISessions_CursorFromAnEarlierServerAnswersTheFullIndex: a cursor
// one server issued at generation 1, sent to a restarted server whose own
// history starts again at generation 1 after a session was removed while
// it was down, answers the full current index, not an empty delta.
func TestAPISessions_CursorFromAnEarlierServerAnswersTheFullIndex(t *testing.T) {
	dir := t.TempDir()
	a, _ := twoSessionCorpus(t, dir)
	first := newTestServer(t, dir)
	old := getSessionsBody(t, first, "?scope=all", fullKeys...)
	if len(old.Sessions) != 2 {
		t.Fatalf("first server rows = %d, want 2", len(old.Sessions))
	}

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	restarted := newTestServer(t, dir)
	if g, f := restarted.cache.Snapshot().Generation, first.cache.Snapshot().Generation; g != f {
		t.Fatalf("restarted generation %d, want it to collide with the first server's %d", g, f)
	}
	got := getSessionsBody(t, restarted, "?scope=all&since="+url.QueryEscape(old.Cursor), fullKeys...)
	if ids := sessionIDs(got.Sessions, func(r agentflow.SessionIndexRow) string { return r.SessionID }); !reflect.DeepEqual(ids, []string{"s-b"}) {
		t.Errorf("restarted server rows = %v, want the current index [s-b]", ids)
	}
	if got.Cursor == old.Cursor {
		t.Errorf("restarted server reissued the old cursor %q", got.Cursor)
	}
	again := getSessionsBody(t, restarted, "?scope=all&since="+url.QueryEscape(got.Cursor), deltaKeys...)
	if len(again.Changed) != 0 || len(again.Removed) != 0 {
		t.Errorf("delta from the restarted server's own cursor = %+v, want empty", again)
	}
}

// TestAPISessions_DeltasFoldToTheFullIndex: three deltas (a session grown,
// a session file added, a session file removed) applied in turn to the
// first full index equal a fresh full fetch at the final generation.
func TestAPISessions_DeltasFoldToTheFullIndex(t *testing.T) {
	dir := t.TempDir()
	a, b := twoSessionCorpus(t, dir)
	srv := newTestServer(t, dir)
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	full := getSessionsBody(t, srv, "?scope=all", fullKeys...)
	rows := map[string]agentflow.SessionIndexRow{}
	for _, r := range full.Sessions {
		rows[r.SessionID] = r
	}
	cursor := full.Cursor
	refresh := func() {
		t.Helper()
		gen := srv.cache.Snapshot().Generation
		if _, err := srv.cache.Refresh(); err != nil {
			t.Fatal(err)
		}
		if srv.cache.Snapshot().Generation == gen {
			t.Fatal("the change did not move the generation")
		}
	}
	steps := []struct {
		name    string
		change  func()
		changed []string
		removed []string
	}{
		{"s-b grew", func() { appendTurn(t, srv, b, "s-b", "b3", "b2", t0.Add(10*time.Minute)) }, []string{"s-b"}, []string{}},
		{"s-c added", func() {
			writeJSONL(t, filepath.Join(dir, "s-c.jsonl"),
				mkUserLineWithSession("s-c", "uc", "/p/c", "write docs", t0.Add(time.Hour)),
				mkAssistantLineWithCWD("s-c", "c1", "uc", "/p/c", "claude-opus-4-7", 100, 10, t0.Add(time.Hour+time.Second)))
			refresh()
		}, []string{"s-c"}, []string{}},
		{"s-a removed", func() {
			if err := os.Remove(a); err != nil {
				t.Fatal(err)
			}
			refresh()
		}, []string{}, []string{"s-a"}},
	}
	for _, st := range steps {
		st.change()
		d := getSessionsBody(t, srv, "?scope=all&since="+url.QueryEscape(cursor), deltaKeys...)
		if got := sessionIDs(d.Changed, func(r agentflow.SessionIndexRow) string { return r.SessionID }); !reflect.DeepEqual(got, st.changed) {
			t.Errorf("%s: changed %v, want %v", st.name, got, st.changed)
		}
		if !reflect.DeepEqual(d.Removed, st.removed) {
			t.Errorf("%s: removed %v, want %v", st.name, d.Removed, st.removed)
		}
		for _, r := range d.Changed {
			rows[r.SessionID] = r
		}
		for _, id := range d.Removed {
			delete(rows, id)
		}
		cursor = d.Cursor
	}

	merged := make([]agentflow.SessionIndexRow, 0, len(rows))
	for _, r := range rows {
		merged = append(merged, r)
	}
	fresh := getSessionsBody(t, srv, "?scope=all", fullKeys...).Sessions
	byID := func(s []agentflow.SessionIndexRow) {
		sort.Slice(s, func(i, j int) bool { return s[i].SessionID < s[j].SessionID })
	}
	byID(merged)
	byID(fresh)
	if !reflect.DeepEqual(merged, fresh) {
		t.Errorf("folded deltas differ from a fresh fetch:\n got  %+v\n want %+v", merged, fresh)
	}
}

// TestAPISessions_DeltaCarriesRowsWhoseActiveFlagExpired: with the
// snapshot standing still, a row active when the cursor was issued and
// inactive now comes back in changed with active false.
func TestAPISessions_DeltaCarriesRowsWhoseActiveFlagExpired(t *testing.T) {
	srv := fixtureServer(t)
	betaLast := time.Date(2026, 5, 1, 11, 0, 1, 0, time.UTC)
	now := betaLast.Add(30 * time.Second)
	srv.now = func() time.Time { return now }

	full := getSessionsBody(t, srv, "", fullKeys...)
	now = betaLast.Add(45 * time.Second)
	if d := getSessionsBody(t, srv, "?since="+url.QueryEscape(full.Cursor), deltaKeys...); len(d.Changed) != 0 {
		t.Errorf("inside the window: changed %d rows, want none", len(d.Changed))
	}
	now = betaLast.Add(61 * time.Second)
	d := getSessionsBody(t, srv, "?since="+url.QueryEscape(full.Cursor), deltaKeys...)
	if len(d.Changed) != 1 || d.Changed[0].SessionID != "s-beta" || d.Changed[0].Active {
		t.Errorf("past the window: changed %+v, want s-beta inactive", d.Changed)
	}
}

// TestAPISessions_CursorBesideADateWindow: the page's ?since date and the
// list's ?since cursor share the URL; the date still narrows the window,
// a second cursor is a 400, and a cursor issued under another window
// answers the full index.
func TestAPISessions_CursorBesideADateWindow(t *testing.T) {
	dir := t.TempDir()
	_, b := twoSessionCorpus(t, dir)
	srv := newTestServer(t, dir)

	full := getSessionsBody(t, srv, "?since=2026-05-01", fullKeys...)
	appendTurn(t, srv, b, "s-b", "b3", "b2", time.Date(2026, 5, 1, 12, 10, 0, 0, time.UTC))
	d := getSessionsBody(t, srv, "?since=2026-05-01&since="+url.QueryEscape(full.Cursor), deltaKeys...)
	if len(d.Changed) != 1 || d.Changed[0].SessionID != "s-b" {
		t.Errorf("changed = %+v, want only s-b", d.Changed)
	}
	if other := getSessionsBody(t, srv, "?since=2026-04-01&since="+url.QueryEscape(full.Cursor), fullKeys...); len(other.Sessions) != 2 {
		t.Errorf("cursor from another window: %d rows, want the full 2", len(other.Sessions))
	}
	if w := doAPI(t, srv, http.MethodGet, apiPathSessions+"?since="+url.QueryEscape(full.Cursor)+"&since="+url.QueryEscape(d.Cursor), nil); w.Code != http.StatusBadRequest {
		t.Errorf("two cursors: status %d, want 400", w.Code)
	}
}
