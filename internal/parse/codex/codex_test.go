package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

func parseFixture(t *testing.T, name, path string) parse.Result {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if path == "" {
		path = filepath.Join("testdata", name)
	}
	res, err := ParseFile(f, path)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", name, err)
	}
	return res
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestParseRollout_Basic(t *testing.T) {
	res := parseFixture(t, "basic.jsonl", "")
	if len(res.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(res.Turns))
	}
	wantTS := []string{"2026-10-25T00:52:06.752Z", "2026-10-25T00:52:10.878Z"}
	wantID := []string{
		"resp_08c58466820d743e016add52b3ab888196b82104086e61dbcb",
		"resp_08c58466820d743e016add52b733f8819693023beb59dc6d3d",
	}
	for i, tr := range res.Turns {
		if tr.Provider != "codex" {
			t.Errorf("turn %d Provider = %q, want codex", i, tr.Provider)
		}
		if tr.Model != "gpt-6-astra" {
			t.Errorf("turn %d Model = %q, want gpt-6-astra", i, tr.Model)
		}
		if tr.SessionID != "01a1708b-04a3-75a1-a07c-92bbb16be66f" {
			t.Errorf("turn %d SessionID = %q", i, tr.SessionID)
		}
		if tr.CWD != "/home/u/proj" {
			t.Errorf("turn %d CWD = %q, want /home/u/proj", i, tr.CWD)
		}
		if tr.Entrypoint != "codex_exec" {
			t.Errorf("turn %d Entrypoint = %q, want codex_exec", i, tr.Entrypoint)
		}
		if want := mustTime(t, wantTS[i]); !tr.Timestamp.Equal(want) || !tr.EndTimestamp.Equal(want) {
			t.Errorf("turn %d Timestamp = %v / End %v, want %v", i, tr.Timestamp, tr.EndTimestamp, want)
		}
		if tr.MessageID != wantID[i] || tr.UUID != wantID[i] {
			t.Errorf("turn %d MessageID/UUID = %q/%q, want %q", i, tr.MessageID, tr.UUID, wantID[i])
		}
		if tr.Sidechain {
			t.Errorf("turn %d Sidechain = true for a user thread", i)
		}
		if tr.SourceFile != filepath.Join("testdata", "basic.jsonl") {
			t.Errorf("turn %d SourceFile = %q", i, tr.SourceFile)
		}
	}
	if res.Malformed != 0 {
		t.Errorf("Malformed = %d, want 0", res.Malformed)
	}
	if len(res.ParentLinks) != 0 {
		t.Errorf("ParentLinks = %v, want none for a user thread", res.ParentLinks)
	}
}

func TestParseRollout_Usage(t *testing.T) {
	res := parseFixture(t, "basic.jsonl", "")
	if len(res.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(res.Turns))
	}
	first := res.Turns[0].Usage
	if first.InputTokens != 38472 || first.CacheReadTokens != 0 || first.CacheCreate5mTokens != 512 ||
		first.CacheCreate1hTokens != 0 || first.OutputTokens != 99 || first.ReasoningOutputTokens != 23 {
		t.Errorf("first usage = %+v", first)
	}
	second := res.Turns[1].Usage
	// usage, not the cumulative turn_token_usage (86745 input) or thread_token_usage.
	want := parse.Usage{InputTokens: 9873, CacheReadTokens: 38400, OutputTokens: 146}
	if second != want {
		t.Errorf("second usage = %+v, want %+v", second, want)
	}
}

