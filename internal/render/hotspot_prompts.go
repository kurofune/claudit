// Hotspot prompts: per-Kind prompt templates addressed to the coding agent
// working in the user's repo. Each carries the card's evidence (example
// invocations with session ids and JSONL paths the agent can open), asks
// for exactly one change, and states the target the next window is
// measured against.
package render

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/kurofune/claudit/internal/aggregate"
)

// header opens every prompt: who it is for and what claudit measured.
const header = `You are the coding agent working in this repository (Claude Code or Codex). claudit, an audit of this machine's Claude Code session logs, ranked the tool output that costs the most to keep in context. A tool result stays in context and is re-read from the prompt cache on every later turn until the session ends or compacts, so a large result is paid for many times over.

`

// evidence lists the card's example invocations; shared by every kind.
const evidence = `
{{- define "examples"}}
{{range $i, $e := .examples}}{{inc $i}}. {{if $e.Input}}` + "`{{snippet $e.Input}}`" + `{{else}}(no input recorded){{end}}
   {{num $e.ContextTokens}} tokens · session {{$e.SessionID}} · {{$e.SourceFile}}
{{end}}{{end}}`

// footer is the shared target line.
const footer = `
## Target

{{.target_label}} below ${{money .target}} in the next equal window. claudit will show this card's change against this window, so the fix can be checked.
`

var hotspotTemplates = map[aggregate.HotspotKind]*template.Template{}

var promptFuncs = template.FuncMap{
	"inc":     func(i int) int { return i + 1 },
	"money":   func(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) },
	"pct":     func(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) },
	"num":     fmtThousands,
	"snippet": promptSnippet,
}

func init() {
	parse := func(kind aggregate.HotspotKind, body string) {
		hotspotTemplates[kind] = template.Must(
			template.New(string(kind)).Funcs(promptFuncs).Parse(evidence + header + body + footer))
	}

	toolEvidence := `## Evidence

- Pattern: {{.what}}
- Context cost: ${{money .cost}} ({{pct .pct}}% of all context cost in the window) over {{num .calls}} call(s)
- Context added: {{num .context_tokens}} tokens, re-read {{num .rereads}} time(s) on later turns (write ${{money .write_cost}}, re-reads ${{money .read_cost}})

Biggest invocations by context added:
{{template "examples" .}}`

	toolAsk := func(levers string) string {
		return `
## Ask

Open one or two of the JSONL files above and look at what these calls returned. Then make exactly one change that shrinks what this pattern puts into context, choosing one of:

` + levers + `

Make that one change in this repository, then reply with the change and the evidence line above it addresses.
`
	}

	parse(aggregate.HotspotBashPattern, toolEvidence+toolAsk(
		"- a CLAUDE.md line telling you how to run `{{.pattern}}` so it prints less (a filter, a line cap, a narrower target)\n"+
			"- a PostToolUse hook on Bash that trims `{{.pattern}}` output before it reaches context\n"+
			"- a CLI flag that shrinks `{{.pattern}}` output (a quiet, summary or line-limited mode), written into the script or Makefile target you call"))

	parse(aggregate.HotspotFileExt, toolEvidence+toolAsk(
		"- a CLAUDE.md line telling you to read `{{.pattern}}` files by range or search for the symbol first instead of reading whole files\n"+
			"- a PreToolUse hook on {{.tool}} that refuses whole-file reads of large `{{.pattern}}` files without an offset and limit\n"+
			"- a CLI flag on the tool you use to locate code (a symbol or outline mode) so you read only the lines you need"))

	parse(aggregate.HotspotGrepGlob, toolEvidence+toolAsk(
		"- a CLAUDE.md line that scopes {{.tool}} searches for `{{.pattern}}` to the directories that matter\n"+
			"- a PreToolUse hook on {{.tool}} that adds a path or head limit when none is given\n"+
			"- a CLI flag that shrinks the match list (files-only output, a count, or a result cap)"))

	parse(aggregate.HotspotWebHost, toolEvidence+toolAsk(
		"- a CLAUDE.md line saying which pages on {{.pattern}} to fetch and to save a fetched page to a local file instead of fetching it again\n"+
			"- a PreToolUse hook on WebFetch that serves a cached local copy of pages already fetched from {{.pattern}}\n"+
			"- a CLI flag on a command-line client for {{.pattern}} that returns only the fields you need"))

	parse(aggregate.HotspotToolPattern, toolEvidence+toolAsk(
		"- a CLAUDE.md line telling you how to call {{.tool}} so it returns less\n"+
			"- a PostToolUse hook on {{.tool}} that trims its result before it reaches context\n"+
			"- a CLI flag or tool argument that shrinks {{.tool}} output"))

	parse(aggregate.HotspotRepeatedCacheWrite, `## Evidence

- Session {{.session_id}}{{if .project}} in project {{.project}}{{end}} wrote {{num .write_tokens}} prompt-cache tokens and read back {{num .read_tokens}} ({{pct .write_pct_of_read}}% as many writes as reads) over {{num .turns}} turn(s).
- Cache-write cost: ${{money .write_cost}} ({{pct .pct}}% of all cache-write cost in the window).

A healthy session writes its prefix once and reads it back. Writes this large mean the cached prefix kept being rebuilt: a gap between turns longer than the cache lifetime, or content early in the prompt (CLAUDE.md, hook output, tool list) that changed mid-session.

Biggest cache-writing turns:
{{template "examples" .}}
## Ask

Open the JSONL file above, look at the turns listed, and find what changed or expired before each write. Then make exactly one change in this repository that keeps the prefix stable, choosing one of:

- a CLAUDE.md line (for example: do not edit CLAUDE.md or other always-loaded files mid-session; commit them between sessions)
- a hook whose output changes every turn made stable, or moved later in the prompt
- a CLI flag on the command that starts these sessions so the prefix stays the same between runs

Make that one change, then reply with the change and the turn above it explains.
`)
}

