package render

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kurofune/claudit/internal/aggregate"
)

// TestBuildStaticBundle_HasAllAPIKeys: the static report's inline
// blob is the SPA's offline data source. It must carry every key
// the SPA's api.js fetches at runtime, so a downloaded report can
// render every tab without a server.
func TestBuildStaticBundle_HasAllAPIKeys(t *testing.T) {
	a := htmlSetup(t)
	st := []aggregate.SessionTimeline{{
		SessionID: "sess-a",
		Prompts: []aggregate.PromptTimeline{
			{UUID: "u1", Key: "k1", Text: "hi"},
		},
	}}
	raw, err := BuildStaticBundle(context.Background(), a, HTMLOptions{SessionTimelines: st})
	if err != nil {
		t.Fatalf("BuildStaticBundle: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v; raw=%s", err, raw)
	}
	for _, want := range []string{"snapshot", "overview", "cost", "cache", "tokens", "tools", "subagents", "sessions", "anomalies", "trends", "session_timelines"} {
		if _, ok := m[want]; !ok {
			t.Errorf("static bundle missing %q key (keys=%v)", want, keys(m))
		}
	}
}

// TestBuildStaticBundle_SessionTimelinesKeyedByID: per-session
// timelines live under session_timelines[sessionID] so api.js's
// offline handler can route /sessions/{id}/timeline to a direct
// lookup.
func TestBuildStaticBundle_SessionTimelinesKeyedByID(t *testing.T) {
	a := htmlSetup(t)
	st := []aggregate.SessionTimeline{
		{SessionID: "sess-a", Prompts: []aggregate.PromptTimeline{{UUID: "u1", Key: "k1", Text: "hi"}}},
		{SessionID: "sess-b", Prompts: []aggregate.PromptTimeline{{UUID: "u2", Key: "k2", Text: "yo"}}},
	}
	raw, err := BuildStaticBundle(context.Background(), a, HTMLOptions{SessionTimelines: st})
	if err != nil {
		t.Fatalf("BuildStaticBundle: %v", err)
	}
	var m struct {
		SessionTimelines map[string]json.RawMessage `json:"session_timelines"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m.SessionTimelines["sess-a"]; !ok {
		t.Errorf("session_timelines missing sess-a (keys=%v)", mapKeys(m.SessionTimelines))
	}
	if _, ok := m.SessionTimelines["sess-b"]; !ok {
		t.Errorf("session_timelines missing sess-b (keys=%v)", mapKeys(m.SessionTimelines))
	}
}

// TestBuildStaticBundle_TrendsKeyedByDim: the SPA fetches
// /trends?dim=model, dim=project, etc. The offline equivalent is
// data.trends[dim] returning the {period, dim, series} shape.
func TestBuildStaticBundle_TrendsKeyedByDim(t *testing.T) {
	a := htmlSetup(t)
	raw, err := BuildStaticBundle(context.Background(), a, HTMLOptions{})
	if err != nil {
		t.Fatalf("BuildStaticBundle: %v", err)
	}
	var m struct {
		Trends map[string]struct {
			Dim    string         `json:"dim"`
			Period string         `json:"period"`
			Series map[string]any `json:"series"`
		} `json:"trends"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, dim := range []string{"model", "project", "session", "tool", "subagent"} {
		entry, ok := m.Trends[dim]
		if !ok {
			t.Errorf("trends missing dim %q", dim)
			continue
		}
		if entry.Dim != dim {
			t.Errorf("trends[%q].dim = %q, want %q", dim, entry.Dim, dim)
		}
	}
}

// TestBuildStaticBundle_OverviewSectionMatchesBuildOverview: the
// section-keyed blob must equal BuildOverview() so the static
// surface and serve mode's /_claudit/api/overview render the
// exact same Overview tab.
func TestBuildStaticBundle_OverviewSectionMatchesBuildOverview(t *testing.T) {
	a := htmlSetup(t)
	raw, err := BuildStaticBundle(context.Background(), a, HTMLOptions{})
	if err != nil {
		t.Fatalf("BuildStaticBundle: %v", err)
	}
	var m struct {
		Overview json.RawMessage `json:"overview"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want, err := json.Marshal(BuildOverview(a))
	if err != nil {
		t.Fatalf("marshal BuildOverview: %v", err)
	}
	// Forecast carries a wall-clock as_of that drifts between calls
	// — normalize before comparing to keep the rest of the struct
	// in the assertion.
	got := normalizeForecast(t, m.Overview)
	want = normalizeForecast(t, want)
	if !jsonBytesEqual(got, want) {
		t.Errorf("overview section drift:\n got:  %s\n want: %s", got, want)
	}
}

// TestBuildStaticBundle_CostSectionMatchesBuildCost: same contract
// as Overview — the static surface's Cost tab must paint with the
// same bytes serve mode's /api/cost returns.
func TestBuildStaticBundle_CostSectionMatchesBuildCost(t *testing.T) {
	a := htmlSetup(t)
	raw, err := BuildStaticBundle(context.Background(), a, HTMLOptions{})
	if err != nil {
		t.Fatalf("BuildStaticBundle: %v", err)
	}
	var m struct {
		Cost json.RawMessage `json:"cost"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want, err := json.Marshal(BuildCost(a))
	if err != nil {
		t.Fatalf("marshal BuildCost: %v", err)
	}
	if !jsonBytesEqual(m.Cost, want) {
		t.Errorf("cost section drift:\n got:  %s\n want: %s", m.Cost, want)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func jsonBytesEqual(a, b []byte) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	ja, _ := json.Marshal(av)
	jb, _ := json.Marshal(bv)
	return string(ja) == string(jb)
}

func normalizeForecast(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	if f, ok := obj["forecast"].(map[string]any); ok {
		for _, k := range []string{"as_of", "days_elapsed", "daily_rate_usd", "projected_month_end_usd"} {
			if _, ok := f[k]; ok {
				f[k] = nil
			}
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return out
}