func TestParseRollout_ToolCalls(t *testing.T) {
	res := parseFixture(t, "tools.jsonl", "")
	if len(res.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(res.Turns))
	}
	uses := res.Turns[0].ToolUses
	if len(uses) != 3 {
		t.Fatalf("first turn tool uses = %d, want 3: %+v", len(uses), uses)
	}
	if len(res.Turns[1].ToolUses) != 0 {
		t.Errorf("second turn tool uses = %+v, want none", res.Turns[1].ToolUses)
	}
	wantUses := []parse.ToolUse{
		{ID: "call_686ff4004f1041329f3b312b349d352c", Name: "exec", Input: "git log -8 --oneline && git status --short", Detail: "git status"},
		{ID: "call_sAX04vI4aehE5s7dZh9nbaDf", Name: "exec_command", Input: "go test -short ./...", Detail: "go test"},
		{ID: "call_EzybDGOhTxzwKLutaKTDUNmf", Name: "list_agents", Input: "{}"},
	}
	for i, w := range wantUses {
		if uses[i] != w {
			t.Errorf("tool use %d = %+v, want %+v", i, uses[i], w)
		}
	}

	if len(res.ToolResults) != 3 {
		t.Fatalf("tool results = %d, want 3", len(res.ToolResults))
	}
	byID := map[string]parse.ToolResult{}
	for _, r := range res.ToolResults {
		byID[r.ToolUseID] = r
	}
	ok := byID["call_686ff4004f1041329f3b312b349d352c"]
	if ok.IsError {
		t.Errorf("exit-code-0 exec result IsError = true")
	}
	if !strings.Contains(ok.Content, `"exit_code":0`) || ok.OutputBytes != len(ok.Content) || ok.OutputLines < 2 {
		t.Errorf("exec result = %+v", ok)
	}
	if want := mustTime(t, "2026-10-31T23:53:18.261Z"); !ok.Timestamp.Equal(want) {
		t.Errorf("exec result Timestamp = %v, want %v", ok.Timestamp, want)
	}
	fail := byID["call_sAX04vI4aehE5s7dZh9nbaDf"]
	if !fail.IsError {
		t.Errorf("exit-code-1 result IsError = false: %+v", fail)
	}
	wantText := "Exit code: 1\nWall time: 3.2 seconds\nOutput:\n--- FAIL: TestSomething (0.00s)\nFAIL\n"
	if fail.Content != wantText || fail.OutputBytes != len(wantText) || fail.OutputLines != 5 {
		t.Errorf("exit-code-1 result = %+v", fail)
	}
	if byID["call_EzybDGOhTxzwKLutaKTDUNmf"].IsError {
		t.Errorf("list_agents result IsError = true")
	}
}

func TestParseRollout_ExecCmdAfterOtherProperties(t *testing.T) {
	line := `{"timestamp":"2026-10-31T23:53:17.000Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"text(await tools.exec_command({workdir:\"/home/u/proj\",cmd:\"go test ./...\"}));"}}
{"timestamp":"2026-10-31T23:53:18.000Z","type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":10,"output_tokens":1}}}`
	res, err := ParseFile(strings.NewReader(line+"\n"), "x.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 1 || len(res.Turns[0].ToolUses) != 1 {
		t.Fatalf("turns = %+v", res.Turns)
	}
	want := parse.ToolUse{ID: "c1", Name: "exec", Input: "go test ./...", Detail: "go test"}
	if got := res.Turns[0].ToolUses[0]; got != want {
		t.Errorf("tool use = %+v, want %+v", got, want)
	}
}

func TestParseRollout_ToolCallsLongOutputIsSnippet(t *testing.T) {
	long := strings.Repeat("x", 10_000)
	line := `{"timestamp":"2026-10-31T23:53:19.294Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Exit code: 0\nWall time: 1 seconds\nOutput:\n` + long + `"}}`
	res, err := ParseFile(strings.NewReader(line+"\n"), "x.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ToolResults) != 1 {
		t.Fatalf("results = %d", len(res.ToolResults))
	}
	r := res.ToolResults[0]
	if len([]rune(r.Content)) >= 10_000 || r.OutputBytes <= 10_000 {
		t.Errorf("Content len %d, OutputBytes %d: want a capped snippet and the full size", len(r.Content), r.OutputBytes)
	}
}

