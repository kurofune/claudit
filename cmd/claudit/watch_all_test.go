package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/watch"
	"github.com/kurofune/claudit/internal/watch/term"
)

func TestMultiHub_HandleEvent_GroupsByProject(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	h := newMultiHub(testPrices(t), 0, 0, nil, r, nil)

	// Two sessions under "claudit", one under "other-repo".
	feed := func(path, cwd string, costUSD float64) {
		ev := fakeAssistantTurn(t, costUSD)
		ev.Turn.CWD = cwd
		h.handleEvent(taggedEvent{path: path, ev: ev})
	}
	feed("/sessions/a.jsonl", "/users/me/claudit", 0.10)
	feed("/sessions/b.jsonl", "/users/me/claudit", 0.05)
	feed("/sessions/c.jsonl", "/users/me/other-repo", 0.02)

	// Combined cost should be the sum across all three events.
	if got := h.state.combinedCost; got != 0.17 {
		t.Errorf("combinedCost = %v, want 0.17", got)
	}

	// Grouping snapshot: two project groups.
	groups := h.state.groupByProject(time.Now())
	if len(groups) != 2 {
		t.Fatalf("expected 2 project groups, got %d", len(groups))
	}

	// Off-TTY frame is one collapsed line; verify the per-group totals
	// landed in the body.
	got := buf.String()
	if !strings.Contains(got, "claudit") || !strings.Contains(got, "other-repo") {
		t.Errorf("frame missing project labels: %q", got)
	}
}

func TestMultiHub_BudgetCross_AcrossSessions(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	h := newMultiHub(testPrices(t), 0.05, 0, nil, r, nil)

	feed := func(path string, costUSD float64) {
		ev := fakeAssistantTurn(t, costUSD)
		h.handleEvent(taggedEvent{path: path, ev: ev})
	}
	feed("/sess/a.jsonl", 0.02)
	feed("/sess/b.jsonl", 0.02)
	if strings.Contains(buf.String(), "BUDGET") {
		t.Errorf("under budget, should not alert; got %q", buf.String())
	}
	buf.Reset()
	feed("/sess/c.jsonl", 0.02) // combined = $0.06 >= $0.05
	if !strings.Contains(buf.String(), "BUDGET") {
		t.Errorf("expected combined-budget alert; got %q", buf.String())
	}
}

func TestMultiHub_IgnoresNonAssistantEvents(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	h := newMultiHub(testPrices(t), 0, 0, nil, r, nil)

	h.handleEvent(taggedEvent{
		path: "/sess/x.jsonl",
		ev: watch.Event{
			Kind: parse.LineUserMessage,
			User: parse.UserMessage{Text: "hello"},
		},
	})
	if h.state.combinedCost != 0 {
		t.Errorf("user-message event should not add cost; got %v", h.state.combinedCost)
	}
	if len(h.state.sessions) != 0 {
		t.Errorf("user-message event should not create a session row; got %d", len(h.state.sessions))
	}
}

func TestMultiState_IdleSessionsHiddenWhenOthersActive(t *testing.T) {
	m := newMultiState()
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	// Active session: turn 1 minute ago.
	a := m.session("/sess/a.jsonl", "sa", "/p/a")
	a.lastTurnAt = now.Add(-time.Minute)
	// Idle session: turn 30 minutes ago, well past idleHide.
	b := m.session("/sess/b.jsonl", "sb", "/p/b")
	b.lastTurnAt = now.Add(-30 * time.Minute)

	groups := m.groupByProject(now)
	if len(groups) != 1 {
		t.Fatalf("expected 1 visible group (idle hidden), got %d", len(groups))
	}
	if groups[0].cwd != "/p/a" {
		t.Errorf("expected active project, got %v", groups[0].cwd)
	}
}

// LIVE header must report the cost of currently-visible sessions, not
// the running combined cost across every session the watch process has
// ever seen. Earlier behavior summed costs into combinedCost on every
// event and showed that in the header — once a session went idle and
// got hidden from the body, its cost stayed in the header total and
// the math no longer added up.
func TestMultiHub_LiveHeader_ExcludesIdleSessions(t *testing.T) {
	var buf bytes.Buffer
	r := newStreamPainter(&buf, term.Style{})
	h := newMultiHub(testPrices(t), 0, 0, nil, r, nil)

	// Active session: $0.05, just now.
	active := h.state.session("/sess/active.jsonl", "sa", "/p/active")
	active.totalCost = 0.05
	active.turns = 1
	active.lastTurnAt = time.Now().Add(-time.Minute)

	// Idle session: $5.00, half an hour ago — past idleHide.
	idle := h.state.session("/sess/idle.jsonl", "si", "/p/idle")
	idle.totalCost = 5.00
	idle.turns = 1
	idle.lastTurnAt = time.Now().Add(-30 * time.Minute)

	// combinedCost reflects everything we've seen; the header must not.
	h.state.combinedCost = 5.05

	h.paint()
	out := buf.String()
	if !strings.Contains(out, "$0.0500") {
		t.Errorf("expected LIVE header total to be $0.0500 (active only); got %q", out)
	}
	if strings.Contains(out, "$5.05") || strings.Contains(out, "$5.0500") {
		t.Errorf("LIVE header should not include idle session cost; got %q", out)
	}
	if !strings.Contains(out, "1 active session") {
		t.Errorf("expected '1 active session' in header; got %q", out)
	}
}

func TestProjectLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/me/claudit", "claudit"},
		{"", "(unknown)"},
		{"/", "/"},
	}
	for _, c := range cases {
		if got := projectLabel(c.in); got != c.want {
			t.Errorf("projectLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
