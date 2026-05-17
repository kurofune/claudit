package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kurofune/claudit/internal/notify"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/stat"
	"github.com/kurofune/claudit/internal/watch"
	"github.com/kurofune/claudit/internal/watch/term"
)

// Tuning knobs for --all. These are intentionally not flags; they
// were chosen to keep the UI calm without missing real activity.
const (
	// discoveryInterval is how often we rescan the projects root for
	// newly-touched JSONLs to tail.
	discoveryInterval = 10 * time.Second
	// recentWindow is the maximum mtime age for a JSONL to be picked
	// up by the discovery loop. Older files are presumed inactive.
	recentWindow = 15 * time.Minute
	// idleHide is how long a tailed session can go without a new
	// assistant turn before it drops off the live display. We don't
	// stop tailing — Claude Code sometimes resumes a session — but we
	// hide it so the UI stays focused on what's currently active.
	idleHide = 15 * time.Minute
	// maxVisibleSessions caps the per-session rows printed. Anything
	// past this is summarized as "+N more sessions" in a single line.
	maxVisibleSessions = 6
)

// runWatchAll is the entry point for `claudit watch --all`. It runs
// the discovery loop, spawns Tail goroutines for each session found,
// fans events into a single render goroutine that owns all state and
// all writes to the terminal.
func runWatchAll(ctx context.Context, root string, prices *pricing.Table, intervalMS int, budget, spikeThresh float64, notifyOn, rolling bool) error {
	fmt.Fprintf(os.Stderr, "claudit watch --all: tailing every session under %s touched in the last %s\n", root, recentWindow)
	if budget > 0 {
		fmt.Fprintf(os.Stderr, "claudit watch --all: budget alert at $%.2f (across all sessions combined)\n", budget)
	}

	renderer := term.New(os.Stdout)
	var notifier notify.Notifier
	if notifyOn {
		notifier = notify.Default()
	}
	var rollingState *rollingTotals
	if rolling {
		rs, scanErr := newRollingTotals(root, prices, time.Now())
		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "claudit watch --all: rolling totals disabled (%v)\n", scanErr)
		} else {
			rollingState = rs
		}
	}

	hub := newMultiHub(prices, budget, spikeThresh, notifier, renderer, rollingState)
	defer hub.shutdown(os.Stderr)
	stopCh := make(chan struct{})

	tailInterval := time.Duration(intervalMS) * time.Millisecond
	if tailInterval <= 0 {
		tailInterval = time.Second
	}

	// Discovery + tailer supervision both feed into the same hub
	// channels. The render goroutine consumes; tail goroutines and
	// the discovery loop are pure producers.
	var wg sync.WaitGroup

	// Render goroutine: owns hub.state. All UI writes happen here.
	// Watches stopCh (not ctx) for shutdown so it stays alive until the
	// tail goroutines have finished posting their final-drain events.
	renderDone := make(chan struct{})
	go func() {
		defer close(renderDone)
		hub.run(stopCh)
	}()

	// Discovery loop: periodically rescan the root and ask the hub
	// to start tailing anything new.
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(discoveryInterval)
		defer tick.Stop()
		hub.discover(ctx, root, tailInterval, &wg) // initial pass
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				hub.discover(ctx, root, tailInterval, &wg)
			}
		}
	}()

	<-ctx.Done()
	wg.Wait()
	// Tail goroutines have stopped posting. Signal hub.run to drain
	// and exit — anything still in the channel buffer gets handled.
	close(stopCh)
	<-renderDone
	return nil
}

// multiHub owns the shared state behind `watch --all`. Producers
// (Tail goroutines, discovery loop) post messages on eventCh /
// noticeCh; the single consumer (hub.run) mutates state.
type multiHub struct {
	prices      *pricing.Table
	budget      float64
	spikeThresh float64
	notifier    notify.Notifier
	renderer    *term.Renderer
	rolling     *rollingTotals

	eventCh  chan taggedEvent
	noticeCh chan taggedNotice

	mu       sync.Mutex // guards tailing
	tailing  map[string]bool

	// state is only ever read/written by hub.run.
	state *multiState
}

type taggedEvent struct {
	path string
	ev   watch.Event
}