func TestExitError(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Exit code: 0\nWall time: 1 seconds\nOutput:\nok\n", false},
		{"Exit code: 2\nWall time: 1 seconds\nOutput:\n", true},
		{"Exit code: 0\nWall time: 1 seconds\nOutput:\n{\"exit_code\":1}\n", false},
		{"Chunk ID: 2e0e43\nWall time: 0.0000 seconds\nProcess exited with code 0\nOutput:\n", false},
		{"Chunk ID: 2e0e43\nWall time: 0.0000 seconds\nProcess exited with code 127\nOutput:\n", true},
		{"Script completed\nWall time 0.1 seconds\nOutput:\n\n{\"chunk_id\":\"a\",\"exit_code\":0,\"output\":\"\\\"exit_code\\\":1\"}", false},
		{"Script completed\nWall time 0.1 seconds\nOutput:\n\n{\"chunk_id\":\"a\",\"exit_code\":2,\"output\":\"\"}", true},
		{"Script failed\nWall time 0.1 seconds\nOutput:\nTypeError: x is undefined", true},
		{"{\"agents\":[]}", false},
		{"", false},
	}
	for _, c := range cases {
		if got := exitError(c.text); got != c.want {
			t.Errorf("exitError(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestExecCommands(t *testing.T) {
	cases := []struct {
		src  string
		want []string
	}{
		{`text(await tools.exec_command({cmd:"ls -la\ngit status","max_output_tokens":6000}));`, []string{"ls -la\ngit status"}},
		{`text(await tools.exec_command({cmd:'echo "hi"'}));`, []string{`echo "hi"`}},
		{"text(await tools.exec_command({cmd:`go test ./...`}));", []string{"go test ./..."}},
		{`await Promise.all([tools.exec_command({ cmd: "a" }), tools.exec_command({"cmd":"b A"})]);`, []string{"a", "b A"}},
		{`const c = "x"; tools.exec_command({cmd:c});`, nil},
		{`tools.exec_command({cmd:"go test " + pkg})`, nil},
		{`tools.exec_command({cmd: "go test " + "./..."})`, nil},
		{`tools.exec_command({cmd:"ls".trim(), workdir:"/w"})`, nil},
		{"tools.exec_command({cmd:`go test ${pkg}`})", nil},
		{"tools.exec_command({cmd:`echo \\${HOME}`})", []string{"echo ${HOME}"}},
		{`tools.exec_command({cmd:"a"}); tools.exec_command({cmd:"b " + x})`, nil},
		{`tools.exec_command({workdir:"/home/u/proj",cmd:"go test ./..."})`, []string{"go test ./..."}},
		{`tools.exec_command({ "workdir": '/a,b}', yield_time_ms: 1000, env: {cmd: "no"}, args: [1, {x: "]"}], cmd: ` + "`make lint`" + ` })`, []string{"make lint"}},
		{`tools.exec_command({workdir:"/w"})`, nil},
		{`tools.exec_command(opts)`, nil},
	}
	for _, c := range cases {
		got, ok := execCommands(c.src)
		if ok != (c.want != nil) || strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("execCommands(%q) = %q, %v; want %q", c.src, got, ok, c.want)
		}
	}
}

func TestToolUse_UnresolvedExecCmdKeepsInput(t *testing.T) {
	for _, src := range []string{
		`text(await tools.exec_command({cmd:"go test " + pkg}));`,
		"text(await tools.exec_command({cmd:`go test ${pkg}`}));",
	} {
		tu := toolUse(rawItem{Type: "custom_tool_call", Name: "exec", Input: src})
		if tu.Input != src || tu.Detail != "" {
			t.Errorf("toolUse(%q) = Input %q Detail %q, want the original input and no Bash pattern", src, tu.Input, tu.Detail)
		}
	}
	tu := toolUse(rawItem{Type: "custom_tool_call", Name: "exec", Input: `text(await tools.exec_command({cmd:"go test ./..."}));`})
	if tu.Input != "go test ./..." || tu.Detail == "" {
		t.Errorf("static cmd: Input %q Detail %q, want the command and its Bash pattern", tu.Input, tu.Detail)
	}
}

func TestParseRollout_SubagentLinksParent(t *testing.T) {
	path := filepath.Join("2026", "10", "31", "rollout-2026-10-31T06-12-26-01a19217-0c60-71a2-ab13-917397bf7ed1.jsonl")
	res := parseFixture(t, "subagent.jsonl", path)
	if len(res.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(res.Turns))
	}
	tr := res.Turns[0]
	if !tr.Sidechain {
		t.Errorf("subagent turn Sidechain = false")
	}
	if tr.SessionID != "01a19217-0c60-71a2-ab13-917397bf7ed1" {
		t.Errorf("subagent SessionID = %q, want the rollout's own uuid", tr.SessionID)
	}
	if tr.Model != "gpt-6-sol" {
		t.Errorf("Model = %q", tr.Model)
	}
	want := parse.ParentLink{UUID: "01a19217-0c60-71a2-ab13-917397bf7ed1", ParentUUID: "01a15d72-75c8-7ec1-bff4-4d487e6488ad"}
	if len(res.ParentLinks) != 1 || res.ParentLinks[0] != want {
		t.Errorf("ParentLinks = %+v, want [%+v]", res.ParentLinks, want)
	}
	if res.Malformed != 0 {
		t.Errorf("Malformed = %d (a token_count with null rate_limits is not malformed)", res.Malformed)
	}
	if len(res.RateLimitSamples) != 0 {
		t.Errorf("RateLimitSamples = %+v, want none for null rate_limits", res.RateLimitSamples)
	}

	// A file name without a uuid falls back to the session_meta thread id.
	res = parseFixture(t, "subagent.jsonl", "")
	if got := res.Turns[0].SessionID; got != "01a19217-0c60-71a2-ab13-917397bf7ed1" {
		t.Errorf("fallback SessionID = %q", got)
	}

	g := parseFixture(t, "guardian.jsonl", "")
	if len(g.Turns) != 1 {
		t.Fatalf("guardian turns = %d, want 1", len(g.Turns))
	}
	gt := g.Turns[0]
	if gt.Sidechain || gt.SessionID != "01a18784-ea7e-78c0-9157-660fde6ba86d" || gt.Model != "codex-auto-review" {
		t.Errorf("guardian turn = Sidechain %v SessionID %q Model %q, want an ordinary session", gt.Sidechain, gt.SessionID, gt.Model)
	}
	if len(g.ParentLinks) != 0 {
		t.Errorf("guardian ParentLinks = %+v, want none", g.ParentLinks)
	}
}

