package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/kurofune/claudit/internal/notify"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/stat"
	"github.com/kurofune/claudit/internal/watch"
	"github.com/kurofune/claudit/internal/watch/term"
)

func runWatch(args []string) error {
	fs := flag.NewFlagSet("claudit watch", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to search for the session JSONL")
	pricesPath := fs.String("prices", "", "override pricing YAML path")
	intervalMS := fs.Int("interval-ms", 1500, "polling interval in milliseconds")
	budget := fs.Float64("budget", 0, "alert when running cost crosses this many USD (0 disables)")
	spikeThresh := fs.Float64("spike-threshold", 5.0, "flag a turn when its cost is >= N x the rolling median of the prior 20 turns (0 disables)")
	notifyOn := fs.Bool("notify", false, "send a desktop notification on budget cross and turn-cost spikes")
	all := fs.Bool("all", false, "tail every recently-modified session under --root, grouped by project")
	rolling := fs.Bool("rolling", true, "scan --root at startup and show today/week/month running totals above the per-session line")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "claudit watch — tail a live session JSONL and print running cost.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Usage:")
		fmt.Fprintln(out, "  claudit watch [flags] [session-id]")
		fmt.Fprintln(out, "  claudit watch --all [flags]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "If session-id is omitted, the most recently modified session is tailed.")
		fmt.Fprintln(out, "--all ignores any positional argument and tails every recently-modified session.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	prices, err := loadPrices(*pricesPath)
	if err != nil {
		return err
	}

	// SIGTERM: graceful shutdown on Unix (kill, docker stop, init systems)
	// so the deferred printSummary runs. No-op on Windows — the constant is
	// defined but the Go runtime never delivers it there.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *all {
		if fs.NArg() > 0 {
			return fmt.Errorf("--all does not take a session-id argument")
		}
		return runWatchAll(ctx, *root, prices, *intervalMS, *budget, *spikeThresh, *notifyOn, *rolling)
	}

	var path string
	switch fs.NArg() {
	case 0:
		path, err = watch.MostRecentJSONL(*root)
		if err != nil {
			return fmt.Errorf("locate latest session under %s: %w", *root, err)
		}
	case 1:
		path, err = watch.FindBySessionID(*root, fs.Arg(0))
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("watch takes 0 or 1 args; got %d", fs.NArg())
	}

	fmt.Fprintf(os.Stderr, "claudit watch: tailing %s\n", path)
	if *budget > 0 {
		fmt.Fprintf(os.Stderr, "claudit watch: budget alert at $%.2f\n", *budget)
	}

	renderer := term.New(os.Stdout)
	var notifier notify.Notifier
	if *notifyOn {
		notifier = notify.Default()
	}
	var rollingState *rollingTotals
	if *rolling {
		// Best-effort: a slow startup scan on a huge corpus shouldn't
		// block the live tail. If it errors we just don't show totals.
		rs, scanErr := newRollingTotals(*root, prices, time.Now())
		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "claudit watch: rolling totals disabled (%v)\n", scanErr)
		} else {
			rollingState = rs
		}
	}

	st := newWatchState(prices, *budget, *spikeThresh, notifier, renderer, rollingState)
	defer st.shutdown(os.Stderr)

	opts := watch.TailOptions{
		Interval:      time.Duration(*intervalMS) * time.Millisecond,
		FromBeginning: true,
	}
	return watch.Tail(ctx, path, opts, st.onEvent, st.onNotice)
}

// spikeWindow is the size of the rolling-median lookback for per-turn
// spike detection. Twenty turns is small enough to react quickly to a
// shift in the user's question style and large enough to stabilize the
// median against single-turn outliers.
const spikeWindow = 20

// watchState is the running aggregation behind the live status line.
// Single-goroutine — Tail runs the callbacks on its polling goroutine,
// and we don't hand state off elsewhere.
type watchState struct {
	prices      *pricing.Table
	budget      float64
	spikeThresh float64
	notifier    notify.Notifier
	renderer    *term.Renderer
	rolling     *rollingTotals
	started     time.Time

	totalCost float64
	turns     int
	tokens    tokensSum
	toolCost  map[string]float64

	lastTurnCost float64
	lastTools    []string

	// turnCosts is a ring buffer of the last spikeWindow turn costs,
	// used to compute the rolling median for spike detection.
	turnCosts [spikeWindow]float64
	tcCount   int // how many slots are valid (caps at spikeWindow)
	tcHead    int // next write position

	// maxTurnCost / maxTurnIndex track the most expensive single turn
	// for the end-of-session anomaly callouts.
	maxTurnCost  float64
	maxTurnIndex int

	budgetAlerted bool
	sessionID     string
	cwd           string
}

