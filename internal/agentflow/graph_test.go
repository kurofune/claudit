package agentflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/v2/internal/aggregate"
	"github.com/kurofune/claudit/v2/internal/corpus"
	"github.com/kurofune/claudit/v2/internal/parse"
	"github.com/kurofune/claudit/v2/internal/parse/codex"
	"github.com/kurofune/claudit/v2/internal/pricing"
)

// TestAssembleSession_CarriesEntrypoint: a session's origin (cli vs sdk-cli)
// is a session invariant lifted from its turns, so the Agents view can badge
// headless runs. First non-empty entrypoint wins; it must reach the JSON.
func TestAssembleSession_CarriesEntrypoint(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Entrypoint = "sdk-cli"
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 1 {
		t.Fatalf("want 1 session, got %d", len(g.Sessions))
	}
	if g.Sessions[0].Entrypoint != "sdk-cli" {
		t.Errorf("session Entrypoint = %q, want sdk-cli", g.Sessions[0].Entrypoint)
	}
	b, err := json.Marshal(g.Sessions[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"entrypoint":"sdk-cli"`) {
		t.Errorf("payload missing entrypoint: %s", b)
	}
}

func TestAssembleSession_CoalescesMessageIntoOneStep(t *testing.T) {
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
	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_StepGenMs(t *testing.T) {
	// A streamed turn's gen_ms is the span between its first and last JSONL
	// line (model think + emit). Here msg_1 streams :01→:05 → gen_ms=4000; the
	// single-line msg_2 at :10 has no measurable span → gen_ms=0. gen_ms is a
	// subset of duration_ms (the 9s gap to the next message), not the whole gap.
	lines := []string{
		`{"type":"assistant","uuid":"a1","parentUuid":"u0","timestamp":"2026-04-10T10:00:01Z","sessionId":"s1","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"thinking","thinking":"reason"}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","timestamp":"2026-04-10T10:00:05Z","sessionId":"s1","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"assistant","uuid":"a3","parentUuid":"a2","timestamp":"2026-04-10T10:00:10Z","sessionId":"s1","message":{"id":"msg_2","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"done"}]}}`,
	}
	res, err := parse.ParseFile(strings.NewReader(strings.Join(lines, "\n")+"\n"), "/p/s1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := pricing.LoadDefault()
	snap := &corpus.Snapshot{Turns: res.Turns, Links: res.ParentLinks}
	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Sessions) != 1 || g.Sessions[0].Main == nil {
		t.Fatalf("expected 1 session with a main agent, got %+v", g.Sessions)
	}
	steps := g.Sessions[0].Main.Steps
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	if steps[0].GenMs != 4000 {
		t.Errorf("step0 GenMs = %d, want 4000 (first→last line span)", steps[0].GenMs)
	}
	if steps[0].DurationMs != 9000 {
		t.Errorf("step0 DurationMs = %d, want 9000 (gap to next message)", steps[0].DurationMs)
	}
	if steps[0].GenMs > steps[0].DurationMs {
		t.Errorf("GenMs %d must not exceed DurationMs %d", steps[0].GenMs, steps[0].DurationMs)
	}
	if steps[1].GenMs != 0 {
		t.Errorf("step1 GenMs = %d, want 0 (single-line message has no span)", steps[1].GenMs)
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

// TestAssembleSession_ExcludesReplayedTurns: a forked session (s2/b.jsonl)
// replays the original session's turn (s1/a.jsonl, same message.id). The
// replay was billed once already, so the Agents view must exclude it — the
// sum of session costs must equal the deduped headline.
func TestAssembleSession_ExcludesReplayedTurns(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	orig := mkTurn("a1", "s1", "a.jsonl", t0)
	orig.MessageID = "m1"
	replay := mkTurn("a1", "s2", "b.jsonl", t0)
	replay.MessageID = "m1" // same generation, replayed into the fork
	fresh := mkTurn("a2", "s2", "b.jsonl", t0.Add(time.Minute))
	fresh.MessageID = "m2"

	turns := []parse.Turn{orig, replay, fresh}
	snap := &corpus.Snapshot{Turns: turns}

	// Deduped headline.
	agg := aggregate.New(prices)
	for _, tn := range turns {
		agg.Add(tn)
	}
	head := agg.Totals()

	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sum float64
	var s2 *AgentSession
	for i := range g.Sessions {
		sum += g.Sessions[i].CostUSD
		if g.Sessions[i].SessionID == "s2" {
			s2 = &g.Sessions[i]
		}
	}
	if d := sum - head.CostUSD; d > 1e-9 || d < -1e-9 {
		t.Errorf("agent-graph cost sum = %.6f, want %.6f (headline)", sum, head.CostUSD)
	}
	if s2 == nil {
		t.Fatalf("forked session s2 missing")
	}
	// s2 should bill only its net-new turn (m2), not the replayed m1.
	oneTurn, _ := prices.Cost("claude-opus-4-7", 1000, 0, 0, 0, 0)
	if d := s2.CostUSD - oneTurn; d > 1e-9 || d < -1e-9 {
		t.Errorf("forked session s2 cost = %.6f, want %.6f (replay excluded)", s2.CostUSD, oneTurn)
	}
}

func TestAssembleSession_EmptySnapshot(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	g, err := graphOf(&corpus.Snapshot{}, prices, aggregate.Filter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(g.Sessions) != 0 {
		t.Errorf("want 0 sessions, got %d", len(g.Sessions))
	}
}

func TestAssembleSession_SingleMainTurn(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.CWD = "/p/x"
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_JoinsToolResults(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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
func TestAssembleSession_SurfacesPerToolTiming(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_MainNodeRollsUp(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	src := "/root/-p-x/s1.jsonl"
	turns := []parse.Turn{
		mkTurn("a1", "s1", src, t0),
		mkTurn("a2", "s1", src, t0.Add(time.Minute)),
	}
	snap := &corpus.Snapshot{Turns: turns}

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_StepCarriesModelCostAndTools(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Model = "claude-opus-4-7"
	turn.ToolUses = []parse.ToolUse{
		{Name: "Bash", Detail: "git status", Input: "git status -s"},
		{Name: "Agent", SubagentType: "Explore", Input: "find callers"},
	}
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_StepCarriesTokens(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

// context_tokens is the prompt size fed to the model that turn — input plus
// the cached prefix replayed in (cache_read) — and deliberately excludes output
// and cache-creation. It's the size that drives context-growth charts and the
// per-turn cache-hit ratio. Here 111 input + 555 cache_read = 666.
func TestAssembleSession_StepContextTokens(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Usage = parse.Usage{
		InputTokens:         111,
		OutputTokens:        222, // excluded
		CacheCreate5mTokens: 333, // excluded
		CacheCreate1hTokens: 44,  // excluded
		CacheReadTokens:     555,
	}
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := g.Sessions[0].Main.Steps
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(steps))
	}
	if steps[0].ContextTokens != 666 {
		t.Errorf("ContextTokens = %d, want 666 (input 111 + cache_read 555)", steps[0].ContextTokens)
	}
}

func TestAssembleSession_InterStepDuration(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_SubagentChildFromMeta(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_SubagentParentToolUseID(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_SubagentWithoutMeta(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_MultipleSubagentsOrderedByStart(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_StepCarriesThinkingAndText(t *testing.T) {
	// A turn's assistant reasoning and narration flow onto its step so
	// tool-less turns become auditable in the Agents view.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	turn.Thinking = "let me reason"
	turn.Text = "here is the plan"
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_StepCarriesUUID(t *testing.T) {
	// The step's uuid is the key the drawer uses to fetch the turn's
	// untruncated thinking/text via /agents/full?turn=.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", t0)
	snap := &corpus.Snapshot{Turns: []parse.Turn{turn}}

	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := g.Sessions[0].Main.Steps[0].UUID; got != "a1" {
		t.Errorf("Step UUID = %q, want a1 (the turn's uuid)", got)
	}
}

func TestAssembleSession_PromptMarkersSegmentMainTimeline(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_PromptMarkerCarriesTextAndTimestamp(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_NodeErrorCount(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_SessionErrorCountSumsNodes(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

func TestAssembleSession_SpawnRollupOnAgentCall(t *testing.T) {
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

	g, err := graphOf(snap, prices, aggregate.Filter{})
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

// testGraph is every session of a snapshot as BuildSessionStory
// assembles it, for tests of the shared grouping.
type testGraph struct{ Sessions []AgentSession }

// graphOf groups snap with collectSessions and assembles each session with
// its own tool results and prompts, as BuildSessionStory does, most
// recently active first.
func graphOf(snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter) (testGraph, error) {
	c := collectSessions(snap, prices, f, "", nil)
	l := NewLayout(snap, nil)
	var g testGraph
	for _, sa := range c.sessions {
		g.Sessions = append(g.Sessions, assembleSession(sa, sessionToolResults(snap, l, sa), storyPrompts(snap, l, sa, nil)))
	}
	sort.SliceStable(g.Sessions, func(i, j int) bool {
		if !g.Sessions[i].EndedAt.Equal(g.Sessions[j].EndedAt) {
			return g.Sessions[i].EndedAt.After(g.Sessions[j].EndedAt)
		}
		return g.Sessions[i].SessionID < g.Sessions[j].SessionID
	})
	return g, nil
}

func TestAssembleSession_FilterExcludesTurns(t *testing.T) {
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

	g, err := graphOf(snap, prices, f)
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

// TestAssembleSession_SessionProjectIsResolvedRepo: a session run in a linked
// worktree is labeled with the main repo's name, beside its raw cwd, so the
// Agents view groups it with the main checkout.
func TestAssembleSession_SessionProjectIsResolvedRepo(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(base, "the-pit")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	wt := filepath.Join(base, "wt", "sub")
	for _, d := range []string{gitdir, wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "wt", ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prices, _ := pricing.LoadDefault()
	turn := mkTurn("a1", "s1", "/root/-p-x/s1.jsonl", time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	turn.CWD = wt
	g, err := graphOf(&corpus.Snapshot{Turns: []parse.Turn{turn}}, prices, aggregate.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	s := g.Sessions[0]
	if s.Project != "the-pit" || s.CWD != wt {
		t.Errorf("session {Project:%q CWD:%q}, want {the-pit %q}", s.Project, s.CWD, wt)
	}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"project":"the-pit"`) {
		t.Errorf("payload missing project: %s", b)
	}
}