type taggedNotice struct {
	path string
	n    watch.Notice
}

func newMultiHub(prices *pricing.Table, budget, spikeThresh float64, notifier notify.Notifier, renderer *term.Renderer, rolling *rollingTotals) *multiHub {
	return &multiHub{
		prices:      prices,
		budget:      budget,
		spikeThresh: spikeThresh,
		notifier:    notifier,
		renderer:    renderer,
		rolling:     rolling,
		eventCh:     make(chan taggedEvent, 256),
		noticeCh:    make(chan taggedNotice, 64),
		tailing:     map[string]bool{},
		state:       newMultiState(),
	}
}

// discover walks root, starting a Tail for every JSONL touched in the
// last recentWindow that we are not already tailing.
func (h *multiHub) discover(ctx context.Context, root string, interval time.Duration, wg *sync.WaitGroup) {
	cutoff := time.Now().Add(-recentWindow)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if parse.IsSubagentFile(path) {
			// Subagent JSONLs are accounted via the parent session's
			// turns; tailing them separately would double-count.
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			return nil
		}
		h.mu.Lock()
		already := h.tailing[path]
		if !already {
			h.tailing[path] = true
		}
		h.mu.Unlock()
		if already {
			return nil
		}
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			opts := watch.TailOptions{Interval: interval, FromBeginning: true}
			_ = watch.Tail(ctx, p, opts,
				func(e watch.Event) {
					select {
					case h.eventCh <- taggedEvent{path: p, ev: e}:
					case <-ctx.Done():
					}
				},
				func(n watch.Notice) {
					select {
					case h.noticeCh <- taggedNotice{path: p, n: n}:
					case <-ctx.Done():
					}
				})
		}(path)
		return nil
	})
}

// run is the single consumer that mutates state and paints frames.
// Drains eventCh and noticeCh until stop is closed, then exits so
// any deferred summary can run. The caller closes stop only after
// the producing tail goroutines have finished, so no events are lost.
func (h *multiHub) run(stop <-chan struct{}) {
	repaint := time.NewTicker(time.Second)
	defer repaint.Stop()
	for {
		select {
		case <-stop:
			h.drain()
			return
		case te := <-h.eventCh:
			h.handleEvent(te)
		case tn := <-h.noticeCh:
			h.handleNotice(tn)
		case <-repaint.C:
			// Periodic repaint so rolling totals roll over visible
			// clock minutes and idle sessions disappear in a timely way
			// even if no new events arrive.
			h.paint()
		}
	}
}

func (h *multiHub) drain() {
	for {
		select {
		case te := <-h.eventCh:
			h.handleEvent(te)
		case tn := <-h.noticeCh:
			h.handleNotice(tn)
		default:
			return
		}
	}
}

func (h *multiHub) handleEvent(te taggedEvent) {
	if te.ev.Kind != parse.LineAssistant {
		return
	}
	t := te.ev.Turn
	cost, _ := h.prices.Cost(t.Model,
		t.Usage.InputTokens, t.Usage.OutputTokens,
		t.Usage.CacheCreate5mTokens, t.Usage.CacheCreate1hTokens,
		t.Usage.CacheReadTokens)
	s := h.state.session(te.path, t.SessionID, t.CWD)
	s.totalCost += cost
	s.turns++
	s.lastTurnCost = cost
	s.lastTurnAt = time.Now()
	s.lastTools = lastToolNames(t.ToolUses)
	h.state.combinedCost += cost
	if h.rolling != nil {
		h.rolling.addLive(t.Timestamp, cost, time.Now())
	}
	// Spike detection runs per-session — the median that matters is
	// the session's own recent turns, not a cross-session blend.
	s.recordCost(cost)
	if h.spikeThresh > 0 && s.tcCount >= spikeWindow/2 {
		med := stat.Median(s.snapshotCosts())
		if med > 0 && cost/med >= h.spikeThresh {
			tools := strings.Join(s.lastTools, "+")
			if tools == "" {
				tools = "no-tool"
			}
			msg := fmt.Sprintf("[claudit watch] SPIKE in %s: turn %d cost $%.4f — %.1fx median ($%.4f) — last: %s",
				projectLabel(s.cwd), s.turns, cost, cost/med, med, tools)
			h.renderer.Println(msg)
			if h.notifier != nil {
				_ = h.notifier.Send("claudit: cost spike",
					fmt.Sprintf("%s turn %d cost $%.4f (%.1fx median)", projectLabel(s.cwd), s.turns, cost, cost/med))
			}
		}
	}
	if h.budget > 0 && !h.state.budgetAlerted && h.state.combinedCost >= h.budget {
		h.renderer.Println(fmt.Sprintf("[claudit watch] BUDGET CROSSED (all sessions): $%.2f >= $%.2f",
			h.state.combinedCost, h.budget))
		h.state.budgetAlerted = true
		if h.notifier != nil {
			_ = h.notifier.Send("claudit: budget crossed",
				fmt.Sprintf("Combined cost $%.2f crossed budget $%.2f", h.state.combinedCost, h.budget))
		}
	}
	h.paint()
}

