package parse

import (
	"strings"
	"testing"
)

// assistantLine builds a minimal assistant JSONL line carrying one Bash
// tool_use with the given id and command.
func assistantLine(id, command string) string {
	return `{"type":"assistant","sessionId":"s1","uuid":"u-asst","message":{"role":"assistant","usage":{"input_tokens":1},"content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{"command":` + jsonString(command) + `}}]}}`
}

// resultLine builds a minimal user JSONL line carrying one tool_result for
// the given tool_use id.
func resultLine(toolUseID, content string, isErr bool) string {
	e := "false"
	if isErr {
		e = "true"
	}
	return `{"type":"user","uuid":"u-res","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + toolUseID + `","is_error":` + e + `,"content":` + jsonString(content) + `}]}}`
}

// jsonString JSON-encodes a string (handles quotes/escapes/unicode) so the
// fixtures stay valid JSONL regardless of content.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func TestFindToolUseDetail_InputAndOutput(t *testing.T) {
	doc := assistantLine("toolu_1", "git status --porcelain") + "\n" +
		resultLine("toolu_1", "M internal/parse/parse.go", false) + "\n"
	got, ok := FindToolUseDetail(strings.NewReader(doc), "toolu_1")
	if !ok {
		t.Fatalf("FindToolUseDetail found=false, want true")
	}
	if got.ID != "toolu_1" {
		t.Errorf("ID = %q, want toolu_1", got.ID)
	}
	if got.Name != "Bash" {
		t.Errorf("Name = %q, want Bash", got.Name)
	}
	if got.Input != "git status --porcelain" {
		t.Errorf("Input = %q, want full command", got.Input)
	}
	if got.Output != "M internal/parse/parse.go" {
		t.Errorf("Output = %q, want full result text", got.Output)
	}
	if got.Status != "ok" {
		t.Errorf("Status = %q, want ok", got.Status)
	}
}

// The whole point of load-full: input and output come back UNTRUNCATED, past
// the bounded snippet caps (toolInputMaxChars / toolResultMaxChars = 2000).
func TestFindToolUseDetail_Untruncated(t *testing.T) {
	bigIn := "echo " + strings.Repeat("x", 5000)
	bigOut := strings.Repeat("y", 6000)
	doc := assistantLine("toolu_big", bigIn) + "\n" + resultLine("toolu_big", bigOut, false) + "\n"
	got, ok := FindToolUseDetail(strings.NewReader(doc), "toolu_big")
	if !ok {
		t.Fatal("found=false, want true")
	}
	if len(got.Input) != len(bigIn) {
		t.Errorf("Input len = %d, want %d (untruncated)", len(got.Input), len(bigIn))
	}
	if len(got.Output) != len(bigOut) {
		t.Errorf("Output len = %d, want %d (untruncated)", len(got.Output), len(bigOut))
	}
	if strings.Contains(got.Input, "…") || strings.Contains(got.Output, "…") {
		t.Errorf("untruncated content must not carry the ellipsis marker")
	}
}

func TestFindToolUseDetail_ErrorStatus(t *testing.T) {
	doc := assistantLine("toolu_e", "false") + "\n" + resultLine("toolu_e", "boom", true) + "\n"
	got, ok := FindToolUseDetail(strings.NewReader(doc), "toolu_e")
	if !ok {
		t.Fatal("found=false, want true")
	}
	if got.Status != "error" {
		t.Errorf("Status = %q, want error", got.Status)
	}
	if got.Output != "boom" {
		t.Errorf("Output = %q, want boom", got.Output)
	}
}

// A tool_use with no matching tool_result (still running, or an old session)
// is still found via its assistant line; output/status stay empty.
func TestFindToolUseDetail_NoResult(t *testing.T) {
	doc := assistantLine("toolu_pending", "sleep 99") + "\n"
	got, ok := FindToolUseDetail(strings.NewReader(doc), "toolu_pending")
	if !ok {
		t.Fatal("found=false, want true (input line present)")
	}
	if got.Output != "" || got.Status != "" {
		t.Errorf("Output=%q Status=%q, want both empty", got.Output, got.Status)
	}
}

