// claudit — audit Claude Code session JSONL files for token & cost spend.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/render"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "claudit:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "--help":
			fmt.Fprint(os.Stdout, topLevelUsage)
			return nil
		case "report":
			return runReport(args[1:])
		case "diff":
			return runDiff(args[1:])
		case "watch":
			return runWatch(args[1:])
		}
	}
	return runReport(args)
}

const topLevelUsage = `claudit — audit Claude Code session JSONL files for token & cost spend.

Usage:
  claudit <command> [flags]
  claudit [flags]              (alias for "claudit report")

Commands:
  report   Generate a cost/usage report (HTML by default; --json or unset --html for markdown).
  diff     Compare two date ranges and report top movers.
  watch    Tail a live session and print running cost.

Run "claudit <command> --help" for command-specific flags.
`

func runReport(args []string) error {
	fs := flag.NewFlagSet("claudit report", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to walk (defaults to ~/.claude/projects/)")
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
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "claudit report — generate a cost/usage report from session JSONL files.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Usage:")
		fmt.Fprintln(out, "  claudit report [flags]")
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
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
		filter.Since = t
	}
	if *until != "" {
		t, err := time.Parse("2006-01-02", *until)
		if err != nil {
			return fmt.Errorf("--until: %w", err)
		}
		filter.Until = t
	}

	files, err := listJSONL(*root)
	if err != nil {
		return err
	}

	turns, userMsgs, parentLinks, malformed, fileErrs := parseConcurrently(files)

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

	// Render. --json takes precedence over --html so an explicit --json
	// wins against the html-by-default behavior.
	if *asJSON {
		if err := render.JSON(os.Stdout, agg); err != nil {
			return err
		}
	} else if *asHTML {
		if err := render.HTML(os.Stdout, agg); err != nil {
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

	emitWarnings(malformed, fileErrs)
	return nil
}

func runDiff(args []string) error {
	fs := flag.NewFlagSet("claudit diff", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to walk (defaults to ~/.claude/projects/)")
	rangeA := fs.String("a", "", "baseline range, format YYYY-MM-DD..YYYY-MM-DD (inclusive-start, exclusive-end)")
	rangeB := fs.String("b", "", "current range, same format as --a")
	project := fs.String("project", "", "case-insensitive substring match on cwd")
	asJSON := fs.Bool("json", false, "emit JSON instead of markdown")
	pricesPath := fs.String("prices", "", "override pricing YAML path (default: ~/.config/claudit/prices.yaml)")
	topMovers := fs.Int("top", 10, "show top-N rows per dimension in the movers tables")
	hotspotN := fs.Int("hotspots", 10, "size of the hotspot pool used to find new-in-B hotspots (0 disables that section)")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "claudit diff — compare two date ranges and report top movers.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Usage:")
		fmt.Fprintln(out, "  claudit diff --a=YYYY-MM-DD..YYYY-MM-DD --b=YYYY-MM-DD..YYYY-MM-DD [flags]")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *rangeA == "" || *rangeB == "" {
		return fmt.Errorf("diff requires --a=YYYY-MM-DD..YYYY-MM-DD and --b=YYYY-MM-DD..YYYY-MM-DD")
	}
	sinceA, untilA, err := parseDateRange(*rangeA)
	if err != nil {
		return fmt.Errorf("--a: %w", err)
	}
	sinceB, untilB, err := parseDateRange(*rangeB)
	if err != nil {
		return fmt.Errorf("--b: %w", err)
	}

	prices, err := loadPrices(*pricesPath)
	if err != nil {
		return err
	}

	files, err := listJSONL(*root)
	if err != nil {
		return err
	}
	turns, userMsgs, parentLinks, malformed, fileErrs := parseConcurrently(files)

	promptIdx := aggregate.BuildPromptIndex(turns, userMsgs, parentLinks)
	aAgg := aggregate.New(prices).WithFilter(aggregate.Filter{
		Since: sinceA, Until: untilA, ProjectSubstring: *project,
	}).WithPromptIndex(promptIdx)
	bAgg := aggregate.New(prices).WithFilter(aggregate.Filter{
		Since: sinceB, Until: untilB, ProjectSubstring: *project,
	}).WithPromptIndex(promptIdx)

	lookup := newSubagentLookup()
	for _, t := range turns {
		aAgg.AddWithSubagent(t, lookup)
		bAgg.AddWithSubagent(t, lookup)
	}

	opt := render.DiffOptions{
		LabelA:    *rangeA,
		LabelB:    *rangeB,
		TopMovers: *topMovers,
		Hotspots:  *hotspotN,
	}
	if *asJSON {
		if err := render.DiffJSON(os.Stdout, aAgg, bAgg, opt); err != nil {
			return err
		}
	} else {
		if err := render.DiffMarkdown(os.Stdout, aAgg, bAgg, opt); err != nil {
			return err
		}
	}

	emitWarnings(malformed, fileErrs)
	return nil
}

// loadPrices resolves the prices file (default or override) and parses it.
func loadPrices(override string) (*pricing.Table, error) {
	pricesFile := override
	if pricesFile == "" {
		p, err := pricing.DefaultPath()
		if err != nil {
			return nil, err
		}
		pricesFile = p
	}
	return pricing.Load(pricesFile)
}

// newSubagentLookup returns a SubagentLookup that lazily reads the
// sibling .meta.json once per source file. Safe for concurrent use; the
// aggregator is single-threaded but we share one lookup across diff's
// two aggregators.
func newSubagentLookup() aggregate.SubagentLookup {
	var cache sync.Map
	return func(t parse.Turn) (string, string) {
		if !parse.IsSubagentFile(t.SourceFile) {
			return "", ""
		}
		if v, ok := cache.Load(t.SourceFile); ok {
			m := v.(parse.SubagentMeta)
			return m.AgentType, m.Description
		}
		m, _ := parse.ReadSubagentMeta(t.SourceFile)
		cache.Store(t.SourceFile, m)
		return m.AgentType, m.Description
	}
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

// parseDateRange parses "YYYY-MM-DD..YYYY-MM-DD" into (since, until).
// since is inclusive, until is exclusive — same convention as --since/--until.
func parseDateRange(s string) (time.Time, time.Time, error) {
	parts := strings.SplitN(s, "..", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("expected START..END, got %q", s)
	}
	since, err := time.Parse("2006-01-02", parts[0])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("start date %q: %w", parts[0], err)
	}
	until, err := time.Parse("2006-01-02", parts[1])
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("end date %q: %w", parts[1], err)
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, fmt.Errorf("start %s must be before end %s", parts[0], parts[1])
	}
	return since, until, nil
}

