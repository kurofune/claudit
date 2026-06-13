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
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()

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
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
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
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
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
		[]byte(`{"agentType":"Explore","description":"find references","toolUseId":"toolu_01N5q2"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := SubagentTypeFor(jsonl); got != "Explore" {
		t.Errorf("type: %q", got)
	}
	m, ok := ReadSubagentMeta(jsonl)
	if !ok || m.AgentType != "Explore" || m.Description != "find references" {
		t.Errorf("meta: %+v ok=%v", m, ok)
	}
	if m.ToolUseID != "toolu_01N5q2" {
		t.Errorf("ToolUseID = %q, want %q", m.ToolUseID, "toolu_01N5q2")
	}

	noMeta := dir + "/abc/subagents/agent-zzz.jsonl"
	if err := os.WriteFile(noMeta, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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

func TestParseFile_UserMessages(t *testing.T) {
	f, err := os.Open("testdata/user_messages.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	res, err := ParseFile(f, "testdata/user_messages.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if res.Malformed != 0 {
		t.Errorf("malformed: %d", res.Malformed)
	}
	// Want only u-string and u-array. The meta/tool_result/empty are filtered.
	if len(res.UserMessages) != 2 {
		t.Fatalf("user messages: %d, want 2 (got %+v)", len(res.UserMessages), res.UserMessages)
	}
	got := map[string]string{}
	for _, m := range res.UserMessages {
		got[m.UUID] = m.Text
	}
	if got["u-string"] != "please add a new feature" {
		t.Errorf("string content: %q", got["u-string"])
	}
	if got["u-array"] != "first part\nsecond part" {
		t.Errorf("array text content: %q", got["u-array"])
	}
	if _, ok := got["u-meta"]; ok {
		t.Errorf("meta user message must be filtered out")
	}
	if _, ok := got["u-toolresult"]; ok {
		t.Errorf("tool_result user message must be filtered out")
	}
	if _, ok := got["u-empty"]; ok {
		t.Errorf("empty user message must be filtered out")
	}
	// And one assistant turn from the same file should still come through.
	if len(res.Turns) != 1 || res.Turns[0].UUID != "a-after-toolresult" {
		t.Errorf("turns: %+v", res.Turns)
	}
}

func TestParseLine_KindClassification(t *testing.T) {
	cases := []struct {
		name string
		line string
		want LineKind
	}{
		{"assistant", `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1}}}`, LineAssistant},
		{"user-string", `{"type":"user","uuid":"u","timestamp":"2026-04-10T10:00:00Z","message":{"role":"user","content":"hi"}}`, LineUserMessage},
		{"user-meta-skipped", `{"type":"user","uuid":"u","isMeta":true,"timestamp":"2026-04-10T10:00:00Z","message":{"role":"user","content":"hi"}}`, LineUnknown},
		{"user-tool-result-skipped", `{"type":"user","uuid":"u","timestamp":"2026-04-10T10:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"x"}]}}`, LineUnknown},
		{"system", `{"type":"system","uuid":"s","timestamp":"2026-04-10T10:00:00Z","content":"x"}`, LineUnknown},
		{"malformed", `{not json}`, LineMalformed},
		{"empty", ``, LineUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, got := ParseLine([]byte(c.line), "test")
			if got != c.want {
				t.Errorf("kind = %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseLine_CapturesEntrypoint(t *testing.T) {
	// Headless sessions carry entrypoint:"sdk-cli"; interactive carry "cli".
	// We surface it on the Turn so sessions can be split by origin.
	line := `{"type":"assistant","uuid":"a","entrypoint":"sdk-cli","timestamp":"2026-04-10T10:00:00Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1}}}`
	turn, _, kind := ParseLine([]byte(line), "t")
	if kind != LineAssistant {
		t.Fatalf("kind = %v, want LineAssistant", kind)
	}
	if turn.Entrypoint != "sdk-cli" {
		t.Errorf("Entrypoint = %q, want %q", turn.Entrypoint, "sdk-cli")
	}
}

func TestParseFile_CoalescesMessageBlocks(t *testing.T) {
	// One streamed assistant turn = 3 JSONL lines sharing message.id, each a
	// different content block, all repeating the SAME cumulative usage. They
	// must collapse to ONE Turn: merged thinking/text/tool_uses, usage once.
	lines := []string{
		`{"type":"assistant","uuid":"a1","parentUuid":"u0","timestamp":"2026-04-10T10:00:01Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":5026,"output_tokens":593,"cache_creation_input_tokens":34743},"content":[{"type":"thinking","thinking":"reason here"}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","timestamp":"2026-04-10T10:00:02Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":5026,"output_tokens":593,"cache_creation_input_tokens":34743},"content":[{"type":"text","text":"narration"}]}}`,
		`{"type":"assistant","uuid":"a3","parentUuid":"a2","timestamp":"2026-04-10T10:00:03Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":5026,"output_tokens":593,"cache_creation_input_tokens":34743},"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]}}`,
	}
	res, err := ParseFile(strings.NewReader(strings.Join(lines, "\n")+"\n"), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 1 {
		t.Fatalf("Turns = %d, want 1 (the 3 blocks of one message coalesced)", len(res.Turns))
	}
	turn := res.Turns[0]
	if turn.Thinking != "reason here" {
		t.Errorf("Thinking = %q, want merged thinking block", turn.Thinking)
	}
	if turn.Text != "narration" {
		t.Errorf("Text = %q, want merged text block", turn.Text)
	}
	if len(turn.ToolUses) != 1 || turn.ToolUses[0].Name != "Bash" {
		t.Errorf("ToolUses = %+v, want the single Bash call", turn.ToolUses)
	}
	if turn.Usage.InputTokens != 5026 || turn.Usage.OutputTokens != 593 || turn.Usage.CacheCreate5mTokens != 34743 {
		t.Errorf("usage = %+v, want counted ONCE (5026/593/34743)", turn.Usage)
	}
}

func TestParseFile_CoalescedTurnKeepsFirstLineIdentityAndAllLinks(t *testing.T) {
	// The coalesced Turn must keep the FIRST line's identity/timing (UUID,
	// ParentUUID, Timestamp) so prompt attribution still anchors correctly.
	// Every per-line parentUuid edge must survive in ParentLinks so the
	// chain walk can still climb intra-message lines to the originating prompt.
	lines := []string{
		`{"type":"assistant","uuid":"a1","parentUuid":"u0","timestamp":"2026-04-10T10:00:01Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"thinking","thinking":"x"}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","timestamp":"2026-04-10T10:00:02Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"assistant","uuid":"a3","parentUuid":"a2","timestamp":"2026-04-10T10:00:03Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/x"}}]}}`,
	}
	res, err := ParseFile(strings.NewReader(strings.Join(lines, "\n")+"\n"), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 1 {
		t.Fatalf("Turns = %d, want 1", len(res.Turns))
	}
	turn := res.Turns[0]
	if turn.UUID != "a1" {
		t.Errorf("UUID = %q, want first line's a1", turn.UUID)
	}
	if turn.ParentUUID != "u0" {
		t.Errorf("ParentUUID = %q, want first line's u0", turn.ParentUUID)
	}
	wantTS, _ := time.Parse(time.RFC3339, "2026-04-10T10:00:01Z")
	if !turn.Timestamp.Equal(wantTS) {
		t.Errorf("Timestamp = %v, want first line's %v", turn.Timestamp, wantTS)
	}
	// All three per-line edges must be present for the chain walk.
	edges := map[string]string{}
	for _, l := range res.ParentLinks {
		edges[l.UUID] = l.ParentUUID
	}
	for child, parent := range map[string]string{"a1": "u0", "a2": "a1", "a3": "a2"} {
		if edges[child] != parent {
			t.Errorf("ParentLinks[%q] = %q, want %q", child, edges[child], parent)
		}
	}
}

func TestParseFile_NoMessageIDStaysOneTurnPerLine(t *testing.T) {
	// Older transcripts (and any line that lost its message.id) carry no
	// message.id. The fix must be a no-op there: each assistant line stays its
	// own Turn, with usage intact, so legacy single-line-per-message accounting
	// is unchanged.
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-04-10T10:00:01Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":10,"output_tokens":20},"content":[{"type":"text","text":"one"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-04-10T10:00:02Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":30,"output_tokens":40},"content":[{"type":"text","text":"two"}]}}`,
	}
	res, err := ParseFile(strings.NewReader(strings.Join(lines, "\n")+"\n"), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 2 {
		t.Fatalf("Turns = %d, want 2 (no coalescing without message.id)", len(res.Turns))
	}
	if res.Turns[0].Text != "one" || res.Turns[1].Text != "two" {
		t.Errorf("turns merged unexpectedly: %q / %q", res.Turns[0].Text, res.Turns[1].Text)
	}
	if res.Turns[0].Usage.InputTokens != 10 || res.Turns[1].Usage.InputTokens != 30 {
		t.Errorf("usage altered: %+v / %+v", res.Turns[0].Usage, res.Turns[1].Usage)
	}
}

func TestCoalescer_StreamingMergeAndFlush(t *testing.T) {
	// The streaming path (claudit watch) feeds one parsed Turn at a time. The
	// Coalescer holds an in-progress message and only emits it once a new
	// message.id (or an explicit Flush) signals the message is complete.
	mk := func(line string) Turn {
		tn, _, kind := ParseLine([]byte(line), "p")
		if kind != LineAssistant {
			t.Fatalf("setup: line not assistant: %s", line)
		}
		return tn
	}
	l1 := mk(`{"type":"assistant","uuid":"a1","timestamp":"2026-04-10T10:00:01Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":5,"output_tokens":6},"content":[{"type":"thinking","thinking":"reason"}]}}`)
	l2 := mk(`{"type":"assistant","uuid":"a2","timestamp":"2026-04-10T10:00:02Z","message":{"id":"msg_1","model":"m","role":"assistant","usage":{"input_tokens":5,"output_tokens":6},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`)
	l3 := mk(`{"type":"assistant","uuid":"a3","timestamp":"2026-04-10T10:00:05Z","message":{"id":"msg_2","model":"m","role":"assistant","usage":{"input_tokens":7,"output_tokens":8},"content":[{"type":"text","text":"answer"}]}}`)

	var c Coalescer
	if done, ok := c.Push(l1); ok {
		t.Fatalf("first line of a message must not flush yet, got %+v", done)
	}
	if done, ok := c.Push(l2); ok {
		t.Fatalf("same-id continuation must not flush, got %+v", done)
	}
	done, ok := c.Push(l3)
	if !ok {
		t.Fatalf("a new message.id must flush the prior message")
	}
	if done.MessageID != "msg_1" {
		t.Errorf("flushed MessageID = %q, want msg_1", done.MessageID)
	}
	if done.Thinking != "reason" || len(done.ToolUses) != 1 || done.ToolUses[0].Name != "Bash" {
		t.Errorf("flushed turn not merged: thinking=%q tools=%+v", done.Thinking, done.ToolUses)
	}
	if done.Usage.InputTokens != 5 || done.Usage.OutputTokens != 6 {
		t.Errorf("flushed usage = %+v, want counted once (5/6)", done.Usage)
	}
	// Flush emits the last in-progress message (msg_2).
	last, ok := c.Flush()
	if !ok || last.MessageID != "msg_2" || last.Text != "answer" {
		t.Errorf("Flush() = %+v ok=%v, want msg_2/answer", last, ok)
	}
	// A second Flush has nothing left.
	if _, ok := c.Flush(); ok {
		t.Errorf("second Flush should report nothing pending")
	}
}

func TestCoalescer_EmptyMessageIDStaysStandalone(t *testing.T) {
	// Legacy lines carry no message.id. In the stream, each must stand alone:
	// pushing a second empty-id turn flushes the first unchanged.
	mk := func(line string) Turn {
		tn, _, _ := ParseLine([]byte(line), "p")
		return tn
	}
	l1 := mk(`{"type":"assistant","uuid":"a1","timestamp":"2026-04-10T10:00:01Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"one"}]}}`)
	l2 := mk(`{"type":"assistant","uuid":"a2","timestamp":"2026-04-10T10:00:02Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":2,"output_tokens":2},"content":[{"type":"text","text":"two"}]}}`)

	var c Coalescer
	if _, ok := c.Push(l1); ok {
		t.Fatalf("first empty-id push should not flush")
	}
	done, ok := c.Push(l2)
	if !ok {
		t.Fatalf("second empty-id push should flush the first")
	}
	if done.Text != "one" || done.Usage.InputTokens != 1 {
		t.Errorf("flushed standalone turn altered: text=%q usage=%+v", done.Text, done.Usage)
	}
	last, ok := c.Flush()
	if !ok || last.Text != "two" {
		t.Errorf("Flush() = %+v ok=%v, want standalone 'two'", last, ok)
	}
}

func TestParseLine_CapturesMessageID(t *testing.T) {
	// Claude Code writes one JSONL line per content block, each repeating the
	// same message.id. We must surface that id so consecutive blocks of one
	// assistant turn can be coalesced into a single logical turn.
	line := `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","message":{"id":"msg_abc","model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1}}}`
	turn, _, kind := ParseLine([]byte(line), "t")
	if kind != LineAssistant {
		t.Fatalf("kind = %v, want LineAssistant", kind)
	}
	if turn.MessageID != "msg_abc" {
		t.Errorf("MessageID = %q, want %q", turn.MessageID, "msg_abc")
	}
}

func TestExtractToolUses_CapturesInput(t *testing.T) {
	// We retain a bounded snippet of high-value tool inputs — the full
	// Bash command and the prompt handed to a subagent — so the Sessions
	// view can show what the agent actually did, not just tool names.
	line := `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
		`{"type":"tool_use","name":"Bash","input":{"command":"git status -s"}},` +
		`{"type":"tool_use","name":"Agent","input":{"subagent_type":"Explore","prompt":"find all callers of Foo"}},` +
		`{"type":"tool_use","name":"Read","input":{"file_path":"/x/y.go"}}` +
		`]}}`
	turn, _, _ := ParseLine([]byte(line), "t")
	if len(turn.ToolUses) != 3 {
		t.Fatalf("ToolUses = %d, want 3 (%+v)", len(turn.ToolUses), turn.ToolUses)
	}
	if got := turn.ToolUses[0].Input; got != "git status -s" {
		t.Errorf("Bash Input = %q, want full command", got)
	}
	if got := turn.ToolUses[1].Input; got != "find all callers of Foo" {
		t.Errorf("Agent Input = %q, want subagent prompt", got)
	}
	// Read has no high-value input snippet — Detail already covers it.
	if got := turn.ToolUses[2].Input; got != "" {
		t.Errorf("Read Input = %q, want empty", got)
	}
}

func TestExtractToolUses_CapturesID(t *testing.T) {
	// The tool_use `id` ties an assistant tool call to its tool_result in
	// the following user turn — load-bearing for the Agents view's result
	// join, so we must retain it on the parsed ToolUse.
	line := `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
		`{"type":"tool_use","id":"toolu_abc","name":"Bash","input":{"command":"ls"}}` +
		`]}}`
	turn, _, _ := ParseLine([]byte(line), "t")
	if len(turn.ToolUses) != 1 {
		t.Fatalf("ToolUses = %d, want 1", len(turn.ToolUses))
	}
	if got := turn.ToolUses[0].ID; got != "toolu_abc" {
		t.Errorf("ToolUse.ID = %q, want toolu_abc", got)
	}
}