func TestParseRollout_RateLimitSamples(t *testing.T) {
	res := parseFixture(t, "basic.jsonl", "")
	if len(res.RateLimitSamples) != 1 {
		t.Fatalf("samples = %d, want 1", len(res.RateLimitSamples))
	}
	s := res.RateLimitSamples[0]
	want := parse.RateLimitSample{
		SessionID:     "01a1708b-04a3-75a1-a07c-92bbb16be66f",
		Provider:      "codex",
		Timestamp:     mustTime(t, "2026-10-25T00:52:10.936Z"),
		UsedPercent:   6.0,
		WindowMinutes: 10080,
		ResetsAt:      time.Unix(1793382085, 0).UTC(),
		PlanType:      "pro",
	}
	if !s.Timestamp.Equal(want.Timestamp) || !s.ResetsAt.Equal(want.ResetsAt) {
		t.Errorf("sample times = %v / %v, want %v / %v", s.Timestamp, s.ResetsAt, want.Timestamp, want.ResetsAt)
	}
	s.Timestamp, s.ResetsAt = want.Timestamp, want.ResetsAt
	if s != want {
		t.Errorf("sample = %+v, want %+v", s, want)
	}
}

func TestParseRollout_SkipsUnknownAndCountsMalformed(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"timestamp":"2026-10-25T00:52:01.951Z","type":"session_meta","payload":{"session_id":"s1","id":"s1","cwd":"/home/u/proj","originator":"codex-tui","thread_source":"user"}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.000Z","type":"response_item","payload":{"type":"additional_tools","tools":[]}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.001Z","type":"event_msg","payload":{"type":"item_completed"}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.002Z","type":"response_item","payload":{"type":"reasoning","summary":[]}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.003Z","type":"response_item","payload":{"type":"message","role":"user","content":[]}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.004Z","type":"event_msg","payload":{"type":"thread_settings_applied"}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.005Z","type":"world_state","payload":{}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.006Z","type":"compacted","payload":{}}` + "\n")
	// Unmodeled item and event types whose fields clash with the modeled
	// shapes (an object where a tool call has a string) are still skips.
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.007Z","type":"response_item","payload":{"type":"tool_search_call","call_id":"c0","arguments":{"query":"x","limit":5}}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.008Z","type":"event_msg","payload":{"type":"thread_goal_updated","rate_limits":"n/a"}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:02.010Z","type":"turn_context","payload":{"turn_id":"t1","model":"gpt-6-astra","cwd":"/home/u/proj"}}` + "\n")
	// A single 2 MB line: a tool output larger than bufio's default token limit.
	big := strings.Repeat("y", 2<<20)
	b.WriteString(`{"timestamp":"2026-10-25T00:52:03.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"Exit code: 0\nWall time: 1 seconds\nOutput:\n` + big + `"}}` + "\n")
	b.WriteString(`{"timestamp":"2026-10-25T00:52:04.000Z","type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2}}}` + "\n")
	// Truncated final line: a writer cut off mid-record.
	b.WriteString(`{"timestamp":"2026-10-25T00:52:05.000Z","type":"token_usage_record","payload":{"response_id":"r2","usa`)

	res, err := ParseFile(strings.NewReader(b.String()), "x.jsonl")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if res.Malformed != 1 {
		t.Errorf("Malformed = %d, want exactly 1", res.Malformed)
	}
	if len(res.Turns) != 1 || res.Turns[0].Usage.InputTokens != 6 {
		t.Errorf("turns = %+v, want the one complete record", res.Turns)
	}
	if len(res.ToolResults) != 1 || res.ToolResults[0].OutputBytes < 2<<20 {
		t.Errorf("2 MB line did not parse: results = %d", len(res.ToolResults))
	}
}

