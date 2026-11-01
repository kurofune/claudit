package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/provider"
)

type storyToolWire struct {
	ID        string `json:"id"`
	Input     string `json:"input"`
	InputKey  string `json:"input_key"`
	InputFull string `json:"input_full"`
	Status    string `json:"status"`
	Count     int    `json:"count"`
}

type storyStepWire struct {
	UUID  string          `json:"uuid"`
	Text  string          `json:"text"`
	Tools []storyToolWire `json:"tools"`
}

type storyPageWire struct {
	Prompts []struct {
		UUID string `json:"uuid"`
		Text string `json:"text"`
	} `json:"prompts"`
	Steps    []storyStepWire `json:"steps"`
	Children []struct {
		Steps []storyStepWire `json:"steps"`
	} `json:"children"`
}

func getStoryPage(t *testing.T, srv *Server, query string) storyPageWire {
	t.Helper()
	w := doAPI(t, srv, http.MethodGet, apiPathSessionsStory+query, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET story%s: status %d; body=%s", query, w.Code, w.Body.String())
	}
	var p storyPageWire
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal story: %v", err)
	}
	return p
}

func jsonLine(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAPISessionsStory_FullTextFromClaudeLog: a prompt whose wrapper block
// fills the parse-time snippet, a message past the snippet cap, seven runs
// of one command, two commands that differ only past the input cap and a
// sub-agent's long message all reach the story page whole, read back from
// the real session logs.
func TestAPISessionsStory_FullTextFromClaudeLog(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	ts := func(s int) string { return t0.Add(time.Duration(s) * time.Second).Format(time.RFC3339) }
	prompt := "<system-reminder>\n" + strings.Repeat("r", 3000) + "\n</system-reminder>\n\n  fix the login bug\n\tthen   rerun"
	narration := strings.Repeat("n", 2500) + " END-OF-NARRATION"
	shared := "echo " + strings.Repeat("x", 2400)
	content := []any{map[string]any{"type": "text", "text": narration}}
	for i := 0; i < 7; i++ {
		content = append(content, map[string]any{"type": "tool_use", "id": "toolu_same" + string(rune('a'+i)), "name": "Bash", "input": map[string]any{"command": "go test ./..."}})
	}
	content = append(content,
		map[string]any{"type": "tool_use", "id": "toolu_long_a", "name": "Bash", "input": map[string]any{"command": shared + " A"}},
		map[string]any{"type": "tool_use", "id": "toolu_long_b", "name": "Bash", "input": map[string]any{"command": shared + " B"}},
	)
	usage := map[string]any{"input_tokens": 10, "output_tokens": 2}
	results := []any{}
	for i := 0; i < 7; i++ {
		results = append(results, map[string]any{"type": "tool_result", "tool_use_id": "toolu_same" + string(rune('a'+i)), "is_error": i < 2, "content": "out"})
	}
	writeJSONL(t, filepath.Join(dir, "s-full.jsonl"),
		jsonLine(t, map[string]any{"type": "user", "uuid": "u1", "timestamp": ts(0), "sessionId": "s-full", "cwd": "/p/full", "message": map[string]any{"role": "user", "content": prompt}}),
		jsonLine(t, map[string]any{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "timestamp": ts(1), "sessionId": "s-full", "cwd": "/p/full",
			"message": map[string]any{"id": "msg_1", "model": "claude-opus-4-7", "role": "assistant", "content": content, "usage": usage}}),
		jsonLine(t, map[string]any{"type": "user", "uuid": "r1", "parentUuid": "a1", "timestamp": ts(5), "sessionId": "s-full", "cwd": "/p/full", "message": map[string]any{"role": "user", "content": results}}),
	)
	subDir := filepath.Join(dir, "s-full", "subagents")
	childText := strings.Repeat("c", 2300) + " CHILD-END"
	writeJSONL(t, filepath.Join(subDir, "agent-k1.jsonl"),
		jsonLine(t, map[string]any{"type": "assistant", "uuid": "k1", "timestamp": ts(2), "sessionId": "s-full", "cwd": "/p/full", "isSidechain": true,
			"message": map[string]any{"id": "msg_k1", "model": "claude-haiku-4-5", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": childText}}, "usage": usage}}),
	)
	if err := os.WriteFile(filepath.Join(subDir, "agent-k1.meta.json"), []byte(`{"agentType":"Explore","description":"find callers","toolUseId":"toolu_samea"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, dir)

	p := getStoryPage(t, srv, "?scope=all&session=s-full")
	if len(p.Prompts) != 1 || p.Prompts[0].Text != prompt {
		t.Fatalf("prompt text = %d runes, want the whole %d-rune prompt verbatim", len([]rune(p.Prompts[0].Text)), len([]rune(prompt)))
	}
	if len(p.Steps) != 1 || p.Steps[0].Text != narration {
		t.Fatalf("step text = %d runes, want the whole %d-rune message", len([]rune(p.Steps[0].Text)), len([]rune(narration)))
	}
	tools := p.Steps[0].Tools
	if len(tools) != 9 {
		t.Fatalf("tools = %d, want each of the 9 calls on its own row", len(tools))
	}
	errs := 0
	for _, tool := range tools[:7] {
		if tool.Count != 1 {
			t.Errorf("tool %s count = %d, want 1", tool.ID, tool.Count)
		}
		if tool.Status == "error" {
			errs++
		}
		if tool.InputKey == "" || tool.InputKey != tools[0].InputKey {
			t.Errorf("tool %s input_key = %q, want the shared key %q", tool.ID, tool.InputKey, tools[0].InputKey)
		}
	}
	if errs != 2 {
		t.Errorf("errored repeats = %d, want 2 with their own outcomes", errs)
	}
	a, b := tools[7], tools[8]
	if a.Input != b.Input {
		t.Errorf("capped inputs differ; the fixture needs them to share the snippet")
	}
	if a.InputKey == b.InputKey {
		t.Errorf("input_key %q shared by commands that differ past the cap", a.InputKey)
	}
	if a.InputFull != shared+" A" || b.InputFull != shared+" B" {
		t.Errorf("input_full = %d/%d runes, want the whole commands (%d)", len(a.InputFull), len(b.InputFull), len(shared)+2)
	}
	if len(p.Children) != 1 || len(p.Children[0].Steps) != 1 || p.Children[0].Steps[0].Text != childText {
		t.Fatalf("children = %+v, want the sub-agent's whole message", p.Children)
	}

	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session=s-full&turn=k1", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "CHILD-END") {
		t.Errorf("sub-agent turn full: status %d body %.80s, want 200 with its whole text", w.Code, w.Body.String())
	}
}

// TestAPISessionsStory_CodexFullText: a Codex root rollout and its
// sub-agent thread. The story page carries the root's whole prompt and
// message and the child's whole message, and /agents/full answers turn
// and tool requests for both root and child under the root session id.
func TestAPISessionsStory_CodexFullText(t *testing.T) {
	const (
		rootID  = "01a20000-0000-7000-8000-00000000000a"
		childID = "01a20000-0000-7000-8000-00000000000b"
	)
	rec := func(ts, typ string, payload any) string {
		return jsonLine(t, map[string]any{"timestamp": ts, "type": typ, "payload": payload})
	}
	msg := func(role, kind, text string) map[string]any {
		return map[string]any{"type": "message", "role": role, "content": []any{map[string]any{"type": kind, "text": text}}}
	}
	usage := map[string]any{"input_tokens": 100, "output_tokens": 10}
	prompt := strings.Repeat("p", 2600) + " PROMPT-END"
	rootText := strings.Repeat("m", 2600) + " ROOT-END"
	childText := strings.Repeat("k", 2600) + " CHILD-END"
	longCmd := "echo " + strings.Repeat("z", 2500) + " CMD-END"

	root := t.TempDir()
	day := filepath.Join(root, "2026", "10", "31")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, filepath.Join(day, "rollout-2026-10-31T10-00-00-"+rootID+".jsonl"),
		rec("2026-10-31T10:00:00.000Z", "session_meta", map[string]any{"session_id": rootID, "id": rootID, "cwd": "/p/codex", "originator": "codex_exec", "thread_source": "user"}),
		rec("2026-10-31T10:00:01.000Z", "turn_context", map[string]any{"cwd": "/p/codex", "model": "gpt-6-astra"}),
		rec("2026-10-31T10:00:02.000Z", "response_item", msg("user", "input_text", prompt)),
		rec("2026-10-31T10:00:03.000Z", "response_item", msg("assistant", "output_text", rootText)),
		rec("2026-10-31T10:00:04.000Z", "token_usage_record", map[string]any{"response_id": "resp_root", "usage": usage}),
	)
	writeJSONL(t, filepath.Join(day, "rollout-2026-10-31T10-00-05-"+childID+".jsonl"),
		rec("2026-10-31T10:00:05.000Z", "session_meta", map[string]any{"session_id": rootID, "id": childID, "cwd": "/p/codex", "originator": "codex_exec", "thread_source": "subagent", "parent_thread_id": rootID}),
		rec("2026-10-31T10:00:06.000Z", "turn_context", map[string]any{"cwd": "/p/codex", "model": "gpt-6-astra"}),
		rec("2026-10-31T10:00:07.000Z", "response_item", map[string]any{"type": "function_call", "call_id": "call_child", "name": "exec_command", "arguments": jsonLine(t, map[string]any{"cmd": longCmd})}),
		rec("2026-10-31T10:00:08.000Z", "response_item", msg("assistant", "output_text", childText)),
		rec("2026-10-31T10:00:09.000Z", "token_usage_record", map[string]any{"response_id": "resp_child", "usage": usage}),
		rec("2026-10-31T10:00:10.000Z", "response_item", map[string]any{"type": "function_call_output", "call_id": "call_child", "output": "Exit code: 0\nOutput:\nok"}),
	)
	cache := corpus.New(provider.NewClaudeCode(t.TempDir()), provider.NewCodex(root))
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	srv := NewServer(cache, Options{Prices: loadPricesForTest(t)})

	p := getStoryPage(t, srv, "?scope=all&session="+rootID)
	if len(p.Prompts) != 1 || p.Prompts[0].Text != prompt {
		t.Fatalf("prompts = %d, text %d runes; want the whole %d-rune prompt", len(p.Prompts), len([]rune(p.Prompts[0].Text)), len([]rune(prompt)))
	}
	if len(p.Steps) != 1 || p.Steps[0].Text != rootText {
		t.Fatalf("root step text = %d runes, want the whole message", len([]rune(p.Steps[0].Text)))
	}
	if len(p.Children) != 1 || len(p.Children[0].Steps) != 1 {
		t.Fatalf("children = %+v, want the one sub-agent step", p.Children)
	}
	child := p.Children[0].Steps[0]
	if child.Text != childText {
		t.Errorf("child step text = %d runes, want the whole message", len([]rune(child.Text)))
	}
	if len(child.Tools) != 1 || child.Tools[0].InputFull != longCmd {
		t.Errorf("child tool input_full = %+v, want the whole command", child.Tools)
	}

	for _, c := range []struct{ query, want string }{
		{"turn=resp_root", "ROOT-END"},
		{"turn=resp_child", "CHILD-END"},
		{"tool=call_child", "CMD-END"},
	} {
		w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session="+rootID+"&"+c.query, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("/agents/full %s: status %d body %.80s, want 200 carrying %s", c.query, w.Code, w.Body.String(), c.want)
		}
	}
}

// TestAPISessionsStory_CodexPromptVerbatim: a Codex prompt that carries
// injected wrapper items around the person's request reaches the story
// whole, below and above the snippet cap: every input_text item in order,
// wrappers included, with the request's own whitespace kept.
func TestAPISessionsStory_CodexPromptVerbatim(t *testing.T) {
	const rootID = "01a20000-0000-7000-8000-00000000000c"
	rec := func(ts, typ string, payload any) string {
		return jsonLine(t, map[string]any{"timestamp": ts, "type": typ, "payload": payload})
	}
	userMsg := func(items ...string) map[string]any {
		content := make([]any, len(items))
		for i, s := range items {
			content[i] = map[string]any{"type": "input_text", "text": s}
		}
		return map[string]any{"type": "message", "role": "user", "content": content}
	}
	answer := func(text string) map[string]any {
		return map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}
	}
	usage := map[string]any{"input_tokens": 100, "output_tokens": 10}
	agentsMD := "# AGENTS.md instructions for /p/codex\n\n<INSTRUCTIONS>\nbe terse\n</INSTRUCTIONS>"
	env := "<environment_context>\n  <cwd>/p/codex</cwd>\n</environment_context>"
	short := "\n    indented request\n\nsecond line  \n"
	long := "  " + strings.Repeat("q", 2600) + " LONG-END\t\n"

	root := t.TempDir()
	day := filepath.Join(root, "2026", "10", "31")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, filepath.Join(day, "rollout-2026-10-31T11-00-00-"+rootID+".jsonl"),
		rec("2026-10-31T11:00:00.000Z", "session_meta", map[string]any{"session_id": rootID, "id": rootID, "cwd": "/p/codex", "originator": "codex_exec", "thread_source": "user"}),
		rec("2026-10-31T11:00:01.000Z", "turn_context", map[string]any{"cwd": "/p/codex", "model": "gpt-6-astra"}),
		rec("2026-10-31T11:00:02.000Z", "response_item", userMsg(agentsMD, env, short)),
		rec("2026-10-31T11:00:03.000Z", "response_item", answer("done one")),
		rec("2026-10-31T11:00:04.000Z", "token_usage_record", map[string]any{"response_id": "resp_one", "usage": usage}),
		rec("2026-10-31T11:00:05.000Z", "response_item", userMsg(env, long)),
		rec("2026-10-31T11:00:06.000Z", "response_item", answer("done two")),
		rec("2026-10-31T11:00:07.000Z", "token_usage_record", map[string]any{"response_id": "resp_two", "usage": usage}),
	)
	cache := corpus.New(provider.NewClaudeCode(t.TempDir()), provider.NewCodex(root))
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	srv := NewServer(cache, Options{Prices: loadPricesForTest(t)})

	p := getStoryPage(t, srv, "?scope=all&session="+rootID)
	want := []string{agentsMD + "\n" + env + "\n" + short, env + "\n" + long}
	if len(p.Prompts) != len(want) {
		t.Fatalf("prompts = %d, want %d", len(p.Prompts), len(want))
	}
	for i, w := range want {
		if p.Prompts[i].Text != w {
			t.Errorf("prompt %d text = %.160q (%d runes), want the verbatim %.160q (%d runes)", i, p.Prompts[i].Text, len([]rune(p.Prompts[i].Text)), w, len([]rune(w)))
		}
	}
}
