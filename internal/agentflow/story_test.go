package agentflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func mustStory(t *testing.T, snap *corpus.Snapshot, f aggregate.Filter, sid string) *SessionStory {
	t.Helper()
	prices, _ := pricing.LoadDefault()
	st, ok := BuildSessionStory(snap, prices, f, sid, StoryOptions{})
	if !ok {
		t.Fatalf("session %s not found", sid)
	}
	return st
}

func mustPage(t *testing.T, st *SessionStory, prompt, offset int) StoryPage {
	t.Helper()
	p, ok := st.Page(prompt, offset, StoryPageTurns)
	if !ok {
		t.Fatalf("page prompt=%d offset=%d not found", prompt, offset)
	}
	return p
}

// TestBuildSessionStory_ChildParentToolUseID: a sub-agent whose .meta.json
// names its toolUseId carries it as parent_tool_use_id; one without meta
// carries "" and its own started_at.
func TestBuildSessionStory_ChildParentToolUseID(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	withMeta := writeSubagent(t, root, "s1", "m1", `{"agentType":"Explore","description":"find callers","toolUseId":"toolu_spawn"}`)
	noMeta := writeSubagent(t, root, "s1", "n1", "")

	spawn := mkTurn("a1", "s1", root+"/s1.jsonl", t0)
	spawn.ToolUses = []parse.ToolUse{{ID: "toolu_spawn", Name: "Agent", SubagentType: "Explore", Input: "find callers"}}
	snap := &corpus.Snapshot{Turns: []parse.Turn{
		spawn,
		mkTurn("m1", "s1", withMeta, t0.Add(time.Second)),
		mkTurn("n1", "s1", noMeta, t0.Add(2*time.Second)),
	}}
	p := mustPage(t, mustStory(t, snap, aggregate.Filter{}, "s1"), 0, 0)
	if len(p.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(p.Children))
	}
	m, n := p.Children[0], p.Children[1]
	if m.ParentToolUseID != "toolu_spawn" || m.AgentType != "Explore" || m.Description != "find callers" {
		t.Errorf("meta child = %+v, want toolu_spawn / Explore / find callers", m)
	}
	if n.ParentToolUseID != "" || !n.StartedAt.Equal(t0.Add(2*time.Second)) {
		t.Errorf("meta-less child = parent %q started %v, want \"\" and %v", n.ParentToolUseID, n.StartedAt, t0.Add(2*time.Second))
	}
	if len(m.Steps) != 1 || m.Steps[0].UUID != "m1" {
		t.Errorf("meta child steps = %+v, want its one turn", m.Steps)
	}
	b, _ := json.Marshal(n)
	if !strings.Contains(string(b), `"parent_tool_use_id":""`) {
		t.Errorf("meta-less child JSON omits parent_tool_use_id: %s", b)
	}
	if sp := p.Steps[0].Tools[0].Spawned; sp == nil || sp.AgentRef != "toolu_spawn" {
		t.Errorf("spawning call carries no rollup: %+v", p.Steps[0].Tools[0])
	}
}

