package aggregate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func TestBuildSessionTimelines_GroupsPromptsAndOrdersChronologically(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	// One session, two prompts. Prompt P1 fires three turns; P2 fires one.
	// The turns are interleaved in input order to confirm we group by
	// prompt (via chain walk), not just by appearance.
	users := []parse.UserMessage{
		chainUser("p1", "", "s1", "first prompt", t0),
		chainUser("p2", "", "s1", "second prompt", t0.Add(10*time.Minute)),
	}
	turns := []parse.Turn{
		chainTurn("a1", "p1", "s1", t0.Add(1*time.Minute)),
		chainTurn("a2", "a1", "s1", t0.Add(2*time.Minute)),
		chainTurn("b1", "p2", "s1", t0.Add(11*time.Minute)),
		chainTurn("a3", "a2", "s1", t0.Add(3*time.Minute)),
	}
	for i := range turns {
		turns[i].CWD = "/p/x"
	}

	out, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{}, SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 session, got %d", len(out))
	}
	s := out[0]
	if s.SessionID != "s1" || s.CWD != "/p/x" || s.Turns != 4 {
		t.Errorf("session metadata wrong: %+v", s)
	}
	if len(s.Prompts) != 2 {
		t.Fatalf("want 2 prompts, got %d", len(s.Prompts))
	}
	if s.Prompts[0].UUID != "p1" || s.Prompts[0].Text != "first prompt" {
		t.Errorf("prompts[0] wrong: %+v", s.Prompts[0])
	}
	if s.Prompts[1].UUID != "p2" || s.Prompts[1].Text != "second prompt" {
		t.Errorf("prompts[1] wrong: %+v", s.Prompts[1])
	}
	if len(s.Prompts[0].Turns) != 3 {
		t.Errorf("p1 should have 3 turns, got %d", len(s.Prompts[0].Turns))
	}
	if len(s.Prompts[1].Turns) != 1 {
		t.Errorf("p2 should have 1 turn, got %d", len(s.Prompts[1].Turns))
	}
	// Turns within a prompt must be chronological even if input order wasn't.
	tsList := s.Prompts[0].Turns
	for i := 1; i < len(tsList); i++ {
		if tsList[i].Timestamp.Before(tsList[i-1].Timestamp) {
			t.Errorf("p1 turns not in chronological order: %+v", tsList)
		}
	}
}

func TestBuildSessionTimelines_CarriesEntrypointAndToolInput(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	users := []parse.UserMessage{chainUser("p1", "", "s1", "go", t0)}
	turn := chainTurn("a1", "p1", "s1", t0.Add(time.Minute))
	turn.CWD = "/p/x"
	turn.Entrypoint = "sdk-cli"
	turn.ToolUses = []parse.ToolUse{
		{Name: "Bash", Detail: "git status", Input: "git status -s"},
		{Name: "Agent", SubagentType: "Explore", Input: "find all callers of Foo"},
	}

	out, err := BuildSessionTimelines(context.Background(), []parse.Turn{turn}, users, nil, prices, Filter{}, SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 session, got %d", len(out))
	}
	s := out[0]
	if s.Entrypoint != "sdk-cli" {
		t.Errorf("session Entrypoint = %q, want sdk-cli", s.Entrypoint)
	}
	if len(s.Prompts) != 1 || len(s.Prompts[0].Turns) != 1 {
		t.Fatalf("unexpected prompt/turn shape: %+v", s.Prompts)
	}
	tools := s.Prompts[0].Turns[0].Tools
	if len(tools) != 2 {
		t.Fatalf("want 2 tool invocations, got %d (%+v)", len(tools), tools)
	}
	if tools[0].Input != "git status -s" {
		t.Errorf("Bash invocation Input = %q, want full command", tools[0].Input)
	}
	if tools[1].Input != "find all callers of Foo" {
		t.Errorf("Agent invocation Input = %q, want subagent prompt", tools[1].Input)
	}
}

