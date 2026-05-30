package render

import (
	"context"
	"encoding/json"
	"sort"
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
	a := htmlSetup(t)
	p := BuildOverview(a)
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
	if got := BuildOverview(a).Period; got != aggregate.PeriodHour {
		t.Errorf("Period = %q, want hour", got)
	}
}

// TestBuildOverview_HitRatioMatchesAggregator: the overview's headline
// cache hit ratio must equal the aggregator's OverallHitRatio() so the
// landing tab's cache stat matches the cache tab exactly.
func TestBuildOverview_HitRatioMatchesAggregator(t *testing.T) {
	a := htmlSetup(t)
	if got, want := BuildOverview(a).OverallHitRatio, a.OverallHitRatio(); got != want {
		t.Errorf("OverallHitRatio = %v, want %v", got, want)
	}
}

// TestBuildCost_ShapeAndKeys: cost tab covers by_model, by_project,
// by_skill, by_prompt — every "where did the money go" lens.
func TestBuildCost_ShapeAndKeys(t *testing.T) {
	a := htmlSetup(t)
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
	})
}

// TestBuildCost_TotalCostMatchesAggregator: the cost tab's headline
// total must equal the aggregator's Totals().CostUSD exactly so JS can
// read it directly instead of re-summing per-row costs.
func TestBuildCost_TotalCostMatchesAggregator(t *testing.T) {
	a := htmlSetup(t)
	if got, want := BuildCost(a).TotalCostUSD, a.Totals().CostUSD; got != want {
		t.Errorf("TotalCostUSD = %v, want %v", got, want)
	}
}

// TestBuildCache_ShapeAndKeys: cache tab covers the four
// cache-efficiency dimensions plus the headline overall ratio.
func TestBuildCache_ShapeAndKeys(t *testing.T) {
	a := htmlSetup(t)
	p := BuildCache(a)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal CachePayload: %v", err)
	}
	wantTopLevel(t, got, []string{
		"overall_hit_ratio",
		"cache_by_project",
		"cache_by_session",
		"cache_by_subagent",
		"cache_by_invocation",
		"total_miss",
	})
}

// TestBuildCache_TotalMissMatchesProjectSum proves the shipped TotalMiss
// is byte-equivalent to the JS reduce it replaces: Σ over cache_by_project
// rows of .Miss. These are int64 sums, so they must be EXACTLY equal.
func TestBuildCache_TotalMissMatchesProjectSum(t *testing.T) {
	a := htmlSetup(t)
	var want int64
	for _, row := range a.CacheByProject() {
		want += row.Miss
	}
	if got := BuildCache(a).TotalMiss; got != want {
		t.Errorf("TotalMiss = %d, want Σ CacheByProject().Miss = %d", got, want)
	}
}

// TestBuildTools_ShapeAndKeys: tools tab has the parent buckets plus
// the per-tool drill-down map.
func TestBuildTools_ShapeAndKeys(t *testing.T) {
	a := htmlSetup(t)
	p := BuildTools(a)
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal ToolsPayload: %v", err)
	}
	wantTopLevel(t, got, []string{
		"by_tool",
		"by_tool_detail",
	})
}

// TestBuildSubagents_ShapeAndKeys: subagents tab has the type
// roll-up plus the per-invocation rows AND the main/sidechain split
// the "Main vs sidechain" subtab consumes — the legacy SSR'd inline
// blob exposed those separately as `main`/`sidechain` but the SPA's
// Subagents tab is the only consumer, so they ride alongside the rest
// of the subagents data here.
func TestBuildSubagents_ShapeAndKeys(t *testing.T) {
	a := htmlSetup(t)
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
	a := htmlSetup(t)
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
	a := htmlSetup(t)
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
	a := htmlSetup(t)
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
	a := htmlSetup(t)
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
	a := htmlSetup(t)
	if _, err := BuildTrends(a, "bogus"); err == nil {
		t.Fatalf("expected error for unknown dim, got nil")
	}
}

// TestBuildSessions_ShapeAndKeys: the sessions LIST endpoint omits
// per-prompt timelines — those are fetched lazily per-session in
// phase 7. Locking the shape now so the API contract is stable
// before the SPA ships.
func TestBuildSessions_ShapeAndKeys(t *testing.T) {
	timelines, err := buildTimelinesForTest(t)
	if err != nil {
		t.Fatalf("build timelines: %v", err)
	}
	p := BuildSessions(timelines, len(timelines))
	got, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal SessionsPayload: %v", err)
	}
	wantTopLevel(t, got, []string{"sessions", "total_sessions"})

	// Each session row must carry totals but NOT the full prompts
	// slice (that's the lazy-fetch payload). Probe one row.
	var unmarshaled SessionsPayload
	if err := json.Unmarshal(got, &unmarshaled); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(unmarshaled.Sessions) == 0 {
		t.Fatal("expected at least one session row from fixture")
	}
	// The session list payload must not carry per-prompt drill-down
	// data — that's what makes the list cheap to fetch.
	if !sessionsListOmitsPrompts(got) {
		t.Errorf("SessionsPayload accidentally carries per-prompt data; should be timeline-free")
	}
}