// TestBuildSessionStory_ExitCodeEnvelope: a Codex tool output opening with
// "Exit code: 1\nOutput:\n" carries exit_code 1 and the text after the
// Output line; a Claude Code output carries exit_code null, unchanged.
func TestBuildSessionStory_ExitCodeEnvelope(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	envelope := "Exit code: 1\nOutput:\n--- FAIL: TestX\nFAIL\n"
	cx := mkTurn("c1", "cx", "/root/rollout-cx.jsonl", t0)
	cx.Provider = parse.ProviderCodex
	cx.ToolUses = []parse.ToolUse{{ID: "tc", Name: "Bash", Input: "go test"}}
	cc := mkTurn("k1", "cc", "/root/-p-x/cc.jsonl", t0)
	cc.ToolUses = []parse.ToolUse{{ID: "tk", Name: "Bash", Input: "go test"}}
	snap := &corpus.Snapshot{
		Turns: []parse.Turn{cx, cc},
		ToolResults: []parse.ToolResult{
			{ToolUseID: "tc", IsError: true, Content: envelope},
			{ToolUseID: "tk", IsError: true, Content: envelope},
		},
	}
	codexTool := mustPage(t, mustStory(t, snap, aggregate.Filter{}, "cx"), 0, 0).Steps[0].Tools[0]
	if codexTool.ExitCode == nil || *codexTool.ExitCode != 1 {
		t.Errorf("codex exit_code = %v, want 1", codexTool.ExitCode)
	}
	if codexTool.Output != "--- FAIL: TestX\nFAIL\n" {
		t.Errorf("codex output = %q, want the text after Output:", codexTool.Output)
	}
	claudeTool := mustPage(t, mustStory(t, snap, aggregate.Filter{}, "cc"), 0, 0).Steps[0].Tools[0]
	if claudeTool.ExitCode != nil {
		t.Errorf("claude-code exit_code = %d, want null", *claudeTool.ExitCode)
	}
	if claudeTool.Output != envelope {
		t.Errorf("claude-code output = %q, want unchanged", claudeTool.Output)
	}
	b, _ := json.Marshal(claudeTool)
	if !strings.Contains(string(b), `"exit_code":null`) {
		t.Errorf("claude-code tool JSON = %s, want exit_code null", b)
	}
}

// twoPromptSnap is one session asked two things, with a sub-agent spawned
// under each prompt.
func twoPromptSnap(t *testing.T) (*corpus.Snapshot, time.Time) {
	root := t.TempDir()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	main := root + "/s1.jsonl"
	first := writeSubagent(t, root, "s1", "f1", `{"agentType":"Explore","toolUseId":"t1"}`)
	second := writeSubagent(t, root, "s1", "g1", `{"agentType":"Plan","toolUseId":"t2"}`)
	a1 := mkTurn("a1", "s1", main, t0.Add(time.Second))
	a1.ParentUUID = "u1"
	a2 := mkTurn("a2", "s1", main, t0.Add(2*time.Second))
	a2.ParentUUID = "a1"
	b1 := mkTurn("b1", "s1", main, t0.Add(48*time.Hour+time.Second))
	b1.ParentUUID = "u2"
	return &corpus.Snapshot{
		Turns: []parse.Turn{
			a1, a2, b1,
			mkTurn("f1", "s1", first, t0.Add(1500*time.Millisecond)),
			mkTurn("g1", "s1", second, t0.Add(48*time.Hour+2*time.Second)),
		},
		Users: []parse.UserMessage{
			{SessionID: "s1", UUID: "u1", Timestamp: t0, Text: "fix the login bug", SourceFile: main},
			{SessionID: "s1", UUID: "u2", Timestamp: t0.Add(48 * time.Hour), Text: "now write a test", SourceFile: main},
		},
	}, t0
}

func TestBuildSessionStory_PagesByPrompt(t *testing.T) {
	snap, t0 := twoPromptSnap(t)
	st := mustStory(t, snap, aggregate.Filter{}, "s1")
	if len(st.Prompts) != 2 {
		t.Fatalf("prompts = %+v, want 2", st.Prompts)
	}
	if p := st.Prompts[0]; p.UUID != "u1" || p.Text != "fix the login bug" || p.Turns != 2 || !p.Timestamp.Equal(t0) {
		t.Errorf("prompt 0 = %+v", p)
	}
	if p := st.Prompts[1]; p.UUID != "u2" || p.Turns != 1 {
		t.Errorf("prompt 1 = %+v", p)
	}
	p0 := mustPage(t, st, 0, 0)
	if len(p0.Steps) != 2 || p0.Steps[0].UUID != "a1" || p0.Steps[1].UUID != "a2" {
		t.Errorf("page 0 steps = %+v, want a1 a2", p0.Steps)
	}
	if len(p0.Children) != 1 || p0.Children[0].AgentType != "Explore" {
		t.Errorf("page 0 children = %+v, want the Explore child only", p0.Children)
	}
	if p0.Page != (StoryPageInfo{PromptIndex: 0, TurnOffset: 0, TurnLimit: StoryPageTurns, HasMore: false}) {
		t.Errorf("page 0 info = %+v", p0.Page)
	}
	p1 := mustPage(t, st, 1, 0)
	if len(p1.Steps) != 1 || p1.Steps[0].UUID != "b1" || len(p1.Children) != 1 || p1.Children[0].AgentType != "Plan" {
		t.Errorf("page 1 = steps %+v children %+v", p1.Steps, p1.Children)
	}
	if _, ok := st.Page(2, 0, StoryPageTurns); ok {
		t.Errorf("prompt 2 of 2 answered, want not found")
	}
}

