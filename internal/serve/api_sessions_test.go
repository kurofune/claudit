package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type indexRowWire struct {
	SessionID    string    `json:"session_id"`
	FamilyKey    string    `json:"family_key"`
	Remainder    string    `json:"remainder"`
	Title        string    `json:"title"`
	Outcome      string    `json:"outcome"`
	Project      string    `json:"project"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	CostUSD      float64   `json:"cost_usd"`
	DurationMs   int64     `json:"duration_ms"`
	Turns        int       `json:"turns"`
	Commands     int       `json:"commands"`
	Failed       int       `json:"failed"`
	LastActivity time.Time `json:"last_activity"`
	Active       bool      `json:"active"`
	StartedAt    time.Time `json:"started_at"`
}

func getSessions(t *testing.T, srv *Server, query string) []indexRowWire {
	t.Helper()
	w := doAPI(t, srv, http.MethodGet, apiPathSessions+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET sessions%s: status %d; body=%s", query, w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	assertTopLevelKeys(t, body, []string{"sessions"})
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

// TestAPISessions_FiltersNarrowLikeAgents: ?since/?until/?project/?provider
// narrow the index to exactly the sessions /agents shows.
func TestAPISessions_FiltersNarrowLikeAgents(t *testing.T) {
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
		w := doAPI(t, srv, http.MethodGet, apiPathAgents+c.query, nil)
		var agents struct {
			Sessions []struct {
				SessionID string `json:"session_id"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &agents); err != nil {
			t.Fatalf("agents%s: %v", c.query, err)
		}
		fromAgents := make([]string, 0, len(agents.Sessions))
		for _, s := range agents.Sessions {
			fromAgents = append(fromAgents, s.SessionID)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("sessions%s = %v, want %v", c.query, got, c.want)
		}
		if strings.Join(got, ",") != strings.Join(fromAgents, ",") {
			t.Errorf("sessions%s = %v, /agents shows %v", c.query, got, fromAgents)
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
	assertTopLevelKeys(t, body, []string{"session_id", "prompts", "page", "steps", "children", "hidden"})

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
	if got, want := keysOf(t, p.Steps[0]), "context_tokens,cost_usd,duration_ms,gen_ms,model,text,timestamp,tokens,tools,uuid"; got != want {
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

// TestAPISessionsStory_PagesShareOneAggregator: a session's later pages
// reuse the Aggregator the first page built rather than rebuilding it.
func TestAPISessionsStory_PagesShareOneAggregator(t *testing.T) {
	srv := newTestServer(t, longCorpus(t))
	getStory(t, srv, "?session=s-long")
	after1 := srv.aggregateBuildCount()
	if after1 != 1 {
		t.Fatalf("first page built %d aggregators, want 1", after1)
	}
	getStory(t, srv, "?session=s-long&offset=200")
	getStory(t, srv, "?session=s-long&offset=400&prompt=0")
	if got := srv.aggregateBuildCount(); got != after1 {
		t.Errorf("aggregate builds after three pages = %d, want %d", got, after1)
	}
}
