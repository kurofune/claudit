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