// TestBuildSessions_TotalSessionsReconcilesWithOverviewTile captures
// the guarantee the total_sessions field exists to provide: the
// sidebar's full session count (SessionsPayload.TotalSessions) must
// equal the Overview tile's session total (OverviewPayload.Totals.
// Sessions) for the same aggregator. Before this field, the sidebar
// rendered the CAPPED len(Sessions) under the same "Sessions" label as
// the tile's full count — two correct numbers that read as a
// contradiction. This test locks the two back into agreement and
// asserts the full count is never less than the (possibly capped)
// shown count.
func TestBuildSessions_TotalSessionsReconcilesWithOverviewTile(t *testing.T) {
	// The reconciliation only holds when the aggregator and the
	// timeline slice describe the SAME corpus. htmlSetup feeds turns
	// with no SessionID (which the aggregator does not count as a
	// session), while buildTimelinesForTest feeds a SessionID-bearing
	// turn — so we build a fresh aggregator from a matching
	// SessionID-bearing turn here, mirroring the timeline fixture.
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	a := aggregate.New(prices)
	a.Add(parse.Turn{
		Model:     "claude-opus-4-7",
		CWD:       "/p/x",
		SessionID: "fixture-session-1",
		UUID:      "turn-uuid-1",
		Usage:     parse.Usage{InputTokens: 1_000_000, OutputTokens: 200_000},
		Timestamp: t0,
	})

	timelines, err := buildTimelinesForTest(t)
	if err != nil {
		t.Fatalf("build timelines: %v", err)
	}
	sessions := BuildSessions(timelines, a.Totals().Sessions)
	overview := BuildOverview(a)

	if got, want := sessions.TotalSessions, overview.Totals.Sessions; got != want {
		t.Errorf("TotalSessions = %d, want overview tile's %d", got, want)
	}
	if sessions.TotalSessions < len(sessions.Sessions) {
		t.Errorf("TotalSessions (%d) must be >= len(Sessions) (%d): the full window count is never less than the shown count",
			sessions.TotalSessions, len(sessions.Sessions))
	}
}

// TestBuildSessions_CarriesEntrypointAndFirstPrompt: each session row must
// carry its origin (cli vs sdk-cli) and a short preview of its kickoff prompt
// so the Sessions list is scannable and splittable without fetching every
// per-session timeline.
func TestBuildSessions_CarriesEntrypointAndFirstPrompt(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	user := parse.UserMessage{UUID: "p1", SessionID: "s1", Text: "drive the dungeon monster brain", Timestamp: t0}
	turn := parse.Turn{
		Model:      "claude-opus-4-7",
		CWD:        "/p/x",
		SessionID:  "s1",
		UUID:       "a1",
		ParentUUID: "p1",
		Entrypoint: "sdk-cli",
		Usage:      parse.Usage{InputTokens: 1000, OutputTokens: 100},
		Timestamp:  t0.Add(time.Minute),
	}
	timelines, err := aggregate.BuildSessionTimelines(
		context.Background(),
		[]parse.Turn{turn}, []parse.UserMessage{user}, nil,
		prices, aggregate.Filter{}, aggregate.SessionTimelinesOptions{MaxPromptChars: 2000})
	if err != nil {
		t.Fatalf("BuildSessionTimelines: %v", err)
	}

	p := BuildSessions(timelines, len(timelines))
	if len(p.Sessions) == 0 {
		t.Fatal("expected at least one session row")
	}
	row := p.Sessions[0]
	if row.Entrypoint != "sdk-cli" {
		t.Errorf("Entrypoint = %q, want sdk-cli", row.Entrypoint)
	}
	if row.FirstPrompt != "drive the dungeon monster brain" {
		t.Errorf("FirstPrompt = %q, want the kickoff prompt", row.FirstPrompt)
	}

	// The fields must serialize under the documented JSON keys.
	got, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	for _, key := range []string{`"entrypoint":`, `"first_prompt":`} {
		if !containsKey(got, key) {
			t.Errorf("session row missing JSON key %s; raw: %s", key, got)
		}
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

// sessionsListOmitsPrompts probes the rendered JSON for the
// "prompts" key — the per-session timeline payload that the list
// endpoint MUST NOT carry. Cheap: marshaled output never contains
// "prompts" as a key when SessionSummary lacks the field.
func sessionsListOmitsPrompts(raw []byte) bool {
	return !containsKey(raw, `"prompts":`)
}

func containsKey(raw []byte, key string) bool {
	return indexOf(raw, []byte(key)) >= 0
}

func indexOf(haystack, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	if len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
