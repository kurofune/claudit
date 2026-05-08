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

	"github.com/nategross/claudit/internal/parse"
	"github.com/nategross/claudit/internal/pricing"
	"github.com/nategross/claudit/internal/watch"
)

func runWatch(args []string) error {
	fs := flag.NewFlagSet("claudit watch", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to search for the session JSONL")
	pricesPath := fs.String("prices", "", "override pricing YAML path")
	intervalMS := fs.Int("interval-ms", 1500, "polling interval in milliseconds")
	budget := fs.Float64("budget", 0, "alert when running cost crosses this many USD (0 disables)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	prices, err := loadPrices(*pricesPath)
	if err != nil {
		return err
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

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	st := newWatchState(prices, *budget, os.Stdout)
	defer st.printSummary(os.Stderr)

	opts := watch.TailOptions{
		Interval:      time.Duration(*intervalMS) * time.Millisecond,
		FromBeginning: true,
	}
	return watch.Tail(ctx, path, opts, st.onEvent, st.onNotice)
}

// watchState is the running aggregation behind the live status line.
// Single-goroutine — Tail runs the callbacks on its polling goroutine,
// and we don't hand state off elsewhere.
type watchState struct {
	prices  *pricing.Table
	budget  float64
	out     io.Writer
	started time.Time

	totalCost   float64
	turns       int
	tokens      tokensSum
	toolCost    map[string]float64

	lastTurnCost float64
	lastTools    []string

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

func newWatchState(prices *pricing.Table, budget float64, out io.Writer) *watchState {
	return &watchState{
		prices:   prices,
		budget:   budget,
		out:      out,
		started:  time.Now(),
		toolCost: map[string]float64{},
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
		s.checkBudget()
	}
	s.printStatus()
}

func (s *watchState) onNotice(n watch.Notice) {
	switch n.Kind {
	case watch.NoticeMalformed:
		// Quietly count; don't pollute the live line.
		return
	default:
		// Print on its own line above the status, then reprint status.
		fmt.Fprintf(os.Stderr, "\r\033[2K[claudit watch] %s\n", n.Message)
		s.printStatus()
	}
}

func (s *watchState) checkBudget() {
	if s.budget <= 0 || s.budgetAlerted {
		return
	}
	if s.totalCost >= s.budget {
		fmt.Fprintf(os.Stderr, "\r\033[2K[claudit watch] BUDGET CROSSED: $%.2f >= $%.2f\n",
			s.totalCost, s.budget)
		s.budgetAlerted = true
	}
}

// printStatus overwrites a single line. \r returns to column 0; \033[2K
// clears the line so a shorter status doesn't leave stale characters.
func (s *watchState) printStatus() {
	tools := strings.Join(s.lastTools, "+")
	if tools == "" {
		tools = "-"
	}
	hit := "—"
	if r := s.tokens.hitRatio(); r > 0 {
		hit = fmt.Sprintf("%.1f%%", 100*r)
	}
	fmt.Fprintf(s.out, "\r\033[2K$%.2f · %d turns · %s hit · last: %s (+$%.4f)",
		s.totalCost, s.turns, hit, tools, s.lastTurnCost)
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
