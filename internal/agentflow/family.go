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
// items. An unclosed block runs to the end of the text. Each opens with
// its marker, so a prompt without the marker skips the regexp.
var wrapperBlocks = []struct {
	marker string
	re     *regexp.Regexp
}{
	{"<local-command-", regexp.MustCompile(`(?s)<local-command-[a-z-]+>.*?(?:</local-command-[a-z-]+>|\z)`)},
	{"<system-reminder>", regexp.MustCompile(`(?s)<system-reminder>.*?(?:</system-reminder>|\z)`)},
	{"<command-", regexp.MustCompile(`(?s)<command-[a-z-]+>.*?(?:</command-[a-z-]+>|\z)`)},
	{"# AGENTS.md instructions", regexp.MustCompile(`(?s)# AGENTS\.md instructions[^\n]*.*?(?:</INSTRUCTIONS>|\z)`)},
	{"<environment_context>", regexp.MustCompile(`(?s)<environment_context>.*?(?:</environment_context>|\z)`)},
	{"<codex_internal_context", regexp.MustCompile(`(?s)<codex_internal_context[^>]*>.*?(?:</codex_internal_context>|\z)`)},
	{"<guardian_tool_descriptions>", regexp.MustCompile(`(?s)<guardian_tool_descriptions>.*?(?:</guardian_tool_descriptions>|\z)`)},
}

// promptToken is one candidate for blanking: a run of characters that can
// make up an id, a path or a number. Quotes, brackets and separators end it.
var promptToken = regexp.MustCompile("[^\\s`'\"()\\[\\]{}<>,;]+")

// beadID is a bead id: a lowercase prefix, a hyphen, a base36 suffix of 3
// or more characters and optional .N child numbers (djinn-bhl6v, bd-abc,
// claudit-fw1.3). A hyphenated word of that shape ("one-sentence") matches
// too; the key blanks it like any other id.
var beadID = regexp.MustCompile(`^([a-z][a-z0-9]*)-([0-9a-z]{3,})((?:\.[0-9]+)*)$`)

// stripWrappers removes every wrapper block from a prompt.
func stripWrappers(prompt string) string {
	for _, w := range wrapperBlocks {
		if strings.Contains(prompt, w.marker) {
			prompt = w.re.ReplaceAllString(prompt, "\n")
		}
	}
	return prompt
}

// family derives a prompt's key, remainder and title in one pass; ok is
// false when the prompt has no text of its own once wrappers are stripped.
func family(prompt string) (Family, bool) {
	body := stripWrappers(prompt)
	line := ""
	for rest := body; rest != "" && line == ""; {
		var l string
		l, rest, _ = strings.Cut(rest, "\n")
		if f := strings.Fields(l); len(f) > 0 {
			line = strings.Join(f, " ")
		}
	}
	if line == "" {
		return Family{Key: NoPromptKey}, false
	}
	key, parts := blank(line)
	fam := Family{Key: key, Title: truncateRunesPlain(line, titleMaxRunes)}
	switch id := idLineBead(body); {
	case id != "":
		fam.Remainder = id
	case len(parts) > 0:
		fam.Remainder = strings.Join(parts, " · ")
	default:
		fam.Remainder = runningTextBead(body)
	}
	return fam, true
}

// beadIDLine is the "ID: <bead>" line a bead block opens with.
var beadIDLine = regexp.MustCompile(`(?m)^ID: (\S+)\s*$`)

// idLineBead is the bead id on a prompt body's first "ID:" line, or "".
func idLineBead(body string) string {
	if !strings.Contains(body, "ID: ") {
		return ""
	}
	if m := beadIDLine.FindStringSubmatch(body); m != nil && isBeadID(m[1]) {
		return m[1]
	}
	return ""
}

// runningTextBead is the first bead id in a prompt body whose suffix
// carries a digit. Hyphenated words that merely look like ids
// ("write-heavy") are not taken from running text.
func runningTextBead(body string) string {
	if !strings.Contains(body, "-") {
		return ""
	}
	for _, tok := range promptToken.FindAllString(body, -1) {
		core := trimTokenPunct(tok)
		if m := beadID.FindStringSubmatch(core); m != nil && strings.ContainsAny(m[2], "0123456789") {
			return core
		}
	}
	return ""
}

// FamilyKey is the prompt-family key the Sessions list groups on by exact
// match: the first line of the prompt's own text, with bead ids, file
// paths, hex strings of 7+ characters and digit runs blanked to "_". A prompt
// with no text of its own keys as NoPromptKey.
func FamilyKey(prompt string) string {
	f, _ := family(prompt)
	return f.Key
}

// Remainder is what tells two prompts of one family apart: the bead on the
// prompt's "ID:" line when it has one (idLineBead), else what FamilyKey
// blanked from the key line joined by " · ", else the first digit-bearing
// bead id in the prompt's running text (runningTextBead).
func Remainder(prompt string) string {
	f, _ := family(prompt)
	return f.Remainder
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
		if f, ok := family(p); ok {
			return f
		}
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
// and at least two non-empty segments ("/Users/x", "web/api.js",
// "./file.go", "~/notes"), a scheme ("https://…"), or a root-level file
// whose name carries a dot ("/file.go", "/.env"). A lone "/wish" stays
// text.
func isPath(tok string) bool {
	if strings.Contains(tok, "://") {
		return true
	}
	if !strings.Contains(tok, "/") {
		return false
	}
	var segs []string
	for _, seg := range strings.Split(tok, "/") {
		if seg != "" {
			segs = append(segs, seg)
		}
	}
	if len(segs) >= 2 {
		return true
	}
	return len(segs) == 1 && strings.HasPrefix(tok, "/") && isFileName(segs[0])
}

// isFileName reports whether a path segment names a file by its dot: a dot
// with a character after it ("file.go", ".env").
func isFileName(seg string) bool {
	i := strings.LastIndexByte(seg, '.')
	return i >= 0 && i < len(seg)-1
}

func isBeadID(tok string) bool {
	return beadID.MatchString(tok)
}

// blankNumbers blanks, within one token, every alphanumeric run that is a
// hex string of 7+ characters (a commit or hash) and every remaining digit
// run.
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
		if len(run) >= 7 && isHex(run) {
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
