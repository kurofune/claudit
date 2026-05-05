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

// HTML writes a self-contained interactive HTML report to w. The page has
// inline CSS/JS, a global text filter, a min-cost threshold, sortable
// tables, donut + horizontal bar charts, collapsible per-tool drill-downs,
// and heat-shaded cost columns.
func HTML(w io.Writer, a *aggregate.Aggregator) error {
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()

	payload := struct {
		Totals           aggregate.Totals                    `json:"totals"`
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
	}{
		Totals:           a.Totals(),
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
