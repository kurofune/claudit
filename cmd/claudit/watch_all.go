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

// Tuning knobs for --all. Chosen to keep the UI calm without missing
// real activity. Not flags — promote to flags if someone needs them.
const (
	discoveryInterval  = 10 * time.Second
	recentWindow       = 15 * time.Minute
	idleHide           = 15 * time.Minute
	maxVisibleSessions = 6
)

// runWatchAll is the entry point for `claudit watch --all`. Runs the
// discovery loop, spawns Tail goroutines per session, fans events
// into a single render goroutine that owns all state.
func runWatchAll(ctx context.Context, root string, prices *pricing.Table, intervalMS int, budget, spikeThresh float64, notifyOn, rolling bool, scanDays int) error {
	fmt.Fprintf(os.Stderr, "claudit watch --all: tailing every session under %s touched in the last %s\n", root, recentWindow)
	if budget > 0 {
		fmt.Fprintf(os.Stderr, "claudit watch --all: budget alert at $%.2f (combined across sessions)\n", budget)
	}

	p := newPainter(os.Stdout)
	var notifier notify.Notifier
	if notifyOn {
		notifier = notify.Default()
	}
	var rollingState *rollingTotals
	if rolling {
		rs, scanErr := newRollingTotalsWithDays(root, prices, time.Now(), scanDays)
		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "claudit watch --all: rolling totals disabled (%v)\n", scanErr)
		} else {
			rollingState = rs
		}
	}

	hub := newMultiHub(prices, budget, spikeThresh, notifier, p, rollingState)
	defer hub.shutdown(os.Stderr)
	stopCh := make(chan struct{})

	tailInterval := time.Duration(intervalMS) * time.Millisecond
	if tailInterval <= 0 {
		tailInterval = time.Second
	}

	var wg sync.WaitGroup

	renderDone := make(chan struct{})
	go func() {
		defer close(renderDone)
		hub.run(stopCh)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(discoveryInterval)
		defer tick.Stop()
		hub.discover(ctx, root, tailInterval, &wg)
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
	close(stopCh)
	<-renderDone
	return nil
}

