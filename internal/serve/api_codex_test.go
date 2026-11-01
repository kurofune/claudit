package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/provider"
)

// codexFixtureRoot lays the committed Codex fixtures into a temp sessions
// root at the YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl paths Codex writes.
func codexFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for fixture, rel := range map[string]string{
		"basic.jsonl":    filepath.Join("2026", "10", "24", "rollout-2026-10-24T17-52-01-01a1708b-04a3-75a1-a07c-92bbb16be66f.jsonl"),
		"tools.jsonl":    filepath.Join("2026", "10", "31", "rollout-2026-10-31T16-53-12-01a19461-aed4-78a0-bad1-efc7859dd26a.jsonl"),
		"subagent.jsonl": filepath.Join("2026", "10", "31", "rollout-2026-10-31T06-12-26-01a19217-0c60-71a2-ab13-917397bf7ed1.jsonl"),
		"guardian.jsonl": filepath.Join("2026", "10", "29", "rollout-2026-10-29T04-56-37-01a18784-eaa5-7850-b7db-776a88eb52ee.jsonl"),
	} {
		b, err := os.ReadFile(filepath.Join("..", "parse", "codex", "testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAPICost_CodexModelRow(t *testing.T) {
	cache := corpus.New(provider.NewClaudeCode(t.TempDir()), provider.NewCodex(codexFixtureRoot(t)))
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	srv := NewServer(cache, Options{Prices: loadPricesForTest(t)})

	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/cost?scope=all", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	var rows []struct {
		Model   string
		CostUSD float64
		Turns   int
	}
	if err := json.Unmarshal(body["by_model"], &rows); err != nil {
		t.Fatalf("by_model: %v", err)
	}
	got := map[string]float64{}
	for _, r := range rows {
		got[r.Model] = r.CostUSD
	}
	if c, ok := got["gpt-6-astra"]; !ok || c <= 0 {
		t.Fatalf("by_model gpt-6-astra cost = %v (present %v), want > 0; rows = %+v", c, ok, rows)
	}
	// basic.jsonl: (38472 in, 512 write, 99 out) + (9873 in, 38400 read, 146 out)
	// at $10 / $12.50 / $50 / $1 per MTok.
	want := (38472*10 + 512*12.5 + 99*50 + 9873*10 + 38400*1 + 146*50) / 1e6
	if c := got["gpt-6-astra"]; c < want-1e-9 || c > want+1e-9 {
		t.Errorf("gpt-6-astra cost = %v, want %v", c, want)
	}
	if got["codex-auto-review"] != 0 {
		t.Errorf("codex-auto-review cost = %v, want 0 (auto-review is free)", got["codex-auto-review"])
	}

	ov := readJSONResponse(t, doAPI(t, srv, http.MethodGet, "/_claudit/api/overview?scope=all", nil))
	var unknown []struct{ Provider, Model string }
	if err := json.Unmarshal(ov["unknown_models"], &unknown); err != nil {
		t.Fatalf("unknown_models: %v; raw %s", err, ov["unknown_models"])
	}
	if unknown == nil || len(unknown) != 0 {
		t.Errorf("unknown_models = %s, want an empty list", ov["unknown_models"])
	}
}

// TestAPIAgentsFull_CodexToolBetweenCaps: a Codex call whose input and output
// run 501–2000 runes is capped in /agents and still expands in full through
// /agents/full, which reads the rollout with the Codex parser.
func TestAPIAgentsFull_CodexToolBetweenCaps(t *testing.T) {
	const (
		sessionID = "01a19999-0000-7000-8000-000000000001"
		callID    = "call_between_caps"
	)
	bigIn := strings.Repeat("p", 1200)
	bigOut := "Exit code: 0\nWall time: 1 seconds\nOutput:\n" + strings.Repeat("q", 1500)
	line := func(ts, typ string, payload any) string {
		b, err := json.Marshal(map[string]any{"timestamp": ts, "type": typ, "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	root := t.TempDir()
	path := filepath.Join(root, "2026", "10", "31", "rollout-2026-10-31T10-00-00-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, path,
		line("2026-10-31T10:00:00.000Z", "session_meta", map[string]any{"session_id": sessionID, "id": sessionID, "cwd": "/p/codex", "originator": "codex_exec", "thread_source": "user"}),
		line("2026-10-31T10:00:01.000Z", "turn_context", map[string]any{"cwd": "/p/codex", "model": "gpt-6-astra"}),
		line("2026-10-31T10:00:02.000Z", "response_item", map[string]any{"type": "custom_tool_call", "call_id": callID, "name": "apply_patch", "input": bigIn}),
		line("2026-10-31T10:00:03.000Z", "token_usage_record", map[string]any{"response_id": "resp_1", "usage": map[string]any{"input_tokens": 100, "output_tokens": 10}}),
		line("2026-10-31T10:00:04.000Z", "response_item", map[string]any{"type": "custom_tool_call_output", "call_id": callID, "output": bigOut}),
	)
	cache := corpus.New(provider.NewClaudeCode(t.TempDir()), provider.NewCodex(root))
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	srv := NewServer(cache, Options{Prices: loadPricesForTest(t)})

	var sessions []struct {
		Main struct {
			Steps []struct {
				Tools []struct{ ID, Input, Output string }
			}
		}
	}
	decodeField(t, getSection(t, srv, "/_claudit/api/agents", "codex"), "sessions", &sessions)
	found := false
	for _, s := range sessions {
		for _, st := range s.Main.Steps {
			for _, tool := range st.Tools {
				if tool.ID != callID {
					continue
				}
				found = true
				if want := bigIn[:agentSnippetRunes] + "…"; tool.Input != want {
					t.Errorf("/agents input = %d runes, want the first %d plus …", len([]rune(tool.Input)), agentSnippetRunes)
				}
				if want := bigOut[:agentSnippetRunes] + "…"; tool.Output != want {
					t.Errorf("/agents output = %d runes, want the first %d plus …", len([]rune(tool.Output)), agentSnippetRunes)
				}
			}
		}
	}
	if !found {
		t.Fatalf("%s missing from /agents payload", callID)
	}

	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session="+sessionID+"&tool="+callID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/agents/full status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct{ ID, Name, Input, Output, Status string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if got.ID != callID || got.Name != "apply_patch" {
		t.Errorf("ID/Name = %q/%q, want %s/apply_patch", got.ID, got.Name, callID)
	}
	if got.Input != bigIn {
		t.Errorf("full input len = %d, want %d", len(got.Input), len(bigIn))
	}
	if got.Output != bigOut {
		t.Errorf("full output len = %d, want %d", len(got.Output), len(bigOut))
	}
	if got.Status != "ok" {
		t.Errorf("Status = %q, want ok", got.Status)
	}
}

// TestAPISessionsStory_CodexSubagentWithoutParentTurn: a Codex sub-agent
// rollout whose spawning thread's transcript is gone folds into the root
// session id in the Sessions index, and the story endpoint finds that same
// listed id and serves its child, not a 404.
func TestAPISessionsStory_CodexSubagentWithoutParentTurn(t *testing.T) {
	const rootID = "01a15d72-75c8-7ec1-bff4-4d487e6488ad"
	const childID = "01a19217-0c60-71a2-ab13-917397bf7ed1"
	root := t.TempDir()
	b, err := os.ReadFile(filepath.Join("..", "parse", "codex", "testdata", "subagent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "2026", "10", "31", "rollout-2026-10-31T06-12-26-"+childID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
	cache := corpus.New(provider.NewClaudeCode(t.TempDir()), provider.NewCodex(root))
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	srv := NewServer(cache, Options{Prices: loadPricesForTest(t)})

	rows := getSessions(t, srv, "?scope=all")
	if got := sessionIDs(rows, func(r indexRowWire) string { return r.SessionID }); len(got) != 1 || got[0] != rootID {
		t.Fatalf("index session ids = %v, want [%s]", got, rootID)
	}

	body := getStory(t, srv, "?scope=all&session="+rootID)
	var sid string
	if err := json.Unmarshal(body["session_id"], &sid); err != nil || sid != rootID {
		t.Errorf("story session_id = %s (%v), want %q", body["session_id"], err, rootID)
	}
	var children []struct {
		Kind  string            `json:"kind"`
		Steps []json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(body["children"], &children); err != nil {
		t.Fatalf("children: %v", err)
	}
	if len(children) != 1 || children[0].Kind != "subagent" || len(children[0].Steps) == 0 {
		t.Errorf("children = %s, want the one sub-agent with its steps", body["children"])
	}

	if w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+"?scope=all&session="+childID, nil); w.Code != http.StatusNotFound {
		t.Errorf("story for the folded child thread id: status %d, want 404", w.Code)
	}
}
