package parse

import "strings"

// DropInstructionCopy drops the leading paragraphs of prompt that appear
// verbatim in any of docs, the instruction documents a harness injected
// into the same session: a harness that prepends a copy of those documents
// to its task leaves the task. A paragraph is text between blank lines,
// compared trimmed, and matches only whole lines of a document. Dropping
// stops at the first paragraph found in no document. The prompt comes back
// unchanged when nothing drops, or when everything would: a prompt that is
// only a copy keeps its own first line.
func DropInstructionCopy(prompt string, docs []string) string {
	if len(docs) == 0 {
		return prompt
	}
	rest, dropped := prompt, false
	for {
		para, next := leadingParagraph(rest)
		if para == "" || !inAnyDoc(para, docs) {
			break
		}
		rest, dropped = next, true
	}
	rest = strings.TrimLeft(rest, " \t\r\n")
	if !dropped || rest == "" {
		return prompt
	}
	return rest
}

// leadingParagraph is the first paragraph of s, trimmed, and the text after
// it; para is "" when s holds only blank lines.
func leadingParagraph(s string) (para, rest string) {
	i, start := 0, -1
	for i < len(s) {
		next := len(s)
		if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
			next = i + n + 1
		}
		blankLine := strings.TrimSpace(s[i:next]) == ""
		if start < 0 && !blankLine {
			start = i
		} else if start >= 0 && blankLine {
			break
		}
		i = next
	}
	if start < 0 {
		return "", ""
	}
	return strings.TrimSpace(s[start:i]), s[i:]
}

func inAnyDoc(para string, docs []string) bool {
	for _, d := range docs {
		if holdsLines(d, para) {
			return true
		}
	}
	return false
}

// holdsLines reports whether doc holds para as whole lines: an occurrence
// with only spaces or tabs between it and the line breaks around it.
func holdsLines(doc, para string) bool {
	for from := 0; ; {
		n := strings.Index(doc[from:], para)
		if n < 0 {
			return false
		}
		at := from + n
		end := at + len(para)
		before := doc[:at]
		if nl := strings.LastIndexByte(before, '\n'); nl >= 0 {
			before = before[nl+1:]
		}
		after := doc[end:]
		if nl := strings.IndexByte(after, '\n'); nl >= 0 {
			after = after[:nl]
		}
		if strings.Trim(before, " \t") == "" && strings.Trim(after, " \t\r") == "" {
			return true
		}
		from = at + 1
	}
}
