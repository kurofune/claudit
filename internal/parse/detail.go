package parse

import (
	"bufio"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// extractDetail returns a per-tool drill-down key (or "" if none applies).
// The goal: bucket Bash by command pattern, file tools by extension,
// WebFetch by host — i.e. anything that lets the user see "git on Opus
// cost X" or "reads of .go files cost Y."
func extractDetail(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch name {
	case "Bash", "Monitor":
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return ""
		}
		return bashPattern(in.Command)
	case "Read", "Edit", "Write", "NotebookEdit":
		var in struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return ""
		}
		p := in.FilePath
		if p == "" {
			p = in.NotebookPath
		}
		return fileExt(p)
	case "Grep":
		var in struct {
			Path   string `json:"path"`
			Glob   string `json:"glob"`
			Output string `json:"output_mode"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return ""
		}
		// Glob narrows the search ("*.go") so it's the most useful single key;
		// fall back to the path's top-level dir.
		if in.Glob != "" {
			return in.Glob
		}
		if in.Path != "" {
			return topLevelDir(in.Path)
		}
		return ""
	case "Glob":
		var in struct {
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return ""
		}
		return in.Pattern
	case "WebFetch":
		var in struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return ""
		}
		return urlHost(in.URL)
	case "WebSearch":
		var in struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return ""
		}
		// Just the first 60 chars — full query is too noisy as a key.
		q := strings.TrimSpace(in.Query)
		if len(q) > 60 {
			q = q[:60] + "…"
		}
		return q
	case "TaskCreate", "TaskUpdate":
		var in struct {
			Subject string `json:"subject"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return ""
		}
		return in.Subject
	}
	return ""
}

// toolInputMaxChars bounds the per-invocation input snippet we retain so a
// single huge subagent prompt or heredoc can't bloat the timeline payload.
const toolInputMaxChars = 2000

// extractToolInput returns a bounded, human-readable snippet of a tool call's
// input for the high-value tools — the full Bash command, the prompt handed
// to an Agent/Task subagent, the slash-command line, a WebFetch URL. Unlike
// extractDetail (which buckets to a coarse key for roll-ups), this preserves
// the actual input so the Sessions view can show what the agent did. Returns
// "" for tools whose Detail already captures everything useful.
func extractToolInput(name string, raw json.RawMessage) string {
	return truncateRunes(extractToolInputFull(name, raw), toolInputMaxChars)
}