// TestBuildSessionStory_HiddenCountsFilteredTurns: a window that starts
// after the first prompt hides its turns and says how many.
func TestBuildSessionStory_HiddenCountsFilteredTurns(t *testing.T) {
	snap, t0 := twoPromptSnap(t)
	st := mustStory(t, snap, aggregate.Filter{Since: t0.Add(24 * time.Hour)}, "s1")
	if st.Hidden.OutsideFilter != 3 || st.Hidden.Replays != 0 {
		t.Errorf("hidden = %+v, want 3 outside the filter (a1, a2, f1)", st.Hidden)
	}
	if len(st.Prompts) != 1 || st.Prompts[0].UUID != "u2" {
		t.Errorf("prompts = %+v, want only u2", st.Prompts)
	}
}

func TestBuildSessionStory_HiddenCountsReplays(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	orig := mkTurn("a1", "s1", "a.jsonl", t0)
	orig.MessageID = "m1"
	replay := mkTurn("a1", "s2", "b.jsonl", t0)
	replay.MessageID = "m1"
	fresh := mkTurn("a2", "s2", "b.jsonl", t0.Add(time.Minute))
	fresh.MessageID = "m2"
	st := mustStory(t, &corpus.Snapshot{Turns: []parse.Turn{orig, replay, fresh}}, aggregate.Filter{}, "s2")
	if st.Hidden.Replays != 1 {
		t.Errorf("hidden.replays = %d, want 1", st.Hidden.Replays)
	}
}

func TestBuildSessionStory_PagesLongPrompt(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turns := make([]parse.Turn, 450)
	for i := range turns {
		turns[i] = mkTurn(fmt.Sprintf("a%03d", i), "s1", "/root/s1.jsonl", t0.Add(time.Duration(i)*time.Second))
		turns[i].ParentUUID = "u1"
	}
	snap := &corpus.Snapshot{Turns: turns, Users: []parse.UserMessage{{SessionID: "s1", UUID: "u1", Timestamp: t0, Text: "go"}}}
	st := mustStory(t, snap, aggregate.Filter{}, "s1")
	for _, c := range []struct {
		offset, n int
		more      bool
		first     string
	}{{0, 200, true, "a000"}, {200, 200, true, "a200"}, {400, 50, false, "a400"}} {
		p := mustPage(t, st, 0, c.offset)
		if len(p.Steps) != c.n || p.Page.HasMore != c.more || p.Steps[0].UUID != c.first {
			t.Errorf("offset %d: %d steps, has_more %v, first %s; want %d %v %s", c.offset, len(p.Steps), p.Page.HasMore, p.Steps[0].UUID, c.n, c.more, c.first)
		}
	}
	if _, ok := st.Page(0, 450, StoryPageTurns); ok {
		t.Errorf("offset past the prompt's turns answered, want not found")
	}
}

func TestBuildSessionStory_UnknownSession(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	snap := &corpus.Snapshot{Turns: []parse.Turn{mkTurn("a1", "s1", "/root/s1.jsonl", time.Now())}}
	if _, ok := BuildSessionStory(snap, prices, aggregate.Filter{}, "nope", StoryOptions{}); ok {
		t.Errorf("unknown session found")
	}
}
