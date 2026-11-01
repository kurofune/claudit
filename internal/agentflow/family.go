package agentflow

import (
	"regexp"
	"strings"
)

// NoPromptKey is the family key of a prompt (or session) with no text of
// its own once wrapper blocks are stripped. Same wording as the Spend view's
// prompt bucket for turns with no originating prompt.
const NoPromptKey = "(no prompt)"

// titleMaxRunes caps a session's title, the first line of its first prompt.
const titleMaxRunes = 200

// wrapperBlocks are the blocks a harness writes into a user message on the
// person's behalf: Claude Code's caveats, reminders and slash-command
// echoes, and Codex's injected AGENTS.md, environment, goal and guardian
// items. An unclosed block runs to the end of the text.
var wrapperBlocks = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<local-command-[a-z-]+>.*?(?:</local-command-[a-z-]+>|\z)`),
	regexp.MustCompile(`(?s)<system-reminder>.*?(?:</system-reminder>|\z)`),
	regexp.MustCompile(`(?s)<command-[a-z-]+>.*?(?:</command-[a-z-]+>|\z)`),
	regexp.MustCompile(`(?s)# AGENTS\.md instructions[^\n]*.*?(?:</INSTRUCTIONS>|\z)`),
	regexp.MustCompile(`(?s)<environment_context>.*?(?:</environment_context>|\z)`),
	regexp.MustCompile(`(?s)<codex_internal_context[^>]*>.*?(?:</codex_internal_context>|\z)`),
	regexp.MustCompile(`(?s)<guardian_tool_descriptions>.*?(?:</guardian_tool_descriptions>|\z)`),
}

// promptToken is one candidate for blanking: a run of characters that can
// make up an id, a path or a number. Quotes, brackets and separators end it.
var promptToken = regexp.MustCompile("[^\\s`'\"()\\[\\]{}<>,;]+")

// beadID is a bead id: a lowercase prefix, a hyphen, a 3–8 character
// base36 suffix and optional .N child numbers (djinn-bhl6v, claudit-fw1.3).
// A prefix shorter than four letters counts only when its suffix carries a
// digit, so hyphenated words ("one-sentence", "sub-agent") stay text.
var beadID = regexp.MustCompile(`^([a-z][a-z0-9]*)-([0-9a-z]{3,8})((?:\.[0-9]+)*)$`)

// stripWrappers removes every wrapper block from a prompt.
func stripWrappers(prompt string) string {
	for _, re := range wrapperBlocks {
		prompt = re.ReplaceAllString(prompt, "\n")
	}
	return prompt
}

// keyLine is the prompt's first non-empty line once wrappers are stripped,
// whitespace collapsed; "" when nothing is left.
func keyLine(prompt string) (line, body string) {
	body = stripWrappers(prompt)
	for _, l := range strings.Split(body, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			return strings.Join(f, " "), body
		}
	}
	return "", body
}

// FamilyKey is the prompt-family key the Sessions list groups on by exact
// match: the first line of the prompt's own text, with bead ids, file
// paths, 7+-character hex strings and digit runs blanked to "_". A prompt
// with no text of its own keys as NoPromptKey.
func FamilyKey(prompt string) string {
	line, _ := keyLine(prompt)
	if line == "" {
		return NoPromptKey
	}
	key, _ := blank(line)
	return key
}

// Remainder is what FamilyKey blanked from the key line, joined by " · ",
// so a row can show what tells two prompts of one family apart. When the
// key line blanked nothing, it is the first bead id the prompt names.
func Remainder(prompt string) string {
	line, body := keyLine(prompt)
	if line == "" {
		return ""
	}
	if _, parts := blank(line); len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	for _, tok := range promptToken.FindAllString(body, -1) {
		if core := trimTokenPunct(tok); isBeadID(core) {
			return core
		}
	}
	return ""
}

// Family is a session's prompt family: its key and remainder, and its
// title (the first line of the prompt they come from, unblanked).
type Family struct {
	Key       string
	Remainder string
	Title     string
}

// SessionFamily picks a session's family from its prompts in order: the
// first prompt with text of its own once wrappers are stripped, else
// NoPromptKey.
func SessionFamily(prompts []string) Family {
	for _, p := range prompts {
		line, _ := keyLine(p)
		if line == "" {
			continue
		}
		return Family{Key: FamilyKey(p), Remainder: Remainder(p), Title: truncateRunesPlain(line, titleMaxRunes)}
	}
	return Family{Key: NoPromptKey}
}

// blank replaces every blankable token in line with "_" and returns the
// blanked line plus the original text of each blanked piece, in order.
func blank(line string) (string, []string) {
	var parts []string
	out := promptToken.ReplaceAllStringFunc(line, func(tok string) string {
		core := trimTokenPunct(tok)
		tail := tok[len(core):]
		if core == "" {
			return tok
		}
		if isPath(core) || isBeadID(core) {
			parts = append(parts, core)
			return "_" + tail
		}
		return blankNumbers(core, &parts) + tail
	})
	return out, parts
}

// trimTokenPunct drops sentence punctuation trailing a token, so the full
// stop after "bead claudit-ucr." is not part of the id.
func trimTokenPunct(tok string) string {
	return strings.TrimRight(tok, ".:!?")
}

// isPath reports whether a token is a file path or URL: it holds a slash
// and at least two non-empty segments ("/Users/x", "web/api.js"), or a
// scheme ("https://…"). A lone "/wish" stays text.
func isPath(tok string) bool {
	if strings.Contains(tok, "://") {
		return true
	}
	if !strings.Contains(tok, "/") {
		return false
	}
	n := 0
	for _, seg := range strings.Split(tok, "/") {
		if seg != "" && seg != "~" && seg != "." && seg != ".." {
			n++
		}
	}
	return n >= 2
}

func isBeadID(tok string) bool {
	m := beadID.FindStringSubmatch(tok)
	if m == nil {
		return false
	}
	prefix, suffix, children := m[1], m[2], m[3]
	return len(prefix) >= 4 || children != "" || strings.ContainsAny(suffix, "0123456789")
}

// blankNumbers blanks, within one token, every alphanumeric run that is a
// hex string of 7+ characters carrying a digit (a commit or hash) and every
// remaining digit run.
func blankNumbers(tok string, parts *[]string) string {
	var b strings.Builder
	i := 0
	for i < len(tok) {
		if !isAlnum(tok[i]) {
			b.WriteByte(tok[i])
			i++
			continue
		}
		j := i
		for j < len(tok) && isAlnum(tok[j]) {
			j++
		}
		run := tok[i:j]
		if len(run) >= 7 && isHex(run) && strings.ContainsAny(run, "0123456789") {
			*parts = append(*parts, run)
			b.WriteByte('_')
		} else {
			b.WriteString(blankDigits(run, parts))
		}
		i = j
	}
	return b.String()
}

func blankDigits(run string, parts *[]string) string {
	var b strings.Builder
	i := 0
	for i < len(run) {
		if run[i] < '0' || run[i] > '9' {
			b.WriteByte(run[i])
			i++
			continue
		}
		j := i
		for j < len(run) && run[j] >= '0' && run[j] <= '9' {
			j++
		}
		*parts = append(*parts, run[i:j])
		b.WriteByte('_')
		i = j
	}
	return b.String()
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune("0123456789abcdefABCDEF", rune(s[i])) {
			return false
		}
	}
	return true
}

// truncateRunesPlain shortens s to at most max runes, without a marker.
func truncateRunesPlain(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