func TestFindToolUseDetail_NotFound(t *testing.T) {
	doc := assistantLine("toolu_1", "ls") + "\n"
	if _, ok := FindToolUseDetail(strings.NewReader(doc), "nope"); ok {
		t.Error("found=true for unknown id, want false")
	}
}

// textLine builds an assistant JSONL line with thinking/text blocks, a uuid
// and a message.id — the shape FindTurnText navigates.
func textLine(uuid, msgID, thinking, text string) string {
	var blocks []string
	if thinking != "" {
		blocks = append(blocks, `{"type":"thinking","thinking":`+jsonString(thinking)+`}`)
	}
	if text != "" {
		blocks = append(blocks, `{"type":"text","text":`+jsonString(text)+`}`)
	}
	id := ""
	if msgID != "" {
		id = `"id":` + jsonString(msgID) + `,`
	}
	return `{"type":"assistant","sessionId":"s1","uuid":` + jsonString(uuid) + `,"message":{` + id + `"role":"assistant","usage":{"input_tokens":1},"content":[` + strings.Join(blocks, ",") + `]}}`
}

// The whole point of load-full for turns: thinking/text come back UNTRUNCATED,
// past the turnTextMaxChars snippet cap the parse path applies.
func TestFindTurnText_SingleLineUntruncated(t *testing.T) {
	bigThink := strings.Repeat("t", 5000)
	bigText := strings.Repeat("n", 6000)
	doc := textLine("uu-1", "msg_1", bigThink, bigText) + "\n"
	got, ok := FindTurnText(strings.NewReader(doc), "uu-1")
	if !ok {
		t.Fatal("found=false, want true")
	}
	if got.Thinking != bigThink {
		t.Errorf("Thinking len = %d, want %d (untruncated)", len(got.Thinking), len(bigThink))
	}
	if got.Text != bigText {
		t.Errorf("Text len = %d, want %d (untruncated)", len(got.Text), len(bigText))
	}
}

// A streamed message spans several JSONL lines sharing message.id; the full
// text is their blocks joined with newlines (joinBlocks semantics), and lines
// of a DIFFERENT message.id are excluded.
func TestFindTurnText_JoinsCoalescedLines(t *testing.T) {
	doc := textLine("uu-1", "msg_1", "think one", "text one") + "\n" +
		textLine("uu-2", "msg_1", "think two", "") + "\n" +
		textLine("uu-3", "msg_1", "", "text three") + "\n" +
		textLine("uu-4", "msg_OTHER", "other think", "other text") + "\n"
	got, ok := FindTurnText(strings.NewReader(doc), "uu-1")
	if !ok {
		t.Fatal("found=false, want true")
	}
	if want := "think one\nthink two"; got.Thinking != want {
		t.Errorf("Thinking = %q, want %q", got.Thinking, want)
	}
	if want := "text one\ntext three"; got.Text != want {
		t.Errorf("Text = %q, want %q", got.Text, want)
	}
}

// Legacy transcripts have no message.id; such a line never coalesces, so the
// full text is just that one line — a later id-less line must NOT merge in.
func TestFindTurnText_EmptyMessageIDStandsAlone(t *testing.T) {
	doc := textLine("uu-legacy", "", "solo think", "solo text") + "\n" +
		textLine("uu-next", "", "other think", "other text") + "\n"
	got, ok := FindTurnText(strings.NewReader(doc), "uu-legacy")
	if !ok {
		t.Fatal("found=false, want true")
	}
	if got.Thinking != "solo think" || got.Text != "solo text" {
		t.Errorf("Thinking/Text = %q/%q, want just the matched line's blocks", got.Thinking, got.Text)
	}
}

func TestFindTurnText_NotFound(t *testing.T) {
	doc := textLine("uu-1", "msg_1", "think", "text") + "\n"
	if _, ok := FindTurnText(strings.NewReader(doc), "nope"); ok {
		t.Error("found=true for unknown uuid, want false")
	}
}