func TestExtractAssistantText_CapturesThinkingAndText(t *testing.T) {
	// Assistant messages carry distinct `thinking` (extended-thinking
	// reasoning) and `text` (narration) blocks. We surface them as two
	// separate fields so a tool-less turn is auditable.
	line := `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
		`{"type":"thinking","thinking":"Let me reason","signature":"abc"},` +
		`{"type":"text","text":"Here is the answer"}` +
		`]}}`
	turn, _, _ := ParseLine([]byte(line), "t")
	if got := turn.Thinking; got != "Let me reason" {
		t.Errorf("Thinking = %q, want %q", got, "Let me reason")
	}
	if got := turn.Text; got != "Here is the answer" {
		t.Errorf("Text = %q, want %q", got, "Here is the answer")
	}
}

func TestExtractAssistantText_OnlyThinking(t *testing.T) {
	// A pure-reasoning turn — only a thinking block, no text, no tool_use —
	// must now carry its reasoning. This is the core gap the feature closes.
	// It is still a chargeable assistant turn.
	line := `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
		`{"type":"thinking","thinking":"Just thinking","signature":"abc"}` +
		`]}}`
	turn, _, kind := ParseLine([]byte(line), "t")
	if kind != LineAssistant {
		t.Fatalf("kind = %v, want LineAssistant", kind)
	}
	if got := turn.Thinking; got != "Just thinking" {
		t.Errorf("Thinking = %q, want %q", got, "Just thinking")
	}
	if got := turn.Text; got != "" {
		t.Errorf("Text = %q, want empty", got)
	}
}