// multiHub owns the shared state behind `watch --all`. Producers
// (Tail goroutines, discovery) post messages on eventCh / noticeCh;
// the single consumer (hub.run) mutates state.
type multiHub struct {
	prices      *pricing.Table
	budget      float64
	spikeThresh float64
	notifier    notify.Notifier
	painter     painter
	rolling     *rollingTotals

	eventCh  chan taggedEvent
	noticeCh chan taggedNotice

	mu      sync.Mutex
	tailing map[string]bool

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

func newMultiHub(prices *pricing.Table, budget, spikeThresh float64, notifier notify.Notifier, p painter, rolling *rollingTotals) *multiHub {
	return &multiHub{
		prices:      prices,
		budget:      budget,
		spikeThresh: spikeThresh,
		notifier:    notifier,
		painter:     p,
		rolling:     rolling,
		eventCh:     make(chan taggedEvent, 256),
		noticeCh:    make(chan taggedNotice, 64),
		tailing:     map[string]bool{},
		state:       newMultiState(),
	}
}

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
// Exits when stop is closed; the caller closes stop only after the
// producing tail goroutines have finished, so no events are lost.
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
	prevTurnCost := s.lastSeenCost()
	s.totalCost += cost
	s.turns++
	s.lastTurnCost = cost
	s.lastTurnAt = time.Now()
	s.lastTools = lastToolNames(t.ToolUses)
	h.state.combinedCost += cost
	if h.rolling != nil && te.ev.Live {
		h.rolling.addLive(t.Timestamp, cost, time.Now())
	}
	s.recordCost(cost)
	if te.ev.Live && h.spikeThresh > 0 && s.tcCount >= spikeWindow/2 && !sameCost(cost, prevTurnCost) {
		med := stat.Median(s.snapshotCosts())
		if med > 0 && cost/med >= h.spikeThresh {
			tools := strings.Join(s.lastTools, "+")
			if tools == "" {
				tools = "no-tool"
			}
			msg := styleSpikeMulti(h.painter.Style(), projectLabel(s.cwd), s.turns, cost, med, tools)
			h.painter.Alert(msg)
			if h.notifier != nil {
				_ = h.notifier.Send("claudit: cost spike",
					fmt.Sprintf("%s turn %d cost $%.4f (%.1fx median)", projectLabel(s.cwd), s.turns, cost, cost/med))
			}
		}
	}
	if te.ev.Live && h.budget > 0 && !h.state.budgetAlerted && h.state.combinedCost >= h.budget {
		h.painter.Alert(styleBudgetMulti(h.painter.Style(), h.state.combinedCost, h.budget))
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
	// We don't surface NoticeOpened — too noisy when --all is tailing
	// half a dozen sessions at startup. Other notices (rotation,
	// truncation) get a dim alert entry.
	if tn.n.Kind == watch.NoticeOpened {
		return
	}
	h.painter.Alert(h.painter.Style().Dim("[notice] " + tn.n.Message))
	h.paint()
}

func sameCost(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// paint builds a Frame and asks the painter to draw it.
func (h *multiHub) paint() {
	now := time.Now()
	st := h.painter.Style()
	frame := Frame{}
	if h.rolling != nil {
		hour, today, week, month := h.rolling.totals(now)
		frame.HasRolling = true
		frame.Rolling = RollingPanelData{Hour: hour, Today: today, Week: week, Month: month}
	}
	groups := h.state.groupByProject(now)
	var visibleTotal float64
	var visible int
	for _, g := range groups {
		visibleTotal += g.totalCost()
		visible += len(g.sessions)
	}
	frame.Live.Header = liveHeader(st, visibleTotal, visible)

	if len(groups) == 0 {
		frame.Live.Rows = nil
		h.painter.Render(frame)
		return
	}

	// Pre-measure detail-row columns across every visible session so
	// turn-count / total-cost cells align across projects. The "last
	// turn" cell is the rightmost (no padding needed) so it's not
	// measured.
	turnCol, totalCol := measureDetailCols(st, groups)

	rows := []string{}
	visibleRows := 0
	overflow := 0
	for gi, g := range groups {
		if visibleRows >= maxVisibleSessions {
			overflow += len(g.sessions)
			continue
		}
		// Blank line between project groups so the eye sees them as
		// separate units. Skipped before the first group.
		if gi > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, projectHeading(st, projectLabel(g.cwd), len(g.sessions), g.totalTurns(), g.totalCost()))
		for _, sess := range g.sessions {
			if visibleRows >= maxVisibleSessions {
				overflow++
				continue
			}
			rows = append(rows, projectDetailRow(st, sess.turns, sess.totalCost, sess.lastTools, sess.lastTurnCost, turnCol, totalCol))
			visibleRows++
		}
	}
	if overflow > 0 {
		rows = append(rows, st.Dim(fmt.Sprintf("    +%d more session(s) hidden", overflow)))
	}
	frame.Live.Rows = rows
	h.painter.Render(frame)
}

// measureDetailCols computes the visible-width column targets for the
// per-session detail row across every visible session. The trailing
// "last turn" cell is unmeasured — it's rightmost, so its width
// doesn't affect alignment of anything else.
func measureDetailCols(st term.Style, groups []projectGroup) (turn, total int) {
	for _, g := range groups {
		for _, s := range g.sessions {
			tc := fmt.Sprintf("%d %s", s.turns, label(st, "turns"))
			if w := term.VisibleWidth(tc); w > turn {
				turn = w
			}
			tot := fmt.Sprintf("%s %s", moneyByMag(st, s.totalCost, 4), label(st, "total"))
			if w := term.VisibleWidth(tot); w > total {
				total = w
			}
		}
	}
	return turn, total
}

func (h *multiHub) shutdown(w *os.File) {
	h.painter.Close()
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

// multiState aggregates per-session data. All access through hub.run,
// so no internal locking.
type multiState struct {
	sessions      map[string]*sessionAgg
	combinedCost  float64
	budgetAlerted bool
}

func newMultiState() *multiState {
	return &multiState{sessions: map[string]*sessionAgg{}}
}

func (m *multiState) session(path, sessID, cwd string) *sessionAgg {
	if s, ok := m.sessions[path]; ok {
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

func (m *multiState) groupByProject(now time.Time) []projectGroup {
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

func (s *sessionAgg) lastSeenCost() float64 {
	if s.tcCount == 0 {
		return 0
	}
	return s.turnCosts[(s.tcHead-1+spikeWindow)%spikeWindow]
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

func projectLabel(cwd string) string {
	if cwd == "" {
		return "(unknown)"
	}
	b := filepath.Base(cwd)
	if b == "." || b == "/" || b == `\` || b == "" {
		return cwd
	}
	return b
}
