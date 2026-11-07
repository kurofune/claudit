package parse

import (
	"regexp"
	"slices"
	"strings"
)

// reminderBlock is one of Claude Code's <system-reminder> blocks, the
// instruction context (CLAUDE.md and the like) it writes into a prompt.
var reminderBlock = regexp.MustCompile(`(?s)<system-reminder>.*?(?:</system-reminder>|\z)`)

// ownPromptText is a Claude Code prompt's own text: its system-reminder
// blocks removed, then its leading copy of any reminder seen in the file so
// far dropped (DropInstructionCopy). docs, when non-nil, holds the file's
// earlier reminders and gains this prompt's. A prompt with no text besides
// its reminders comes back unchanged.
func ownPromptText(text string, docs *[]string) string {
	blocks := reminderBlock.FindAllString(text, -1)
	all := blocks
	if docs != nil {
		for _, b := range blocks {
			if !slices.Contains(*docs, b) {
				*docs = append(*docs, b)
			}
		}
		all = *docs
	}
	if len(all) == 0 {
		return text
	}
	body := text
	if len(blocks) > 0 {
		body = reminderBlock.ReplaceAllString(text, "\n")
	}
	own := DropInstructionCopy(body, all)
	if strings.TrimSpace(own) == "" {
		return text
	}
	return own
}

// DropInstructionCopy drops the leading paragraphs of prompt that appear
// verbatim in any of docs, the instruction documents a harness injected
// into the same session: a harness that prepends a copy of those documents
// to its task leaves the task. A paragraph is the lines between blank lines
// or lone instruction-wrapper lines (<INSTRUCTIONS>, </system-reminder>, an
// <!-- … --> marker; other markup is paragraph text), compared trimmed, and matches only a whole paragraph
// of a document, never part of one. Dropping stops at the first paragraph
// found in no document. The prompt comes back unchanged when nothing drops,
// or when everything would: a prompt that is only a copy keeps its own
// first line.
func DropInstructionCopy(prompt string, docs []string) string {
	if len(docs) == 0 {
		return prompt
	}
	var known map[string]bool
	rest, dropped := prompt, false
	for {
		para, next := leadingParagraph(rest)
		if para == "" {
			break
		}
		if known == nil {
			known = docParagraphs(docs)
		}
		if !known[para] {
			break
		}
		rest, dropped = next, true
	}
	rest = skipBreaks(rest)
	if !dropped || rest == "" {
		return prompt
	}
	return rest
}

// wrapperLine is a line holding only an instruction wrapper's delimiter: a
// Codex <INSTRUCTIONS> or Claude Code <system-reminder> tag, or an
// <!-- … --> marker such as an AGENTS.md block's. It separates paragraphs
// rather than belonging to one; any other markup line is paragraph text.
var wrapperLine = regexp.MustCompile(`^\s*(?:</?(?:INSTRUCTIONS|system-reminder)>|<!--.*?-->)\s*$`)

// breakLine reports whether a line ends a paragraph.
func breakLine(line string) bool {
	return strings.TrimSpace(line) == "" || wrapperLine.MatchString(line)
}

// skipBreaks is s from its first line that does not end a paragraph, or ""
// when there is none.
func skipBreaks(s string) string {
	for s != "" {
		line, next, _ := strings.Cut(s, "\n")
		if !breakLine(line) {
			return s
		}
		s = next
	}
	return ""
}

// leadingParagraph is the first paragraph of s, trimmed, and the text after
// it; para is "" when s holds only blank and markup lines.
func leadingParagraph(s string) (para, rest string) {
	i, start, end := 0, -1, len(s)
	for i < len(s) {
		next := len(s)
		if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
			next = i + n + 1
		}
		brk := breakLine(s[i:next])
		if start < 0 && !brk {
			start = i
		} else if start >= 0 && brk {
			end = i
			break
		}
		i = next
	}
	if start < 0 {
		return "", ""
	}
	return strings.TrimSpace(s[start:end]), s[end:]
}

// docParagraphs is the set of every paragraph of docs, trimmed.
func docParagraphs(docs []string) map[string]bool {
	set := map[string]bool{}
	for _, d := range docs {
		for rest := d; ; {
			para, next := leadingParagraph(rest)
			if para == "" {
				break
			}
			set[para] = true
			rest = next
		}
	}
	return set
}
