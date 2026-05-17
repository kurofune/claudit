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
	rolling := fs.Bool("rolling", true, "scan --root at startup and show today/week/month running totals at the top of the UI")
	scanDays := fs.Int("scan-days", defaultScanDays, "rolling-totals startup scan window in days; smaller is faster but clamps the month total to this window")
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
		fmt.Fprintln(out, "On a TTY, watch takes over the screen (alt buffer) and restores it on Ctrl-C.")
		fmt.Fprintln(out, "Piped output falls back to one line per status update.")
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
	// so the deferred summary runs. No-op on Windows.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *all {
		if fs.NArg() > 0 {
			return fmt.Errorf("--all does not take a session-id argument")
		}
		return runWatchAll(ctx, *root, prices, *intervalMS, *budget, *spikeThresh, *notifyOn, *rolling, *scanDays)
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

	p := newPainter(os.Stdout)
	var notifier notify.Notifier
	if *notifyOn {
		notifier = notify.Default()
	}
	var rollingState *rollingTotals
	if *rolling {
		rs, scanErr := newRollingTotalsWithDays(*root, prices, time.Now(), *scanDays)
		if scanErr != nil {
			fmt.Fprintf(os.Stderr, "claudit watch: rolling totals disabled (%v)\n", scanErr)
		} else {
			rollingState = rs
		}
	}

	st := newWatchState(prices, *budget, *spikeThresh, notifier, p, rollingState)
	defer st.shutdown(os.Stderr)

	opts := watch.TailOptions{
		Interval:      time.Duration(*intervalMS) * time.Millisecond,
		FromBeginning: true,
	}
	return watch.Tail(ctx, path, opts, st.onEvent, st.onNotice)
}

// spikeWindow is the size of the rolling-median lookback for per-turn
// spike detection. Twenty turns is small enough to react quickly and
// large enough to stabilize against single-turn outliers.
const spikeWindow = 20

type watchState struct {
	prices      *pricing.Table
	budget      float64
	spikeThresh float64
	notifier    notify.Notifier
	painter     painter
	rolling     *rollingTotals
	started     time.Time

	totalCost float64
	turns     int
	tokens    tokensSum
	toolCost  map[string]float64

	lastTurnCost float64
	lastTools    []string

	turnCosts [spikeWindow]float64
	tcCount   int
	tcHead    int

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

func (t tokensSum) hitRatio() float64 {
	denom := t.cr + t.in + t.c5m + t.c1h
	if denom <= 0 {
		return 0
	}
	return float64(t.cr) / float64(denom)
}

func newWatchState(prices *pricing.Table, budget, spikeThresh float64, notifier notify.Notifier, p painter, rolling *rollingTotals) *watchState {
	return &watchState{
		prices:      prices,
		budget:      budget,
		spikeThresh: spikeThresh,
		notifier:    notifier,
		painter:     p,
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
		if s.rolling != nil && e.Live {
			s.rolling.addLive(t.Timestamp, cost, time.Now())
		}
		if e.Live {
			s.checkSpike(cost)
			s.checkBudget()
		}
		s.recordTurnCost(cost)
	}
	s.render()
}

func (s *watchState) onNotice(n watch.Notice) {
	switch n.Kind {
	case watch.NoticeMalformed:
		return
	default:
		s.painter.Alert(s.painter.Style().Dim("[notice] " + n.Message))
	}
}

func (s *watchState) checkBudget() {
	if s.budget <= 0 || s.budgetAlerted {
		return
	}
	if s.totalCost >= s.budget {
		s.painter.Alert(styleBudgetSingle(s.painter.Style(), s.totalCost, s.budget))
		s.budgetAlerted = true
		if s.notifier != nil {
			_ = s.notifier.Send("claudit: budget crossed",
				fmt.Sprintf("Running cost $%.2f crossed budget $%.2f", s.totalCost, s.budget))
		}
	}
}

// checkSpike flags the just-completed turn when its cost is >=
// spikeThresh × the rolling median of the prior spikeWindow turns.
// Needs at least spikeWindow/2 prior samples. Dedupes against the
// immediately-previous turn's cost so back-to-back identical-cost
// rows (Claude Code wire pattern) only fire once.
func (s *watchState) checkSpike(cost float64) {
	if s.spikeThresh <= 0 || s.tcCount < spikeWindow/2 {
		return
	}
	if s.tcCount > 0 {
		prev := s.turnCosts[(s.tcHead-1+spikeWindow)%spikeWindow]
		if sameCost(cost, prev) {
			return
		}
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
	s.painter.Alert(styleSpikeSingle(s.painter.Style(), s.turns, cost, ratio, s.tcCount, med, tools))
	if s.notifier != nil {
		_ = s.notifier.Send("claudit: cost spike",
			fmt.Sprintf("Turn %d cost $%.4f (%.1fx median)", s.turns, cost, ratio))
	}
}

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

func (s *watchState) render() {
	st := s.painter.Style()
	frame := Frame{
		Live: LivePanelData{
			Header: liveHeader(st, s.totalCost, 1),
			Rows: []string{
				singleSessionLine(st, s.totalCost, s.turns, s.tokens.hitRatio(), s.lastTools, s.lastTurnCost),
			},
		},
	}
	if s.rolling != nil {
		today, week, month := s.rolling.totals(time.Now())
		frame.HasRolling = true
		frame.Rolling = RollingPanelData{Today: today, Week: week, Month: month}
	}
	s.painter.Render(frame)
}

func (s *watchState) shutdown(w io.Writer) {
	s.painter.Close()
	s.printSummary(w)
}

func (s *watchState) printSummary(w io.Writer) {
	fmt.Fprintln(w)
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
