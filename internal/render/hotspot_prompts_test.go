package render

import (
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
)

// promptExamples returns two examples; the i-th carries providers[i %
// len(providers)], or no provider when none are given (a log from before the
// provider seam).
func promptExamples(providers ...string) []aggregate.ContextExample {
	ex := []aggregate.ContextExample{
		{Input: "for f in *.go; do wc -l \"$f\"; done", SessionID: "sess-aaa", SourceFile: "/home/u/.claude/projects/p/sess-aaa.jsonl", ContextTokens: 42_000},
		{Input: "wc -l main.go", SessionID: "sess-bbb", SourceFile: "/home/u/.claude/projects/p/sess-bbb.jsonl", ContextTokens: 9_000},
	}
	if len(providers) > 0 {
		for i := range ex {
			ex[i].Provider = providers[i%len(providers)]
		}
	}
	return ex
}

// promptHotspot builds a renderable hotspot of kind with the Context keys
// aggregate.Hotspots populates for it, its examples from providers.
func promptHotspot(kind aggregate.HotspotKind, providers ...string) aggregate.Hotspot {
	h := aggregate.Hotspot{Kind: kind, CostUSD: 12.34, PctOfTotal: 4.5, Examples: promptExamples(providers...)}
	if kind == aggregate.HotspotRepeatedCacheWrite {
		h.Context = map[string]any{"session_id": "sess-aaa", "project": "claudit", "write_tokens": int64(3_000_000),
			"read_tokens": int64(5_000_000), "write_cost": 12.34, "write_pct_of_read": 60.0, "turns": 40}
		return h
	}
	tool := map[aggregate.HotspotKind]string{
		aggregate.HotspotBashPattern: "Bash", aggregate.HotspotFileExt: "Read", aggregate.HotspotGrepGlob: "Grep",
		aggregate.HotspotWebHost: "WebFetch", aggregate.HotspotToolPattern: "mcp__x__y",
	}[kind]
	h.Context = map[string]any{"tool": tool, "pattern": "wc", "calls": 7, "context_tokens": int64(51_000),
		"rereads": 30, "write_cost": 1.0, "read_cost": 11.34}
	return h
}

// TestHotspotPromptsRenderEvidenceAndTarget: every template names the
// in-repo agent, embeds each example's session id and JSONL path, and
// states the target as half the current cost.
func TestHotspotPromptsRenderEvidenceAndTarget(t *testing.T) {
	if len(hotspotTemplates) == 0 {
		t.Fatal("no templates")
	}
	for kind := range hotspotTemplates {
		t.Run(string(kind), func(t *testing.T) {
			got, err := HotspotPrompt(promptHotspot(kind))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got, "<no value>") {
				t.Errorf("prompt has an unset template key:\n%s", got)
			}
			for _, want := range []string{
				"coding agent working in this repository",
				"sess-aaa", "/home/u/.claude/projects/p/sess-aaa.jsonl",
				"sess-bbb", "/home/u/.claude/projects/p/sess-bbb.jsonl",
				"42,000 tokens",
				"below $6.17 in the next equal window",
				"exactly one change",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("prompt missing %q:\n%s", want, got)
				}
			}
			for _, banned := range []string{"well-reviewed", "MCP", "repo names"} {
				if strings.Contains(got, banned) {
					t.Errorf("prompt asks the agent to name outside tools (%q)", banned)
				}
			}
		})
	}
}

// TestHotspotPromptsCoverEveryKind: each kind aggregate.Hotspots emits has
// a template.
func TestHotspotPromptsCoverEveryKind(t *testing.T) {
	for _, k := range []aggregate.HotspotKind{
		aggregate.HotspotBashPattern, aggregate.HotspotFileExt, aggregate.HotspotGrepGlob,
		aggregate.HotspotWebHost, aggregate.HotspotToolPattern, aggregate.HotspotRepeatedCacheWrite,
	} {
		if _, ok := hotspotTemplates[k]; !ok {
			t.Errorf("no template for %s", k)
		}
	}
}

func TestHotspotPrompt_UnknownKind(t *testing.T) {
	_, err := HotspotPrompt(aggregate.Hotspot{Kind: "nonexistent"})
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}

// providerMixes are the example-provider mixes a hotspot can carry.
var providerMixes = map[string][]string{
	"claude-code": {parse.ProviderClaudeCode},
	"codex":       {parse.ProviderCodex},
	"mixed":       {parse.ProviderClaudeCode, parse.ProviderCodex},
}

