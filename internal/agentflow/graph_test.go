package agentflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func TestBuildAgentGraph_CoalescesMessageIntoOneStep(t *testing.T) {
	// A multi-block message (5 JSONL lines, one message.id) followed by a second
	// one-line message. After ParseFile coalesces, the main agent must show ONE
	// step per message — merged thinking/text/tools, cost counted once — and the
	// first step's duration must be the gap to the NEXT MESSAGE (9s), not the
	// gap to the next line within the same message (1s).
	lines := []string{
		`{"type":"assistant","uuid":"a1","parentUuid":"u0","timestamp":"2026-04-10T10:00:01Z","sessionId":"s1","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1000000,"output_tokens":0},"content":[{"type":"thinking","thinking":"reason"}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","timestamp":"2026-04-10T10:00:02Z","sessionId":"s1","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1000000,"output_tokens":0},"content":[{"type":"text","text":"narration"}]}}`,
		`{"type":"assistant","uuid":"a3","parentUuid":"a2","timestamp":"2026-04-10T10:00:03Z","sessionId":"s1","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1000000,"output_tokens":0},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"assistant","uuid":"a4","parentUuid":"a3","timestamp":"2026-04-10T10:00:04Z","sessionId":"s1","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1000000,"output_tokens":0},"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/x"}}]}}`,
		`{"type":"assistant","uuid":"a5","parentUuid":"a4","timestamp":"2026-04-10T10:00:05Z","sessionId":"s1","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1000000,"output_tokens":0},"content":[{"type":"tool_use","id":"t3","name":"Edit","input":{"file_path":"/y"}}]}}`,
		`{"type":"assistant","uuid":"a6","parentUuid":"a5","timestamp":"2026-04-10T10:00:10Z","sessionId":"s1","message":{"id":"msg_2","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"done"}]}}`,
	}
	res, err := parse.ParseFile(strings.NewReader(strings.Join(lines, "\n")+"\n"), "/p/s1.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	prices, _ := pricing.LoadDefault()
	snap := &corpus.Snapshot{Turns: res.Turns, Links: res.ParentLinks}
	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Sessions) != 1 || g.Sessions[0].Main == nil {
		t.Fatalf("expected 1 session with a main agent, got %+v", g.Sessions)
	}
	main := g.Sessions[0].Main
	if len(main.Steps) != 2 {
		t.Fatalf("main steps = %d, want 2 (one per message, not per line)", len(main.Steps))
	}
	s0 := main.Steps[0]
	if s0.Thinking != "reason" || s0.Text != "narration" {
		t.Errorf("step0 content not merged: thinking=%q text=%q", s0.Thinking, s0.Text)
	}
	if len(s0.Tools) != 3 {
		t.Errorf("step0 tools = %d, want 3 merged", len(s0.Tools))
	}
	if s0.CostUSD < 4.99 || s0.CostUSD > 5.01 {
		t.Errorf("step0 cost = %v, want ~$5 once (not ~$25)", s0.CostUSD)
	}
	if s0.DurationMs != 9000 {
		t.Errorf("step0 duration = %dms, want 9000 (gap to next message, not next line)", s0.DurationMs)
	}
	if g.Sessions[0].CostUSD < 4.99 || g.Sessions[0].CostUSD > 5.01 {
		t.Errorf("session cost = %v, want ~$5 (message counted once)", g.Sessions[0].CostUSD)
	}
}

// writeSubagent creates a subagents/agent-<id>.jsonl path (the file itself is
// not needed — the graph reads turns from the snapshot) plus its sibling
// .meta.json, and returns the jsonl path to use as a turn's SourceFile.
func writeSubagent(t *testing.T, root, sessionID, agentID, metaJSON string) string {
	t.Helper()
	subDir := filepath.Join(root, sessionID, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonlPath := filepath.Join(subDir, "agent-"+agentID+".jsonl")
	if metaJSON != "" {
		metaPath := filepath.Join(subDir, "agent-"+agentID+".meta.json")
		if err := os.WriteFile(metaPath, []byte(metaJSON), 0o644); err != nil {
			t.Fatalf("write meta: %v", err)
		}
	}
	return jsonlPath
}

// mkTurn builds a parse.Turn for tests. src is the SourceFile path that
// identifies which agent (main vs subagent) the turn belongs to.
func mkTurn(uuid, sid, src string, ts time.Time) parse.Turn {
	return parse.Turn{
		UUID:       uuid,
		SessionID:  sid,
		SourceFile: src,
		Timestamp:  ts,
		Model:      "claude-opus-4-7",
		Usage:      parse.Usage{InputTokens: 1000},
	}
}

func TestBuildAgentGraph_EmptySnapshot(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	g, err := BuildAgentGraph(&corpus.Snapshot{}, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 0 {
		t.Errorf("want 0 sessions, got %d", len(g.Sessions))
	}
}

func TestBuildAgentGraph_SingleMainTurn(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.CWD = "/p/x"
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(g.Sessions))
	}
	s := g.Sessions[0]
	if s.SessionID != "s1" || s.CWD != "/p/x" {
		t.Errorf("session metadata wrong: %+v", s)
	}
	if s.Main == nil {
		t.Fatalf("want a main node, got nil")
	}
	if s.Main.Kind != "main" {
		t.Errorf("main node Kind = %q, want %q", s.Main.Kind, "main")
	}
	if len(s.Children) != 0 {
		t.Errorf("want 0 children, got %d", len(s.Children))
	}
}

