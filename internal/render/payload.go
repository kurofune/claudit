package render

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// buildHotspotsForJSON resolves the aggregator's top hotspots into
// the renderable HotspotForJSON shape — same data plus a baked-in
// LLM prompt and an optional cross-link key. Shared by BuildPayload
// (for the JSON island) and HTMLWithOptions (for the SSR hotspot
// cards) so the two surfaces stay in lockstep automatically.
func buildHotspotsForJSON(a *aggregate.Aggregator) []HotspotForJSON {
	raw := a.Hotspots(10)
	out := make([]HotspotForJSON, 0, len(raw))
	for _, h := range raw {
		prompt, err := HotspotPrompt(h)
		if err != nil {
			continue
		}
		var pk string
		if h.Kind == aggregate.HotspotPromptPattern && h.Context != nil {
			if v, ok := h.Context["key"].(string); ok {
				pk = v
			}
		}
		out = append(out, HotspotForJSON{
			Kind:       h.Kind,
			Title:      h.Title,
			CostUSD:    h.CostUSD,
			PctOfTotal: h.PctOfTotal,
			Prompt:     prompt,
			PromptKey:  pk,
		})
	}
	return out
}

// OverviewPayload is what /_claudit/api/overview returns. It bundles
// the umbrella set the SPA needs to paint the landing tab in one
// fetch — headline totals, the ranked hotspot stack, the totals trend
// line, the month-end forecast, and any unknown-model warnings.
//
// Phase 3 of the serve-API plan: this struct (and the matching
// per-tab structs below) is the single source of truth for "what
// each section's data looks like." The static report's inline blob
// (BuildPayload) and the served-mode API both walk through these
// builders so the two surfaces can't drift.
type OverviewPayload struct {
	Totals        aggregate.Totals       `json:"totals"`
	Hotspots      []HotspotForJSON       `json:"hotspots"`
	TrendTotals   []aggregate.TrendPoint `json:"trend_totals"`
	Forecast      aggregate.Forecast     `json:"forecast"`
	UnknownModels []string               `json:"unknown_models"`
}

// CostPayload backs /_claudit/api/cost — every "where did the money
// go" lens (model, project, skill, prompt) in one umbrella so the
// Cost tab paints with one fetch.
type CostPayload struct {
	ByModel   []aggregate.ModelBucket   `json:"by_model"`
	ByProject []aggregate.ProjectBucket `json:"by_project"`
	BySkill   []aggregate.SkillBucket   `json:"by_skill"`
	ByPrompt  []aggregate.PromptBucket  `json:"by_prompt"`
}

// CachePayload backs /_claudit/api/cache — the four cache-efficiency
// drill-downs plus the headline overall hit ratio.
type CachePayload struct {
	OverallHitRatio   float64              `json:"overall_hit_ratio"`
	CacheByProject    []aggregate.CacheRow `json:"cache_by_project"`
	CacheBySession    []aggregate.CacheRow `json:"cache_by_session"`
	CacheBySubagent   []aggregate.CacheRow `json:"cache_by_subagent"`
	CacheByInvocation []aggregate.CacheRow `json:"cache_by_invocation"`
}

// ToolsPayload backs /_claudit/api/tools — the parent ToolBucket
// rows plus the per-tool drill-down map (Bash · `git status`, Read ·
// `.go`, etc.).
type ToolsPayload struct {
	ByTool       []aggregate.ToolBucket              `json:"by_tool"`
	ByToolDetail map[string][]aggregate.DetailBucket `json:"by_tool_detail"`
}

// SubagentsPayload backs /_claudit/api/subagents — the subagent-type
// roll-up plus every individual invocation row.
type SubagentsPayload struct {
	BySubagent       []aggregate.SubagentBucket  `json:"by_subagent"`
	AgentInvocations []aggregate.AgentInvocation `json:"agent_invocations"`
}

// AnomaliesPayload backs /_claudit/api/anomalies — a single top-level
// array so the SPA can swap the cached payload as one unit.
type AnomaliesPayload struct {
	Anomalies []aggregate.Anomaly `json:"anomalies"`
}

// TrendsPayload backs /_claudit/api/trends. The dim field echoes the
// request so a SPA caller writing one fetch wrapper can route to the
// right chart without re-deriving the dimension from the request URL.
// Period is carried alongside so a "by week" view doesn't need a
// separate metadata fetch to render its X-axis.
type TrendsPayload struct {
	Period aggregate.Period                  `json:"period"`
	Dim    string                            `json:"dim"`
	Series map[string][]aggregate.TrendPoint `json:"series"`
}