func TestBuildSessionTimelines_RedactsToolInput(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	users := []parse.UserMessage{chainUser("p1", "", "s1", "go", t0)}
	turn := chainTurn("a1", "p1", "s1", t0.Add(time.Minute))
	turn.ToolUses = []parse.ToolUse{
		{Name: "Bash", Detail: "git status", Input: "git status -s"},               // len 13
		{Name: "Agent", SubagentType: "Explore", Input: "find all callers of Foo"}, // len 23
		{Name: "Read", Detail: ".go", Input: ""},                                   // empty input
	}

	out, err := BuildSessionTimelines(context.Background(), []parse.Turn{turn}, users, nil, prices, Filter{},
		SessionTimelinesOptions{Redact: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || len(out[0].Prompts) != 1 || len(out[0].Prompts[0].Turns) != 1 {
		t.Fatalf("unexpected shape: %+v", out)
	}
	tools := out[0].Prompts[0].Turns[0].Tools
	if len(tools) != 3 {
		t.Fatalf("want 3 tool invocations, got %d (%+v)", len(tools), tools)
	}
	// Bash: Input redacted to marker, Detail (coarse bucket key) untouched.
	if tools[0].Input != "[redacted 13 chars]" {
		t.Errorf("Bash Input = %q, want [redacted 13 chars]", tools[0].Input)
	}
	if tools[0].Detail != "git status" {
		t.Errorf("Bash Detail = %q, want git status (Detail must not be redacted)", tools[0].Detail)
	}
	// Agent: subagent prompt redacted, SubagentType bucket untouched.
	if tools[1].Input != "[redacted 23 chars]" {
		t.Errorf("Agent Input = %q, want [redacted 23 chars]", tools[1].Input)
	}
	if tools[1].Detail != "Explore" {
		t.Errorf("Agent Detail = %q, want Explore (Detail must not be redacted)", tools[1].Detail)
	}
	// Empty input stays empty — redacting "" would add noise and leaks nothing.
	if tools[2].Input != "" {
		t.Errorf("Read Input = %q, want empty (empty input stays empty)", tools[2].Input)
	}
}

func TestBuildSessionTimelines_RedactKeepsDistinctSameLengthInputs(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	users := []parse.UserMessage{chainUser("p1", "", "s1", "go", t0)}
	turn := chainTurn("a1", "p1", "s1", t0.Add(time.Minute))
	// Two distinct Bash commands of the SAME byte length (13 each) that also
	// share the SAME coarse Detail bucket. Only the real Input distinguishes
	// them, so dedup MUST run on the real input BEFORE redaction — otherwise
	// they'd collapse into one once both inputs read "[redacted 13 chars]".
	turn.ToolUses = []parse.ToolUse{
		{Name: "Bash", Detail: "git status", Input: "git status -s"}, // len 13
		{Name: "Bash", Detail: "git status", Input: "git statuses "}, // len 13
	}

	out, err := BuildSessionTimelines(context.Background(), []parse.Turn{turn}, users, nil, prices, Filter{},
		SessionTimelinesOptions{Redact: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || len(out[0].Prompts) != 1 || len(out[0].Prompts[0].Turns) != 1 {
		t.Fatalf("unexpected shape: %+v", out)
	}
	tools := out[0].Prompts[0].Turns[0].Tools
	if len(tools) != 2 {
		t.Fatalf("want 2 distinct tool invocations (dedup before redact), got %d (%+v)", len(tools), tools)
	}
	if tools[0].Input != "[redacted 13 chars]" || tools[1].Input != "[redacted 13 chars]" {
		t.Errorf("both inputs should redact to the 13-char marker, got %q and %q", tools[0].Input, tools[1].Input)
	}
}

func TestBuildSessionTimelines_RanksSessionsByRecencyAndCaps(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Three sessions whose last activity (EndedAt = newest turn) differs.
	// s_old ended first, s_new ended last — ordering must be by last
	// activity descending, independent of cost (so we make the OLDEST the
	// most expensive to prove cost no longer drives the rank).
	mkSession := func(sid string, endOffset time.Duration, inputMtok int) (parse.UserMessage, parse.Turn) {
		u := chainUser("u-"+sid, "", sid, "prompt for "+sid, t0)
		tn := chainTurn("a-"+sid, "u-"+sid, sid, t0.Add(endOffset))
		tn.Usage = parse.Usage{InputTokens: inputMtok * 1_000_000}
		return u, tn
	}
	uOld, tOld := mkSession("s_old", 1*time.Hour, 100) // oldest, priciest
	uMid, tMid := mkSession("s_mid", 2*time.Hour, 10)
	uNew, tNew := mkSession("s_new", 3*time.Hour, 1) // newest, cheapest

	out, err := BuildSessionTimelines(
		context.Background(),
		[]parse.Turn{tOld, tMid, tNew},
		[]parse.UserMessage{uOld, uMid, uNew},
		nil, prices, Filter{},
		SessionTimelinesOptions{TopN: 2},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("TopN=2 should cap to 2, got %d", len(out))
	}
	// Newest-ended first, regardless of cost. s_new (cheapest) leads.
	if out[0].SessionID != "s_new" || out[1].SessionID != "s_mid" {
		t.Errorf("sessions not ranked by last activity desc: got %v", []string{out[0].SessionID, out[1].SessionID})
	}
	if !out[0].EndedAt.After(out[1].EndedAt) {
		t.Errorf("expected out[0].EndedAt after out[1].EndedAt; got %v vs %v", out[0].EndedAt, out[1].EndedAt)
	}
}

func TestBuildSessionTimelines_NoCapReturnsAllInWindow(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Five sessions, no TopN cap (0). All five must come back, newest first.
	var turns []parse.Turn
	var users []parse.UserMessage
	for i := 0; i < 5; i++ {
		sid := string(rune('a' + i))
		users = append(users, chainUser("u-"+sid, "", sid, "p", t0))
		users[i].SessionID = sid
		turns = append(turns, chainTurn("a-"+sid, "u-"+sid, sid, t0.Add(time.Duration(i)*time.Hour)))
	}

	out, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{TopN: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("TopN=0 should return all 5 sessions, got %d", len(out))
	}
	// Strictly descending by last activity.
	for i := 1; i < len(out); i++ {
		if out[i-1].EndedAt.Before(out[i].EndedAt) {
			t.Errorf("not sorted newest-first at %d: %v before %v", i, out[i-1].EndedAt, out[i].EndedAt)
		}
	}
}

func TestBuildSessionTimelines_Redacts(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	users := []parse.UserMessage{chainUser("u1", "", "s1", "sensitive content here", t0)}
	turns := []parse.Turn{chainTurn("a1", "u1", "s1", t0.Add(time.Second))}

	out, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{Redact: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || len(out[0].Prompts) != 1 {
		t.Fatalf("unexpected shape: %+v", out)
	}
	got := out[0].Prompts[0].Text
	if !strings.HasPrefix(got, "[redacted") {
		t.Errorf("expected redacted body, got %q", got)
	}
	if !strings.Contains(got, "22") { // len("sensitive content here") = 22
		t.Errorf("redaction should echo raw length 22, got %q", got)
	}
}

func TestBuildSessionTimelines_TruncatesLongPrompts(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 5000)
	users := []parse.UserMessage{chainUser("u1", "", "s1", long, t0)}
	turns := []parse.Turn{chainTurn("a1", "u1", "s1", t0.Add(time.Second))}

	out, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{MaxPromptChars: 2000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := out[0].Prompts[0]
	if !p.Truncated {
		t.Errorf("Truncated flag should be true for 5000-char prompt with 2000 cap")
	}
	if len(p.Text) != 2000 {
		t.Errorf("text len = %d, want 2000", len(p.Text))
	}
}

func TestBuildSessionTimelines_RespectsFilterWindow(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	users := []parse.UserMessage{
		chainUser("u1", "", "s1", "early", t0),
		chainUser("u2", "", "s2", "late", t0.Add(48*time.Hour)),
	}
	turns := []parse.Turn{
		chainTurn("a1", "u1", "s1", t0.Add(time.Second)),
		chainTurn("a2", "u2", "s2", t0.Add(48*time.Hour+time.Second)),
	}
	out, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices,
		Filter{Since: t0.Add(24 * time.Hour)},
		SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0].SessionID != "s2" {
		t.Errorf("filter should drop early session: %+v", out)
	}
}

func TestBuildSessionTimelines_DistinctToolInvocations(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	users := []parse.UserMessage{chainUser("u1", "", "s1", "do work", t0)}
	turn := chainTurn("a1", "u1", "s1", t0.Add(time.Second))
	// Same-tool / different-detail must stay distinct; same-tool /
	// same-detail collapses; and the special tools (Agent, Skill,
	// SlashCommand) use their dedicated fields, not Detail.
	turn.ToolUses = []parse.ToolUse{
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
	out, err := BuildSessionTimelines(context.Background(), []parse.Turn{turn}, users, nil, prices, Filter{},
		SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out[0].Prompts[0].Turns[0].Tools
	want := []ToolInvocation{
		{Name: "Bash", Kind: "exec", Detail: "git status"},
		{Name: "Read", Kind: "read", Detail: ".go"},
		{Name: "Bash", Kind: "exec", Detail: "go test"},
		{Name: "Agent", Kind: "agent", Detail: "Explore"},
		{Name: "Skill", Kind: "skill", Detail: "handoff"},
		{Name: "SlashCommand", Kind: "command", Detail: "/review"},
		{Name: "Edit", Kind: "edit", Detail: ""},
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
	got := DistinctToolInvocations(uses, results, false, time.Time{})
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
	got := DistinctToolInvocations(uses, nil, false, time.Time{})
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
	got := DistinctToolInvocations(uses, results, false, turnTS)
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
	got := DistinctToolInvocations(uses, results, false, turnTS)
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

func TestDistinctToolInvocations_PopulatesKind(t *testing.T) {
	uses := []parse.ToolUse{
		{ID: "t1", Name: "Agent", SubagentType: "review-triage"},
		{ID: "t2", Name: "Bash", Input: "go test"},
		{ID: "t3", Name: "mcp__github__create_issue"},
	}
	got := DistinctToolInvocations(uses, nil, false, time.Time{})
	if len(got) != 3 {
		t.Fatalf("got %d invocations, want 3", len(got))
	}
	want := []string{"agent", "exec", "mcp"}
	for i, w := range want {
		if got[i].Kind != w {
			t.Errorf("invocation[%d] (%s) Kind = %q, want %q", i, got[i].Name, got[i].Kind, w)
		}
	}
}

func TestDistinctToolInvocations_RedactsOutput(t *testing.T) {
	uses := []parse.ToolUse{{ID: "t1", Name: "Bash", Input: "secret cmd"}}
	results := map[string]parse.ToolResult{
		"t1": {ToolUseID: "t1", IsError: false, Content: "sensitive output"},
	}
	got := DistinctToolInvocations(uses, results, true, time.Time{})
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	// Status survives redaction (it's not content); Output is masked.
	if got[0].Status != "ok" {
		t.Errorf("status = %q, want ok", got[0].Status)
	}
	if got[0].Output != redactMarker("sensitive output") {
		t.Errorf("output = %q, want redaction marker", got[0].Output)
	}
}

func TestBuildSessionTimelines_TurnDuration(t *testing.T) {
	// Inter-turn duration measures the wall-clock gap from one turn to the
	// next within the same prompt. The last turn has no successor, so its
	// DurationMs stays zero.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	users := []parse.UserMessage{chainUser("u1", "", "s1", "p", t0)}
	turns := []parse.Turn{
		chainTurn("a1", "u1", "s1", t0.Add(1*time.Second)),
		chainTurn("a2", "a1", "s1", t0.Add(12*time.Second)), // +11s
		chainTurn("a3", "a2", "s1", t0.Add(15*time.Second)), // +3s, then last
	}
	out, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ts := out[0].Prompts[0].Turns
	if ts[0].DurationMs != 11_000 {
		t.Errorf("ts[0].DurationMs = %d, want 11000", ts[0].DurationMs)
	}
	if ts[1].DurationMs != 3_000 {
		t.Errorf("ts[1].DurationMs = %d, want 3000", ts[1].DurationMs)
	}
	if ts[2].DurationMs != 0 {
		t.Errorf("ts[2].DurationMs = %d, want 0 (last turn has no successor)", ts[2].DurationMs)
	}
}

func TestBuildSessionTimelines_KeyMatchesPromptBucket(t *testing.T) {
	// The cross-link feature depends on the timeline's PromptTimeline.Key
	// matching what PromptBucket.Key (and prompt-kind Hotspot.Title) would
	// produce for the same raw text. If these ever diverge, hotspots stop
	// resolving to their session.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	users := []parse.UserMessage{
		chainUser("u1", "", "s1", "Refactor the auth Middleware\n  for IDOR", t0),
	}
	turns := []parse.Turn{chainTurn("a1", "u1", "s1", t0.Add(time.Second))}

	out, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out[0].Prompts[0].Key
	want := normalizePromptKey("Refactor the auth Middleware\n  for IDOR")
	if got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
}

func TestBuildSessionTimelines_KeyIgnoresRedaction(t *testing.T) {
	// Cross-links must work even when the visible prompt body is hidden.
	// Key is derived from the RAW text, not the displayed Text — so a
	// report generated with --redact still resolves hotspot clicks.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	users := []parse.UserMessage{chainUser("u1", "", "s1", "investigate flaky test", t0)}
	turns := []parse.Turn{chainTurn("a1", "u1", "s1", t0.Add(time.Second))}

	withRedact, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{Redact: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	withoutRedact, err := BuildSessionTimelines(context.Background(), turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if withRedact[0].Prompts[0].Key != withoutRedact[0].Prompts[0].Key {
		t.Errorf("Key changed under redaction: %q vs %q",
			withRedact[0].Prompts[0].Key, withoutRedact[0].Prompts[0].Key)
	}
	if withRedact[0].Prompts[0].Key == "" {
		t.Errorf("Key should be set even under redaction")
	}
}

func TestBuildSessionTimelines_CanceledContextReturnsError(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// A non-trivial corpus so cancellation has something to short-circuit.
	users := []parse.UserMessage{
		chainUser("p1", "", "s1", "prompt", t0),
	}
	turns := []parse.Turn{
		chainTurn("a1", "p1", "s1", t0.Add(1*time.Minute)),
		chainTurn("a2", "a1", "s1", t0.Add(2*time.Minute)),
		chainTurn("a3", "a2", "s1", t0.Add(3*time.Minute)),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out, err := BuildSessionTimelines(ctx, turns, users, nil, prices, Filter{},
		SessionTimelinesOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if out != nil {
		t.Errorf("want nil timelines on cancellation, got %d", len(out))
	}
}

func TestBuildSessionTimelines_OrphanTurnFallsIntoNoPromptBucket(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	// Turn with no parent and no matching user message — chain walk
	// terminates without finding a prompt UUID. Should still appear in
	// the timeline under the "" prompt key.
	turn := chainTurn("a1", "", "s1", t0)
	out, err := BuildSessionTimelines(context.Background(), []parse.Turn{turn}, nil, nil, prices, Filter{},
		SessionTimelinesOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || len(out[0].Prompts) != 1 {
		t.Fatalf("unexpected shape: %+v", out)
	}
	if out[0].Prompts[0].UUID != "" {
		t.Errorf("orphan should have empty prompt UUID, got %q", out[0].Prompts[0].UUID)
	}
}