func renderMix(t *testing.T, kind aggregate.HotspotKind, mix string) string {
	t.Helper()
	got, err := HotspotPrompt(promptHotspot(kind, providerMixes[mix]...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<no value>") {
		t.Errorf("prompt has an unset template key:\n%s", got)
	}
	return got
}

// leverList is the bulleted lever list under the prompt's Ask heading.
func leverList(t *testing.T, prompt string) []string {
	t.Helper()
	_, ask, ok := strings.Cut(prompt, "## Ask")
	if !ok {
		t.Fatalf("no Ask section:\n%s", prompt)
	}
	var out []string
	for _, line := range strings.Split(ask, "\n") {
		if strings.HasPrefix(line, "- ") {
			out = append(out, line)
		}
	}
	return out
}

func wantLevers(t *testing.T, prompt string, want []string, banned []string) {
	t.Helper()
	levers := leverList(t, prompt)
	if len(levers) != len(want) {
		t.Errorf("levers = %q, want %d", levers, len(want))
	}
	for i, w := range want {
		if i < len(levers) && !strings.Contains(levers[i], w) {
			t.Errorf("lever %d = %q, want it to contain %q", i+1, levers[i], w)
		}
	}
	for _, b := range banned {
		if strings.Contains(prompt, b) {
			t.Errorf("prompt contains %q:\n%s", b, prompt)
		}
	}
}

// TestHotspotPromptLeversClaudeCode: Claude Code evidence offers the
// CLAUDE.md line, a tool hook and a CLI flag, and never names AGENTS.md.
func TestHotspotPromptLeversClaudeCode(t *testing.T) {
	for kind := range hotspotTemplates {
		t.Run(string(kind), func(t *testing.T) {
			got := renderMix(t, kind, "claude-code")
			hook := "ToolUse hook"
			if kind == aggregate.HotspotRepeatedCacheWrite {
				hook = "a hook"
			}
			wantLevers(t, got, []string{"a CLAUDE.md line", hook, "a CLI flag"}, []string{"AGENTS.md", "Claude Code only"})
			if kind != aggregate.HotspotRepeatedCacheWrite && !strings.Contains(got, "PreToolUse hook") && !strings.Contains(got, "PostToolUse hook") {
				t.Errorf("no PreToolUse or PostToolUse lever:\n%s", got)
			}
		})
	}
}

// TestHotspotPromptLeversCodex: Codex evidence offers an AGENTS.md line and
// a CLI flag or wrapper script — Codex has no tool-call hook — and names
// neither CLAUDE.md nor a Claude Code hook.
func TestHotspotPromptLeversCodex(t *testing.T) {
	for kind := range hotspotTemplates {
		t.Run(string(kind), func(t *testing.T) {
			got := renderMix(t, kind, "codex")
			wantLevers(t, got, []string{"an AGENTS.md line", "a CLI flag or wrapper script"},
				[]string{"CLAUDE.md", "PreToolUse", "PostToolUse", "hook on"})
		})
	}
}

// TestHotspotPromptLeversMixed: evidence from both providers names both
// instruction files, marks the hook lever Claude Code only, and keeps the
// CLI-flag lever.
func TestHotspotPromptLeversMixed(t *testing.T) {
	for kind := range hotspotTemplates {
		t.Run(string(kind), func(t *testing.T) {
			got := renderMix(t, kind, "mixed")
			levers := leverList(t, got)
			wantLevers(t, got, []string{"CLAUDE.md (Claude Code) and AGENTS.md (Codex)", "(Claude Code only)", "a CLI flag or wrapper script"}, nil)
			if len(levers) == 3 && !strings.Contains(levers[1], "hook") {
				t.Errorf("Claude Code only lever %q is not the hook lever", levers[1])
			}
		})
	}
}

// TestHotspotPromptExampleLinesNameProvider: each numbered example line
// names its provider between the token count and the session id.
func TestHotspotPromptExampleLinesNameProvider(t *testing.T) {
	for kind := range hotspotTemplates {
		t.Run(string(kind), func(t *testing.T) {
			got := renderMix(t, kind, "mixed")
			for _, want := range []string{
				"42,000 tokens · claude-code · session sess-aaa · /home/u/.claude/projects/p/sess-aaa.jsonl",
				"9,000 tokens · codex · session sess-bbb · /home/u/.claude/projects/p/sess-bbb.jsonl",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("prompt missing example line %q:\n%s", want, got)
				}
			}
		})
	}
}

// TestHotspotPromptHeaderNamesBothProviders: the header describes claudit as
// an audit of coding-agent session logs from either provider, under every
// provider mix.
func TestHotspotPromptHeaderNamesBothProviders(t *testing.T) {
	for kind := range hotspotTemplates {
		for mix := range providerMixes {
			t.Run(string(kind)+"/"+mix, func(t *testing.T) {
				got := renderMix(t, kind, mix)
				if strings.Contains(got, "Claude Code session logs") {
					t.Errorf("prompt calls the logs Claude Code's:\n%s", got)
				}
				if !strings.Contains(got, "an audit of this machine's coding-agent session logs (Claude Code, Codex)") {
					t.Errorf("header does not describe the audit across providers:\n%s", got)
				}
			})
		}
	}
}

// TestHotspotPromptCacheWriteNamesInstructionFile: a repeated-cache-write
// prompt names the session's provider's always-loaded file, both in the
// "content early in the prompt" sentence and in the instruction-file lever.
func TestHotspotPromptCacheWriteNamesInstructionFile(t *testing.T) {
	for _, c := range []struct{ mix, want, banned string }{
		{"codex", "AGENTS.md", "CLAUDE.md"},
		{"claude-code", "CLAUDE.md", "AGENTS.md"},
	} {
		t.Run(c.mix, func(t *testing.T) {
			got := renderMix(t, aggregate.HotspotRepeatedCacheWrite, c.mix)
			if want := "content early in the prompt (" + c.want + ","; !strings.Contains(got, want) {
				t.Errorf("prompt missing %q:\n%s", want, got)
			}
			if levers := leverList(t, got); len(levers) == 0 || !strings.Contains(levers[0], c.want+" line") {
				t.Errorf("instruction-file lever %q does not name %s", levers, c.want)
			}
			if strings.Contains(got, c.banned) {
				t.Errorf("prompt names %s:\n%s", c.banned, got)
			}
		})
	}
}
