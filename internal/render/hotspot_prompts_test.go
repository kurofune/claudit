package render

import (
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/aggregate"
)

func promptExamples() []aggregate.ContextExample {
	return []aggregate.ContextExample{
		{Input: "for f in *.go; do wc -l \"$f\"; done", SessionID: "sess-aaa", SourceFile: "/home/u/.claude/projects/p/sess-aaa.jsonl", ContextTokens: 42_000},
		{Input: "wc -l main.go", SessionID: "sess-bbb", SourceFile: "/home/u/.claude/projects/p/sess-bbb.jsonl", ContextTokens: 9_000},
	}
}

// promptHotspot builds a renderable hotspot of kind with the Context keys
// aggregate.Hotspots populates for it.
func promptHotspot(kind aggregate.HotspotKind) aggregate.Hotspot {
	h := aggregate.Hotspot{Kind: kind, CostUSD: 12.34, PctOfTotal: 4.5, Examples: promptExamples()}
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