// HotspotPrompt renders the prompt for one hotspot. The target is half the
// hotspot's current cost. Returns an error when no template exists for the
// kind.
func HotspotPrompt(h aggregate.Hotspot) (string, error) {
	tpl, ok := hotspotTemplates[h.Kind]
	if !ok {
		return "", fmt.Errorf("no prompt template for kind %q", h.Kind)
	}
	ctx := map[string]any{}
	for k, v := range h.Context {
		ctx[k] = v
	}
	ctx["cost"] = h.CostUSD
	ctx["pct"] = h.PctOfTotal
	ctx["examples"] = h.Examples
	ctx["target"] = h.CostUSD / 2
	ctx["target_label"] = "Context cost of this pattern"
	if h.Kind == aggregate.HotspotRepeatedCacheWrite {
		ctx["target_label"] = "Cache-write cost of sessions like this one"
	}
	ctx["what"] = patternPhrase(h)
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()) + "\n", nil
}

// patternPhrase names a tool pattern in prose for the evidence line.
func patternPhrase(h aggregate.Hotspot) string {
	tool, _ := h.Context["tool"].(string)
	pattern, _ := h.Context["pattern"].(string)
	switch h.Kind {
	case aggregate.HotspotBashPattern:
		return tool + " commands that run `" + pattern + "`"
	case aggregate.HotspotFileExt:
		return tool + " of `" + pattern + "` files"
	case aggregate.HotspotGrepGlob:
		return tool + " with pattern `" + pattern + "`"
	case aggregate.HotspotWebHost:
		return "WebFetch of " + pattern
	}
	return tool + " `" + pattern + "`"
}

// promptSnippet flattens an input to one line of at most 200 runes, so a
// multi-line command stays readable in a numbered list.
func promptSnippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "`", "'")
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

// fmtThousands renders an integer with comma thousands separators.
func fmtThousands(v any) string {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case float64:
		n = int64(x)
	default:
		return fmt.Sprint(v)
	}
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}
