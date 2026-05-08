package render

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"

	"github.com/nategross/claudit/internal/aggregate"
)

//go:embed report.html.tmpl
var htmlTemplate string

var htmlTpl = template.Must(template.New("report").Parse(htmlTemplate))

// hotspotForJSON is the same data as aggregate.Hotspot but with the
// pre-rendered LLM prompt baked in, so the front-end can copy it to the
// clipboard without re-rendering the template in JS.
type hotspotForJSON struct {
	Kind       aggregate.HotspotKind `json:"kind"`
	Title      string                `json:"title"`
	CostUSD    float64               `json:"cost_usd"`
	PctOfTotal float64               `json:"pct_of_total"`
	Prompt     string                `json:"prompt"`
}

// HTML writes a self-contained interactive HTML report to w. The page has
// inline CSS/JS, a global text filter, a min-cost threshold, sortable
// tables, donut + horizontal bar charts, collapsible per-tool drill-downs,
// heat-shaded cost columns, and a top-of-report "hotspots" section where
// each row carries a copyable LLM prompt.
func HTML(w io.Writer, a *aggregate.Aggregator) error {
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()

	rawHotspots := a.Hotspots(10)
	hotspots := make([]hotspotForJSON, 0, len(rawHotspots))
	for _, h := range rawHotspots {
		prompt, err := HotspotPrompt(h)
		if err != nil {
			continue
		}
		hotspots = append(hotspots, hotspotForJSON{
			Kind:       h.Kind,
			Title:      h.Title,
			CostUSD:    h.CostUSD,
			PctOfTotal: h.PctOfTotal,
			Prompt:     prompt,
		})
	}

	payload := struct {
		Totals           aggregate.Totals                    `json:"totals"`
		Hotspots         []hotspotForJSON                    `json:"hotspots"`
		ByModel          []aggregate.ModelBucket             `json:"by_model"`
		ByProject        []aggregate.ProjectBucket           `json:"by_project"`
		ByTool           []aggregate.ToolBucket              `json:"by_tool"`
		ByToolDetail     map[string][]aggregate.DetailBucket `json:"by_tool_detail"`
		BySkill          []aggregate.SkillBucket             `json:"by_skill"`
		Main             sidePart                            `json:"main"`
		Sidechain        sidePart                            `json:"sidechain"`
		BySubagent       []aggregate.SubagentBucket          `json:"by_subagent"`
		AgentInvocations []aggregate.AgentInvocation         `json:"agent_invocations"`
		UnknownModels    []string                            `json:"unknown_models"`
		Period           aggregate.Period                    `json:"period"`
		TrendTotals      []aggregate.TrendPoint              `json:"trend_totals"`
		TrendByModel     map[string][]aggregate.TrendPoint   `json:"trend_by_model"`
		TrendByProject   map[string][]aggregate.TrendPoint   `json:"trend_by_project"`
		TrendByTool      map[string][]aggregate.TrendPoint   `json:"trend_by_tool"`
		OverallHitRatio  float64                             `json:"overall_hit_ratio"`
		CacheByProject   []aggregate.CacheRow                `json:"cache_by_project"`
		CacheBySession   []aggregate.CacheRow                `json:"cache_by_session"`
		CacheBySubagent  []aggregate.CacheRow                `json:"cache_by_subagent"`
		CacheByInvocation []aggregate.CacheRow               `json:"cache_by_invocation"`
	}{
		Totals:           a.Totals(),
		Hotspots:         hotspots,
		ByModel:          a.ByModel(),
		ByProject:        a.ByProject(),
		ByTool:           a.ByTool(),
		ByToolDetail:     a.ByToolDetail(),
		BySkill:          a.BySkill(),
		Main:             sidePart{Cost: mainCost, Turns: mainTurns, Tokens: mainTok},
		Sidechain:        sidePart{Cost: sideCost, Turns: sideTurns, Tokens: sideTok},
		BySubagent:       a.BySubagent(),
		AgentInvocations: a.AgentInvocations(""),
		UnknownModels:    a.UnknownModels(),
		Period:           a.Period(),
		TrendTotals:      a.TrendTotals(),
		TrendByModel:     a.TrendByModel(),
		TrendByProject:   a.TrendByProject(),
		TrendByTool:      a.TrendByTool(),
		OverallHitRatio:  a.OverallHitRatio(),
		CacheByProject:    a.CacheByProject(),
		CacheBySession:    a.CacheBySession(),
		CacheBySubagent:   a.CacheBySubagent(),
		CacheByInvocation: a.CacheByInvocation(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal report data: %w", err)
	}
	// json.Marshal already escapes <, >, & as < etc., so the bytes
	// are safe to drop into a <script type="application/json"> island.
	return htmlTpl.Execute(w, struct {
		DataJSON template.JS
	}{
		DataJSON: template.JS(data),
	})
}

type sidePart struct {
	Cost   float64          `json:"cost"`
	Turns  int              `json:"turns"`
	Tokens aggregate.Tokens `json:"tokens"`
}
