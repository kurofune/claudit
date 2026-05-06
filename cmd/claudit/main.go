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
	// Default subcommand: report. We accept "report" as an explicit first arg
	// and also work without it.
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "report" {
		args = args[1:]
	}

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
	if err := fs.Parse(args); err != nil {
		return err
	}

	pricesFile := *pricesPath
	if pricesFile == "" {
		p, err := pricing.DefaultPath()
		if err != nil {
			return err
		}
		pricesFile = p
	}
	prices, err := pricing.Load(pricesFile)
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

	turns, malformed, fileErrs := parseConcurrently(files)

	period := aggregate.Period(*by)
	if *by != "" && !period.Valid() {
		return fmt.Errorf("--by: must be one of day|week|month, got %q", *by)
	}

	agg := aggregate.New(prices).WithFilter(filter).WithPeriod(period)

	// Subagent meta cache — keyed on the source jsonl path so we don't
	// re-stat / re-read the sibling .meta.json across the file's many turns.
	metaCache := sync.Map{}
	lookup := func(t parse.Turn) (string, string) {
		if !parse.IsSubagentFile(t.SourceFile) {
			return "", ""
		}
		if v, ok := metaCache.Load(t.SourceFile); ok {
			m := v.(parse.SubagentMeta)
			return m.AgentType, m.Description
		}
		m, _ := parse.ReadSubagentMeta(t.SourceFile)
		metaCache.Store(t.SourceFile, m)
		return m.AgentType, m.Description
	}

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
		}); err != nil {
			return err
		}
	}

	// Footer warnings to stderr so stdout stays clean for piping.
	if malformed > 0 {
		fmt.Fprintf(os.Stderr, "\nclaudit: skipped %d malformed JSON line(s)\n", malformed)
	}
	if len(fileErrs) > 0 {
		// Surface the first few to keep noise down.
		fmt.Fprintf(os.Stderr, "claudit: %d file(s) failed to read; first: %v\n", len(fileErrs), fileErrs[0])
	}

	return nil
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
// concatenated turns, the malformed count, and any non-fatal file errors.
func parseConcurrently(files []string) ([]parse.Turn, int, []error) {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string, workers*2)
	type result struct {
		turns     []parse.Turn
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
					results <- result{err: fmt.Errorf("parse %s: %w", path, err), malformed: r.Malformed, turns: r.Turns}
					continue
				}
				results <- result{turns: r.Turns, malformed: r.Malformed}
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
		all       []parse.Turn
		malformed int
		errs      []error
	)
	for r := range results {
		all = append(all, r.turns...)
		malformed += r.malformed
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return all, malformed, errs
}
