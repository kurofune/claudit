package aggregate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/parse/codex"
)

func TestDistinctToolInvocations_DedupsByNameAndDetail(t *testing.T) {
	// Same-tool / different-detail must stay distinct; same-tool /
	// same-detail collapses; and the special tools (Agent, Skill,
	// SlashCommand) use their dedicated fields, not Detail.
	uses := []parse.ToolUse{
		{Name: "Bash", Detail: "git status"},
		{Name: "Read", Detail: ".go"},
		{Name: "Bash", Detail: "git status"}, // duplicate, drops
		{Name: "Bash", Detail: "go test"},    // same tool, new detail — keep
		{Name: "Read", Detail: ".go"},        // duplicate, drops
		{Name: "Agent", SubagentType: "Explore"},
		{Name: "Skill", SkillName: "handoff"},
		{Name: "SlashCommand", SlashCommand: "/review"},
		{Name: "Edit"},
	}
	got := DistinctToolInvocations(uses, nil, time.Time{})
	// Count reflects the collapse: the two duplicated calls (git status, .go)
	// report Count:2; the rest fired once.
	want := []ToolInvocation{
		{Name: "Bash", Kind: "exec", Detail: "git status", Count: 2},
		{Name: "Read", Kind: "read", Detail: ".go", Count: 2},
		{Name: "Bash", Kind: "exec", Detail: "go test", Count: 1},
		{Name: "Agent", Kind: "agent", Detail: "Explore", Count: 1},
		{Name: "Skill", Kind: "skill", Detail: "handoff", Count: 1},
		{Name: "SlashCommand", Kind: "command", Detail: "/review", Count: 1},
		{Name: "Edit", Kind: "edit", Detail: "", Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tools[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDistinctToolInvocations_JoinsResults(t *testing.T) {
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Bash", Input: "go test"},
		{ID: "t2", Name: "Read", Detail: ".go"},
		{ID: "t3", Name: "Edit"}, // no matching result — status stays empty
	}
	results := map[string]parse.ToolResult{
		"t1": {ToolUseID: "t1", IsError: true, Content: "FAIL: boom"},
		"t2": {ToolUseID: "t2", IsError: false, Content: "package main"},
	}
	got := DistinctToolInvocations(uses, results, time.Time{})
	if len(got) != 3 {
		t.Fatalf("got %d invocations, want 3 (%+v)", len(got), got)
	}
	if got[0].Status != "error" || got[0].Output != "FAIL: boom" {
		t.Errorf("t1 = %+v, want error/FAIL: boom", got[0])
	}
	if got[1].Status != "ok" || got[1].Output != "package main" {
		t.Errorf("t2 = %+v, want ok/package main", got[1])
	}
	if got[2].Status != "" || got[2].Output != "" {
		t.Errorf("t3 = %+v, want empty status/output (no result)", got[2])
	}
}

// The tool_use id rides on each invocation so the drawer can load the full,
// untruncated input/output back from disk on demand. Deduped invocations keep
// the FIRST occurrence's id (a representative call for the collapsed group).
func TestDistinctToolInvocations_CarriesToolUseID(t *testing.T) {
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Bash", Input: "go test"},
		{ID: "t2", Name: "Bash", Input: "go test"}, // dup of t1 → collapses
		{ID: "t3", Name: "Read", Detail: ".go"},
	}
	got := DistinctToolInvocations(uses, nil, time.Time{})
	if len(got) != 2 {
		t.Fatalf("got %d invocations, want 2 (dedup)", len(got))
	}
	if got[0].ID != "t1" {
		t.Errorf("collapsed Bash ID = %q, want t1 (first occurrence)", got[0].ID)
	}
	if got[1].ID != "t3" {
		t.Errorf("Read ID = %q, want t3", got[1].ID)
	}
}

// The tool_use side only stamps the whole assistant turn, so every tool in a
// turn shares that turn timestamp as its start; the matched tool_result's
// timestamp is the tool's end. Together they give per-tool wall-clock the
// Timeline renders as a sub-span.
func TestDistinctToolInvocations_CapturesPerToolTiming(t *testing.T) {
	turnTS := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Bash", Input: "go build"},
		{ID: "t2", Name: "Read", Detail: ".go"},
	}
	results := map[string]parse.ToolResult{
		"t1": {ToolUseID: "t1", Timestamp: turnTS.Add(8 * time.Second)},
		"t2": {ToolUseID: "t2", Timestamp: turnTS.Add(8200 * time.Millisecond)},
	}
	got := DistinctToolInvocations(uses, results, turnTS)
	if len(got) != 2 {
		t.Fatalf("got %d invocations, want 2", len(got))
	}
	if got[0].StartedAt == nil || !got[0].StartedAt.Equal(turnTS) {
		t.Errorf("t1 StartedAt = %v, want %v", got[0].StartedAt, turnTS)
	}
	if got[0].EndedAt == nil || !got[0].EndedAt.Equal(turnTS.Add(8*time.Second)) {
		t.Errorf("t1 EndedAt = %v, want %v", got[0].EndedAt, turnTS.Add(8*time.Second))
	}
	if got[1].EndedAt == nil || !got[1].EndedAt.Equal(turnTS.Add(8200*time.Millisecond)) {
		t.Errorf("t2 EndedAt = %v, want %v", got[1].EndedAt, turnTS.Add(8200*time.Millisecond))
	}
}

// Without a joinable end (no matching tool_result, or one whose line had no
// parseable timestamp), EndedAt must stay nil — the frontend reads nil as "no
// tool timing" and falls back to a turn-level segment instead of drawing a
// bogus zero-width or year-1 span. StartedAt still rides along (we know when the
// turn emitted the call even if we never saw its result).
func TestDistinctToolInvocations_NilEndWhenNoJoinableResult(t *testing.T) {
	turnTS := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Bash", Input: "a"},    // no result in map
		{ID: "t2", Name: "Read", Detail: ".go"}, // result present but zero ts
		{ID: "", Name: "Edit"},                  // no id → never joins
	}
	results := map[string]parse.ToolResult{
		"t2": {ToolUseID: "t2", IsError: false, Content: "x"}, // Timestamp zero
	}
	got := DistinctToolInvocations(uses, results, turnTS)
	if len(got) != 3 {
		t.Fatalf("got %d invocations, want 3", len(got))
	}
	for i, inv := range got {
		if inv.EndedAt != nil {
			t.Errorf("got[%d] (%s) EndedAt = %v, want nil", i, inv.Name, inv.EndedAt)
		}
		if inv.StartedAt == nil || !inv.StartedAt.Equal(turnTS) {
			t.Errorf("got[%d] (%s) StartedAt = %v, want %v", i, inv.Name, inv.StartedAt, turnTS)
		}
	}
}

