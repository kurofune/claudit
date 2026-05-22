package render

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// StaticBundle is the offline data source the static HTML report
// embeds for the SPA bundle. Each key mirrors a /_claudit/api/*
// endpoint so api.js's offline branch can route a path to a
// pre-built section payload without a fetch round-trip.
//
// Snapshot is a tiny metadata stub (generation 0, no live host)
// kept for shape parity with the serve-mode endpoint — the static
// surface has no concept of generation but the SPA reads the field
// to display a "data freshness" indicator.
//
// SessionTimelines is keyed by session ID so /sessions/{id}/timeline
// resolves to a direct map lookup. The full prompt+turn tree per
// session is the biggest contributor to bundle size on busy
// corpora; that's the well-understood cost of "self-contained for
// offline." Trim is the user's existing knob via --hotspots /
// --sessions / -since flags upstream of HTMLWithOptions.
type StaticBundle struct {
	Snapshot         SnapshotPayload                      `json:"snapshot"`
	Overview         OverviewPayload                      `json:"overview"`
	Cost             CostPayload                          `json:"cost"`
	Cache            CachePayload                         `json:"cache"`
	Tools            ToolsPayload                         `json:"tools"`
	Subagents        SubagentsPayload                     `json:"subagents"`
	Sessions         SessionsPayload                      `json:"sessions"`
	Anomalies        AnomaliesPayload                     `json:"anomalies"`
	Trends           map[string]TrendsPayload             `json:"trends"`
	SessionTimelines map[string]aggregate.SessionTimeline `json:"session_timelines"`
	PromptKeys       []string                             `json:"prompt_keys"`
}

// SnapshotPayload is the metadata stub the static report ships so
// the SPA's snapshot fetch never 404s offline. Mirrors the shape
// /_claudit/api/snapshot returns in serve mode minus the host
// field (there is no host for a downloaded file).
type SnapshotPayload struct {
	Generation  int64     `json:"generation"`
	LastUpdated time.Time `json:"last_updated"`
	FileCount   int       `json:"file_count"`
	TurnCount   int       `json:"turn_count"`
	Malformed   int       `json:"malformed"`
}

// trendDims are the dimensions the SPA fetches at runtime. Listed
// in one place so a new dim added to BuildTrends shows up offline
// too — adding it here is the only step.
var trendDims = []string{"model", "project", "session", "tool", "subagent"}

// promptKeysFromTimelines returns the deduplicated set of non-empty
// PromptTimeline.Key values across all sessions, in first-occurrence
// order. The SPA uses this slice to check hotspot cross-link
// availability in O(1) without needing the full session_timelines
// payload.
//
// Returns an empty (non-nil) slice when no usable keys exist so
// json.Marshal emits "[]" rather than "null" — keeps the JS
// consumer's `for (const k of D.prompt_keys)` loop simple.
func promptKeysFromTimelines(sessions []aggregate.SessionTimeline) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, s := range sessions {
		for _, p := range s.Prompts {
			if p.Key == "" {
				continue
			}
			if _, ok := seen[p.Key]; ok {
				continue
			}
			seen[p.Key] = struct{}{}
			out = append(out, p.Key)
		}
	}
	return out
}

// BuildStaticBundle composes every section the SPA needs to render
// offline. Called only from the static one-shot render path; serve
// mode keeps using the per-endpoint handlers + BuildPayload for the
// legacy /_claudit/data.json prewarm.
//
// Returns ctx.Err() early if the caller cancels before json.Marshal
// — large corpora produce multi-MB output and the marshal step is
// the only meaningful CPU cost here.
func BuildStaticBundle(ctx context.Context, a *aggregate.Aggregator, opts HTMLOptions) ([]byte, error) {
	trends := make(map[string]TrendsPayload, len(trendDims))
	for _, dim := range trendDims {
		p, err := BuildTrends(a, dim)
		if err != nil {
			return nil, fmt.Errorf("BuildTrends(%q): %w", dim, err)
		}
		trends[dim] = p
	}

	timelines := make(map[string]aggregate.SessionTimeline, len(opts.SessionTimelines))
	for _, s := range opts.SessionTimelines {
		timelines[s.SessionID] = s
	}

	bundle := StaticBundle{
		Snapshot: SnapshotPayload{
			Generation:  0,
			LastUpdated: a.Totals().Last,
			FileCount:   0,
			TurnCount:   a.Totals().Turns,
			Malformed:   0,
		},
		Overview:         BuildOverview(a),
		Cost:             BuildCost(a),
		Cache:            BuildCache(a),
		Tools:            BuildTools(a),
		Subagents:        BuildSubagents(a),
		Sessions:         BuildSessions(opts.SessionTimelines),
		Anomalies:        BuildAnomalies(a),
		Trends:           trends,
		SessionTimelines: timelines,
		PromptKeys:       promptKeysFromTimelines(opts.SessionTimelines),
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(bundle)
}
