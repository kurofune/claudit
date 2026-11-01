package render

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// TestBuildOverview_ShapeAndKeys is the red-phase entry for the
// per-section payload split. The new BuildOverview umbrella feeds
// `GET /_claudit/api/overview` and must include everything a SPA
// needs to paint the landing tab in one fetch.
func TestBuildOverview_ShapeAndKeys(t *testing.T) {
	a := payloadSetup(t)
	p := BuildOverview(a, nil)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal OverviewPayload: %v", err)
	}
	wantTopLevel(t, got, []string{
		"totals",
		"hotspots",
		"trend_totals",
		"forecast",
		"unknown_models",
		"overall_hit_ratio",
		"total_tokens",
		"period",
		"providers",
	})
}

// TestBuildOverview_CarriesPeriod: the SPA reads the bucket granularity
// off the payload (rather than hardcoding "day") so the single-day
// hourly view labels its axis in HH:MM.
func TestBuildOverview_CarriesPeriod(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	a := aggregate.New(prices).WithPeriod(aggregate.PeriodHour)
	a.Add(mkTurn("claude-opus-4-7", "/p/x", 1_000_000, 200_000, time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)))
	if got := BuildOverview(a, nil).Period; got != aggregate.PeriodHour {
		t.Errorf("Period = %q, want hour", got)
	}
}

// TestBuildOverview_HitRatioMatchesAggregator: the overview's headline
// cache hit ratio must equal the aggregator's OverallHitRatio() so the
// landing tab's cache stat matches the cache tab exactly.
func TestBuildOverview_HitRatioMatchesAggregator(t *testing.T) {
	a := payloadSetup(t)
	if got, want := BuildOverview(a, nil).OverallHitRatio, a.OverallHitRatio(); got != want {
		t.Errorf("OverallHitRatio = %v, want %v", got, want)
	}
}

// TestBuildCost_ShapeAndKeys: cost tab covers by_model, by_project,
// by_skill, by_prompt — every "where did the money go" lens.
func TestBuildCost_ShapeAndKeys(t *testing.T) {
	a := payloadSetup(t)
	p := BuildCost(a)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal CostPayload: %v", err)
	}
	wantTopLevel(t, got, []string{
		"by_model",
		"by_project",
		"by_skill",
		"by_prompt",
		"total_cost_usd",
		"sessions",
	})
}

// TestBuildCost_TotalCostMatchesAggregator: the cost tab's headline
// total must equal the aggregator's Totals().CostUSD exactly so JS can
// read it directly instead of re-summing per-row costs.
func TestBuildCost_TotalCostMatchesAggregator(t *testing.T) {
	a := payloadSetup(t)
	if got, want := BuildCost(a).TotalCostUSD, a.Totals().CostUSD; got != want {
		t.Errorf("TotalCostUSD = %v, want %v", got, want)
	}
}

// TestBuildTools_ShapeAndKeys: tools tab has the parent buckets plus
// the per-tool drill-down map.
func TestBuildTools_ShapeAndKeys(t *testing.T) {
	a := payloadSetup(t)
	p := BuildTools(a)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal ToolsPayload: %v", err)
	}
	wantTopLevel(t, got, []string{
		"by_tool",
		"by_tool_detail",
		"sessions",
	})
}