// Dedup collapses repeated calls to one row, which hides how many times a tool
// actually ran. Count records the collapsed cardinality so the UI can say "ran
// Read 5×": every row reports Count≥1, and a row standing for N identical calls
// reports Count:N.
func TestDistinctToolInvocations_CountsCollapsedCalls(t *testing.T) {
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Read", Detail: ".go"},
		{ID: "t2", Name: "Read", Detail: ".go"}, // dup of t1
		{ID: "t3", Name: "Read", Detail: ".go"}, // dup of t1
		{ID: "t4", Name: "Bash", Input: "go test"},
	}
	got := DistinctToolInvocations(uses, nil, time.Time{})
	if len(got) != 2 {
		t.Fatalf("got %d invocations, want 2 (dedup)", len(got))
	}
	if got[0].Count != 3 {
		t.Errorf("collapsed Read Count = %d, want 3", got[0].Count)
	}
	if got[1].Count != 1 {
		t.Errorf("singleton Bash Count = %d, want 1", got[1].Count)
	}
}

// The full output size measured at parse time (OutputBytes/OutputLines/Rows)
// must ride through the join onto the invocation, so downstream views can rank
// tools by how much they returned without re-reading the transcript.
func TestDistinctToolInvocations_JoinsOutputSize(t *testing.T) {
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Bash", Input: "ls"},
		{ID: "t2", Name: "Edit", Detail: ".go"},
	}
	results := map[string]parse.ToolResult{
		"t1": {ToolUseID: "t1", Content: "a\nb", OutputBytes: 4096, OutputLines: 120},
		"t2": {ToolUseID: "t2", Content: "ok", Rows: 7},
	}
	got := DistinctToolInvocations(uses, results, time.Time{})
	if len(got) != 2 {
		t.Fatalf("got %d invocations, want 2", len(got))
	}
	if got[0].OutputBytes != 4096 || got[0].OutputLines != 120 {
		t.Errorf("t1 size = %d bytes / %d lines, want 4096/120", got[0].OutputBytes, got[0].OutputLines)
	}
	if got[1].Rows != 7 {
		t.Errorf("t2 Rows = %d, want 7", got[1].Rows)
	}
}

