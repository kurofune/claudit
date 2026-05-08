package aggregate

import (
	"testing"
	"time"

	"github.com/nategross/claudit/internal/parse"
	"github.com/nategross/claudit/internal/pricing"
)

func TestNormalizePromptKey_CollapsesWhitespaceAndLowercases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Hello, World!", "hello, world!"},
		{"   leading   and   trailing   ", "leading and trailing"},
		{"line1\nline2\tindented", "line1 line2 indented"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizePromptKey(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizePromptKey_TruncatesByRune(t *testing.T) {
	long := "α"
	for i := 0; i < 200; i++ {
		long += "β"
	}
	got := normalizePromptKey(long)
	// Each rune is 2 bytes; we want exactly 120 runes.
	if r := []rune(got); len(r) != promptKeyMaxRunes {
		t.Errorf("rune count: %d, want %d", len(r), promptKeyMaxRunes)
	}
}

// chainTurn builds an assistant turn with parent linkage for the chain
// walk tests.
func chainTurn(uuid, parent, sid string, ts time.Time) parse.Turn {
	return parse.Turn{
		UUID:       uuid,
		ParentUUID: parent,
		SessionID:  sid,
		Timestamp:  ts,
		Model:      "claude-opus-4-7",
		Usage:      parse.Usage{InputTokens: 1000},
	}
}

func chainUser(uuid, parent, sid, text string, ts time.Time) parse.UserMessage {
	return parse.UserMessage{
		UUID:       uuid,
		ParentUUID: parent,
		SessionID:  sid,
		Text:       text,
		Timestamp:  ts,
	}
}

func TestPromptIndex_DirectAttribution(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	users := []parse.UserMessage{
		chainUser("u1", "", "s", "do thing A", t0),
	}
	turns := []parse.Turn{
		chainTurn("a1", "u1", "s", t0.Add(time.Second)),
		chainTurn("a2", "a1", "s", t0.Add(2*time.Second)),
		chainTurn("a3", "a2", "s", t0.Add(3*time.Second)),
	}
	idx := BuildPromptIndex(turns, users)
	for _, tr := range turns {
		got := idx.Lookup(tr.UUID)
		if got.Key != "do thing a" {
			t.Errorf("turn %s key = %q, want %q", tr.UUID, got.Key, "do thing a")
		}
		if got.UserUUID != "u1" {
			t.Errorf("turn %s user uuid = %q", tr.UUID, got.UserUUID)
		}
	}
}

func TestPromptIndex_ClustersTriviallyDifferentPrompts(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	// Two prompts that differ only in case + trailing whitespace.
	users := []parse.UserMessage{
		chainUser("u1", "", "s", "Add a Test for foo.go", t0),
		chainUser("u2", "", "s", "add a   test for foo.go   ", t0.Add(time.Hour)),
	}
	turns := []parse.Turn{
		chainTurn("a1", "u1", "s", t0),
		chainTurn("a2", "u2", "s", t0.Add(time.Hour)),
	}
	idx := BuildPromptIndex(turns, users)
	if idx.Lookup("a1").Key != idx.Lookup("a2").Key {
		t.Errorf("expected same key, got %q vs %q",
			idx.Lookup("a1").Key, idx.Lookup("a2").Key)
	}
}

func TestPromptIndex_OrphanFallsBackToNoPrompt(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	// a1's parent is "u-missing" — not in either list, so chain dies.
	turns := []parse.Turn{
		chainTurn("a1", "u-missing", "s", t0),
		chainTurn("a2", "a1", "s", t0.Add(time.Second)),
	}
	idx := BuildPromptIndex(turns, nil)
	if got := idx.Lookup("a1").Key; got != noPromptKey {
		t.Errorf("a1 = %q, want %q", got, noPromptKey)
	}
	if got := idx.Lookup("a2").Key; got != noPromptKey {
		t.Errorf("a2 = %q, want %q", got, noPromptKey)
	}
}

func TestAggregator_ByPrompt_RanksByCost(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	users := []parse.UserMessage{
		chainUser("u1", "", "s1", "cheap prompt", t0),
		chainUser("u2", "", "s1", "expensive prompt", t0.Add(time.Hour)),
		// u3 normalizes to the same key as u2.
		chainUser("u3", "", "s2", "EXPENSIVE PROMPT", t0.Add(2*time.Hour)),
	}
	turns := []parse.Turn{
		// 1 turn from cheap prompt.
		chainTurn("a-cheap-1", "u1", "s1", t0),
		// 2 turns from expensive prompt (u2 invocation).
		chainTurn("a-exp1-1", "u2", "s1", t0.Add(time.Hour)),
		chainTurn("a-exp1-2", "a-exp1-1", "s1", t0.Add(time.Hour)),
		// 1 turn from second invocation in a different session.
		chainTurn("a-exp2-1", "u3", "s2", t0.Add(2*time.Hour)),
	}
	idx := BuildPromptIndex(turns, users)
	agg := New(prices).WithPromptIndex(idx)
	for _, tr := range turns {
		agg.Add(tr)
	}

	rows := agg.ByPrompt()
	if len(rows) != 2 {
		t.Fatalf("want 2 prompt rows (clustered), got %d: %+v", len(rows), rows)
	}
	// Expensive should rank first.
	if rows[0].Key != "expensive prompt" {
		t.Errorf("rank[0] key = %q", rows[0].Key)
	}
	if rows[0].Invocations != 2 {
		t.Errorf("expensive invocations = %d, want 2", rows[0].Invocations)
	}
	if rows[0].Sessions != 2 {
		t.Errorf("expensive sessions = %d, want 2", rows[0].Sessions)
	}
	if rows[0].TurnCount != 3 {
		t.Errorf("expensive turn count = %d, want 3", rows[0].TurnCount)
	}
	if rows[1].Key != "cheap prompt" {
		t.Errorf("rank[1] key = %q", rows[1].Key)
	}
}

func TestAggregator_ByPrompt_OrphanBucket(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	turns := []parse.Turn{
		chainTurn("a1", "u-gone", "s1", t0),
	}
	idx := BuildPromptIndex(turns, nil)
	agg := New(prices).WithPromptIndex(idx)
	for _, tr := range turns {
		agg.Add(tr)
	}
	rows := agg.ByPrompt()
	if len(rows) != 1 || rows[0].Key != noPromptKey {
		t.Errorf("orphan should bucket as %q, got %+v", noPromptKey, rows)
	}
	if rows[0].Invocations != 0 {
		t.Errorf("orphan invocations should be 0 (no user message), got %d", rows[0].Invocations)
	}
}

func TestAggregator_ByPrompt_WithoutIndex(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	agg := New(prices) // no WithPromptIndex
	agg.Add(chainTurn("a1", "u1", "s1", t0))
	if got := agg.ByPrompt(); len(got) != 0 {
		t.Errorf("expected empty without index, got %d rows", len(got))
	}
}