func TestBuildAgentGraph_JoinsToolResults(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.ToolUses = []parse.ToolUse{
		{ID: "toolu_1", Name: "Bash", Input: "go test"},
	}
	snap := &corpus.Snapshot{
		Turns: []parse.Turn{turn},
		ToolResults: []parse.ToolResult{
			{ToolUseID: "toolu_1", IsError: true, Content: "FAIL"},
		},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 1 || g.Sessions[0].Main == nil {
		t.Fatalf("want 1 session with a main node, got %+v", g.Sessions)
	}
	steps := g.Sessions[0].Main.Steps
	if len(steps) != 1 || len(steps[0].Tools) != 1 {
		t.Fatalf("want 1 step with 1 tool, got %+v", steps)
	}
	tool := steps[0].Tools[0]
	if tool.Status != "error" || tool.Output != "FAIL" {
		t.Errorf("tool outcome = %+v, want error/FAIL", tool)
	}
}

// Per-tool timing must survive the whole pipeline: the turn timestamp becomes
// each tool's StartedAt and the matching tool_result line's timestamp becomes
// its EndedAt, so the Timeline can draw a sub-span whose width is the tool's
// wall-clock. An 8s Bash here should report an 8s [start,end].
func TestBuildAgentGraph_SurfacesPerToolTiming(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.ToolUses = []parse.ToolUse{{ID: "toolu_1", Name: "Bash", Input: "go build"}}
	snap := &corpus.Snapshot{
		Turns: []parse.Turn{turn},
		ToolResults: []parse.ToolResult{
			{ToolUseID: "toolu_1", IsError: false, Content: "ok", Timestamp: t0.Add(8 * time.Second)},
		},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tool := g.Sessions[0].Main.Steps[0].Tools[0]
	if tool.StartedAt == nil || !tool.StartedAt.Equal(t0) {
		t.Errorf("tool StartedAt = %v, want %v", tool.StartedAt, t0)
	}
	if tool.EndedAt == nil || !tool.EndedAt.Equal(t0.Add(8*time.Second)) {
		t.Errorf("tool EndedAt = %v, want %v", tool.EndedAt, t0.Add(8*time.Second))
	}
}

func TestBuildAgentGraph_MainNodeRollsUp(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	src := "/root/-p-x/s1.jsonl"
	turns := []parse.Turn{
		mkTurn("a1", "s1", src, t0),
		mkTurn("a2", "s1", src, t0.Add(time.Minute)),
	}
	snap := &corpus.Snapshot{Turns: turns}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := g.Sessions[0].Main
	if m == nil {
		t.Fatalf("want a main node, got nil")
	}
	if !m.StartedAt.Equal(t0) {
		t.Errorf("StartedAt = %v, want %v", m.StartedAt, t0)
	}
	if !m.EndedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("EndedAt = %v, want %v", m.EndedAt, t0.Add(time.Minute))
	}
	if len(m.Steps) != 2 {
		t.Errorf("want 2 steps, got %d", len(m.Steps))
	}
	if m.Tokens.InputTokens != 2000 {
		t.Errorf("Tokens.InputTokens = %d, want 2000", m.Tokens.InputTokens)
	}
	if m.CostUSD <= 0 {
		t.Errorf("CostUSD = %v, want > 0", m.CostUSD)
	}
}

func TestBuildAgentGraph_StepCarriesModelCostAndTools(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Model = "claude-opus-4-7"
	turn.ToolUses = []parse.ToolUse{
		{Name: "Bash", Detail: "git status", Input: "git status -s"},
		{Name: "Agent", SubagentType: "Explore", Input: "find callers"},
	}
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := g.Sessions[0].Main.Steps
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(steps))
	}
	step := steps[0]
	if step.Model != "claude-opus-4-7" {
		t.Errorf("step.Model = %q, want %q", step.Model, "claude-opus-4-7")
	}
	if step.CostUSD <= 0 {
		t.Errorf("step.CostUSD = %v, want > 0", step.CostUSD)
	}
	if len(step.Tools) != 2 {
		t.Fatalf("want 2 tool invocations, got %d", len(step.Tools))
	}
	if step.Tools[0].Name != "Bash" || step.Tools[0].Detail != "git status" {
		t.Errorf("tools[0] = %+v, want Bash/git status", step.Tools[0])
	}
	if step.Tools[1].Name != "Agent" || step.Tools[1].Detail != "Explore" {
		t.Errorf("tools[1] = %+v, want Agent/Explore", step.Tools[1])
	}
}