func TestDistinctToolInvocations_PopulatesKind(t *testing.T) {
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Agent", SubagentType: "review-triage"},
		{ID: "t2", Name: "Bash", Input: "go test"},
		{ID: "t3", Name: "mcp__github__create_issue"},
	}
	got := DistinctToolInvocations(uses, nil, time.Time{})
	if len(got) != 3 {
		t.Fatalf("got %d invocations, want 3", len(got))
	}
	want := []string{"agent", "exec", "mcp"}
	for i, w := range want {
		if got[i].Kind != w {
			t.Errorf("invocation[%d] (%s) Kind = %q, want %q", i, got[i].Name, got[i].Kind, w)
		}
	}

	// A Codex rollout's shell calls (exec and exec_command) are exec, like
	// Bash; its non-shell list_agents stays other.
	f, err := os.Open(filepath.Join("..", "parse", "codex", "testdata", "tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := codex.ParseFile(f, "rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, inv := range DistinctToolInvocations(res.Turns[0].ToolUses, nil, time.Time{}) {
		kinds[inv.ID] = inv.Kind
	}
	for id, w := range map[string]string{
		"call_686ff4004f1041329f3b312b349d352c": "exec",
		"call_sAX04vI4aehE5s7dZh9nbaDf":         "exec",
		"call_EzybDGOhTxzwKLutaKTDUNmf":         "other",
	} {
		if kinds[id] != w {
			t.Errorf("codex invocation %s Kind = %q, want %q", id, kinds[id], w)
		}
	}
}

// Path tools carry their file path as Input, which is part of the per-turn
// dedup key: two Reads of different .go files share Detail ".go" but stay two
// rows of Count 1, and the Tools rollups still count both calls.
func TestDistinctToolInvocations_PathToolsKeepDistinctFiles(t *testing.T) {
	line := `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","sessionId":"s","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
		`{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"/x/a.go"}},` +
		`{"type":"tool_use","id":"r2","name":"Read","input":{"file_path":"/x/b.go"}}` +
		`]}}`
	turn, _, _ := parse.ParseLine([]byte(line), "/p/s.jsonl")
	got := DistinctToolInvocations(turn.ToolUses, nil, time.Time{})
	if len(got) != 2 {
		t.Fatalf("rows = %+v, want 2 Read rows", got)
	}
	for i, want := range []string{"/x/a.go", "/x/b.go"} {
		if got[i].Name != "Read" || got[i].Detail != ".go" || got[i].Input != want || got[i].Count != 1 {
			t.Errorf("row[%d] = %+v, want Read .go %s count 1", i, got[i], want)
		}
	}

	a := New(ctxTable())
	a.Add(turn)
	byTool, byDetail := a.ToolCallStats()
	if c := byTool["Read"].Calls; c != 2 {
		t.Errorf("Tools payload Read calls = %d, want 2", c)
	}
	if c := byDetail["Read"][".go"].Calls; c != 2 {
		t.Errorf("Tools payload Read·.go calls = %d, want 2", c)
	}
	for _, b := range a.ByTool() {
		if b.Name == "Read" && b.Count != 2 {
			t.Errorf("ByTool Read count = %d, want 2", b.Count)
		}
	}
}
