package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/watch"
	"github.com/kurofune/claudit/internal/watch/term"
)

// testPrices returns a tiny pricing table with one model so we can
// drive cost by tweaking output tokens (input is free in this table).
func testPrices(t *testing.T) *pricing.Table {
	t.Helper()
	return &pricing.Table{Models: map[string]pricing.ModelPrice{
		"claude-test": {Output: 1.0}, // $1 per 1M output tokens
	}}
}

// fakeAssistantTurn builds a watch.Event whose cost is `costUSD` on the
// test pricing table (1 output-token = $1e-6).
func fakeAssistantTurn(t *testing.T, costUSD float64) watch.Event {
	t.Helper()
	outTokens := int(costUSD * 1_000_000)
	return watch.Event{
		Kind: parse.LineAssistant,
		Turn: parse.Turn{
			SessionID: "s1",
			UUID:      "u",
			Model:     "claude-test",
			Timestamp: time.Now(),
			CWD:       "/proj",
			Usage:     parse.Usage{OutputTokens: outTokens},
		},
	}
}

func TestSpike_NoFlagBelowMinSamples(t *testing.T) {
	var buf bytes.Buffer
	r := term.New(&buf) // non-TTY
	s := newWatchState(testPrices(t), 0, 5.0, nil, r, nil)
	// Feed spikeWindow/2 - 1 cheap turns, then a huge spike. Detector
	// requires at least spikeWindow/2 prior samples — so no flag yet.
	for i := 0; i < spikeWindow/2-1; i++ {
		s.onEvent(fakeAssistantTurn(t, 0.001))
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 1.0))
	if strings.Contains(buf.String(), "SPIKE") {
		t.Errorf("should not have flagged spike before warmup; got %q", buf.String())
	}
}

func TestSpike_FlagsAfterWarmup(t *testing.T) {
	var buf bytes.Buffer
	r := term.New(&buf)
	s := newWatchState(testPrices(t), 0, 5.0, nil, r, nil)
	for i := 0; i < spikeWindow; i++ {
		s.onEvent(fakeAssistantTurn(t, 0.01))
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.10)) // 10x median
	if !strings.Contains(buf.String(), "SPIKE") {
		t.Errorf("expected SPIKE callout; got %q", buf.String())
	}
}

func TestSpike_NoFlagWhenBelowThreshold(t *testing.T) {
	var buf bytes.Buffer
	r := term.New(&buf)
	s := newWatchState(testPrices(t), 0, 5.0, nil, r, nil)
	for i := 0; i < spikeWindow; i++ {
		s.onEvent(fakeAssistantTurn(t, 0.01))
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.03)) // 3x — under 5x threshold
	if strings.Contains(buf.String(), "SPIKE") {
		t.Errorf("3x should not trigger 5x threshold; got %q", buf.String())
	}
}

func TestSpike_DisabledByZeroThreshold(t *testing.T) {
	var buf bytes.Buffer
	r := term.New(&buf)
	s := newWatchState(testPrices(t), 0, 0, nil, r, nil)
	for i := 0; i < spikeWindow; i++ {
		s.onEvent(fakeAssistantTurn(t, 0.01))
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 1.0))
	if strings.Contains(buf.String(), "SPIKE") {
		t.Errorf("zero threshold should disable detector; got %q", buf.String())
	}
}

func TestBudget_AlertsOnceOnCross(t *testing.T) {
	var buf bytes.Buffer
	r := term.New(&buf)
	s := newWatchState(testPrices(t), 0.05, 0, nil, r, nil)
	s.onEvent(fakeAssistantTurn(t, 0.02))
	s.onEvent(fakeAssistantTurn(t, 0.02))
	if strings.Contains(buf.String(), "BUDGET CROSSED") {
		t.Errorf("under budget, should not alert; got %q", buf.String())
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.05)) // total now $0.09 >= $0.05
	if !strings.Contains(buf.String(), "BUDGET CROSSED") {
		t.Errorf("expected budget alert; got %q", buf.String())
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.05))
	if strings.Contains(buf.String(), "BUDGET CROSSED") {
		t.Errorf("budget should alert only once; got %q", buf.String())
	}
}

func TestSummary_IncludesMaxTurnRatio(t *testing.T) {
	var buf bytes.Buffer
	r := term.New(&buf)
	s := newWatchState(testPrices(t), 0, 0, nil, r, nil)
	for i := 0; i < 5; i++ {
		s.onEvent(fakeAssistantTurn(t, 0.01))
	}
	s.onEvent(fakeAssistantTurn(t, 0.10))

	var sum bytes.Buffer
	s.printSummary(&sum)
	got := sum.String()
	if !strings.Contains(got, "max turn:") {
		t.Errorf("summary missing max turn line: %q", got)
	}
	if !strings.Contains(got, "session median") {
		t.Errorf("summary missing ratio detail: %q", got)
	}
}
