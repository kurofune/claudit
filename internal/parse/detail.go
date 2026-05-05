package parse

import (
	"encoding/json"
	"net/url"
	"path/filepath"
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
	"git":    true,
	"gh":     true,
	"npm":    true,
	"yarn":   true,
	"pnpm":   true,
	"bun":    true,
	"docker": true,
	"kubectl": true,
	"brew":   true,
	"cargo":  true,
	"go":     true,
	"rustup": true,
	"pip":    true,
	"pip3":   true,
	"poetry": true,
	"uv":     true,
	"make":   true,
	"just":   true,
	"mise":   true,
	"asdf":   true,
	"bd":     true,
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

// topLevelDir extracts a coarse "where" from an absolute path. We only keep
// the segment after $HOME (or the leading "/" segment if not under home) so
// /Users/x/Projects/foo/bar → "Projects/foo".
func topLevelDir(p string) string {
	p = filepath.Clean(p)
	parts := strings.Split(strings.Trim(p, "/"), "/")
	// /Users/<name>/<top>/<sub>/...
	if len(parts) >= 4 && parts[0] == "Users" {
		return strings.Join(parts[2:4], "/")
	}
	if len(parts) >= 1 {
		return "/" + parts[0]
	}
	return p
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