// SessionSummary is one row in /_claudit/api/sessions. Carries the
// totals view of a session without the per-prompt Timeline body —
// that's the lazy-fetch payload served by
// /_claudit/api/sessions/{id}/timeline (Phase 7 of the SPA plan).
// Trimming Prompts here keeps the list endpoint to ~50KB for a busy
// corpus instead of the multi-MB blob the static report ships today.
type SessionSummary struct {
	SessionID string    `json:"session_id"`
	CWD       string    `json:"cwd"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	CostUSD   float64   `json:"cost_usd"`
	Turns     int       `json:"turns"`
}

// SessionsPayload backs /_claudit/api/sessions. The wrapper struct
// (vs a bare []SessionSummary) lets future fields (pagination
// cursor, redaction notice) land without an API-shape break.
type SessionsPayload struct {
	Sessions []SessionSummary `json:"sessions"`
}

// BuildOverview rolls the aggregator's landing-tab data into the
// umbrella struct the SPA fetches in one round trip.
func BuildOverview(a *aggregate.Aggregator) OverviewPayload {
	return OverviewPayload{
		Totals:        a.Totals(),
		Hotspots:      buildHotspotsForJSON(a),
		TrendTotals:   a.TrendTotals(),
		Forecast:      a.MonthEndForecast(time.Now()),
		UnknownModels: a.UnknownModels(),
	}
}

// BuildCost rolls the aggregator into the Cost-tab payload.
func BuildCost(a *aggregate.Aggregator) CostPayload {
	return CostPayload{
		ByModel:   a.ByModel(),
		ByProject: a.ByProject(),
		BySkill:   a.BySkill(),
		ByPrompt:  a.ByPrompt(),
	}
}

// BuildCache rolls the aggregator into the Cache-tab payload.
func BuildCache(a *aggregate.Aggregator) CachePayload {
	return CachePayload{
		OverallHitRatio:   a.OverallHitRatio(),
		CacheByProject:    a.CacheByProject(),
		CacheBySession:    a.CacheBySession(),
		CacheBySubagent:   a.CacheBySubagent(),
		CacheByInvocation: a.CacheByInvocation(),
	}
}

// BuildTools rolls the aggregator into the Tools-tab payload.
func BuildTools(a *aggregate.Aggregator) ToolsPayload {
	return ToolsPayload{
		ByTool:       a.ByTool(),
		ByToolDetail: a.ByToolDetail(),
	}
}

// BuildSubagents rolls the aggregator into the Subagents-tab payload.
// AgentInvocations is unfiltered — the SPA filters client-side; the
// type filter on the aggregator method is a holdover from the CLI's
// --subagent flag.
func BuildSubagents(a *aggregate.Aggregator) SubagentsPayload {
	return SubagentsPayload{
		BySubagent:       a.BySubagent(),
		AgentInvocations: a.AgentInvocations(""),
	}
}

// BuildAnomalies rolls the aggregator's flagged trend buckets into
// the wire-shape /_claudit/api/anomalies returns.
func BuildAnomalies(a *aggregate.Aggregator) AnomaliesPayload {
	return AnomaliesPayload{
		Anomalies: a.Anomalies(),
	}
}

// BuildTrends returns the per-key time series for the requested
// dimension. Returns an error on an unrecognized dim so the handler
// can surface a 400 — silently emitting an empty series would let a
// typo render as "no data" rather than as a wrong-URL signal.
//
// Totals are intentionally NOT a dim value here — those are part of
// /_claudit/api/overview. Splitting them keeps the trends endpoint
// purely per-key.
func BuildTrends(a *aggregate.Aggregator, dim string) (TrendsPayload, error) {
	var series map[string][]aggregate.TrendPoint
	switch dim {
	case "model":
		series = a.TrendByModel()
	case "project":
		series = a.TrendByProject()
	case "session":
		series = a.TrendBySession()
	case "tool":
		series = a.TrendByTool()
	case "subagent":
		series = a.TrendBySubagent()
	default:
		return TrendsPayload{}, fmt.Errorf("unknown trends dim %q (want one of model|project|session|tool|subagent)", dim)
	}
	return TrendsPayload{
		Period: a.Period(),
		Dim:    dim,
		Series: series,
	}, nil
}

// BuildSessions projects an already-built timeline slice into the
// summary-only payload the sessions LIST endpoint returns. Takes
// timelines (rather than rebuilding from scratch) so callers that
// already have them — e.g. the static report renderer — don't pay
// twice. The Prompts slice is intentionally dropped here; clients
// fetch it per-session via /_claudit/api/sessions/{id}/timeline.
func BuildSessions(timelines []aggregate.SessionTimeline) SessionsPayload {
	out := make([]SessionSummary, 0, len(timelines))
	for _, s := range timelines {
		out = append(out, SessionSummary{
			SessionID: s.SessionID,
			CWD:       s.CWD,
			StartedAt: s.StartedAt,
			EndedAt:   s.EndedAt,
			CostUSD:   s.CostUSD,
			Turns:     s.Turns,
		})
	}
	return SessionsPayload{Sessions: out}
}

// BuildPayload returns the JSON bytes that the static HTML report
// consumes as its data island. Today the same bytes are embedded
// inline in the rendered HTML via <script id="claudit-data"
// type="application/json">; the serve daemon also serves them at
// /_claudit/data.json so the page can paint without waiting for the
// data.
//
// Phase 3 of the serve-API plan: this is now structurally the union
// of the per-section payload builders above. Both the static report
// and the new /_claudit/api/* endpoints route through those builders,
// so a field added to (say) BuildCost shows up in both surfaces
// without manual sync.
//
// Hotspots are deliberately omitted from the static-report payload
// because the static surface SSRs the hotspot cards directly — see
// renderHotspotsHTML in html.go. The /api/overview endpoint DOES
// emit hotspots so the SPA can render them client-side.
//
// Returns ctx.Err() early when the caller (typically a disconnected
// HTTP client) cancels before json.Marshal completes.
func BuildPayload(ctx context.Context, a *aggregate.Aggregator, opts HTMLOptions) ([]byte, error) {
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()

	overview := BuildOverview(a)
	cost := BuildCost(a)
	cache := BuildCache(a)
	tools := BuildTools(a)
	sub := BuildSubagents(a)
	anomalies := BuildAnomalies(a)

	payload := struct {
		Totals            aggregate.Totals                    `json:"totals"`
		ByModel           []aggregate.ModelBucket             `json:"by_model"`
		ByProject         []aggregate.ProjectBucket           `json:"by_project"`
		ByTool            []aggregate.ToolBucket              `json:"by_tool"`
		ByToolDetail      map[string][]aggregate.DetailBucket `json:"by_tool_detail"`
		BySkill           []aggregate.SkillBucket             `json:"by_skill"`
		Main              sidePart                            `json:"main"`
		Sidechain         sidePart                            `json:"sidechain"`
		BySubagent        []aggregate.SubagentBucket          `json:"by_subagent"`
		AgentInvocations  []aggregate.AgentInvocation         `json:"agent_invocations"`
		UnknownModels     []string                            `json:"unknown_models"`
		Period            aggregate.Period                    `json:"period"`
		TrendTotals       []aggregate.TrendPoint              `json:"trend_totals"`
		TrendByModel      map[string][]aggregate.TrendPoint   `json:"trend_by_model"`
		TrendByProject    map[string][]aggregate.TrendPoint   `json:"trend_by_project"`
		TrendByTool       map[string][]aggregate.TrendPoint   `json:"trend_by_tool"`
		TrendBySession    map[string][]aggregate.TrendPoint   `json:"trend_by_session"`
		TrendBySubagent   map[string][]aggregate.TrendPoint   `json:"trend_by_subagent"`
		OverallHitRatio   float64                             `json:"overall_hit_ratio"`
		CacheByProject    []aggregate.CacheRow                `json:"cache_by_project"`
		CacheBySession    []aggregate.CacheRow                `json:"cache_by_session"`
		CacheBySubagent   []aggregate.CacheRow                `json:"cache_by_subagent"`
		CacheByInvocation []aggregate.CacheRow                `json:"cache_by_invocation"`
		ByPrompt          []aggregate.PromptBucket            `json:"by_prompt"`
		Anomalies         []aggregate.Anomaly                 `json:"anomalies"`
		PromptKeys        []string                            `json:"prompt_keys"`
		Forecast          aggregate.Forecast                  `json:"forecast"`
	}{
		Totals:            overview.Totals,
		ByModel:           cost.ByModel,
		ByProject:         cost.ByProject,
		ByTool:            tools.ByTool,
		ByToolDetail:      tools.ByToolDetail,
		BySkill:           cost.BySkill,
		Main:              sidePart{Cost: mainCost, Turns: mainTurns, Tokens: mainTok},
		Sidechain:         sidePart{Cost: sideCost, Turns: sideTurns, Tokens: sideTok},
		BySubagent:        sub.BySubagent,
		AgentInvocations:  sub.AgentInvocations,
		UnknownModels:     overview.UnknownModels,
		Period:            a.Period(),
		TrendTotals:       overview.TrendTotals,
		TrendByModel:      a.TrendByModel(),
		TrendByProject:    a.TrendByProject(),
		TrendByTool:       a.TrendByTool(),
		TrendBySession:    a.TrendBySession(),
		TrendBySubagent:   a.TrendBySubagent(),
		OverallHitRatio:   cache.OverallHitRatio,
		CacheByProject:    cache.CacheByProject,
		CacheBySession:    cache.CacheBySession,
		CacheBySubagent:   cache.CacheBySubagent,
		CacheByInvocation: cache.CacheByInvocation,
		ByPrompt:          cost.ByPrompt,
		Anomalies:         anomalies.Anomalies,
		PromptKeys:        promptKeysFromTimelines(opts.SessionTimelines),
		Forecast:          overview.Forecast,
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal report data: %w", err)
	}
	return data, nil
}
