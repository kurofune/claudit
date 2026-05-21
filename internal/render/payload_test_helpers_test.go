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
// timelines from a synthetic single-turn corpus. Mirrors htmlSetup's
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