func emitWarnings(malformed int, fileErrs []error) {
	if malformed > 0 {
		fmt.Fprintf(os.Stderr, "\nclaudit: skipped %d malformed JSON line(s)\n", malformed)
	}
	if len(fileErrs) > 0 {
		fmt.Fprintf(os.Stderr, "claudit: %d file(s) failed to read; first: %v\n", len(fileErrs), fileErrs[0])
	}
}

func defaultProjectsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

func listJSONL(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate transient errors during walk
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".jsonl") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// parseConcurrently fans out parsing to GOMAXPROCS workers and returns the
// concatenated turns, user messages, parent links, the malformed count,
// and any non-fatal file errors.
func parseConcurrently(files []string) ([]parse.Turn, []parse.UserMessage, []parse.ParentLink, int, []error) {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string, workers*2)
	type result struct {
		turns     []parse.Turn
		users     []parse.UserMessage
		links     []parse.ParentLink
		malformed int
		err       error
	}
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				f, err := os.Open(path)
				if err != nil {
					results <- result{err: fmt.Errorf("open %s: %w", path, err)}
					continue
				}
				r, err := parse.ParseFile(f, path)
				f.Close()
				if err != nil {
					results <- result{err: fmt.Errorf("parse %s: %w", path, err), malformed: r.Malformed, turns: r.Turns, users: r.UserMessages, links: r.ParentLinks}
					continue
				}
				results <- result{turns: r.Turns, users: r.UserMessages, links: r.ParentLinks, malformed: r.Malformed}
			}
		}()
	}
	go func() {
		for _, p := range files {
			jobs <- p
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		allTurns  []parse.Turn
		allUsers  []parse.UserMessage
		allLinks  []parse.ParentLink
		malformed int
		errs      []error
	)
	for r := range results {
		allTurns = append(allTurns, r.turns...)
		allUsers = append(allUsers, r.users...)
		allLinks = append(allLinks, r.links...)
		malformed += r.malformed
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return allTurns, allUsers, allLinks, malformed, errs
}
