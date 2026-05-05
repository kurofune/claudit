package parse

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseFile_MainSession(t *testing.T) {
	f, err := os.Open("testdata/main_session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	res, err := ParseFile(f, "testdata/main_session.jsonl")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if res.Malformed != 0 {
		t.Errorf("expected 0 malformed lines, got %d", res.Malformed)
	}
	if len(res.Turns) != 3 {
		t.Fatalf("expected 3 assistant turns, got %d", len(res.Turns))
	}
	t0 := res.Turns[0]
	if t0.Model != "claude-opus-4-7" {
		t.Errorf("model: %q", t0.Model)
	}
	if t0.Sidechain {
		t.Errorf("expected non-sidechain")
	}
	if t0.Usage.InputTokens != 100 || t0.Usage.OutputTokens != 200 {
		t.Errorf("usage tokens wrong: %+v", t0.Usage)
	}
	if t0.Usage.CacheCreate5mTokens != 800 || t0.Usage.CacheCreate1hTokens != 200 {
		t.Errorf("cache tier split wrong: %+v", t0.Usage)
	}
	if t0.Usage.CacheReadTokens != 500 {
		t.Errorf("cache read wrong: %d", t0.Usage.CacheReadTokens)
	}
	if t0.CWD != "/Users/x/Projects/foo" {
		t.Errorf("cwd: %q", t0.CWD)
	}
	if len(t0.ToolUses) != 1 || t0.ToolUses[0].Name != "Bash" {
		t.Errorf("tool uses: %+v", t0.ToolUses)
	}

	t3 := res.Turns[2]
	if len(t3.ToolUses) != 3 {
		t.Fatalf("turn3 tool uses: %+v", t3.ToolUses)
	}
	skill := t3.ToolUses[0]
	if skill.Name != "Skill" || skill.SkillName != "tdd" {
		t.Errorf("skill: %+v", skill)
	}
	slash := t3.ToolUses[1]
	if slash.Name != "SlashCommand" || slash.SlashCommand != "/review" {
		t.Errorf("slash: %+v", slash)
	}
	agent := t3.ToolUses[2]
	if agent.Name != "Agent" || agent.SubagentType != "Explore" {
		t.Errorf("agent: %+v", agent)
	}

	want, _ := time.Parse(time.RFC3339, "2026-04-10T10:00:01Z")
	if !t0.Timestamp.Equal(want) {
		t.Errorf("ts: %v", t0.Timestamp)
	}
}

func TestParseFile_Sidechain(t *testing.T) {
	f, err := os.Open("testdata/sidechain_session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := ParseFile(f, "testdata/sidechain_session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 2 {
		t.Fatalf("turns: %d", len(res.Turns))
	}
	for _, tn := range res.Turns {
		if !tn.Sidechain {
			t.Errorf("expected sidechain")
		}
		if tn.Model != "claude-haiku-4-5-20251001" {
			t.Errorf("model: %q", tn.Model)
		}
	}
}

func TestParseFile_Malformed(t *testing.T) {
	f, err := os.Open("testdata/malformed.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	res, err := ParseFile(f, "testdata/malformed.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if res.Malformed != 2 {
		t.Errorf("expected 2 malformed, got %d", res.Malformed)
	}
	if len(res.Turns) != 2 {
		t.Errorf("expected 2 turns with usage (third turn omits usage and is skipped), got %d", len(res.Turns))
	}
}

func TestSubagentMeta(t *testing.T) {
	dir := t.TempDir()
	subDir := dir + "/abc/subagents"
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonl := subDir + "/agent-a4efdd2.jsonl"
	if err := os.WriteFile(jsonl, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subDir+"/agent-a4efdd2.meta.json",
		[]byte(`{"agentType":"Explore","description":"find references"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := SubagentTypeFor(jsonl); got != "Explore" {
		t.Errorf("type: %q", got)
	}
	m, ok := ReadSubagentMeta(jsonl)
	if !ok || m.AgentType != "Explore" || m.Description != "find references" {
		t.Errorf("meta: %+v ok=%v", m, ok)
	}

	noMeta := dir + "/abc/subagents/agent-zzz.jsonl"
	os.WriteFile(noMeta, []byte("{}\n"), 0o644)
	if SubagentTypeFor(noMeta) != "" {
		t.Errorf("expected empty for missing meta")
	}
	if _, ok := ReadSubagentMeta(noMeta); ok {
		t.Errorf("expected !ok for missing meta")
	}
}

func TestIsSubagentFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/x/y/sess-id/subagents/agent-abc.jsonl", true},
		{"/x/y/sess-id.jsonl", false},
		{"/x/y/foo/bar.jsonl", false},
	}
	for _, c := range cases {
		if got := IsSubagentFile(c.path); got != c.want {
			t.Errorf("IsSubagentFile(%q) = %v want %v", c.path, got, c.want)
		}
	}
}

func TestDecodeProjectDir(t *testing.T) {
	// The encoding is lossy: dots and spaces also become "-", so we can
	// only do a best-effort round-trip. The aggregator prefers the cwd
	// field from each turn for accuracy.
	cases := []struct {
		in, want string
	}{
		{"-Users-nathangross-Projects-claudit", "/Users/nathangross/Projects/claudit"},
	}
	for _, c := range cases {
		if got := DecodeProjectDir(c.in); got != c.want {
			t.Errorf("decode(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestParseFile_StreamingNotInMemory(t *testing.T) {
	// Smoke test that we use a Scanner (won't allocate the whole file).
	// Verify by reading a moderately long synthetic input.
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		b.WriteString(`{"type":"system","content":"x"}` + "\n")
	}
	res, err := ParseFile(strings.NewReader(b.String()), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 0 || res.Malformed != 0 {
		t.Errorf("turns=%d mal=%d", len(res.Turns), res.Malformed)
	}
}