func TestBuildAgentGraph_StepCarriesTokens(t *testing.T) {
	// The turn drawer shows per-turn token counts, so each AgentStep must carry
	// its turn's usage (counted once for a coalesced message). Two cache tiers
	// plus input/output cover the whole tuple.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Usage = parse.Usage{
		InputTokens:         111,
		OutputTokens:        222,
		CacheCreate5mTokens: 333,
		CacheCreate1hTokens: 44,
		CacheReadTokens:     555,
	}
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := g.Sessions[0].Main.Steps
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(steps))
	}
	got := steps[0].Tokens
	if got.InputTokens != 111 || got.OutputTokens != 222 ||
		got.CacheCreate5mTokens != 333 || got.CacheCreate1hTokens != 44 ||
		got.CacheReadTokens != 555 {
		t.Errorf("step.Tokens = %+v, want the turn's usage (111/222/333/44/555)", got)
	}
}

func TestBuildAgentGraph_InterStepDuration(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	src := "/root/-p-x/s1.jsonl"
	// Input order is reversed to confirm steps are ordered chronologically
	// before durations are computed.
	turns := []parse.Turn{
		mkTurn("a2", "s1", src, t0.Add(90*time.Second)),
		mkTurn("a1", "s1", src, t0),
	}
	snap := &corpus.Snapshot{Turns: turns}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := g.Sessions[0].Main.Steps
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[0].DurationMs != 90000 {
		t.Errorf("steps[0].DurationMs = %d, want 90000", steps[0].DurationMs)
	}
	if steps[1].DurationMs != 0 {
		t.Errorf("steps[1].DurationMs = %d, want 0 (last step)", steps[1].DurationMs)
	}
}

func TestBuildAgentGraph_SubagentChildFromMeta(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	mainSrc := filepath.Join(root, "s1.jsonl")
	subSrc := writeSubagent(t, root, "s1", "abc",
		`{"agentType":"Explore","description":"find callers","toolUseId":"toolu_1"}`)

	turns := []parse.Turn{
		mkTurn("a1", "s1", mainSrc, t0),
		mkTurn("b1", "s1", subSrc, t0.Add(time.Second)),
	}
	turns[1].Sidechain = true
	snap := &corpus.Snapshot{Turns: turns}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(g.Sessions))
	}
	s := g.Sessions[0]
	if s.Main == nil {
		t.Fatalf("want a main node, got nil")
	}
	if len(s.Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(s.Children))
	}
	c := s.Children[0]
	if c.Kind != "subagent" {
		t.Errorf("child Kind = %q, want %q", c.Kind, "subagent")
	}
	if c.AgentType != "Explore" {
		t.Errorf("child AgentType = %q, want %q", c.AgentType, "Explore")
	}
	if c.Description != "find callers" {
		t.Errorf("child Description = %q, want %q", c.Description, "find callers")
	}
}

