package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func TestHotspots_RanksMixedDimensions(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	ts := time.Now()

	// Cheap project with one big Bash pattern
	for i := 0; i < 100; i++ {
		agg.Add(turn("claude-opus-4-7", 0, 1000, false, "/p/foo", ts,
			parse.ToolUse{Name: "Bash", Detail: "git status"}))
	}
	// Mid-cost project with a single big subagent invocation
	bigInv := turn("claude-opus-4-7", 0, 50000, true, "/p/bar", ts)
	bigInv.SourceFile = "/sub/agent-XXX.jsonl"
	lookup := func(t parse.Turn) (string, string) {
		return "general-purpose", "implement the big feature"
	}
	agg.AddWithSubagent(bigInv, lookup)

	hs := agg.Hotspots(10)
	if len(hs) == 0 {
		t.Fatal("expected hotspots")
	}
	// Cost-descending order.
	for i := 1; i < len(hs); i++ {
		if hs[i-1].CostUSD < hs[i].CostUSD {
			t.Errorf("not sorted: %v then %v", hs[i-1].CostUSD, hs[i].CostUSD)
		}
	}
	// Should contain at least one of each *actionable* kind we exercised.
	// Projects are intentionally excluded — they're diagnostic, not actionable.
	kinds := map[HotspotKind]bool{}
	for _, h := range hs {
		kinds[h.Kind] = true
	}
	for _, want := range []HotspotKind{HotspotBashPattern, HotspotSubagentType, HotspotInvocation} {
		if !kinds[want] {
			t.Errorf("missing kind %s in %v", want, kinds)
		}
	}
	if kinds[HotspotProject] {
		t.Error("HotspotProject should be excluded from the actionable list")
	}
}

func TestHotspots_ExcludesUnknownSubagent(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	ts := time.Now()
	// A sidechain turn with no resolved subagent type → goes to the
	// "sidechain — unknown subagent" bucket, which must NOT appear as a
	// hotspot (it isn't actionable).
	tn := turn("claude-opus-4-7", 0, 100000, true, "/p/foo", ts)
	tn.SourceFile = "/no-meta/agent-zzz.jsonl"
	agg.AddWithSubagent(tn, func(parse.Turn) (string, string) { return "", "" })

	hs := agg.Hotspots(10)
	for _, h := range hs {
		if h.Title == "Subagent type: sidechain — unknown subagent" {
			t.Errorf("unknown-subagent bucket should be filtered out")
		}
	}
}

func TestHotspots_ZeroTopReturnsNil(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	if hs := agg.Hotspots(0); hs != nil {
		t.Errorf("expected nil for top=0, got %v", hs)
	}
}
