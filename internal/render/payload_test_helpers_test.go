package render

import (
	"context"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// buildTimelinesForTest is a thin wrapper that builds session
// timelines from a synthetic single-turn corpus. Mirrors payloadSetup's
// fixture exactly (model, CWD, tokens, timestamp) but attaches a
// SessionID and UUID so BuildSessionTimelines produces at least one
// session row for the BuildSessions shape test.
//
// Kept in a _test.go file so production code doesn't grow a test-only
// dependency on parse.Turn synthesis.
func buildTimelinesForTest(t *testing.T) ([]aggregate.SessionTimeline, error) {
	t.Helper()
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	turn := parse.Turn{
		Model:     "claude-opus-4-7",
		CWD:       "/p/x",
		SessionID: "fixture-session-1",
		UUID:      "turn-uuid-1",
		Usage:     parse.Usage{InputTokens: 1_000_000, OutputTokens: 200_000},
		Timestamp: t0,
	}
	return aggregate.BuildSessionTimelines(
		context.Background(),
		[]parse.Turn{turn},
		nil, nil,
		prices,
		aggregate.Filter{},
		aggregate.SessionTimelinesOptions{},
	)
}

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