// TestAssembleSession_PathToolsCarryFilePathInput: Read rows reach the Agents
// payload with the file path as input, not just the extension in detail — so
// one errored .png Read never makes every later .png Read look like a retry.
func TestAssembleSession_PathToolsCarryFilePathInput(t *testing.T) {
	read := func(n int, path string) string {
		return fmt.Sprintf(`{"type":"assistant","uuid":"a%d","timestamp":"2026-04-10T10:00:0%dZ","sessionId":"s1","message":{"id":"msg_%d","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","id":"t%d","name":"Read","input":{"file_path":%q}}]}}`, n, n, n, n, path)
	}
	result := func(n int, isErr bool) string {
		return fmt.Sprintf(`{"type":"user","uuid":"u%d","timestamp":"2026-04-10T10:00:0%dZ","sessionId":"s1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t%d","is_error":%t,"content":"x"}]}}`, n, n, n, isErr)
	}
	paths := []string{"/x/a.png", "/x/b.png", "/x/c.png", "/x/d.png"}
	var lines []string
	for i, p := range paths {
		lines = append(lines, read(i+1, p), result(i+1, i == 0))
	}
	res, err := parse.ParseFile(strings.NewReader(strings.Join(lines, "\n")+"\n"), "/p/s1.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	prices, _ := pricing.LoadDefault()
	snap := &corpus.Snapshot{Turns: res.Turns, Links: res.ParentLinks, ToolResults: res.ToolResults}
	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Sessions) != 1 || g.Sessions[0].Main == nil {
		t.Fatalf("expected 1 session with a main agent, got %+v", g.Sessions)
	}
	var reads []aggregate.ToolInvocation
	for _, s := range g.Sessions[0].Main.Steps {
		for _, tool := range s.Tools {
			if tool.Name == "Read" {
				reads = append(reads, tool)
			}
		}
	}
	if len(reads) != len(paths) {
		t.Fatalf("Read rows = %d, want %d: %+v", len(reads), len(paths), reads)
	}
	for i, p := range paths {
		if reads[i].Input != p || reads[i].Detail != ".png" {
			t.Errorf("Read[%d] input/detail = %q/%q, want %q/.png", i, reads[i].Input, reads[i].Detail, p)
		}
	}
	if reads[0].Status != "error" || reads[1].Status != "ok" {
		t.Errorf("statuses = %q, %q; want error, ok", reads[0].Status, reads[1].Status)
	}
}

