package render

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/v2/internal/aggregate"
	"github.com/kurofune/claudit/v2/internal/parse"
	"github.com/kurofune/claudit/v2/internal/pricing"
)

func mkTurn(model, cwd string, in, out int, ts time.Time) parse.Turn {
	return parse.Turn{
		Model: model, CWD: cwd, Timestamp: ts,
		Usage: parse.Usage{InputTokens: in, OutputTokens: out},
	}
}

// payloadSetup builds the smallest aggregator every payload builder
// accepts: one Opus turn so totals are non-zero and every section has
// at least one row.
func payloadSetup(t *testing.T) *aggregate.Aggregator {
	t.Helper()
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	a := aggregate.New(prices)
	a.Add(mkTurn("claude-opus-4-7", "/p/x", 1_000_000, 200_000, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)))
	return a
}

// turnSkill is mkTurn plus a Skill tool_use, so the turn lands in the
// by-skill bucket keyed "skill:<name>".
func turnSkill(model, cwd string, in, out int, ts time.Time, skill string) parse.Turn {
	t := mkTurn(model, cwd, in, out, ts)
	t.ToolUses = []parse.ToolUse{{Name: "Skill", SkillName: skill}}
	return t
}
