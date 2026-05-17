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
// test pricing table (1 output-token = $1e-6). Default Live=true so
// detectors fire; tests that need a historical-replay event override.
func fakeAssistantTurn(t *testing.T, costUSD float64) watch.Event {
	t.Helper()
	outTokens := int(costUSD * 1_000_000)
	return watch.Event{
		Kind: parse.LineAssistant,
		Live: true,
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
	r := newStreamPainter(&buf, term.Style{}) // non-TTY
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
	r := newStreamPainter(&buf, term.Style{})
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
	r := newStreamPainter(&buf, term.Style{})
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
	r := newStreamPainter(&buf, term.Style{})
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
	r := newStreamPainter(&buf, term.Style{})
	s := newWatchState(testPrices(t), 0.05, 0, nil, r, nil)
	s.onEvent(fakeAssistantTurn(t, 0.02))
	s.onEvent(fakeAssistantTurn(t, 0.02))
	if strings.Contains(buf.String(), "BUDGET") {
		t.Errorf("under budget, should not alert; got %q", buf.String())
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.05)) // total now $0.09 >= $0.05
	if !strings.Contains(buf.String(), "BUDGET") {
		t.Errorf("expected budget alert; got %q", buf.String())
	}
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.05))
	if strings.Contains(buf.String(), "BUDGET") {
		t.Errorf("budget should alert only once; got %q", buf.String())
	}
}

func TestSpike_SuppressedDuringHistoryReplay(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	s := newWatchState(testPrices(t), 0, 5.0, nil, r, nil)
	// Warm the ring with cheap historical events (Live=false).
	for i := 0; i < spikeWindow; i++ {
		e := fakeAssistantTurn(t, 0.01)
		e.Live = false
		s.onEvent(e)
	}
	buf.Reset()
	huge := fakeAssistantTurn(t, 1.0) // would be 100x median
	huge.Live = false
	s.onEvent(huge)
	if strings.Contains(buf.String(), "SPIKE") {
		t.Errorf("history-replay event should not fire SPIKE; got %q", buf.String())
	}
}

func TestSpike_SuppressesConsecutiveDuplicateCost(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	s := newWatchState(testPrices(t), 0, 5.0, nil, r, nil)
	for i := 0; i < spikeWindow; i++ {
		s.onEvent(fakeAssistantTurn(t, 0.01))
	}
	// First $0.10 turn — should fire.
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.10))
	if !strings.Contains(buf.String(), "SPIKE") {
		t.Errorf("first spike should fire; got %q", buf.String())
	}
	// Immediately-following $0.10 turn — Claude Code's duplicate-usage
	// wire pattern. Must not fire again.
	buf.Reset()
	s.onEvent(fakeAssistantTurn(t, 0.10))
	if strings.Contains(buf.String(), "SPIKE") {
		t.Errorf("duplicate-cost spike should be suppressed; got %q", buf.String())
	}
}

func TestBudget_SuppressedDuringReplay(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	s := newWatchState(testPrices(t), 0.05, 0, nil, r, nil)
	for i := 0; i < 10; i++ {
		e := fakeAssistantTurn(t, 0.02)
		e.Live = false
		s.onEvent(e)
	}
	if strings.Contains(buf.String(), "BUDGET") {
		t.Errorf("history-replay budget cross should be suppressed; got %q", buf.String())
	}
}

func TestSummary_IncludesMaxTurnRatio(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
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