// TestAssembleSession_CodexSubagentNestsUnderParent: a Codex subagent rollout
// carries its own thread uuid as SessionID and a ParentLink to the spawning
// thread. The Agents graph must nest it under the parent session as a child,
// not show it as a second main session, and roll its cost into the parent's.
func TestAssembleSession_CodexSubagentNestsUnderParent(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	f, err := os.Open(filepath.Join("..", "parse", "codex", "testdata", "subagent.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	childSrc := "/codex/2026/10/31/rollout-2026-10-31T06-12-26-01a19217-0c60-71a2-ab13-917397bf7ed1.jsonl"
	child, err := codex.ParseFile(f, childSrc)
	if err != nil {
		t.Fatal(err)
	}
	const parentID = "01a15d72-75c8-7ec1-bff4-4d487e6488ad"
	parent := mkTurn("resp_parent", parentID, "/codex/2026/10/31/rollout-2026-10-31T06-00-00-"+parentID+".jsonl",
		time.Date(2026, 10, 31, 13, 0, 0, 0, time.UTC))
	parent.Provider = parse.ProviderCodex
	parent.Model = "gpt-6-sol"
	parent.Entrypoint = "codex-tui"
	snap := &corpus.Snapshot{
		Turns: append([]parse.Turn{parent}, child.Turns...),
		Links: child.ParentLinks,
	}

	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1 (the child nests under the parent)", len(g.Sessions))
	}
	s := g.Sessions[0]
	if s.SessionID != parentID {
		t.Errorf("SessionID = %q, want the parent's %q", s.SessionID, parentID)
	}
	if s.Main == nil || len(s.Main.Steps) != 1 || s.Main.Steps[0].UUID != "resp_parent" {
		t.Fatalf("Main = %+v, want the parent's one step", s.Main)
	}
	if len(s.Children) != 1 {
		t.Fatalf("children = %d, want 1", len(s.Children))
	}
	c := s.Children[0]
	if c.Kind != "subagent" || len(c.Steps) != 1 || c.Steps[0].UUID != child.Turns[0].UUID {
		t.Errorf("child = Kind %q steps %+v, want the subagent rollout's step", c.Kind, c.Steps)
	}
	if c.CostUSD <= 0 || s.Main.CostUSD <= 0 {
		t.Fatalf("costs main %v child %v, want both > 0", s.Main.CostUSD, c.CostUSD)
	}
	if got, want := s.CostUSD, s.Main.CostUSD+c.CostUSD; got != want {
		t.Errorf("session CostUSD = %v, want main+child %v", got, want)
	}
	if s.Entrypoint != "codex-tui" {
		t.Errorf("Entrypoint = %q", s.Entrypoint)
	}
}

// TestAssembleSession_CodexNestedSubagentClimbsToRoot: a subagent spawned by
// a subagent nests under the root session, and a Claude-shaped turn whose
// SessionID happens to appear as a link UUID without being a sidechain stays
// its own session.
func TestAssembleSession_CodexNestedSubagentClimbsToRoot(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 10, 31, 13, 0, 0, 0, time.UTC)
	root := mkTurn("r1", "root", "/codex/rollout-root.jsonl", t0)
	kid := mkTurn("k1", "kid", "/codex/rollout-kid.jsonl", t0.Add(time.Second))
	kid.Sidechain = true
	grand := mkTurn("g1", "grand", "/codex/rollout-grand.jsonl", t0.Add(2*time.Second))
	grand.Sidechain = true
	plain := mkTurn("p1", "plain", "/codex/rollout-plain.jsonl", t0.Add(3*time.Second))
	snap := &corpus.Snapshot{
		Turns: []parse.Turn{root, kid, grand, plain},
		Links: []parse.ParentLink{
			{UUID: "kid", ParentUUID: "root"},
			{UUID: "grand", ParentUUID: "kid"},
			{UUID: "plain", ParentUUID: "root"},
		},
	}
	g, err := graphOf(snap, prices, aggregate.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]AgentSession{}
	for _, s := range g.Sessions {
		byID[s.SessionID] = s
	}
	if len(g.Sessions) != 2 || len(byID["root"].Children) != 2 || byID["plain"].Main == nil {
		t.Errorf("sessions = %+v, want root with 2 children and plain on its own", g.Sessions)
	}
}