func (h *multiHub) handleNotice(tn taggedNotice) {
	if tn.n.Kind == watch.NoticeMalformed {
		return
	}
	// Only surface "opened" notices — the others (waiting, rotated)
	// are noisy when 4 sessions all reopen during a `claude` restart.
	if tn.n.Kind == watch.NoticeOpened {
		h.renderer.Println(fmt.Sprintf("[claudit watch] tailing %s", tn.n.Message))
		h.paint()
	}
}

// paint builds a Frame from the current state and asks the renderer
// to draw it. Sessions are grouped by project (cwd); within each
// project they are sorted by most-recent activity.
func (h *multiHub) paint() {
	now := time.Now()
	frame := term.Frame{}

	// Header: rolling totals (if enabled) + combined live cost.
	header := []string{}
	if h.rolling != nil {
		header = append(header, h.rolling.statusLine())
	}
	combined := fmt.Sprintf("live: $%.4f across %d session(s)",
		h.state.combinedCost, h.state.visibleCount(now))
	header = append(header, combined)
	frame.Header = header

	// Group sessions by project. Hide sessions whose last turn is older
	// than idleHide, *unless* nothing else is visible (the user should
	// always see something).
	groups := h.state.groupByProject(now)
	if len(groups) == 0 {
		frame.Body = []string{"(no sessions yet — waiting for assistant turns)"}
		h.renderer.Render(frame)
		return
	}

	body := []string{}
	visible := 0
	overflow := 0
	for _, g := range groups {
		if visible >= maxVisibleSessions {
			overflow += len(g.sessions)
			continue
		}
		// Project header row: sum of session costs in this group.
		body = append(body, fmt.Sprintf("%s   %d turns  $%.4f",
			projectLabel(g.cwd), g.totalTurns(), g.totalCost()))
		for _, s := range g.sessions {
			if visible >= maxVisibleSessions {
				overflow++
				continue
			}
			body = append(body, fmt.Sprintf("  └ %d turns  $%.4f  last: %s (+$%.4f)",
				s.turns, s.totalCost, lastToolsLabel(s.lastTools), s.lastTurnCost))
			visible++
		}
	}
	if overflow > 0 {
		body = append(body, fmt.Sprintf("  +%d more session(s) hidden", overflow))
	}
	frame.Body = body
	h.renderer.Render(frame)
}

// shutdown clears the live region and prints the cross-session summary.
func (h *multiHub) shutdown(w *os.File) {
	h.renderer.Clear()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "=== claudit watch --all summary ===")
	fmt.Fprintf(w, "combined cost: $%.4f\n", h.state.combinedCost)
	fmt.Fprintf(w, "sessions seen: %d\n", len(h.state.sessions))
	groups := h.state.groupByProject(time.Now())
	for _, g := range groups {
		fmt.Fprintf(w, "  %s: $%.4f across %d session(s), %d turn(s)\n",
			projectLabel(g.cwd), g.totalCost(), len(g.sessions), g.totalTurns())
	}
}

// multiState aggregates per-session data behind `watch --all`. All
// access goes through hub.run, so no internal locking is needed.
type multiState struct {
	sessions      map[string]*sessionAgg // keyed by JSONL path
	combinedCost  float64
	budgetAlerted bool
}

