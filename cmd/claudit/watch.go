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
	"sync"
	"syscall"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/notify"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/stat"
	"github.com/kurofune/claudit/internal/watch"
)

// rollingPollInterval is how often watch re-walks the projects root to
// refresh the hour/today/week/month panel. Stat-only for unchanged
// files, so this stays cheap even on a large tree.
const rollingPollInterval = 2 * time.Second

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
	// Deprecated: the rolling panel now reads the full corpus (the same
	// data report/serve use) and re-scans on a poll, so there is no
	// window to clamp the month total. Kept registered so existing
	// invocations/aliases don't error; the value is ignored.
	fs.Int("scan-days", 30, "(deprecated; ignored) the rolling panel now scans the full corpus, so the month total is never clamped")
	fs.Usage = func() {
		ew := &errWriter{w: fs.Output()}
		ew.Println("claudit watch — tail a live session JSONL and print running cost.")
		ew.Println()
		ew.Println("Usage:")
		ew.Println("  claudit watch [flags] [session-id]")
		ew.Println("  claudit watch --all [flags]")
		ew.Println()
		ew.Println("If session-id is omitted, the most recently modified session is tailed.")
		ew.Println("--all ignores any positional argument and tails every recently-modified session.")
		ew.Println()
		ew.Println("On a TTY, watch takes over the screen (alt buffer) and restores it on Ctrl-C.")
		ew.Println("Piped output falls back to one line per status update.")
		ew.Println()
		ew.Println("Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "scan-days" {
			fmt.Fprintln(os.Stderr, "claudit watch: --scan-days is deprecated and ignored; the rolling panel now scans the full corpus (no clamp).")
		}
	})

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

	p := newPainter(os.Stdout)
	var notifier notify.Notifier
	if *notifyOn {
		notifier = notify.Default()
	}
	var cache *corpus.Cache
	if *rolling {
		cache = corpus.New(*root)
		// Prime synchronously so the first frame already shows real
		// totals; the poller's own initial refresh is then a cheap no-op.
		if _, scanErr := cache.Refresh(); scanErr != nil {
			fmt.Fprintf(os.Stderr, "claudit watch: rolling totals disabled (%v)\n", scanErr)
			cache = nil
		} else {
			go cache.RunPoller(ctx, rollingPollInterval, func(err error) {
				fmt.Fprintf(os.Stderr, "claudit watch: rolling refresh: %v\n", err)
			})
		}
	}

	st := newWatchState(prices, *budget, *spikeThresh, notifier, p, cache)
	defer st.shutdown(os.Stderr)

	// Repaint on a steady tick so the rolling panel reflects corpus
	// changes (spend in other projects) even while the tailed session
	// is idle. Live turns from the tail trigger their own repaint too.
	if cache != nil {
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					st.tick()
				}
			}
		}()
	}

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
	// cache is the shared corpus loader backing the rolling panel; nil
	// when rolling totals are disabled. The same data layer serve and
	// report use, so watch's hour/today/week/month match them.
	cache *corpus.Cache

	// mu guards the live-session fields below, which onEvent mutates
	// from the tail goroutine and the repaint ticker reads.
	mu      sync.Mutex
	started time.Time

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

func newWatchState(prices *pricing.Table, budget, spikeThresh float64, notifier notify.Notifier, p painter, cache *corpus.Cache) *watchState {
	return &watchState{
		prices:      prices,
		budget:      budget,
		spikeThresh: spikeThresh,
		notifier:    notifier,
		painter:     p,
		cache:       cache,
		started:     time.Now(),
		toolCost:    map[string]float64{},
	}
}

func (s *watchState) onEvent(e watch.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		s.notifyAsync("claudit: budget crossed",
			fmt.Sprintf("Running cost $%.2f crossed budget $%.2f", s.totalCost, s.budget))
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
	s.notifyAsync("claudit: cost spike",
		fmt.Sprintf("Turn %d cost $%.4f (%.1fx median)", s.turns, cost, ratio))
}