// TestAssembleSession_CodexGuardianSharesSessionWithUser: a guardian_review
// rollout parses as an ordinary session carrying its user session's id. Both
// rollouts' responses must stay visible — the user rollout as Main (its file
// name carries the session id, even when the guardian started first), the
// guardian as a kind "main" child, never a sub-agent — and the displayed
// nodes must sum to the session totals.
func TestAssembleSession_CodexGuardianSharesSessionWithUser(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	f, err := os.Open(filepath.Join("..", "parse", "codex", "testdata", "guardian.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	guardian, err := codex.ParseFile(f, "/codex/2026/10/29/rollout-2026-10-29T04-56-37-01a18784-eaa5-7850-b7db-776a88eb52ee.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	const sessionID = "01a18784-ea7e-78c0-9157-660fde6ba86d"
	userSrc := "/codex/2026/10/29/rollout-2026-10-29T04-56-00-" + sessionID + ".jsonl"
	t0 := time.Date(2026, 10, 29, 11, 57, 0, 0, time.UTC)
	var turns []parse.Turn
	for i, id := range []string{"resp_user_1", "resp_user_2"} {
		u := mkTurn(id, sessionID, userSrc, t0.Add(time.Duration(i)*time.Minute))
		u.Provider = parse.ProviderCodex
		u.Model = "gpt-6-sol"
		turns = append(turns, u)
	}
	turns = append(turns, guardian.Turns...)

	for run := 0; run < 20; run++ {
		g, err := graphOf(&corpus.Snapshot{Turns: turns}, prices, aggregate.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Sessions) != 1 {
			t.Fatalf("sessions = %d, want 1", len(g.Sessions))
		}
		s := g.Sessions[0]
		if s.Main == nil || len(s.Main.Steps) != 2 || s.Main.Steps[0].UUID != "resp_user_1" {
			t.Fatalf("Main = %+v, want the user rollout's two steps", s.Main)
		}
		if len(s.Children) != 1 {
			t.Fatalf("children = %d, want the guardian rollout", len(s.Children))
		}
		gc := s.Children[0]
		if gc.Kind != "main" || len(gc.Steps) != 1 || gc.Steps[0].UUID != guardian.Turns[0].UUID {
			t.Fatalf("guardian = Kind %q steps %+v, want an ordinary node with its one step", gc.Kind, gc.Steps)
		}
		seen := map[string]bool{}
		for _, n := range append([]AgentNode{*s.Main}, s.Children...) {
			for _, st := range n.Steps {
				seen[st.UUID] = true
			}
		}
		for _, tn := range turns {
			if !seen[tn.UUID] {
				t.Errorf("response %q missing from the displayed nodes", tn.UUID)
			}
		}
		if s.Main.CostUSD <= 0 || gc.Tokens.InputTokens <= 0 {
			t.Fatalf("main cost %v guardian input tokens %d, want both > 0", s.Main.CostUSD, gc.Tokens.InputTokens)
		}
		if got, want := s.CostUSD, s.Main.CostUSD+gc.CostUSD; got != want {
			t.Errorf("session CostUSD = %v, want main+guardian %v", got, want)
		}
		var wantIn int64
		for _, tn := range turns {
			wantIn += int64(tn.Usage.InputTokens)
		}
		if got := int64(s.Main.Tokens.InputTokens + gc.Tokens.InputTokens); got != wantIn {
			t.Errorf("displayed input tokens = %d, want every turn's %d", got, wantIn)
		}
		if !s.StartedAt.Equal(gc.StartedAt) {
			t.Errorf("session StartedAt = %v, want the guardian's earlier start %v", s.StartedAt, gc.StartedAt)
		}
	}
}
