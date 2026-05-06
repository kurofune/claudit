package render

import (
	"strings"
	"testing"

	"github.com/nategross/claudit/internal/aggregate"
)

func TestHotspotPrompt_AllKindsProduceText(t *testing.T) {
	cases := []aggregate.Hotspot{
		{Kind: aggregate.HotspotBashPattern, CostUSD: 12.34, PctOfTotal: 1.5,
			Context: map[string]any{"pattern": "git status", "calls": 100, "turns": 100, "output_tokens": int64(50000), "tool": "Bash", "dominant_model": "claude-opus-4-7"}},
		{Kind: aggregate.HotspotFileExt, CostUSD: 99,
			Context: map[string]any{"tool": "Read", "pattern": ".go", "calls": 200, "turns": 200, "dominant_model": "claude-opus-4-7"}},
		{Kind: aggregate.HotspotGrepGlob, CostUSD: 30,
			Context: map[string]any{"tool": "Grep", "pattern": "*.go", "calls": 50, "turns": 50}},
		{Kind: aggregate.HotspotWebHost, CostUSD: 4,
			Context: map[string]any{"pattern": "github.com", "calls": 12}},
		{Kind: aggregate.HotspotProject, CostUSD: 500,
			Context: map[string]any{"project": "/x/y", "sessions": 10, "turns": 100, "dominant_model": "claude-opus-4-7", "cache_read": int64(1e9)}},
		{Kind: aggregate.HotspotSubagentType, CostUSD: 100,
			Context: map[string]any{"subagent_type": "general-purpose", "turns": 500, "cache_read": int64(1e8)}},
		{Kind: aggregate.HotspotInvocation, CostUSD: 50,
			Context: map[string]any{"subagent_type": "general-purpose", "description": "implement X", "turns": 30, "project": "/x", "started": "2026-05-01"}},
		{Kind: aggregate.HotspotSkill, CostUSD: 10,
			Context: map[string]any{"skill_key": "skill:tdd", "calls": 5, "turns": 5}},
	}
	for _, h := range cases {
		got, err := HotspotPrompt(h)
		if err != nil {
			t.Errorf("kind %s: %v", h.Kind, err)
			continue
		}
		// Sanity assertions: non-empty, contains the preamble's no-trivial-answer line.
		if !strings.Contains(got, "Switch to a cheaper Anthropic model") {
			t.Errorf("kind %s: missing trivial-answer guard", h.Kind)
		}
		if !strings.Contains(got, "Be opinionated and prescriptive") {
			t.Errorf("kind %s: missing specificity demand", h.Kind)
		}
		if len(got) < 500 {
			t.Errorf("kind %s: prompt too short (%d chars)", h.Kind, len(got))
		}
	}
}

func TestHotspotPrompt_UnknownKind(t *testing.T) {
	_, err := HotspotPrompt(aggregate.Hotspot{Kind: "nonexistent"})
	if err == nil {
		t.Error("expected error for unknown kind")
	}
}