// TestParseRollout_AssistantText: an assistant message's output_text lands
// on the turn whose usage record follows it; text after the last usage
// record belongs to that last response.
func TestParseRollout_AssistantText(t *testing.T) {
	res := parseFixture(t, "basic.jsonl", "")
	if len(res.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(res.Turns))
	}
	if res.Turns[0].Text != "" {
		t.Errorf("turn 0 text = %q, want empty", res.Turns[0].Text)
	}
	if got := res.Turns[1].Text; got != "Summary. (replaced)" {
		t.Errorf("turn 1 text = %q, want the trailing assistant message", got)
	}

	rollout := strings.Join([]string{
		`{"timestamp":"2026-10-29T10:00:00Z","type":"session_meta","payload":{"session_id":"s1","id":"s1","cwd":"/p"}}`,
		`{"timestamp":"2026-10-29T10:00:01Z","type":"turn_context","payload":{"model":"gpt-6-astra"}}`,
		`{"timestamp":"2026-10-29T10:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}`,
		`{"timestamp":"2026-10-29T10:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"First."},{"type":"output_text","text":"Second."}]}}`,
		`{"timestamp":"2026-10-29T10:00:04Z","type":"token_usage_record","payload":{"response_id":"r1","usage":{"input_tokens":10,"output_tokens":2}}}`,
		`{"timestamp":"2026-10-29T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Third."}]}}`,
		`{"timestamp":"2026-10-29T10:00:06Z","type":"token_usage_record","payload":{"response_id":"r2","usage":{"input_tokens":10,"output_tokens":2}}}`,
	}, "\n")
	r, err := ParseFile(strings.NewReader(rollout), "rollout-x.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Turns) != 2 || r.Turns[0].Text != "First.\nSecond." || r.Turns[1].Text != "Third." {
		t.Errorf("turn texts = %q / %q, want %q / %q", r.Turns[0].Text, r.Turns[1].Text, "First.\nSecond.", "Third.")
	}
}