func TestExtractAssistantText_JoinsMultipleBlocks(t *testing.T) {
	// Multiple blocks of the same kind join with a newline, per kind.
	line := `{"type":"assistant","uuid":"a","timestamp":"2026-04-10T10:00:00Z","message":{"model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
		`{"type":"thinking","thinking":"first","signature":"s1"},` +
		`{"type":"thinking","thinking":"second","signature":"s2"},` +
		`{"type":"text","text":"narr one"},` +
		`{"type":"text","text":"narr two"}` +
		`]}}`
	turn, _, _ := ParseLine([]byte(line), "t")
	if got := turn.Thinking; got != "first\nsecond" {
		t.Errorf("Thinking = %q, want %q", got, "first\nsecond")
	}
	if got := turn.Text; got != "narr one\nnarr two" {
		t.Errorf("Text = %q, want %q", got, "narr one\nnarr two")
	}
}

func TestParseFile_ToolResults(t *testing.T) {
	// tool_result blocks live in user turns that we otherwise filter out;
	// ParseFile must still surface them (keyed by tool_use_id) so the
	// aggregator can join each call to its outcome. content may be a bare
	// string or an array of text blocks, and is_error flags a failure.
	f, err := os.Open("testdata/user_messages.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := ParseFile(f, "testdata/user_messages.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ToolResults) != 2 {
		t.Fatalf("ToolResults = %d, want 2 (%+v)", len(res.ToolResults), res.ToolResults)
	}
	byID := map[string]ToolResult{}
	for _, r := range res.ToolResults {
		byID[r.ToolUseID] = r
	}
	ok := byID["toolu_1"]
	if ok.Content != "output of bash" || ok.IsError {
		t.Errorf("toolu_1 result = %+v, want content/ok", ok)
	}
	bad := byID["toolu_2"]
	if bad.Content != "boom" || !bad.IsError {
		t.Errorf("toolu_2 result = %+v, want boom/error", bad)
	}
}

// A tool_result's wall-clock end is the timestamp of the user line that carries
// it — the only per-tool time we can recover (the tool_use side only stamps the
// whole assistant turn). peekToolResults must lift that line timestamp onto each
// ToolResult so the join can compute per-tool duration.
func TestPeekToolResults_StampsTimestamp(t *testing.T) {
	line := `{"type":"user","timestamp":"2026-04-10T10:00:08Z","message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_1","is_error":false,"content":"done"}` +
		`]}}`
	got := peekToolResults([]byte(line))
	if len(got) != 1 {
		t.Fatalf("peekToolResults len = %d, want 1 (%+v)", len(got), got)
	}
	want := time.Date(2026, 4, 10, 10, 0, 8, 0, time.UTC)
	if !got[0].Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", got[0].Timestamp, want)
	}
}

// Older transcripts may omit the line timestamp; the result must carry a zero
// time then (not a bogus epoch), so the join can tell "no end captured" apart
// from a real one and the frontend falls back to turn-level segments.
func TestPeekToolResults_ZeroTimestampWhenLineHasNone(t *testing.T) {
	line := `{"type":"user","message":{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_1","is_error":false,"content":"done"}` +
		`]}}`
	got := peekToolResults([]byte(line))
	if len(got) != 1 {
		t.Fatalf("peekToolResults len = %d, want 1 (%+v)", len(got), got)
	}
	if !got[0].Timestamp.IsZero() {
		t.Errorf("Timestamp = %v, want zero", got[0].Timestamp)
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
