package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
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
		"claude-test": {Rate: pricing.Rate{Output: 1.0}}, // $1 per 1M output tokens
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

func TestMultiHub_DedupsDuplicateMessageIDAcrossFiles(t *testing.T) {
	// `watch --all` tails many files. A resumed/forked session replays the
	// same generation into a second file (same message.id + usage, fresh
	// uuid). combinedCost must count it once, not once per file.
	r := newStreamPainter(&bytes.Buffer{}, term.Style{})
	h := newMultiHub(testPrices(t), 0, 0, nil, r, nil)

	mk := func(path, msgID string) taggedEvent {
		return taggedEvent{path: path, ev: watch.Event{
			Kind: parse.LineAssistant, Live: true,
			Turn: parse.Turn{
				SessionID: path, MessageID: msgID, Model: "claude-test",
				Timestamp: time.Now(), CWD: "/proj",
				Usage: parse.Usage{OutputTokens: 100_000}, // $0.10
			},
		}}
	}

	h.handleEvent(mk("a.jsonl", "msg_dup")) // original
	h.handleEvent(mk("b.jsonl", "msg_dup")) // fork replay — same generation

	const eps = 1e-9
	if got := h.state.combinedCost; got < 0.10-eps || got > 0.10+eps {
		t.Errorf("combinedCost = %.4f, want 0.10 (duplicate counted once, not $0.20)", got)
	}

	// A genuinely distinct generation still adds.
	h.handleEvent(mk("b.jsonl", "msg_other"))
	if got := h.state.combinedCost; got < 0.20-eps || got > 0.20+eps {
		t.Errorf("combinedCost after distinct id = %.4f, want 0.20", got)
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

// rollingSnap builds a corpus snapshot with one turn per given cost,
// timestamped `age` before now, tagged with the given generation.
// Distinct MessageIDs so the panel's dedup doesn't collapse them.
func rollingSnap(t *testing.T, gen int64, now time.Time, costs ...float64) *corpus.Snapshot {
	t.Helper()
	snap := &corpus.Snapshot{Generation: gen}
	for i, c := range costs {
		snap.Turns = append(snap.Turns, parse.Turn{
			SessionID: "s1",
			MessageID: fmt.Sprintf("msg_g%d_%d", gen, i),
			Model:     "claude-test",
			Timestamp: now.Add(-time.Minute),
			Usage:     parse.Usage{OutputTokens: int(c * 1_000_000)},
		})
	}
	return snap
}

func TestRollingTotals_FirstCallMatchesAggregate(t *testing.T) {
	prices := testPrices(t)
	r := newStreamPainter(&bytes.Buffer{}, term.Style{})
	s := newWatchState(prices, 0, 0, nil, r, nil)

	now := time.Now()
	snap := rollingSnap(t, 1, now, 0.10, 0.05)

	wantHour, wantToday, wantWeek, wantMonth := aggregate.RollingTotals(snap.Turns, prices, now)
	hour, today, week, month := s.rollingTotals(snap, now)
	if hour != wantHour || today != wantToday || week != wantWeek || month != wantMonth {
		t.Errorf("rollingTotals = (%.4f, %.4f, %.4f, %.4f), want (%.4f, %.4f, %.4f, %.4f)",
			hour, today, week, month, wantHour, wantToday, wantWeek, wantMonth)
	}
}

func TestRollingTotals_CachedWithinSameGenerationAndMinute(t *testing.T) {
	prices := testPrices(t)
	r := newStreamPainter(&bytes.Buffer{}, term.Style{})
	s := newWatchState(prices, 0, 0, nil, r, nil)

	// Fix now to mid-minute so both calls share the same minute stamp.
	now := time.Now().Truncate(time.Minute).Add(30 * time.Second)
	snap := rollingSnap(t, 1, now, 0.10)

	hour1, _, _, _ := s.rollingTotals(snap, now)
	if hour1 != 0.10 {
		t.Fatalf("first call hour = %.4f, want 0.10", hour1)
	}

	// Mutate the turns behind the memo's back. Same generation + same
	// minute must serve the cached figure, not recompute from this.
	snap.Turns = append(snap.Turns, rollingSnap(t, 9, now, 5.0).Turns...)

	hour2, _, _, _ := s.rollingTotals(snap, now.Add(time.Second))
	if hour2 != hour1 {
		t.Errorf("second call hour = %.4f, want cached %.4f (must not recompute)", hour2, hour1)
	}
}

func TestRollingTotals_RecomputesOnGenerationBump(t *testing.T) {
	prices := testPrices(t)
	r := newStreamPainter(&bytes.Buffer{}, term.Style{})
	s := newWatchState(prices, 0, 0, nil, r, nil)

	now := time.Now().Truncate(time.Minute).Add(30 * time.Second)
	hour1, _, _, _ := s.rollingTotals(rollingSnap(t, 1, now, 0.10), now)
	if hour1 != 0.10 {
		t.Fatalf("first call hour = %.4f, want 0.10", hour1)
	}

	// New snapshot with a higher generation at the same minute: the
	// fresh turn must show up immediately.
	const eps = 1e-9
	hour2, _, _, _ := s.rollingTotals(rollingSnap(t, 2, now, 0.10, 0.05), now)
	if hour2 < 0.15-eps || hour2 > 0.15+eps {
		t.Errorf("post-bump hour = %.4f, want 0.15 (must recompute on new generation)", hour2)
	}
}

func TestRollingTotals_RecomputesOnMinuteRollover(t *testing.T) {
	prices := testPrices(t)
	r := newStreamPainter(&bytes.Buffer{}, term.Style{})
	s := newWatchState(prices, 0, 0, nil, r, nil)

	now := time.Now().Truncate(time.Minute).Add(30 * time.Second)
	// One turn 59m45s old: inside the trailing hour at `now`, aged out
	// one minute later.
	snap := &corpus.Snapshot{Generation: 1, Turns: []parse.Turn{{
		SessionID: "s1",
		MessageID: "msg_old",
		Model:     "claude-test",
		Timestamp: now.Add(-59*time.Minute - 45*time.Second),
		Usage:     parse.Usage{OutputTokens: 100_000}, // $0.10
	}}}

	hour1, _, _, _ := s.rollingTotals(snap, now)
	if hour1 != 0.10 {
		t.Fatalf("hour at t0 = %.4f, want 0.10", hour1)
	}

	// Same generation, next minute: the turn has aged out of the
	// rolling hour, so serving the cached 0.10 would be stale.
	hour2, _, _, _ := s.rollingTotals(snap, now.Add(time.Minute))
	if hour2 != 0 {
		t.Errorf("hour after minute rollover = %.4f, want 0 (must recompute on new minute)", hour2)
	}
}

func TestRender_NilCacheHasNoRollingPanel(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	s := newWatchState(testPrices(t), 0, 0, nil, r, nil) // no corpus cache
	s.onEvent(fakeAssistantTurn(t, 0.01))
	got := buf.String()
	if strings.Contains(got, "hour") || strings.Contains(got, "month") {
		t.Errorf("nil cache must render no rolling panel; got %q", got)
	}
	if !strings.Contains(got, "turns") {
		t.Errorf("live session line missing from frame; got %q", got)
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
