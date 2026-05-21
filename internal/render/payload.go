package render

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// buildHotspotsForJSON resolves the aggregator's top hotspots into
// the renderable hotspotForJSON shape — same data plus a baked-in
// LLM prompt and an optional cross-link key. Shared by BuildPayload
// (for the JSON island) and HTMLWithOptions (for the SSR hotspot
// cards) so the two surfaces stay in lockstep automatically.
func buildHotspotsForJSON(a *aggregate.Aggregator) []hotspotForJSON {
	raw := a.Hotspots(10)
	out := make([]hotspotForJSON, 0, len(raw))
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
		out = append(out, hotspotForJSON{
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

// BuildPayload returns the JSON bytes that the HTML report consumes as
// its data island. Today the same bytes are embedded inline in the
// rendered HTML via <script id="claudit-data" type="application/json">;
// the serve daemon also serves them at /_claudit/data.json so the page
// can paint without waiting for the data.
//
// Returns ctx.Err() early when the caller (typically a disconnected
// HTTP client) cancels before json.Marshal completes.
func BuildPayload(ctx context.Context, a *aggregate.Aggregator, opts HTMLOptions) ([]byte, error) {
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()

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
		Totals:            a.Totals(),
		ByModel:           a.ByModel(),
		ByProject:         a.ByProject(),
		ByTool:            a.ByTool(),
		ByToolDetail:      a.ByToolDetail(),
		BySkill:           a.BySkill(),
		Main:              sidePart{Cost: mainCost, Turns: mainTurns, Tokens: mainTok},
		Sidechain:         sidePart{Cost: sideCost, Turns: sideTurns, Tokens: sideTok},
		BySubagent:        a.BySubagent(),
		AgentInvocations:  a.AgentInvocations(""),
		UnknownModels:     a.UnknownModels(),
		Period:            a.Period(),
		TrendTotals:       a.TrendTotals(),
		TrendByModel:      a.TrendByModel(),
		TrendByProject:    a.TrendByProject(),
		TrendByTool:       a.TrendByTool(),
		TrendBySession:    a.TrendBySession(),
		TrendBySubagent:   a.TrendBySubagent(),
		OverallHitRatio:   a.OverallHitRatio(),
		CacheByProject:    a.CacheByProject(),
		CacheBySession:    a.CacheBySession(),
		CacheBySubagent:   a.CacheBySubagent(),
		CacheByInvocation: a.CacheByInvocation(),
		ByPrompt:          a.ByPrompt(),
		Anomalies:         a.Anomalies(),
		PromptKeys:        promptKeysFromTimelines(opts.SessionTimelines),
		Forecast:          a.MonthEndForecast(time.Now()),
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
