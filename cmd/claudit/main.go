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
	"strings"
	"sync"
	"time"

	"github.com/nategross/claudit/internal/aggregate"
	"github.com/nategross/claudit/internal/parse"
	"github.com/nategross/claudit/internal/pricing"
	"github.com/nategross/claudit/internal/render"
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
		case "report":
			return runReport(args[1:])
		case "diff":
			return runDiff(args[1:])
		}
	}
	return runReport(args)
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("claudit report", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to walk (defaults to ~/.claude/projects/)")
	since := fs.String("since", "", "only include turns at or after this date (YYYY-MM-DD)")
	until := fs.String("until", "", "only include turns strictly before this date (YYYY-MM-DD)")
	project := fs.String("project", "", "case-insensitive substring match on cwd")
	asJSON := fs.Bool("json", false, "emit JSON instead of markdown")
	asHTML := fs.Bool("html", false, "emit a self-contained interactive HTML report")
	pricesPath := fs.String("prices", "", "override pricing YAML path (default: ~/.config/claudit/prices.yaml)")
	minCost := fs.Float64("min-cost", 0.01, "hide by-project rows below this USD cost (markdown only)")
	drillTop := fs.Int("drill-top", 15, "show top-N rows per tool in the drill-down section (0 disables)")
	agentTop := fs.Int("agent-top", 20, "show top-N most expensive subagent invocations (0 disables)")
	agentType := fs.String("agent-type", "", "restrict the invocation section to one subagent type (e.g. general-purpose)")
	hotspots := fs.Int("hotspots", 10, "show top-N cost hotspots at the top of the report, each with a copyable LLM prompt (0 disables)")
	by := fs.String("by", "", "trend mode: bucket spend over time — one of day|week|month (empty disables)")
	cacheTop := fs.Int("cache-top", 10, "show top-N cache-miss offenders per dimension in the cache efficiency section (0 disables)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	prices, err := loadPrices(*pricesPath)
	if err != nil {
		return err
	}

	filter := aggregate.Filter{ProjectSubstring: *project}
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

	turns, _, malformed, fileErrs := parseConcurrently(files)

	period := aggregate.Period(*by)
	if *by != "" && !period.Valid() {
		return fmt.Errorf("--by: must be one of day|week|month, got %q", *by)
	}

	agg := aggregate.New(prices).WithFilter(filter).WithPeriod(period)

	lookup := newSubagentLookup()
	for _, t := range turns {
		agg.AddWithSubagent(t, lookup)
	}

	if *asJSON && *asHTML {
		return fmt.Errorf("--json and --html are mutually exclusive")
	}

	// Render.
	if *asHTML {
		if err := render.HTML(os.Stdout, agg); err != nil {
			return err
		}
	} else if *asJSON {
		if err := render.JSON(os.Stdout, agg); err != nil {
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
	turns, _, malformed, fileErrs := parseConcurrently(files)

	aAgg := aggregate.New(prices).WithFilter(aggregate.Filter{
		Since: sinceA, Until: untilA, ProjectSubstring: *project,
	})
	bAgg := aggregate.New(prices).WithFilter(aggregate.Filter{
		Since: sinceB, Until: untilB, ProjectSubstring: *project,
	})

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
// concatenated turns, user messages, the malformed count, and any
// non-fatal file errors.
func parseConcurrently(files []string) ([]parse.Turn, []parse.UserMessage, int, []error) {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string, workers*2)
	type result struct {
		turns     []parse.Turn
		users     []parse.UserMessage
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
					results <- result{err: fmt.Errorf("parse %s: %w", path, err), malformed: r.Malformed, turns: r.Turns, users: r.UserMessages}
					continue
				}
				results <- result{turns: r.Turns, users: r.UserMessages, malformed: r.Malformed}
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
		malformed int
		errs      []error
	)
	for r := range results {
		allTurns = append(allTurns, r.turns...)
		allUsers = append(allUsers, r.users...)
		malformed += r.malformed
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return allTurns, allUsers, malformed, errs
}
