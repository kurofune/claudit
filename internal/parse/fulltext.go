package parse

import (
	"bufio"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// TextRequest names what FindTexts reads back from one session log: turns
// by their first line's uuid, prompts by their user line's uuid and tool
// calls by their tool_use id.
type TextRequest struct {
	Turns, Users, Tools map[string]bool
}

// FullTexts is the untruncated text FindTexts found, keyed as requested.
// Each representation matches its bounded snippet minus the cap.
type FullTexts struct {
	Turns  map[string]TurnTextDetail
	Users  map[string]string
	Inputs map[string]string
}

// NewFullTexts is an empty FullTexts ready to fill.
func NewFullTexts() FullTexts {
	return FullTexts{Turns: map[string]TurnTextDetail{}, Users: map[string]string{}, Inputs: map[string]string{}}
}

// TextSnippetCut reports whether a Turn.Thinking, Turn.Text or
// UserMessage.Text snippet was cut at its cap.
func TextSnippetCut(s string) bool { return utf8.RuneCountInString(s) > turnTextMaxChars }

// ToolInputSnippetCut reports whether a ToolUse.Input snippet was cut at
// its cap.
func ToolInputSnippetCut(s string) bool { return utf8.RuneCountInString(s) > toolInputMaxChars }

// JoinText appends one more text block to a turn's text, uncapped: the
// full-text counterpart of JoinTurnText.
func JoinText(text, block string) string { return joinBlocks(text, block) }

// FindTexts streams r (one session's JSONL) once and returns the untruncated
// thinking and text of each requested turn, the text of each requested
// prompt and the input of each requested tool call. A turn joins the lines
// that share its first line's message.id, as FindTurnText does.
func FindTexts(r io.Reader, want TextRequest) FullTexts {
	out := NewFullTexts()
	// open maps a found turn's message.id to its uuid, so the message's later
	// lines join it.
	open := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil || len(raw.Message) == 0 {
			continue
		}
		switch raw.Type {
		case "assistant":
			findAssistantTexts(&raw, want, open, &out)
		case "user":
			if raw.IsMeta || !want.Users[raw.UUID] {
				continue
			}
			if _, done := out.Users[raw.UUID]; done {
				continue
			}
			var msg rawMessage
			if err := json.Unmarshal(raw.Message, &msg); err != nil {
				continue
			}
			if text, hasToolResult := extractUserText(msg.Content); !hasToolResult && text != "" {
				out.Users[raw.UUID] = text
			}
		}
	}
	return out
}

func findAssistantTexts(raw *rawLine, want TextRequest, open map[string]string, out *FullTexts) {
	var msg rawMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil {
		return
	}
	uuid := ""
	if _, done := out.Turns[raw.UUID]; want.Turns[raw.UUID] && !done {
		uuid = raw.UUID
		out.Turns[uuid] = TurnTextDetail{}
		if msg.ID != "" {
			open[msg.ID] = uuid
		}
	} else if msg.ID != "" {
		uuid = open[msg.ID]
	}
	if uuid == "" && len(want.Tools) == 0 {
		return
	}
	var entries []rawContentEntry
	if err := json.Unmarshal(msg.Content, &entries); err != nil {
		return
	}
	for _, e := range entries {
		switch e.Type {
		case "thinking", "text":
			if uuid == "" {
				continue
			}
			d := out.Turns[uuid]
			if e.Type == "thinking" {
				d.Thinking = joinBlocks(d.Thinking, e.Thinking)
			} else {
				d.Text = joinBlocks(d.Text, e.Text)
			}
			out.Turns[uuid] = d
		case "tool_use":
			if _, done := out.Inputs[e.ID]; want.Tools[e.ID] && !done {
				out.Inputs[e.ID] = extractToolInputFull(e.Name, e.Input)
			}
		}
	}
}