func TestBuildAgentGraph_SubagentParentToolUseID(t *testing.T) {
	// The exact reverse link: a sub-agent whose meta carries toolUseId X gets
	// ParentToolUseID == X, while a sub-agent without meta gets an empty one.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	mainSrc := filepath.Join(root, "s1.jsonl")
	linkedSrc := writeSubagent(t, root, "s1", "abc",
		`{"agentType":"Explore","description":"e","toolUseId":"toolu_X"}`)
	orphanSrc := writeSubagent(t, root, "s1", "def", "") // no meta.json

	turns := []parse.Turn{
		mkTurn("a1", "s1", mainSrc, t0),
		mkTurn("b1", "s1", linkedSrc, t0.Add(time.Second)),
		mkTurn("c1", "s1", orphanSrc, t0.Add(2*time.Second)),
	}
	turns[1].Sidechain = true
	turns[2].Sidechain = true
	snap := &corpus.Snapshot{Turns: turns}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := g.Sessions[0]
	if len(s.Children) != 2 {
		t.Fatalf("want 2 children, got %d", len(s.Children))
	}
	var linked, orphan *AgentNode
	for i := range s.Children {
		switch s.Children[i].AgentType {
		case "Explore":
			linked = &s.Children[i]
		default:
			orphan = &s.Children[i]
		}
	}
	if linked == nil || orphan == nil {
		t.Fatalf("missing child: linked=%v orphan=%v", linked, orphan)
	}
	if linked.ParentToolUseID != "toolu_X" {
		t.Errorf("linked ParentToolUseID = %q, want %q", linked.ParentToolUseID, "toolu_X")
	}
	if orphan.ParentToolUseID != "" {
		t.Errorf("orphan ParentToolUseID = %q, want empty", orphan.ParentToolUseID)
	}
}

func TestBuildAgentGraph_SubagentWithoutMeta(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	mainSrc := filepath.Join(root, "s1.jsonl")
	subSrc := writeSubagent(t, root, "s1", "nometa", "") // no meta.json written

	turns := []parse.Turn{
		mkTurn("a1", "s1", mainSrc, t0),
		mkTurn("b1", "s1", subSrc, t0.Add(time.Second)),
	}
	snap := &corpus.Snapshot{Turns: turns}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions[0].Children) != 1 {
		t.Fatalf("want 1 child, got %d", len(g.Sessions[0].Children))
	}
	c := g.Sessions[0].Children[0]
	if c.Kind != "subagent" {
		t.Errorf("child Kind = %q, want %q", c.Kind, "subagent")
	}
	if c.AgentType != "" {
		t.Errorf("child AgentType = %q, want empty (no meta)", c.AgentType)
	}
	if len(c.Steps) != 1 {
		t.Errorf("want 1 step on the meta-less child, got %d", len(c.Steps))
	}
}

func TestBuildAgentGraph_MultipleSubagentsOrderedByStart(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	mainSrc := filepath.Join(root, "s1.jsonl")
	subEarly := writeSubagent(t, root, "s1", "aaa", `{"agentType":"early","description":"e"}`)
	subLate := writeSubagent(t, root, "s1", "bbb", `{"agentType":"late","description":"l"}`)

	// subLate's agent-id sorts before subEarly's would by some orderings, but
	// it starts later — children must order by StartedAt, not id or insertion.
	turns := []parse.Turn{
		mkTurn("m1", "s1", mainSrc, t0),
		mkTurn("l1", "s1", subLate, t0.Add(30*time.Second)),
		mkTurn("e1", "s1", subEarly, t0.Add(5*time.Second)),
	}
	snap := &corpus.Snapshot{Turns: turns}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	children := g.Sessions[0].Children
	if len(children) != 2 {
		t.Fatalf("want 2 children, got %d", len(children))
	}
	if children[0].AgentType != "early" {
		t.Errorf("children[0].AgentType = %q, want %q (earlier start first)", children[0].AgentType, "early")
	}
	if children[1].AgentType != "late" {
		t.Errorf("children[1].AgentType = %q, want %q", children[1].AgentType, "late")
	}
}

func TestBuildAgentGraph_StepCarriesThinkingAndText(t *testing.T) {
	// A turn's assistant reasoning and narration flow onto its step so
	// tool-less turns become auditable in the Agents view.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Thinking = "let me reason"
	turn.Text = "here is the plan"
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	step := g.Sessions[0].Main.Steps[0]
	if step.Thinking != "let me reason" {
		t.Errorf("Thinking = %q, want %q", step.Thinking, "let me reason")
	}
	if step.Text != "here is the plan" {
		t.Errorf("Text = %q, want %q", step.Text, "here is the plan")
	}
}