func newMultiState() *multiState {
	return &multiState{sessions: map[string]*sessionAgg{}}
}

func (m *multiState) session(path, sessID, cwd string) *sessionAgg {
	if s, ok := m.sessions[path]; ok {
		// Backfill identifying metadata if the first event into this
		// session was a user-message line that lacked them.
		if s.sessionID == "" {
			s.sessionID = sessID
		}
		if s.cwd == "" {
			s.cwd = cwd
		}
		return s
	}
	s := &sessionAgg{path: path, sessionID: sessID, cwd: cwd}
	m.sessions[path] = s
	return s
}

func (m *multiState) visibleCount(now time.Time) int {
	n := 0
	for _, s := range m.sessions {
		if now.Sub(s.lastTurnAt) <= idleHide {
			n++
		}
	}
	return n
}

// groupByProject returns project groups sorted by most-recent activity
// across their member sessions, with each group's sessions also sorted
// by recency. Idle sessions are hidden when at least one non-idle
// session exists overall.
func (m *multiState) groupByProject(now time.Time) []projectGroup {
	// First pass: bucket and decide visibility.
	anyActive := false
	for _, s := range m.sessions {
		if now.Sub(s.lastTurnAt) <= idleHide {
			anyActive = true
			break
		}
	}
	buckets := map[string]*projectGroup{}
	for _, s := range m.sessions {
		if anyActive && now.Sub(s.lastTurnAt) > idleHide {
			continue
		}
		g, ok := buckets[s.cwd]
		if !ok {
			g = &projectGroup{cwd: s.cwd}
			buckets[s.cwd] = g
		}
		g.sessions = append(g.sessions, s)
	}
	out := make([]projectGroup, 0, len(buckets))
	for _, g := range buckets {
		sort.Slice(g.sessions, func(i, j int) bool {
			return g.sessions[i].lastTurnAt.After(g.sessions[j].lastTurnAt)
		})
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].mostRecent().After(out[j].mostRecent())
	})
	return out
}

type projectGroup struct {
	cwd      string
	sessions []*sessionAgg
}

func (g projectGroup) totalCost() float64 {
	var c float64
	for _, s := range g.sessions {
		c += s.totalCost
	}
	return c
}

func (g projectGroup) totalTurns() int {
	var n int
	for _, s := range g.sessions {
		n += s.turns
	}
	return n
}

func (g projectGroup) mostRecent() time.Time {
	var t time.Time
	for _, s := range g.sessions {
		if s.lastTurnAt.After(t) {
			t = s.lastTurnAt
		}
	}
	return t
}

// sessionAgg is the per-tailed-session running state inside multiHub.
// Lighter than the single-session watchState — no rolling baselines,
// no per-tool breakdown (deferred to a future drill-down view).
type sessionAgg struct {
	path       string
	sessionID  string
	cwd        string
	totalCost  float64
	turns      int
	lastTurnAt time.Time

	lastTurnCost float64
	lastTools    []string

	turnCosts [spikeWindow]float64
	tcCount   int
	tcHead    int
}

func (s *sessionAgg) recordCost(cost float64) {
	s.turnCosts[s.tcHead] = cost
	s.tcHead = (s.tcHead + 1) % spikeWindow
	if s.tcCount < spikeWindow {
		s.tcCount++
	}
}

func (s *sessionAgg) snapshotCosts() []float64 {
	out := make([]float64, s.tcCount)
	copy(out, s.turnCosts[:s.tcCount])
	return out
}

func lastToolNames(uses []parse.ToolUse) []string {
	if len(uses) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, u := range uses {
		if !seen[u.Name] {
			seen[u.Name] = true
			out = append(out, u.Name)
		}
	}
	return out
}

func lastToolsLabel(tools []string) string {
	if len(tools) == 0 {
		return "-"
	}
	return strings.Join(tools, "+")
}

// projectLabel returns the basename of cwd for compact display; falls
// back to the full path if Base reduces to "." or empty.
func projectLabel(cwd string) string {
	if cwd == "" {
		return "(unknown)"
	}
	b := filepath.Base(cwd)
	if b == "." || b == "/" || b == "" {
		return cwd
	}
	return b
}
