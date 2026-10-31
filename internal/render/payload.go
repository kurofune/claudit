package render

import (
	"fmt"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// HotspotForJSON is the same data as aggregate.Hotspot plus the rendered
// prompt and the change against the previous window, so the front-end
// copies the prompt without re-rendering the template in JS. Used by
// OverviewPayload, which /_claudit/api/overview returns.
type HotspotForJSON struct {
	Kind       aggregate.HotspotKind `json:"kind"`
	Title      string                `json:"title"`
	CostUSD    float64               `json:"cost_usd"`
	PctOfTotal float64               `json:"pct_of_total"`
	Prompt     string                `json:"prompt"`
	Examples   []HotspotExample      `json:"examples"`
	// PrevCostUSD is this hotspot's cost over the previous window of equal
	// length, 0 when it had none there. Absent when no previous window was
	// compared (an unbounded window has no equal-length predecessor).
	PrevCostUSD *float64 `json:"prev_cost_usd,omitempty"`
	// TrendPct is 100 × (cost − prev) / prev. Absent when PrevCostUSD is
	// absent or 0 — the latter is a "new" hotspot.
	TrendPct *float64 `json:"trend_pct,omitempty"`
}

// HotspotExample is one invocation behind a hotspot card.
type HotspotExample struct {
	Input          string  `json:"input"`
	SessionID      string  `json:"session_id"`
	SourceFile     string  `json:"source_file"`
	ContextTokens  int64   `json:"context_tokens"`
	ContextCostUSD float64 `json:"context_cost_usd"`
}

// buildHotspotsForJSON resolves a's top hotspots into HotspotForJSON. With
// a non-nil prev (the aggregator over the previous equal window), each
// hotspot carries its cost there, joined by (Kind, Title).
func buildHotspotsForJSON(a, prev *aggregate.Aggregator) []HotspotForJSON {
	raw := a.Hotspots(10)
	var prevCosts map[aggregate.HotspotKey]float64
	if prev != nil {
		prevCosts = prev.HotspotCosts()
	}
	out := make([]HotspotForJSON, 0, len(raw))
	for _, h := range raw {
		prompt, err := HotspotPrompt(h)
		if err != nil {
			continue
		}
		examples := make([]HotspotExample, 0, len(h.Examples))
		for _, e := range h.Examples {
			examples = append(examples, HotspotExample(e))
		}
		j := HotspotForJSON{
			Kind:       h.Kind,
			Title:      h.Title,
			CostUSD:    h.CostUSD,
			PctOfTotal: h.PctOfTotal,
			Prompt:     prompt,
			Examples:   examples,
		}
		if prevCosts != nil {
			pc := prevCosts[aggregate.HotspotKey{Kind: h.Kind, Title: h.Title}]
			j.PrevCostUSD = &pc
			if pc > 0 {
				trend := 100 * (h.CostUSD - pc) / pc
				j.TrendPct = &trend
			}
		}
		out = append(out, j)
	}
	return out
}

// sidePart is the per-side roll-up the Subagents tab uses to show
// "Main vs sidechain" totals. Travels inside SubagentsPayload.
type sidePart struct {
	Cost   float64          `json:"cost"`
	Turns  int              `json:"turns"`
	Tokens aggregate.Tokens `json:"tokens"`
}

// OverviewPayload is what /_claudit/api/overview returns. It bundles
// the umbrella set the SPA needs to paint the landing tab in one
// fetch — headline totals, the ranked hotspot stack, the totals trend
// line, the month-end forecast, and any unknown-model warnings.
type OverviewPayload struct {
	Totals          aggregate.Totals         `json:"totals"`
	OverallHitRatio float64                  `json:"overall_hit_ratio"`
	TotalTokens     int64                    `json:"total_tokens"`
	Hotspots        []HotspotForJSON         `json:"hotspots"`
	TrendTotals     []aggregate.TrendPoint   `json:"trend_totals"`
	Forecast        aggregate.Forecast       `json:"forecast"`
	UnknownModels   []aggregate.UnknownModel `json:"unknown_models"`
	// Period is the bucket granularity of TrendTotals. The SPA reads it
	// to label the trend axis ("hour" → HH:MM) instead of hardcoding day.
	Period aggregate.Period `json:"period"`
}

// CostPayload backs /_claudit/api/cost — every "where did the money
// go" lens (model, project, skill, prompt) in one umbrella so the
// Cost tab paints with one fetch.
type CostPayload struct {
	TotalCostUSD float64                   `json:"total_cost_usd"`
	ByModel      []aggregate.ModelBucket   `json:"by_model"`
	ByProject    []aggregate.ProjectBucket `json:"by_project"`
	BySkill      []aggregate.SkillBucket   `json:"by_skill"`
	ByPrompt     []aggregate.PromptBucket  `json:"by_prompt"`
}

// ToolsPayload backs /_claudit/api/tools — the parent ToolBucket
// rows plus the per-tool drill-down map (Bash · `git status`, Read ·
// `.go`, etc.).
type ToolsPayload struct {
	ByTool       []aggregate.ToolBucket              `json:"by_tool"`
	ByToolDetail map[string][]aggregate.DetailBucket `json:"by_tool_detail"`
}

// SubagentsPayload backs /_claudit/api/subagents — the subagent-type
// roll-up plus every individual invocation row. Main and Sidechain
// carry the main-thread / sidechain totals fueling the "Main vs
// sidechain" subtab.
type SubagentsPayload struct {
	BySubagent       []aggregate.SubagentBucket  `json:"by_subagent"`
	AgentInvocations []aggregate.AgentInvocation `json:"agent_invocations"`
	Main             sidePart                    `json:"main"`
	Sidechain        sidePart                    `json:"sidechain"`
	MainSideCost     float64                     `json:"main_side_cost"`
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

// BuildOverview rolls the aggregator's landing-tab data into the
// umbrella struct the SPA fetches in one round trip. prev, when non-nil,
// aggregates the previous window of equal length for the hotspot trend.
func BuildOverview(a, prev *aggregate.Aggregator) OverviewPayload {
	return OverviewPayload{
		Totals:          a.Totals(),
		OverallHitRatio: a.OverallHitRatio(),
		TotalTokens:     a.Totals().Total(),
		Hotspots:        buildHotspotsForJSON(a, prev),
		TrendTotals:     a.TrendTotals(),
		Forecast:        a.MonthEndForecast(time.Now()),
		UnknownModels:   a.UnknownModels(),
		Period:          a.Period(),
	}
}

// BuildCost rolls the aggregator into the Cost-tab payload.
func BuildCost(a *aggregate.Aggregator) CostPayload {
	return CostPayload{
		TotalCostUSD: a.Totals().CostUSD,
		ByModel:      a.ByModel(),
		ByProject:    a.ByProject(),
		BySkill:      a.BySkill(),
		ByPrompt:     a.ByPrompt(),
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
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()
	return SubagentsPayload{
		BySubagent:       a.BySubagent(),
		AgentInvocations: a.AgentInvocations(""),
		Main:             sidePart{Cost: mainCost, Turns: mainTurns, Tokens: mainTok},
		Sidechain:        sidePart{Cost: sideCost, Turns: sideTurns, Tokens: sideTok},
		// Literal main+side sum (NOT a.Totals().CostUSD) so JS reading
		// main_side_cost is byte-identical to `main.cost + side.cost`.
		MainSideCost: mainCost + sideCost,
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
