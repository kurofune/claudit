package agentflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

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
