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

func TestBuildSessionTimelines_RanksSessionsByCostAndCaps(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Three sessions. s_cheap < s_mid < s_expensive by token volume.
	mkSession := func(sid string, inputMtok int) (parse.UserMessage, parse.Turn) {
		u := chainUser("u-"+sid, "", sid, "prompt for "+sid, t0)
		t := chainTurn("a-"+sid, "u-"+sid, sid, t0.Add(time.Second))
		t.Usage = parse.Usage{InputTokens: inputMtok * 1_000_000}
		return u, t
	}
	uCh, tCh := mkSession("s_cheap", 1)
	uMid, tMid := mkSession("s_mid", 10)
	uExp, tExp := mkSession("s_expensive", 100)

	out, err := BuildSessionTimelines(
		context.Background(),
		[]parse.Turn{tCh, tMid, tExp},
		[]parse.UserMessage{uCh, uMid, uExp},
		nil, prices, Filter{},
		SessionTimelinesOptions{TopN: 2},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("TopN=2 should cap to 2, got %d", len(out))
	}
	if out[0].SessionID != "s_expensive" || out[1].SessionID != "s_mid" {
		t.Errorf("sessions not ranked by cost desc: got %v", []string{out[0].SessionID, out[1].SessionID})
	}
	if out[0].CostUSD <= out[1].CostUSD {
		t.Errorf("expected out[0].cost > out[1].cost; got %f vs %f", out[0].CostUSD, out[1].CostUSD)
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
		{Name: "Bash", Detail: "git status"},
		{Name: "Read", Detail: ".go"},
		{Name: "Bash", Detail: "go test"},
		{Name: "Agent", Detail: "Explore"},
		{Name: "Skill", Detail: "handoff"},
		{Name: "SlashCommand", Detail: "/review"},
		{Name: "Edit", Detail: ""},
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
