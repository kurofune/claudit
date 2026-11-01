package parse

import (
	"strings"
	"testing"
)

// TestFindTexts_OnePassManyTargets: one read returns two turns (one
// coalesced over two lines sharing its message.id), a prompt and a tool
// input, all past their snippet caps, and skips what was not asked for.
func TestFindTexts_OnePassManyTargets(t *testing.T) {
	long := strings.Repeat("w", 2500)
	cmd := "echo " + strings.Repeat("c", 2500)
	doc := strings.Join([]string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"  ` + long + ` ask\n\n"}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","content":[{"type":"text","text":"` + long + `"}]}}`,
		`{"type":"assistant","uuid":"a1b","message":{"id":"m1","content":[{"type":"text","text":"tail"},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"` + cmd + `"}}]}}`,
		`{"type":"assistant","uuid":"a2","message":{"id":"m2","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"second"}]}}`,
		`{"type":"assistant","uuid":"a3","message":{"id":"m3","content":[{"type":"text","text":"not asked"}]}}`,
	}, "\n")
	got := FindTexts(strings.NewReader(doc), TextRequest{
		Turns: map[string]bool{"a1": true, "a2": true},
		Users: map[string]bool{"u1": true},
		Tools: map[string]bool{"t1": true},
	})
	if d := got.Turns["a1"]; d.Text != long+"\ntail" {
		t.Errorf("a1 text = %d runes, want the coalesced whole text", len(d.Text))
	}
	if d := got.Turns["a2"]; d.Thinking != "hm" || d.Text != "second" {
		t.Errorf("a2 = %+v", d)
	}
	if _, ok := got.Turns["a3"]; ok {
		t.Errorf("a3 returned although not asked for")
	}
	if got.Users["u1"] != "  "+long+" ask\n\n" {
		t.Errorf("u1 text = %d runes, want the prompt verbatim with its whitespace", len(got.Users["u1"]))
	}
	if got.Inputs["t1"] != cmd {
		t.Errorf("t1 input = %d runes, want the whole command", len(got.Inputs["t1"]))
	}
	if !TextSnippetCut(PromptSnippet(long)) || TextSnippetCut(strings.Repeat("w", 2000)) {
		t.Errorf("TextSnippetCut disagrees with PromptSnippet's cap")
	}
}