// notifyAsync fires a desktop notification on a fresh goroutine so a
// slow or hung backend (osascript / notify-send shells out and waits
// for the subprocess) cannot stall the polling goroutine — which
// would otherwise miss ctx cancellation and wedge shutdown.
func (s *watchState) notifyAsync(title, body string) {
	if s.notifier == nil {
		return
	}
	n := s.notifier
	go func() { _ = n.Send(title, body) }()
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

// tick repaints from the repaint ticker. It takes the lock the tail
// goroutine also uses for the live-session fields, then renders the
// current frame — picking up any corpus refresh the poller published.
func (s *watchState) tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.render()
}

// render builds and paints the current frame. Caller must hold s.mu.
// The rolling panel is computed from the shared corpus snapshot via
// aggregate.RollingTotals — the same data + math report and serve use.
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
	if s.cache != nil {
		hour, today, week, month := aggregate.RollingTotals(s.cache.Snapshot().Turns, s.prices, time.Now())
		frame.HasRolling = true
		frame.Rolling = RollingPanelData{Hour: hour, Today: today, Week: week, Month: month}
	}
	s.painter.Render(frame)
}

func (s *watchState) shutdown(w io.Writer) {
	s.painter.Close()
	s.printSummary(w)
}

// baselineHitRatio7d returns the trailing-7-day cache hit ratio (0..1)
// across the whole corpus, or 0 when there's no cache-eligible traffic.
// Computed via a filtered aggregator over the shared snapshot — the
// same token accounting every other view uses.
func (s *watchState) baselineHitRatio7d() float64 {
	if s.cache == nil {
		return 0
	}
	agg := aggregate.New(s.prices).WithFilter(aggregate.Filter{
		Since: time.Now().AddDate(0, 0, -7),
	})
	for _, t := range s.cache.Snapshot().Turns {
		agg.Add(t)
	}
	tok := agg.Totals().Tokens
	denom := tok.CacheReadTokens + tok.InputTokens + tok.CacheCreate5mTokens + tok.CacheCreate1hTokens
	if denom <= 0 {
		return 0
	}
	return float64(tok.CacheReadTokens) / float64(denom)
}

func (s *watchState) printSummary(w io.Writer) {
	ew := &errWriter{w: w}
	ew.Println()
	dur := time.Since(s.started).Truncate(time.Second)
	ew.Println("=== claudit watch summary ===")
	ew.Printf("session:     %s\n", firstNonEmpty(s.sessionID, "(none seen)"))
	ew.Printf("cwd:         %s\n", firstNonEmpty(s.cwd, "(none seen)"))
	ew.Printf("duration:    %s\n", dur)
	ew.Printf("turns:       %d\n", s.turns)
	ew.Printf("cost:        $%.4f\n", s.totalCost)
	r := s.tokens.hitRatio()
	if r > 0 {
		ew.Printf("hit ratio:   %.1f%%\n", 100*r)
	} else {
		ew.Printf("hit ratio:   —\n")
	}
	if s.turns >= 3 && s.maxTurnCost > 0 {
		med := stat.Median(s.snapshotTurnCosts())
		if med > 0 {
			ew.Printf("max turn:    $%.4f at turn %d (%.1fx session median)\n",
				s.maxTurnCost, s.maxTurnIndex, s.maxTurnCost/med)
		} else {
			ew.Printf("max turn:    $%.4f at turn %d\n", s.maxTurnCost, s.maxTurnIndex)
		}
	}
	if s.cache != nil {
		if base := s.baselineHitRatio7d(); base > 0 && r > 0 {
			delta := 100 * (r - base)
			ew.Printf("vs 7d avg:   hit ratio %+.1f pp (this session %.1f%%, 7-day %.1f%%)\n",
				delta, 100*r, 100*base)
		}
	}
	if len(s.toolCost) > 0 {
		ew.Println("top tools:")
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
			ew.Printf("  - %s: $%.4f\n", p.name, p.cost)
		}
	}
}

func firstNonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