// TestBuildTools_RowFields: one turn calls Bash three times — `go test`
// twice (one errors) and `git status` once — and all three results arrive
// timed, 100/200/300ms after the turn's last line. The next turn writes
// their 5,000 bytes-split cache tokens; the one after re-reads them, so
// every row counts that one turn once however many results it re-read. Every
// field on by_tool and by_tool_detail rows is asserted through the JSON the
// SPA reads.
func TestBuildTools_RowFields(t *testing.T) {
	prices := &pricing.Table{Models: map[string]pricing.ModelPrice{
		"m": {Rate: pricing.Rate{CacheRead: 1, CacheWrite5m: 4}},
	}}
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	end := t0.Add(time.Second)
	a := aggregate.New(prices)
	a.Add(parse.Turn{SessionID: "s", MessageID: "t1", Model: "m", Timestamp: t0, EndTimestamp: end, SourceFile: "f",
		ToolUses: []parse.ToolUse{
			{ID: "b1", Name: "Bash", Detail: "go test"},
			{ID: "b2", Name: "Bash", Detail: "go test"},
			{ID: "b3", Name: "Bash", Detail: "git status"},
		}})
	a.Add(parse.Turn{SessionID: "s", MessageID: "t2", Model: "m", Timestamp: t0.Add(2 * time.Second), SourceFile: "f",
		Usage: parse.Usage{CacheCreate5mTokens: 5000}})
	a.Add(parse.Turn{SessionID: "s", MessageID: "t3", Model: "m", Timestamp: t0.Add(3 * time.Second), SourceFile: "f",
		Usage: parse.Usage{CacheReadTokens: 5000}})
	a.AddToolResults([]parse.ToolResult{
		{ToolUseID: "b1", IsError: true, OutputBytes: 300, Timestamp: end.Add(300 * time.Millisecond)},
		{ToolUseID: "b2", OutputBytes: 100, Timestamp: end.Add(100 * time.Millisecond)},
		{ToolUseID: "b3", OutputBytes: 100, Timestamp: end.Add(200 * time.Millisecond)},
	})

	raw, err := json.Marshal(BuildTools(a))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ByTool       []map[string]any            `json:"by_tool"`
		ByToolDetail map[string][]map[string]any `json:"by_tool_detail"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.ByTool) != 1 {
		t.Fatalf("by_tool = %v, want one Bash row", got.ByTool)
	}
	assertRow(t, "Bash", got.ByTool[0], map[string]any{
		"tool": "Bash", "calls": 3.0, "results": 3.0, "error_count": 1.0, "error_rate": 1.0 / 3,
		"timed_calls": 3.0, "duration_median_ms": 200.0, "duration_p95_ms": 300.0,
		"context_tokens": 5000.0, "reread_turns": 1.0,
		"context_write_usd": 0.02, "context_read_usd": 0.005, "context_cost_usd": 0.025,
	})
	detail := got.ByToolDetail["Bash"]
	if len(detail) != 2 {
		t.Fatalf("by_tool_detail[Bash] = %v, want two patterns", detail)
	}
	assertRow(t, "Bash·go test", detail[0], map[string]any{
		"tool": "Bash", "pattern": "go test", "calls": 2.0, "results": 2.0, "error_count": 1.0, "error_rate": 0.5,
		"timed_calls": 2.0, "duration_median_ms": 100.0, "duration_p95_ms": 300.0,
		"context_tokens": 4000.0, "reread_turns": 1.0,
		"context_write_usd": 0.016, "context_read_usd": 0.004, "context_cost_usd": 0.02,
	})
	assertRow(t, "Bash·git status", detail[1], map[string]any{
		"tool": "Bash", "pattern": "git status", "calls": 1.0, "results": 1.0, "error_count": 0.0, "error_rate": 0.0,
		"timed_calls": 1.0, "duration_median_ms": 200.0, "duration_p95_ms": 200.0,
		"context_tokens": 1000.0, "reread_turns": 1.0,
		"context_write_usd": 0.004, "context_read_usd": 0.001, "context_cost_usd": 0.005,
	})
}

func assertRow(t *testing.T, label string, row, want map[string]any) {
	t.Helper()
	for k, w := range want {
		g, ok := row[k]
		if !ok {
			t.Errorf("%s: missing %q in %v", label, k, row)
			continue
		}
		if wf, isNum := w.(float64); isNum {
			if gf, _ := g.(float64); gf-wf > 1e-9 || wf-gf > 1e-9 {
				t.Errorf("%s: %s = %v, want %v", label, k, g, w)
			}
			continue
		}
		if g != w {
			t.Errorf("%s: %s = %v, want %v", label, k, g, w)
		}
	}
	if len(row) != len(want) {
		t.Errorf("%s: row has %d fields, test asserts %d: %v", label, len(row), len(want), row)
	}
}

// TestBuildSubagents_ShapeAndKeys: subagents tab has the type
// roll-up plus the per-invocation rows AND the main/sidechain split
// the "Main vs sidechain" subtab consumes — the legacy SSR'd inline
// blob exposed those separately as `main`/`sidechain` but the SPA's
// Subagents tab is the only consumer, so they ride alongside the rest
// of the subagents data here.
func TestBuildSubagents_ShapeAndKeys(t *testing.T) {
	a := payloadSetup(t)
	p := BuildSubagents(a)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal SubagentsPayload: %v", err)
	}
	wantTopLevel(t, got, []string{
		"by_subagent",
		"agent_invocations",
		"main",
		"sidechain",
		"main_side_cost",
	})
}

// TestBuildSubagents_MainSideCostIsMainPlusSide: the shipped MainSideCost
// must equal the literal Main.Cost + Sidechain.Cost sum so JS reading it
// is byte-identical to the `main.cost + side.cost` it replaces — NOT
// a.Totals().CostUSD, which sums in a different order.
func TestBuildSubagents_MainSideCostIsMainPlusSide(t *testing.T) {
	a := payloadSetup(t)
	p := BuildSubagents(a)
	if got, want := p.MainSideCost, p.Main.Cost+p.Sidechain.Cost; got != want {
		t.Errorf("MainSideCost = %v, want Main.Cost+Sidechain.Cost = %v", got, want)
	}
}

// TestBuildSubagents_MainSidechainMatchesAggregator: the Main and
// Sidechain fields must reflect the aggregator's MainTotals() and
// SidechainTotals() exactly — the SPA's mainside table reads cost,
// turns, and tokens.CacheReadTokens directly.
func TestBuildSubagents_MainSidechainMatchesAggregator(t *testing.T) {
	a := payloadSetup(t)
	p := BuildSubagents(a)

	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()

	if p.Main.Cost != mainCost {
		t.Errorf("main.cost: got %v, want %v", p.Main.Cost, mainCost)
	}
	if p.Main.Turns != mainTurns {
		t.Errorf("main.turns: got %v, want %v", p.Main.Turns, mainTurns)
	}
	if p.Main.Tokens != mainTok {
		t.Errorf("main.tokens: got %+v, want %+v", p.Main.Tokens, mainTok)
	}
	if p.Sidechain.Cost != sideCost {
		t.Errorf("sidechain.cost: got %v, want %v", p.Sidechain.Cost, sideCost)
	}
	if p.Sidechain.Turns != sideTurns {
		t.Errorf("sidechain.turns: got %v, want %v", p.Sidechain.Turns, sideTurns)
	}
	if p.Sidechain.Tokens != sideTok {
		t.Errorf("sidechain.tokens: got %+v, want %+v", p.Sidechain.Tokens, sideTok)
	}
}

// TestBuildAnomalies_ShapeAndKeys: anomalies endpoint is a thin
// wrapper over Aggregator.Anomalies() — a single top-level array
// under "anomalies" so the SPA can swap the cached payload by key.
func TestBuildAnomalies_ShapeAndKeys(t *testing.T) {
	a := payloadSetup(t)
	p := BuildAnomalies(a)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal AnomaliesPayload: %v", err)
	}
	wantTopLevel(t, got, []string{"anomalies"})
}

// TestBuildTrends_Dim covers every supported series dimension.
// The flat shape — {period, dim, series} — lets the SPA route a
// single fetch per chart without parsing branch-by-dim. Totals are
// served by /overview so they are intentionally not a ?dim value.
func TestBuildTrends_Dim(t *testing.T) {
	a := payloadSetup(t)
	cases := []string{"model", "project", "session", "tool", "subagent"}
	for _, dim := range cases {
		t.Run(dim, func(t *testing.T) {
			p, err := BuildTrends(a, dim)
			if err != nil {
				t.Fatalf("BuildTrends(%q): %v", dim, err)
			}
			got, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("marshal TrendsPayload(%q): %v", dim, err)
			}
			wantTopLevel(t, got, []string{"period", "dim", "series"})
		})
	}
}

// TestBuildTrends_UnknownDim must error rather than silently
// returning an empty series — a typo'd ?dim should surface as a
// 400 at the handler, not as a misleading empty chart.
func TestBuildTrends_UnknownDim(t *testing.T) {
	a := payloadSetup(t)
	if _, err := BuildTrends(a, "bogus"); err == nil {
		t.Fatalf("expected error for unknown dim, got nil")
	}
}

// wantTopLevel asserts the JSON object has exactly the given top-
// level keys — no missing, no extras. Catches both content drift
// (renamed key) and accidental field bleed (a private struct's
// unsealed JSON tag leaking into the response).
func wantTopLevel(t *testing.T, raw []byte, want []string) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("payload not valid JSON: %v; raw: %s", err, string(raw))
	}
	got := make([]string, 0, len(obj))
	for k := range obj {
		got = append(got, k)
	}
	sort.Strings(got)
	wantCopy := append([]string(nil), want...)
	sort.Strings(wantCopy)
	missing := diffStringSets(wantCopy, got)
	extra := diffStringSets(got, wantCopy)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("top-level keys mismatch\n got:     %v\n want:    %v\n missing: %v\n extra:   %v",
			got, wantCopy, missing, extra)
	}
}

func diffStringSets(a, b []string) []string {
	set := map[string]struct{}{}
	for _, s := range b {
		set[s] = struct{}{}
	}
	var out []string
	for _, s := range a {
		if _, ok := set[s]; !ok {
			out = append(out, s)
		}
	}
	return out
}

// addContextPattern feeds a one Bash call of pattern whose result the next
// turn writes as writeTokens of cache, then re-reads once.
func addContextPattern(a *aggregate.Aggregator, file, pattern string, writeTokens int, ts time.Time) {
	id := file + pattern
	a.Add(parse.Turn{SessionID: file, MessageID: id + "-a", Model: "claude-opus-4-7", Timestamp: ts, SourceFile: file,
		ToolUses: []parse.ToolUse{{ID: id, Name: "Bash", Detail: pattern, Input: pattern + " x"}}})
	a.Add(parse.Turn{SessionID: file, MessageID: id + "-b", Model: "claude-opus-4-7", Timestamp: ts.Add(time.Second), SourceFile: file,
		Usage: parse.Usage{CacheCreate5mTokens: writeTokens}})
	a.Add(parse.Turn{SessionID: file, MessageID: id + "-c", Model: "claude-opus-4-7", Timestamp: ts.Add(2 * time.Second), SourceFile: file,
		Usage: parse.Usage{CacheReadTokens: writeTokens}})
	a.AddToolResults([]parse.ToolResult{{ToolUseID: id, OutputBytes: 100}})
}

// TestBuildOverview_HotspotTrend: each hotspot carries its cost over the
// previous equal window — up, down, or new when it cost nothing there.
func TestBuildOverview_HotspotTrend(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	cur, prev := aggregate.New(prices), aggregate.New(prices)
	addContextPattern(cur, "c1", "go test", 200_000, t0)
	addContextPattern(cur, "c2", "git log", 50_000, t0)
	addContextPattern(cur, "c3", "rg", 100_000, t0)
	addContextPattern(prev, "p1", "go test", 100_000, t0.Add(-7*24*time.Hour))
	addContextPattern(prev, "p2", "git log", 100_000, t0.Add(-7*24*time.Hour))

	byTitle := map[string]HotspotForJSON{}
	for _, h := range BuildOverview(cur, prev).Hotspots {
		byTitle[h.Title] = h
	}
	up, down, fresh := byTitle["Bash: `go test`"], byTitle["Bash: `git log`"], byTitle["Bash: `rg`"]
	if up.TrendPct == nil || up.PrevCostUSD == nil || *up.TrendPct < 99 || *up.TrendPct > 101 {
		t.Errorf("go test trend = %v (prev %v), want ≈ +100%%", up.TrendPct, up.PrevCostUSD)
	}
	if down.TrendPct == nil || *down.TrendPct > -49 || *down.TrendPct < -51 {
		t.Errorf("git log trend = %v, want ≈ -50%%", down.TrendPct)
	}
	if fresh.PrevCostUSD == nil || *fresh.PrevCostUSD != 0 || fresh.TrendPct != nil {
		t.Errorf("rg prev = %v trend = %v, want prev 0 and no trend (new)", fresh.PrevCostUSD, fresh.TrendPct)
	}

	raw, err := json.Marshal(up)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"prev_cost_usd"`, `"trend_pct"`, `"examples"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("hotspot JSON %s missing %s", raw, key)
		}
	}

	for _, h := range BuildOverview(cur, nil).Hotspots {
		if h.PrevCostUSD != nil || h.TrendPct != nil {
			t.Errorf("%s: trend set with no previous window", h.Title)
		}
	}
}

// TestBuildOverview_HotspotExampleProvider: every hotspot example in the
// Overview JSON names the provider whose session it came from.
func TestBuildOverview_HotspotExampleProvider(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := aggregate.New(prices)
	addContextPattern(a, "claude.jsonl", "go test", 200_000, t0)
	a.Add(parse.Turn{SessionID: "cx", MessageID: "cx-a", Provider: parse.ProviderCodex, Model: "gpt-6-astra", Timestamp: t0, SourceFile: "cx.jsonl",
		ToolUses: []parse.ToolUse{{ID: "cx-tu", Name: "Bash", Detail: "rg", Input: "rg x"}}})
	a.Add(parse.Turn{SessionID: "cx", MessageID: "cx-b", Provider: parse.ProviderCodex, Model: "gpt-6-astra", Timestamp: t0.Add(time.Second), SourceFile: "cx.jsonl",
		Usage: parse.Usage{InputTokens: 200_000}})
	a.AddToolResults([]parse.ToolResult{{ToolUseID: "cx-tu", OutputBytes: 100}})

	raw, err := json.Marshal(BuildOverview(a, nil).Hotspots)
	if err != nil {
		t.Fatal(err)
	}
	var hs []struct {
		Title    string
		Examples []map[string]any `json:"examples"`
	}
	if err := json.Unmarshal(raw, &hs); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Bash: `go test`": "claude-code", "Bash: `rg`": "codex"}
	seen := 0
	for _, h := range hs {
		p, ok := want[h.Title]
		if !ok {
			continue
		}
		seen++
		if len(h.Examples) == 0 {
			t.Errorf("%s: no examples", h.Title)
		}
		for _, e := range h.Examples {
			if e["provider"] != p {
				t.Errorf("%s example provider = %v, want %q in %s", h.Title, e["provider"], p, raw)
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d of %v in %s", seen, want, raw)
	}
}

// TestBuildOverview_CacheWriteTrendAcrossThreshold: a session under the
// repeated-cache-write floor in the previous window still supplies that
// window's cost, so crossing the floor reads as a rise, not "new".
func TestBuildOverview_CacheWriteTrendAcrossThreshold(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	cur, prev := aggregate.New(prices), aggregate.New(prices)
	prev.Add(parse.Turn{SessionID: "s", MessageID: "p", Model: "claude-opus-4-7", Timestamp: t0.Add(-7 * 24 * time.Hour), SourceFile: "/p/s.jsonl",
		Usage: parse.Usage{CacheCreate5mTokens: 900_000}})
	cur.Add(parse.Turn{SessionID: "s", MessageID: "c", Model: "claude-opus-4-7", Timestamp: t0, SourceFile: "/p/s.jsonl",
		Usage: parse.Usage{CacheCreate5mTokens: 1_200_000}})

	var h *HotspotForJSON
	for _, x := range BuildOverview(cur, prev).Hotspots {
		if x.Kind == aggregate.HotspotRepeatedCacheWrite {
			x := x
			h = &x
		}
	}
	if h == nil {
		t.Fatal("no repeated_cache_write card")
	}
	if h.PrevCostUSD == nil || *h.PrevCostUSD <= 0 {
		t.Fatalf("prev_cost_usd = %v, want the previous window's write cost", h.PrevCostUSD)
	}
	if h.TrendPct == nil || *h.TrendPct < 33 || *h.TrendPct > 34 {
		t.Errorf("trend_pct = %v, want ≈ +33%%", h.TrendPct)
	}
}
