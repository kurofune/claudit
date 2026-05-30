package parse

import (
	"encoding/json"
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
	return truncateRunes(strings.TrimSpace(s), toolInputMaxChars)
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