func TestBuildAgentGraph_RedactsThinkingAndText(t *testing.T) {
	// Under Redact, reasoning/narration become length-echoing markers — but
	// an empty field stays empty (no "[redacted 0 chars]" noise), matching
	// how tool-input redaction is gated on non-empty.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Thinking = "secret reasoning"
	turn.Text = "" // empty narration must stay empty
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{Redact: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	step := g.Sessions[0].Main.Steps[0]
	if want := aggregate.RedactMarker("secret reasoning"); step.Thinking != want {
		t.Errorf("Thinking = %q, want %q", step.Thinking, want)
	}
	if step.Text != "" {
		t.Errorf("Text = %q, want empty (not a redacted-0 marker)", step.Text)
	}
}

func TestBuildAgentGraph_PromptMarkersSegmentMainTimeline(t *testing.T) {
	// Two user prompts in one session: prompt A produced two turns, prompt B
	// produced one. The main timeline (sorted by time) should segment into two
	// PromptMarkers in order, the second starting at the step index where
	// prompt B's first turn lands.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	src := "/root/-p-x/s1.jsonl"

	userA := parse.UserMessage{UUID: "uA", SessionID: "s1", Text: "first prompt", Timestamp: t0}
	userB := parse.UserMessage{UUID: "uB", SessionID: "s1", Text: "second prompt", Timestamp: t0.Add(2 * time.Minute)}

	turn1 := mkTurn("t1", "s1", src, t0.Add(10*time.Second))
	turn1.ParentUUID = "uA"
	turn2 := mkTurn("t2", "s1", src, t0.Add(20*time.Second))
	turn2.ParentUUID = "uA"
	turn3 := mkTurn("t3", "s1", src, t0.Add(2*time.Minute+10*time.Second))
	turn3.ParentUUID = "uB"

	snap := &corpus.Snapshot{
		Turns: []parse.Turn{turn1, turn2, turn3},
		Users: []parse.UserMessage{userA, userB},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompts := g.Sessions[0].Prompts
	if len(prompts) != 2 {
		t.Fatalf("want 2 prompt markers, got %d: %+v", len(prompts), prompts)
	}
	if prompts[0].UUID != "uA" || prompts[0].FirstStepIndex != 0 {
		t.Errorf("marker[0] = {UUID:%q, FirstStepIndex:%d}, want {uA, 0}", prompts[0].UUID, prompts[0].FirstStepIndex)
	}
	if prompts[1].UUID != "uB" || prompts[1].FirstStepIndex != 2 {
		t.Errorf("marker[1] = {UUID:%q, FirstStepIndex:%d}, want {uB, 2}", prompts[1].UUID, prompts[1].FirstStepIndex)
	}
}

func TestBuildAgentGraph_PromptMarkerCarriesTextAndTimestamp(t *testing.T) {
	// A marker surfaces its originating prompt's text and timestamp so the
	// Conversation lens can render the prompt bubble without a second lookup.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	src := "/root/-p-x/s1.jsonl"
	promptTS := t0.Add(5 * time.Second)

	user := parse.UserMessage{UUID: "uA", SessionID: "s1", Text: "audit the parser", Timestamp: promptTS}
	turn := mkTurn("t1", "s1", src, t0.Add(10*time.Second))
	turn.ParentUUID = "uA"

	snap := &corpus.Snapshot{
		Turns: []parse.Turn{turn},
		Users: []parse.UserMessage{user},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompts := g.Sessions[0].Prompts
	if len(prompts) != 1 {
		t.Fatalf("want 1 prompt marker, got %d", len(prompts))
	}
	if prompts[0].Text != "audit the parser" {
		t.Errorf("Text = %q, want %q", prompts[0].Text, "audit the parser")
	}
	if !prompts[0].Timestamp.Equal(promptTS) {
		t.Errorf("Timestamp = %v, want %v", prompts[0].Timestamp, promptTS)
	}
}

func TestBuildAgentGraph_RedactsPromptMarkerText(t *testing.T) {
	// Under Redact the prompt body becomes a length-echoing marker so a shared
	// report doesn't leak the conversation — mirroring tool-input/thinking
	// redaction. UUID and timestamp still flow (they're not secret).
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	src := "/root/-p-x/s1.jsonl"

	user := parse.UserMessage{UUID: "uA", SessionID: "s1", Text: "secret prompt body", Timestamp: t0}
	turn := mkTurn("t1", "s1", src, t0.Add(10*time.Second))
	turn.ParentUUID = "uA"

	snap := &corpus.Snapshot{
		Turns: []parse.Turn{turn},
		Users: []parse.UserMessage{user},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{Redact: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prompts := g.Sessions[0].Prompts
	if len(prompts) != 1 {
		t.Fatalf("want 1 prompt marker, got %d", len(prompts))
	}
	if want := aggregate.RedactMarker("secret prompt body"); prompts[0].Text != want {
		t.Errorf("Text = %q, want %q", prompts[0].Text, want)
	}
	if prompts[0].UUID != "uA" {
		t.Errorf("UUID = %q, want uA (redaction must not hide the id)", prompts[0].UUID)
	}
}

func TestBuildAgentGraph_RedactsToolInput(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.ToolUses = []parse.ToolUse{{Name: "Bash", Detail: "git status", Input: "git status -s"}}
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	// Redact off: raw input passes through.
	plain, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := plain.Sessions[0].Main.Steps[0].Tools[0].Input; got != "git status -s" {
		t.Errorf("unredacted Input = %q, want %q", got, "git status -s")
	}

	// Redact on: input becomes the length-echoing marker, never the raw text.
	red, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{Redact: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := red.Sessions[0].Main.Steps[0].Tools[0].Input
	if got == "git status -s" {
		t.Errorf("Input = %q, want redacted marker (raw text leaked under Redact)", got)
	}
	if got != "[redacted 13 chars]" {
		t.Errorf("Input = %q, want %q", got, "[redacted 13 chars]")
	}
}

func TestBuildAgentGraph_NodeErrorCount(t *testing.T) {
	// A node's ErrorCount counts only its tool calls whose Status is "error";
	// an "ok" call alongside an errored one yields ErrorCount == 1.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.ToolUses = []parse.ToolUse{
		{ID: "toolu_bad", Name: "Bash", Input: "go test"},
		{ID: "toolu_ok", Name: "Read", Input: "main.go"},
	}
	snap := &corpus.Snapshot{
		Turns: []parse.Turn{turn},
		ToolResults: []parse.ToolResult{
			{ToolUseID: "toolu_bad", IsError: true, Content: "FAIL"},
			{ToolUseID: "toolu_ok", IsError: false, Content: "ok"},
		},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := g.Sessions[0].Main
	if m == nil {
		t.Fatalf("want a main node, got nil")
	}
	if m.ErrorCount != 1 {
		t.Errorf("node ErrorCount = %d, want 1", m.ErrorCount)
	}
}

func TestBuildAgentGraph_SessionErrorCountSumsNodes(t *testing.T) {
	// The session-level ErrorCount totals errored tool calls across the main
	// agent and every sub-agent — one error in main plus one in a sub-agent
	// rolls up to 2.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	mainSrc := filepath.Join(root, "s1.jsonl")
	subSrc := writeSubagent(t, root, "s1", "abc", `{"agentType":"Explore","description":"e"}`)

	mainTurn := mkTurn("a1", "s1", mainSrc, t0)
	mainTurn.ToolUses = []parse.ToolUse{{ID: "m_bad", Name: "Bash", Input: "go test"}}
	subTurn := mkTurn("b1", "s1", subSrc, t0.Add(time.Second))
	subTurn.Sidechain = true
	subTurn.ToolUses = []parse.ToolUse{{ID: "s_bad", Name: "Read", Input: "x.go"}}

	snap := &corpus.Snapshot{
		Turns: []parse.Turn{mainTurn, subTurn},
		ToolResults: []parse.ToolResult{
			{ToolUseID: "m_bad", IsError: true, Content: "boom"},
			{ToolUseID: "s_bad", IsError: true, Content: "boom"},
		},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(g.Sessions))
	}
	if g.Sessions[0].ErrorCount != 2 {
		t.Errorf("session ErrorCount = %d, want 2", g.Sessions[0].ErrorCount)
	}
}

func TestBuildAgentGraph_SpawnRollupOnAgentCall(t *testing.T) {
	// The Agent tool_use that launched a sub-agent carries a Spawned rollup of
	// that child's cost/tokens/errors/duration — the blast radius of one
	// decision. A non-spawning tool call in the same turn has nil Spawned.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	mainSrc := filepath.Join(root, "s1.jsonl")
	subSrc := writeSubagent(t, root, "s1", "abc",
		`{"agentType":"Explore","description":"find callers","toolUseId":"toolu_X"}`)

	mainTurn := mkTurn("a1", "s1", mainSrc, t0)
	mainTurn.ToolUses = []parse.ToolUse{
		{ID: "toolu_X", Name: "Agent", Detail: "Explore"},
		{ID: "toolu_read", Name: "Read", Input: "main.go"},
	}
	// Sub-agent spans two turns (so EndedAt-StartedAt is a real duration) and
	// errors on one tool call.
	sub1 := mkTurn("b1", "s1", subSrc, t0.Add(time.Second))
	sub1.Sidechain = true
	sub1.ToolUses = []parse.ToolUse{{ID: "s_bad", Name: "Bash", Input: "go test"}}
	sub2 := mkTurn("b2", "s1", subSrc, t0.Add(5*time.Second))
	sub2.Sidechain = true

	snap := &corpus.Snapshot{
		Turns: []parse.Turn{mainTurn, sub1, sub2},
		ToolResults: []parse.ToolResult{
			{ToolUseID: "s_bad", IsError: true, Content: "FAIL"},
		},
	}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := g.Sessions[0]
	if s.Main == nil || len(s.Children) != 1 {
		t.Fatalf("want main + 1 child, got main=%v children=%d", s.Main, len(s.Children))
	}
	child := s.Children[0]

	// Locate the Agent and Read invocations in the main agent's only step.
	var agentInv, readInv *aggregate.ToolInvocation
	for i := range s.Main.Steps[0].Tools {
		switch s.Main.Steps[0].Tools[i].ID {
		case "toolu_X":
			agentInv = &s.Main.Steps[0].Tools[i]
		case "toolu_read":
			readInv = &s.Main.Steps[0].Tools[i]
		}
	}
	if agentInv == nil || readInv == nil {
		t.Fatalf("missing invocation: agent=%v read=%v", agentInv, readInv)
	}
	if readInv.Spawned != nil {
		t.Errorf("Read call Spawned = %+v, want nil", readInv.Spawned)
	}
	if agentInv.Spawned == nil {
		t.Fatalf("Agent call Spawned = nil, want a rollup")
	}
	sp := agentInv.Spawned
	if sp.AgentRef != "toolu_X" {
		t.Errorf("Spawned.AgentRef = %q, want %q", sp.AgentRef, "toolu_X")
	}
	if sp.CostUSD != child.CostUSD {
		t.Errorf("Spawned.CostUSD = %v, want child cost %v", sp.CostUSD, child.CostUSD)
	}
	if sp.Tokens != child.Tokens {
		t.Errorf("Spawned.Tokens = %+v, want child tokens %+v", sp.Tokens, child.Tokens)
	}
	if sp.ErrorCount != child.ErrorCount || sp.ErrorCount != 1 {
		t.Errorf("Spawned.ErrorCount = %d, want child %d (==1)", sp.ErrorCount, child.ErrorCount)
	}
	wantDur := child.EndedAt.Sub(child.StartedAt).Milliseconds()
	if sp.DurationMs != wantDur || wantDur != 4000 {
		t.Errorf("Spawned.DurationMs = %d, want child elapsed %d (==4000)", sp.DurationMs, wantDur)
	}
}

func findSession(g AgentGraph, id string) *AgentSession {
	for i := range g.Sessions {
		if g.Sessions[i].SessionID == id {
			return &g.Sessions[i]
		}
	}
	return nil
}

func TestBuildAgentGraph_Status(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	newest := t0.Add(time.Hour)

	// s1: last turn is the newest in the corpus → within the active window.
	live := mkTurn("a1", "s1", "/root/-p/s1.jsonl", newest)
	live.ToolUses = []parse.ToolUse{{Name: "Bash", Detail: "go test"}}
	// s2: last turn is an hour old → outside the active window.
	stale := mkTurn("b1", "s2", "/root/-p/s2.jsonl", t0)

	snap := &corpus.Snapshot{Turns: []parse.Turn{live, stale}}

	// Now is "live"'s instant — so s1 (ended at Now) is inside the window and
	// s2 (an hour earlier) is outside it.
	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{Now: newest, ActiveWindow: 30 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s1 := findSession(g, "s1")
	s2 := findSession(g, "s2")
	if s1 == nil || s2 == nil {
		t.Fatalf("missing sessions: s1=%v s2=%v", s1, s2)
	}
	if s1.Main.Status != "running" {
		t.Errorf("s1 status = %q, want %q", s1.Main.Status, "running")
	}
	if s1.Main.CurrentTool != "Bash" {
		t.Errorf("s1 CurrentTool = %q, want %q", s1.Main.CurrentTool, "Bash")
	}
	if s2.Main.Status != "done" {
		t.Errorf("s2 status = %q, want %q", s2.Main.Status, "done")
	}
	if s2.Main.CurrentTool != "" {
		t.Errorf("s2 CurrentTool = %q, want empty (done)", s2.Main.CurrentTool)
	}
}

func TestBuildAgentGraph_FilterExcludesTurns(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	inWindow := mkTurn("a1", "s1", "/root/-proj-alpha/s1.jsonl", t0.Add(time.Hour))
	inWindow.CWD = "/proj/alpha"
	tooEarly := mkTurn("a2", "s1", "/root/-proj-alpha/s1.jsonl", t0.Add(-time.Hour))
	tooEarly.CWD = "/proj/alpha"
	wrongProject := mkTurn("c1", "s2", "/root/-proj-beta/s2.jsonl", t0.Add(time.Hour))
	wrongProject.CWD = "/proj/beta"

	snap := &corpus.Snapshot{Turns: []parse.Turn{inWindow, tooEarly, wrongProject}}
	f := aggregate.Filter{Since: t0, ProjectSubstring: "alpha"}

	g, err := BuildAgentGraph(snap, prices, f, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 1 {
		t.Fatalf("want 1 session after filter, got %d", len(g.Sessions))
	}
	s := g.Sessions[0]
	if s.SessionID != "s1" {
		t.Errorf("session = %q, want s1 (alpha, in-window)", s.SessionID)
	}
	if s.Main == nil || len(s.Main.Steps) != 1 {
		t.Fatalf("want main with 1 surviving step, got %+v", s.Main)
	}
}

func TestBuildAgentGraph_TopNCapsByRecency(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Cost and recency deliberately DISAGREE: the oldest session is the
	// costliest. A recency cap must keep the two most recent and drop the
	// old-but-expensive one — proving it's not selecting by cost.
	oldRich := mkTurn("a1", "old-rich", "/root/-p/old.jsonl", t0)
	oldRich.Usage = parse.Usage{InputTokens: 500_000}
	mid := mkTurn("b1", "mid", "/root/-p/mid.jsonl", t0.Add(time.Minute))
	mid.Usage = parse.Usage{InputTokens: 50_000}
	newCheap := mkTurn("c1", "new-cheap", "/root/-p/new.jsonl", t0.Add(2*time.Minute))
	newCheap.Usage = parse.Usage{InputTokens: 1_000}

	snap := &corpus.Snapshot{Turns: []parse.Turn{oldRich, mid, newCheap}}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{TopN: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 2 {
		t.Fatalf("want 2 sessions after TopN cap, got %d", len(g.Sessions))
	}
	if findSession(g, "old-rich") != nil {
		t.Errorf("oldest session should be dropped by TopN=2 recency cap, even though it's costliest")
	}
	if findSession(g, "mid") == nil || findSession(g, "new-cheap") == nil {
		t.Errorf("the two most recent sessions (mid, new-cheap) must survive the cap")
	}
}

func TestBuildAgentGraph_TopNZeroIsUnlimited(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turns := []parse.Turn{
		mkTurn("a1", "s1", "/root/-p/s1.jsonl", t0),
		mkTurn("b1", "s2", "/root/-p/s2.jsonl", t0.Add(time.Minute)),
		mkTurn("c1", "s3", "/root/-p/s3.jsonl", t0.Add(2*time.Minute)),
	}
	g, err := BuildAgentGraph(&corpus.Snapshot{Turns: turns}, prices, aggregate.Filter{}, Options{TopN: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 3 {
		t.Errorf("TopN=0 must not cap; want 3 sessions, got %d", len(g.Sessions))
	}
}

func TestBuildAgentGraph_SessionsSortedNewestFirst(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// Input order (early first) is the opposite of what we expect out: the
	// Agents view leads with the most recently active session so a live run
	// sits on top.
	early := mkTurn("a1", "early", "/root/-p/early.jsonl", t0.Add(time.Minute))
	late := mkTurn("b1", "late", "/root/-p/late.jsonl", t0.Add(time.Hour))
	snap := &corpus.Snapshot{Turns: []parse.Turn{early, late}}

	g, err := BuildAgentGraph(snap, prices, aggregate.Filter{}, Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(g.Sessions))
	}
	if g.Sessions[0].SessionID != "late" {
		t.Errorf("Sessions[0] = %q, want %q (most recent first)", g.Sessions[0].SessionID, "late")
	}
	if g.Sessions[1].SessionID != "early" {
		t.Errorf("Sessions[1] = %q, want %q", g.Sessions[1].SessionID, "early")
	}
}