type tokensSum struct {
	in, out, c5m, c1h, cr int64
}

func (t *tokensSum) addUsage(u parse.Usage) {
	t.in += int64(u.InputTokens)
	t.out += int64(u.OutputTokens)
	t.c5m += int64(u.CacheCreate5mTokens)
	t.c1h += int64(u.CacheCreate1hTokens)
	t.cr += int64(u.CacheReadTokens)
}

// hitRatio is cache_read / (cache_read + input + cache_create_5m +
// cache_create_1h). Returns 0 when no cacheable traffic.
func (t tokensSum) hitRatio() float64 {
	denom := t.cr + t.in + t.c5m + t.c1h
	if denom <= 0 {
		return 0
	}
	return float64(t.cr) / float64(denom)
}

func newWatchState(prices *pricing.Table, budget, spikeThresh float64, notifier notify.Notifier, renderer *term.Renderer, rolling *rollingTotals) *watchState {
	return &watchState{
		prices:      prices,
		budget:      budget,
		spikeThresh: spikeThresh,
		notifier:    notifier,
		renderer:    renderer,
		rolling:     rolling,
		started:     time.Now(),
		toolCost:    map[string]float64{},
	}
}

func (s *watchState) onEvent(e watch.Event) {
	switch e.Kind {
	case parse.LineAssistant:
		t := e.Turn
		cost, _ := s.prices.Cost(t.Model,
			t.Usage.InputTokens, t.Usage.OutputTokens,
			t.Usage.CacheCreate5mTokens, t.Usage.CacheCreate1hTokens,
			t.Usage.CacheReadTokens)
		s.totalCost += cost
		s.turns++
		s.tokens.addUsage(t.Usage)
		s.lastTurnCost = cost
		s.lastTools = s.lastTools[:0]
		seen := map[string]bool{}
		for _, tu := range t.ToolUses {
			if !seen[tu.Name] {
				seen[tu.Name] = true
				s.lastTools = append(s.lastTools, tu.Name)
			}
			s.toolCost[tu.Name] += cost / float64(max(len(t.ToolUses), 1))
		}
		if s.sessionID == "" {
			s.sessionID = t.SessionID
		}
		if s.cwd == "" {
			s.cwd = t.CWD
		}
		if cost > s.maxTurnCost {
			s.maxTurnCost = cost
			s.maxTurnIndex = s.turns
		}
		if s.rolling != nil {
			s.rolling.addLive(t.Timestamp, cost, time.Now())
		}
		s.checkSpike(cost)
		s.checkBudget()
		s.recordTurnCost(cost)
	}
	s.render()
}

func (s *watchState) onNotice(n watch.Notice) {
	switch n.Kind {
	case watch.NoticeMalformed:
		// Quietly count; don't pollute the live line.
		return
	default:
		s.renderer.Println(fmt.Sprintf("[claudit watch] %s", n.Message))
		s.render()
	}
}

func (s *watchState) checkBudget() {
	if s.budget <= 0 || s.budgetAlerted {
		return
	}
	if s.totalCost >= s.budget {
		s.renderer.Println(fmt.Sprintf("[claudit watch] BUDGET CROSSED: $%.2f >= $%.2f", s.totalCost, s.budget))
		s.budgetAlerted = true
		if s.notifier != nil {
			_ = s.notifier.Send("claudit: budget crossed",
				fmt.Sprintf("Running cost $%.2f crossed budget $%.2f", s.totalCost, s.budget))
		}
	}
}

// checkSpike flags the just-completed turn when its cost is >=
// spikeThresh × the rolling median of the prior spikeWindow turns.
// Needs at least spikeWindow/2 prior samples — earlier than that, the
// median is too noisy and false positives drown the signal.
func (s *watchState) checkSpike(cost float64) {
	if s.spikeThresh <= 0 || s.tcCount < spikeWindow/2 {
		return
	}
	med := stat.Median(s.snapshotTurnCosts())
	if med <= 0 {
		return
	}
	ratio := cost / med
	if ratio < s.spikeThresh {
		return
	}
	tools := strings.Join(s.lastTools, "+")
	if tools == "" {
		tools = "no-tool"
	}
	msg := fmt.Sprintf("[claudit watch] SPIKE: turn %d cost $%.4f — %.1fx your %d-turn median ($%.4f) — last tools: %s",
		s.turns, cost, ratio, s.tcCount, med, tools)
	s.renderer.Println(msg)
	if s.notifier != nil {
		_ = s.notifier.Send("claudit: cost spike",
			fmt.Sprintf("Turn %d cost $%.4f (%.1fx median)", s.turns, cost, ratio))
	}
}

