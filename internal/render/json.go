package render

import (
	"encoding/json"
	"io"

	"github.com/kurofune/claudit/internal/aggregate"
)

// JSON writes a JSON representation of every aggregate slice.
func JSON(w io.Writer, a *aggregate.Aggregator) error {
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()
	out := struct {
		Totals        aggregate.Totals                       `json:"totals"`
		ByModel       []aggregate.ModelBucket                `json:"by_model"`
		ByProject     []aggregate.ProjectBucket              `json:"by_project"`
		ByTool        []aggregate.ToolBucket                 `json:"by_tool"`
		ByToolDetail  map[string][]aggregate.DetailBucket    `json:"by_tool_detail"`
		BySkill       []aggregate.SkillBucket                `json:"by_skill"`
		Main          sidechainPart                          `json:"main"`
		Sidechain     sidechainPart                          `json:"sidechain"`
		BySubagent       []aggregate.SubagentBucket          `json:"by_subagent"`
		AgentInvocations []aggregate.AgentInvocation         `json:"agent_invocations"`
		ByPrompt         []aggregate.PromptBucket            `json:"by_prompt"`
		UnknownModels    []string                            `json:"unknown_models"`
	}{
		Totals:        a.Totals(),
		ByModel:       a.ByModel(),
		ByProject:     a.ByProject(),
		ByTool:        a.ByTool(),
		ByToolDetail:  a.ByToolDetail(),
		BySkill:       a.BySkill(),
		Main:          sidechainPart{Tokens: mainTok, CostUSD: mainCost, Turns: mainTurns},
		Sidechain:     sidechainPart{Tokens: sideTok, CostUSD: sideCost, Turns: sideTurns},
		BySubagent:       a.BySubagent(),
		AgentInvocations: a.AgentInvocations(""),
		ByPrompt:         a.ByPrompt(),
		UnknownModels:    a.UnknownModels(),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

type sidechainPart struct {
	aggregate.Tokens
	CostUSD float64 `json:"cost_usd"`
	Turns   int     `json:"turns"`
}