// extractToolInputFull is extractToolInput without the length cap — the
// untruncated representation the drawer's "show full" action reads back from
// disk. Returns "" for tools whose input we don't surface (same set as the
// snippet) and for empty/unparseable input.
func extractToolInputFull(name string, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	switch name {
	case "Bash", "Monitor":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return ""
		}
		s = in.Command
	case "Agent", "Task":
		var in struct {
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return ""
		}
		s = in.Prompt
	case "Skill":
		var in struct {
			Args    string `json:"args"`
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return ""
		}
		s = in.Args
		if s == "" {
			s = in.Command
		}
	case "SlashCommand":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return ""
		}
		s = in.Command
	case "WebFetch":
		var in struct {
			URL    string `json:"url"`
			Prompt string `json:"prompt"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return ""
		}
		s = in.URL
		if in.Prompt != "" {
			s = in.URL + " — " + in.Prompt
		}
	default:
		return ""
	}
	return strings.TrimSpace(s)
}

// ToolUseDetail is the untruncated record for a single tool call, read back
// from a session JSONL on demand (the drawer's "show full" action). Fields
// mirror their bounded counterparts on ToolUse / ToolResult but without the
// snippet caps.
type ToolUseDetail struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Input  string `json:"input"`  // untruncated, same representation as ToolUse.Input
	Output string `json:"output"` // untruncated tool_result text
	Status string `json:"status"` // "ok" | "error" | "" when no result line
}

// FindToolUseDetail streams r (one session's JSONL) and returns the
// untruncated input/output for the tool_use whose id == toolUseID. found is
// false when no assistant line in r carried that id. Output/Status come from
// the matching tool_result (a later user line) and stay empty when no result
// was recorded — the tool is still running, or it's an older session.
//
// Reuses the same raw schema and content extractors as ParseFile, so the
// "full" representation matches the bounded snippet exactly minus the cap.
func FindToolUseDetail(r io.Reader, toolUseID string) (ToolUseDetail, bool) {
	out := ToolUseDetail{ID: toolUseID}
	found := false
	sc := bufio.NewScanner(r)
	// Match ParseFile's buffer: a single tool_use input or result can be large.
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
			if found {
				continue
			}
			var msg rawMessage
			if err := json.Unmarshal(raw.Message, &msg); err != nil {
				continue
			}
			var entries []rawContentEntry
			if err := json.Unmarshal(msg.Content, &entries); err != nil {
				continue
			}
			for _, e := range entries {
				if e.Type == "tool_use" && e.ID == toolUseID {
					out.Name = e.Name
					out.Input = extractToolInputFull(e.Name, e.Input)
					found = true
					break
				}
			}
		case "user":
			var msg rawMessage
			if err := json.Unmarshal(raw.Message, &msg); err != nil || len(msg.Content) == 0 || msg.Content[0] != '[' {
				continue
			}
			var entries []rawToolResultEntry
			if err := json.Unmarshal(msg.Content, &entries); err != nil {
				continue
			}
			for _, e := range entries {
				if e.Type == "tool_result" && e.ToolUseID == toolUseID {
					out.Output = toolResultText(e.Content)
					if e.IsError {
						out.Status = "error"
					} else {
						out.Status = "ok"
					}
					break
				}
			}
		}
	}
	return out, found
}

// TurnTextDetail is the untruncated thinking/text of one assistant turn, read
// back from a session JSONL on demand (the drawer's "show full" action).
// Fields mirror Turn.Thinking/.Text but without the turnTextMaxChars cap.
type TurnTextDetail struct {
	Thinking string `json:"thinking"`
	Text     string `json:"text"`
}

// FindTurnText streams r (one session's JSONL) and returns the untruncated
// thinking/text for the coalesced turn whose FIRST line's uuid == turnUUID.
// It notes that line's message.id and joins (newline between non-empty
// blocks, matching joinBlocks) the thinking/text of that line plus every
// subsequent assistant line sharing the id — the same grouping coalesceTurns
// applies, so the full text matches the bounded snippet minus the cap. A line
// with an empty message.id stands alone. found is false when no assistant
// line carries turnUUID. Single forward pass: the uuid is always the first
// line of its message, so no lookback is needed.
func FindTurnText(r io.Reader, turnUUID string) (TurnTextDetail, bool) {
	var out TurnTextDetail
	found := false
	msgID := ""
	sc := bufio.NewScanner(r)
	// Match ParseFile's buffer: a single line's content can be large.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw rawLine
		if err := json.Unmarshal(line, &raw); err != nil || raw.Type != "assistant" || len(raw.Message) == 0 {
			continue
		}
		if found && msgID == "" {
			break // empty message.id never coalesces — the uuid line stood alone
		}
		var msg rawMessage
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			continue
		}
		switch {
		case !found:
			if raw.UUID != turnUUID {
				continue
			}
			found = true
			msgID = msg.ID
		case msg.ID != msgID:
			continue
		}
		var entries []rawContentEntry
		if err := json.Unmarshal(msg.Content, &entries); err != nil {
			continue
		}
		for _, e := range entries {
			switch e.Type {
			case "thinking":
				out.Thinking = joinBlocks(out.Thinking, e.Thinking)
			case "text":
				out.Text = joinBlocks(out.Text, e.Text)
			}
		}
	}
	return out, found
}

// truncateRunes shortens s to at most max runes, appending an ellipsis when
// it had to cut. Rune-safe so multibyte input isn't split mid-character.
func truncateRunes(s string, max int) string {
	if len(s) <= max { // bytes >= runes, so this is a safe fast path
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// countLines returns the number of text lines in s — `\n`-count plus one for a
// final unterminated line. Empty string is zero lines (not one). So "a\nb" is
// 2, "a\nb\n" is 2, and "" is 0.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// rawToolUseResult decodes only the size-bearing fields of a line's top-level
// `toolUseResult`. The wire object is large and tool-specific; we ignore
// everything but the few shapes that carry a record count.
type rawToolUseResult struct {
	StructuredPatch []struct {
		Lines []string `json:"lines"`
	} `json:"structuredPatch"`
	Matches []json.RawMessage `json:"matches"`
	Results []json.RawMessage `json:"results"`
}

// toolResultRows extracts a structured record count from a line's raw
// `toolUseResult`, or 0 when none applies. Precedence reflects which signal is
// most specific to the tool:
//   - structuredPatch (Edit/Write) → changed lines: those starting with '+' or
//     '-' across all hunks (context lines, which start with a space, don't count).
//   - matches (Grep-style) → number of matches.
//   - results (search-style) → number of results.
func toolResultRows(raw json.RawMessage) int {
	if len(raw) == 0 || raw[0] != '{' {
		return 0
	}
	var r rawToolUseResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return 0
	}
	if len(r.StructuredPatch) > 0 {
		changed := 0
		for _, h := range r.StructuredPatch {
			for _, ln := range h.Lines {
				if strings.HasPrefix(ln, "+") || strings.HasPrefix(ln, "-") {
					changed++
				}
			}
		}
		return changed
	}
	if len(r.Matches) > 0 {
		return len(r.Matches)
	}
	return len(r.Results)
}

// bashPattern collapses a shell command to its "shape" so similar invocations
// bucket together. Strategy:
//  1. If the command has "&&", "||", or ";", take the LAST segment — `cd foo
//     && git status` should bucket as "git status", not "cd".
//  2. Strip leading env vars (FOO=bar) and `sudo`.
//  3. Take the program name. If it's a known multi-command tool, also take
//     the next non-flag token ("git status", "npm install").
func bashPattern(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Walk to the rightmost segment separated by && || ;
	// (rough — doesn't handle quoted separators, fine for bucketing).
	for _, sep := range []string{"&&", "||", ";"} {
		if i := strings.LastIndex(cmd, sep); i >= 0 {
			cmd = strings.TrimSpace(cmd[i+len(sep):])
		}
	}
	// Tokenize on whitespace (rough — quoted args bleed but we only need
	// the first 1-2 tokens, and those are almost never quoted).
	toks := strings.Fields(cmd)
	// Strip leading FOO=bar env vars and `sudo`.
	for len(toks) > 0 {
		t := toks[0]
		if t == "sudo" {
			toks = toks[1:]
			continue
		}
		if eq := strings.Index(t, "="); eq > 0 && !strings.ContainsAny(t[:eq], "/.-") {
			toks = toks[1:]
			continue
		}
		break
	}
	if len(toks) == 0 {
		return ""
	}
	prog := filepath.Base(toks[0])
	// Strip a leading time/builtin wrapper.
	if prog == "time" || prog == "exec" {
		toks = toks[1:]
		if len(toks) == 0 {
			return prog
		}
		prog = filepath.Base(toks[0])
	}
	if !multiCommand[prog] || len(toks) < 2 {
		return prog
	}
	sub := toks[1]
	// Skip flags like "-l" or "--global".
	if strings.HasPrefix(sub, "-") {
		return prog
	}
	return prog + " " + sub
}

// multiCommand is the set of tools where the next non-flag token is a real
// sub-command worth keeping in the bucket (so we get "git status" not "git").
var multiCommand = map[string]bool{
	"git":     true,
	"gh":      true,
	"npm":     true,
	"yarn":    true,
	"pnpm":    true,
	"bun":     true,
	"docker":  true,
	"kubectl": true,
	"brew":    true,
	"cargo":   true,
	"go":      true,
	"rustup":  true,
	"pip":     true,
	"pip3":    true,
	"poetry":  true,
	"uv":      true,
	"make":    true,
	"just":    true,
	"mise":    true,
	"asdf":    true,
	"bd":      true,
}

// fileExt returns ".ext" lowercased, or "(no ext)" / "(empty)" sentinels.
func fileExt(path string) string {
	if path == "" {
		return "(empty)"
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return "(no ext)"
	}
	return ext
}

// topLevelDir extracts a coarse "where" from an absolute path. The two
// segments after $HOME bucket the work (so /Users/x/Projects/foo/bar →
// "Projects/foo"); paths outside any home fall back to their leading
// filesystem segment.
func topLevelDir(p string) string {
	p = filepath.Clean(p)
	if rel := relativeToHome(p); rel != "" {
		parts := strings.Split(rel, "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0] + "/" + parts[1]
		}
	}
	slash := strings.ReplaceAll(p, `\`, "/")
	trimmed := strings.Trim(slash, "/")
	if trimmed == "" {
		// p is root after filepath.Clean — on Windows that's "\", which
		// would leak the native separator. Return the slash-normalized
		// form so callers get "/" on every OS.
		return slash
	}
	first := strings.SplitN(trimmed, "/", 2)[0]
	// Strip a Windows drive letter so C:\etc\hosts → "/etc" (matches the
	// Unix shape of this function's other return values).
	if len(first) == 2 && first[1] == ':' {
		rest := strings.SplitN(trimmed, "/", 2)
		if len(rest) == 2 {
			first = strings.SplitN(rest[1], "/", 2)[0]
		}
	}
	return "/" + first
}

// homePathRE matches the common home-directory shapes from foreign OSes,
// for the case where a JSONL was produced on a different machine than the
// one parsing it. Drive letter is optional so it works on Windows paths
// after backslash normalization.
var homePathRE = regexp.MustCompile(`(?i)^(?:[a-z]:)?/(?:Users|home)/[^/]+/(.+)$`)

// relativeToHome returns p stripped of its home-directory prefix, with
// forward-slash separators. Empty result means p is not under any home.
func relativeToHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil &&
			!strings.HasPrefix(rel, "..") && rel != "." {
			return strings.ReplaceAll(rel, `\`, "/")
		}
	}
	slash := strings.ReplaceAll(p, `\`, "/")
	if m := homePathRE.FindStringSubmatch(slash); m != nil {
		return m[1]
	}
	return ""
}

func urlHost(u string) string {
	if u == "" {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Host)
}