// snapshotTurnCosts returns the current contents of the ring buffer
// in unspecified order — fine for median, which sorts anyway.
func (s *watchState) snapshotTurnCosts() []float64 {
	out := make([]float64, s.tcCount)
	copy(out, s.turnCosts[:s.tcCount])
	return out
}

func (s *watchState) recordTurnCost(cost float64) {
	s.turnCosts[s.tcHead] = cost
	s.tcHead = (s.tcHead + 1) % spikeWindow
	if s.tcCount < spikeWindow {
		s.tcCount++
	}
}

// render assembles a Frame and asks the renderer to paint it.
func (s *watchState) render() {
	frame := term.Frame{}
	if s.rolling != nil {
		frame.Header = []string{s.rolling.statusLine()}
	}
	frame.Body = []string{s.statusLine()}
	s.renderer.Render(frame)
}

func (s *watchState) statusLine() string {
	tools := strings.Join(s.lastTools, "+")
	if tools == "" {
		tools = "-"
	}
	hit := "—"
	if r := s.tokens.hitRatio(); r > 0 {
		hit = fmt.Sprintf("%.1f%%", 100*r)
	}
	return fmt.Sprintf("$%.2f · %d turns · %s hit · last: %s (+$%.4f)",
		s.totalCost, s.turns, hit, tools, s.lastTurnCost)
}

// shutdown clears the live region and writes the final summary block.
func (s *watchState) shutdown(w io.Writer) {
	s.renderer.Clear()
	s.printSummary(w)
}

// printSummary writes a final block to w on shutdown.
func (s *watchState) printSummary(w io.Writer) {
	fmt.Fprintln(w) // newline to escape the rolling status line
	dur := time.Since(s.started).Truncate(time.Second)
	fmt.Fprintln(w, "=== claudit watch summary ===")
	fmt.Fprintf(w, "session:     %s\n", firstNonEmpty(s.sessionID, "(none seen)"))
	fmt.Fprintf(w, "cwd:         %s\n", firstNonEmpty(s.cwd, "(none seen)"))
	fmt.Fprintf(w, "duration:    %s\n", dur)
	fmt.Fprintf(w, "turns:       %d\n", s.turns)
	fmt.Fprintf(w, "cost:        $%.4f\n", s.totalCost)
	r := s.tokens.hitRatio()
	if r > 0 {
		fmt.Fprintf(w, "hit ratio:   %.1f%%\n", 100*r)
	} else {
		fmt.Fprintf(w, "hit ratio:   —\n")
	}
	// Anomaly callouts. Session median is computed over the (capped)
	// ring buffer — for a long session this is the trailing 20 turns,
	// matching the spike detector. Two-sample sessions don't get this
	// line; the ratio would be meaningless.
	if s.turns >= 3 && s.maxTurnCost > 0 {
		med := stat.Median(s.snapshotTurnCosts())
		if med > 0 {
			fmt.Fprintf(w, "max turn:    $%.4f at turn %d (%.1fx session median)\n",
				s.maxTurnCost, s.maxTurnIndex, s.maxTurnCost/med)
		} else {
			fmt.Fprintf(w, "max turn:    $%.4f at turn %d\n", s.maxTurnCost, s.maxTurnIndex)
		}
	}
	if s.rolling != nil {
		if base := s.rolling.baselineHitRatio(); base > 0 && r > 0 {
			delta := 100 * (r - base)
			fmt.Fprintf(w, "vs 7d avg:   hit ratio %+.1f pp (this session %.1f%%, 7-day %.1f%%)\n",
				delta, 100*r, 100*base)
		}
	}
	if len(s.toolCost) > 0 {
		fmt.Fprintln(w, "top tools:")
		type kv struct {
			name string
			cost float64
		}
		var pairs []kv
		for n, c := range s.toolCost {
			pairs = append(pairs, kv{n, c})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].cost > pairs[j].cost })
		limit := 3
		if limit > len(pairs) {
			limit = len(pairs)
		}
		for _, p := range pairs[:limit] {
			fmt.Fprintf(w, "  - %s: $%.4f\n", p.name, p.cost)
		}
	}
}

func firstNonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
