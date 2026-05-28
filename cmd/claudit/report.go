package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/render"
)

func runReport(args []string) error {
	fs := flag.NewFlagSet("claudit report", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to walk (defaults to $CLAUDE_CONFIG_DIR/projects or ~/.claude/projects)")
	since := fs.String("since", "", "only include turns at or after this date (YYYY-MM-DD)")
	until := fs.String("until", "", "only include turns strictly before this date (YYYY-MM-DD)")
	last := fs.String("last", "", "shorthand for --since: window of the last N days or weeks, e.g. 7d, 2w (conflicts with --since)")
	project := fs.String("project", "", "case-insensitive substring match on cwd")
	asJSON := fs.Bool("json", false, "emit JSON instead of markdown")
	asHTML := fs.Bool("html", true, "emit a self-contained interactive HTML report")
	pricesPath := fs.String("prices", "", "override pricing YAML path (default: ~/.config/claudit/prices.yaml)")
	minCost := fs.Float64("min-cost", 0.01, "hide by-project rows below this USD cost (markdown only)")
	drillTop := fs.Int("drill-top", 15, "show top-N rows per tool in the drill-down section (0 disables)")
	agentTop := fs.Int("agent-top", 20, "show top-N most expensive subagent invocations (0 disables)")
	agentType := fs.String("agent-type", "", "restrict the invocation section to one subagent type (e.g. general-purpose)")
	hotspots := fs.Int("hotspots", 10, "show top-N cost hotspots at the top of the report, each with a copyable LLM prompt (0 disables)")
	by := fs.String("by", "day", "trend mode: bucket spend over time — one of day|week|month (empty disables)")
	cacheTop := fs.Int("cache-top", 10, "show top-N cache-miss offenders per dimension in the cache efficiency section (0 disables)")
	promptTop := fs.Int("prompt-top", 15, "show top-N most expensive user prompts (0 disables)")
	sessionsTop := fs.Int("sessions", 50, "show top-N most expensive sessions in the drill-down view (0 disables the Sessions section; HTML only)")
	redact := fs.Bool("redact", false, "replace prompt text in the Sessions view with '[redacted N chars]' (HTML only)")
	fs.Usage = func() {
		ew := &errWriter{w: fs.Output()}
		ew.Println("claudit report — generate a cost/usage report from session JSONL files.")
		ew.Println()
		ew.Println("Usage:")
		ew.Println("  claudit report [flags]")
		ew.Println()
		ew.Println("Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	prices, err := loadPrices(*pricesPath)
	if err != nil {
		return err
	}

	filter := aggregate.Filter{ProjectSubstring: *project}
	if *last != "" && *since != "" {
		return fmt.Errorf("--last and --since are mutually exclusive")
	}
	if *last != "" {
		d, err := parseLastDuration(*last)
		if err != nil {
			return fmt.Errorf("--last: %w", err)
		}
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		filter.Since = midnight.Add(-d)
	}
	if *since != "" {
		t, err := parseLocalDate(*since)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
		filter.Since = t
	}
	if *until != "" {
		t, err := parseLocalDate(*until)
		if err != nil {
			return fmt.Errorf("--until: %w", err)
		}
		filter.Until = t
	}

	// mtime pre-filter: when --since (or --last) bounds the window,
	// any file whose mtime predates the bound can't contain a turn
	// inside the window, so corpus skips it before opening.
	snap, err := corpus.LoadConcurrent(*root, filter.Since)
	if err != nil {
		return err
	}
	turns, userMsgs, parentLinks := snap.Turns, snap.Users, snap.Links

	period := aggregate.Period(*by)
	if *by != "" && !period.Valid() {
		return fmt.Errorf("--by: must be one of day|week|month, got %q", *by)
	}

	// PromptIndex is built from the full corpus (unfiltered) — the
	// chain walk is structural, and a turn's originating prompt is the
	// same regardless of whether the report's date window includes it.
	promptIdx := aggregate.BuildPromptIndex(turns, userMsgs, parentLinks)
	agg := aggregate.New(prices).WithFilter(filter).WithPeriod(period).WithPromptIndex(promptIdx)

	lookup := newSubagentLookup()
	for _, t := range turns {
		agg.AddWithSubagent(t, lookup)
	}

	// Build per-session drill-down timelines. Done outside the aggregator
	// because it needs the raw turn/msg/parent-link arrays (which the
	// aggregator doesn't retain). Skipped when --sessions=0 or rendering
	// non-HTML — keeps the markdown/JSON paths unchanged.
	var sessionTimelines []aggregate.SessionTimeline
	if *asHTML && !*asJSON && *sessionsTop > 0 {
		var err error
		sessionTimelines, err = aggregate.BuildSessionTimelines(
			context.Background(), turns, userMsgs, parentLinks, prices, filter,
			aggregate.SessionTimelinesOptions{
				TopN:           *sessionsTop,
				Redact:         *redact,
				MaxPromptChars: 2000,
			},
		)
		if err != nil {
			return err
		}
	}

	// Render. --json takes precedence over --html so an explicit --json
	// wins against the html-by-default behavior.
	if *asJSON {
		if err := render.JSON(os.Stdout, agg); err != nil {
			return err
		}
	} else if *asHTML {
		if err := render.HTMLWithOptions(context.Background(), os.Stdout, agg, render.HTMLOptions{
			SessionTimelines: sessionTimelines,
			Version:          versionShort(),
		}); err != nil {
			return err
		}
	} else {
		if err := render.MarkdownWithOptions(os.Stdout, agg, render.Options{
			MinProjectCost:  *minCost,
			DrillTop:        *drillTop,
			AgentTop:        *agentTop,
			AgentTypeFilter: *agentType,
			Hotspots:        *hotspots,
			CacheTop:        *cacheTop,
			PromptTop:       *promptTop,
		}); err != nil {
			return err
		}
	}

	emitWarnings(snap.Malformed, snap.FileErrors)
	return nil
}

// parseLocalDate parses a YYYY-MM-DD date at midnight in the local
// zone. Date filters express wall-clock intent ("everything from the
// 1st"), so they resolve against local time — consistent with --last,
// serve's ?since/?until, and watch's rolling buckets. Plain time.Parse
// would silently pin the boundary to UTC.
func parseLocalDate(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02", s, time.Local)
}

// parseLastDuration parses "Nd" or "Nw" (positive integer N) into a duration.
// time.ParseDuration doesn't recognize d/w, hence the hand-roll.
func parseLastDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("expected Nd or Nw, got %q", s)
	}
	unit := s[len(s)-1]
	var mult time.Duration
	switch unit {
	case 'd':
		mult = 24 * time.Hour
	case 'w':
		mult = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unit must be 'd' or 'w', got %q", string(unit))
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("expected positive integer prefix, got %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive, got %d", n)
	}
	return time.Duration(n) * mult, nil
}
