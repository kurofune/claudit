// claudit — audit Claude Code session JSONL files for token & cost spend.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
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
			if _, err := fmt.Fprint(os.Stdout, topLevelUsage); err != nil {
				return err
			}
			return nil
		case "version", "--version":
			fmt.Println(versionString())
			return nil
		case "report":
			return runReport(args[1:])
		case "diff":
			return runDiff(args[1:])
		case "watch":
			return runWatch(args[1:])
		case "serve":
			return runServe(args[1:])
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
  diff     Compare two date ranges and report top movers (HTML by default; --json or unset --html for markdown).
  watch    Tail a live session and print running cost.
  serve    Run a local web daemon that serves a live-updating report (filters via URL query).
  version  Print the installed claudit version and exit (also: --version).

Run "claudit <command> --help" for command-specific flags.
`

// versionString returns "claudit vX.Y.Z (commit abc1234)" for go-install
// builds and "claudit (devel) (commit abc1234, dirty)" for local builds.
// Both forms answer the same diagnostic question — "is the binary on my
// PATH actually built from the source/tag I expect" — which is exactly
// the trap we hit when a stale go-module-proxy cache served v1.1.1 in
// response to @latest after v1.2.0 was already tagged.
func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "claudit (unknown)"
	}
	version := info.Main.Version
	if version == "" {
		version = "(devel)"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = ", dirty"
			}
		}
	}
	if rev != "" {
		return fmt.Sprintf("claudit %s (commit %s%s)", version, rev, dirty)
	}
	return fmt.Sprintf("claudit %s", version)
}

// versionShort returns a compact label for the report sidebar.
//
//	tagged go-install:  "v1.2.0"
//	local build past a tag:  "v1.2.1-dev abc1234 (dirty)"
//	  — base parsed from the pseudo-version (v1.2.1-0.<ts>-<sha>+dirty)
//	    that go synthesizes; the parsed base is the *next* version this
//	    commit would become, with "-dev" + sha making "future-tag, not
//	    actually released" explicit
//	first-commit / no parent tag:  "(devel) abc1234 (dirty)"
//	no build info at all:  ""
//
// Distinct from versionString — that one answers a diagnostic question
// ("which binary is on PATH?"), this one chrome-tags the UI.
func versionShort() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	version := info.Main.Version
	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = true
			}
		}
	}
	dirtySuffix := ""
	if dirty {
		dirtySuffix = " (dirty)"
	}

	// Strip the "+dirty" suffix Go appends to pseudo-versions for
	// modified working trees — we surface that signal via dirtySuffix.
	clean := version
	if i := strings.Index(clean, "+"); i >= 0 {
		clean = clean[:i]
	}

	// Pseudo-version base extraction. Go's pseudo-version format is
	// "<base>-0.<yyyymmddhhmmss>-<12hexchars>" for commits past a tag,
	// e.g. v1.2.1-0.20260519211036-0eb38df485a5. The "-0." infix is the
	// reliable marker. Everything before it is the version that the
	// *next* tag would become — we display that with a "-dev" suffix.
	if i := strings.Index(clean, "-0."); i > 0 {
		base := clean[:i]
		label := base + "-dev"
		if rev != "" {
			label += " " + rev
		}
		return label + dirtySuffix
	}

	// Not a pseudo-version with a parsable base. Could be a clean tag
	// (v1.2.0), the literal "(devel)" Go uses when no module info is
	// available, or the v0.0.0-<ts>-<sha> form for repos with no
	// reachable tag. Treat the last two as devel.
	if clean != "" && clean != "(devel)" && !strings.HasPrefix(clean, "v0.0.0-") {
		return clean + dirtySuffix
	}
	if rev != "" {
		return "(devel) " + rev + dirtySuffix
	}
	return "(devel)" + dirtySuffix
}

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

	// mtime pre-filter: when --since (or --last) bounds the window,
	// any file whose mtime predates the bound can't contain a turn
	// inside the window, so skip it before opening.
	files, err := listJSONL(*root, filter.Since)
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

	// Build per-session drill-down timelines. Done outside the aggregator
	// because it needs the raw turn/msg/parent-link arrays (which the
	// aggregator doesn't retain). Skipped when --sessions=0 or rendering
	// non-HTML — keeps the markdown/JSON paths unchanged.
	var sessionTimelines []aggregate.SessionTimeline
	if *asHTML && !*asJSON && *sessionsTop > 0 {
		sessionTimelines = aggregate.BuildSessionTimelines(
			turns, userMsgs, parentLinks, prices, filter,
			aggregate.SessionTimelinesOptions{
				TopN:           *sessionsTop,
				Redact:         *redact,
				MaxPromptChars: 2000,
			},
		)
	}

	// Render. --json takes precedence over --html so an explicit --json
	// wins against the html-by-default behavior.
	if *asJSON {
		if err := render.JSON(os.Stdout, agg); err != nil {
			return err
		}
	} else if *asHTML {
		if err := render.HTMLWithOptions(os.Stdout, agg, render.HTMLOptions{
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

	emitWarnings(malformed, fileErrs)
	return nil
}

func runDiff(args []string) error {
	fs := flag.NewFlagSet("claudit diff", flag.ExitOnError)
	defaultRoot := defaultProjectsRoot()
	root := fs.String("root", defaultRoot, "root directory to walk (defaults to $CLAUDE_CONFIG_DIR/projects or ~/.claude/projects)")
	rangeA := fs.String("a", "", "baseline range, format YYYY-MM-DD..YYYY-MM-DD (inclusive-start, exclusive-end)")
	rangeB := fs.String("b", "", "current range, same format as --a")
	by := fs.String("by", "week", "default comparison window when --a/--b are absent: week (7d vs prior 7d) or month (30d vs prior 30d)")
	project := fs.String("project", "", "case-insensitive substring match on cwd")
	asJSON := fs.Bool("json", false, "emit JSON (overrides the HTML default)")
	asHTML := fs.Bool("html", true, "emit a self-contained interactive HTML diff (default)")
	pricesPath := fs.String("prices", "", "override pricing YAML path (default: ~/.config/claudit/prices.yaml)")
	topMovers := fs.Int("top", 10, "show top-N rows per dimension in the movers tables")
	hotspotN := fs.Int("hotspots", 10, "size of the hotspot pool used to find new-in-B hotspots (0 disables that section)")
	fs.Usage = func() {
		ew := &errWriter{w: fs.Output()}
		ew.Println("claudit diff — compare two date ranges and report top movers.")
		ew.Println()
		ew.Println("Usage:")
		ew.Println("  claudit diff [flags]                       (defaults to last 7 days vs prior 7 days)")
		ew.Println("  claudit diff --by=month                    (last 30 days vs prior 30 days)")
		ew.Println("  claudit diff --a=YYYY-MM-DD..YYYY-MM-DD --b=YYYY-MM-DD..YYYY-MM-DD")
		ew.Println()
		ew.Println("Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Derive default ranges when both --a and --b are absent. Equal-size
	// rolling windows ending at midnight tonight keep the delta math
	// honest: B = the last 7d/30d, A = the 7d/30d before that. Mixing one
	// --a with no --b (or vice versa) is still an error — partial overrides
	// invite footguns.
	hasA, hasB := *rangeA != "", *rangeB != ""
	if !hasA && !hasB {
		sinceA, untilA, sinceB, untilB, labelA, labelB, err := defaultDiffWindows(*by, time.Now())
		if err != nil {
			return err
		}
		return runDiffWithRanges(*root, *project, *pricesPath, *asJSON, *asHTML, *topMovers, *hotspotN,
			sinceA, untilA, sinceB, untilB, labelA, labelB)
	}
	if !hasA || !hasB {
		dayCount := 7
		if *by == "month" {
			dayCount = 30
		}
		return fmt.Errorf("diff requires both --a and --b, or neither (defaults to last %d days vs prior %d days)", dayCount, dayCount)
	}
	sinceA, untilA, err := parseDateRange(*rangeA)
	if err != nil {
		return fmt.Errorf("--a: %w", err)
	}
	sinceB, untilB, err := parseDateRange(*rangeB)
	if err != nil {
		return fmt.Errorf("--b: %w", err)
	}
	return runDiffWithRanges(*root, *project, *pricesPath, *asJSON, *asHTML, *topMovers, *hotspotN,
		sinceA, untilA, sinceB, untilB, *rangeA, *rangeB)
}

func runDiffWithRanges(
	root, project, pricesPath string,
	asJSON, asHTML bool,
	topMovers, hotspotN int,
	sinceA, untilA, sinceB, untilB time.Time,
	labelA, labelB string,
) error {

	prices, err := loadPrices(pricesPath)
	if err != nil {
		return err
	}

	// mtime pre-filter: use the earlier of the two range starts as the
	// cutoff so we don't skip a file whose tail is in --b but whose
	// head is in --a.
	earliest := sinceA
	if sinceB.Before(earliest) {
		earliest = sinceB
	}
	files, err := listJSONL(root, earliest)
	if err != nil {
		return err
	}
	turns, userMsgs, parentLinks, malformed, fileErrs := parseConcurrently(files)

	promptIdx := aggregate.BuildPromptIndex(turns, userMsgs, parentLinks)
	aAgg := aggregate.New(prices).WithFilter(aggregate.Filter{
		Since: sinceA, Until: untilA, ProjectSubstring: project,
	}).WithPromptIndex(promptIdx)
	bAgg := aggregate.New(prices).WithFilter(aggregate.Filter{
		Since: sinceB, Until: untilB, ProjectSubstring: project,
	}).WithPromptIndex(promptIdx)

	lookup := newSubagentLookup()
	for _, t := range turns {
		aAgg.AddWithSubagent(t, lookup)
		bAgg.AddWithSubagent(t, lookup)
	}

	opt := render.DiffOptions{
		LabelA:    labelA,
		LabelB:    labelB,
		TopMovers: topMovers,
		Hotspots:  hotspotN,
		Version:   versionShort(),
	}
	// --json takes precedence over --html so an explicit --json wins
	// against the html-by-default behavior. Matches `claudit report`.
	switch {
	case asJSON:
		if err := render.DiffJSON(os.Stdout, aAgg, bAgg, opt); err != nil {
			return err
		}
	case asHTML:
		if err := render.DiffHTML(os.Stdout, aAgg, bAgg, opt); err != nil {
			return err
		}
	default:
		if err := render.DiffMarkdown(os.Stdout, aAgg, bAgg, opt); err != nil {
			return err
		}
	}

	emitWarnings(malformed, fileErrs)
	return nil
}

// defaultDiffWindows derives equal-size rolling windows for `claudit diff`
// when --a and --b are absent. Windows end at midnight tonight (local) so
// the same invocation across the day produces identical results.
//
//	by="week"  → B = [today-7d, today),  A = [today-14d, today-7d)
//	by="month" → B = [today-30d, today), A = [today-60d, today-30d)
//
// Labels are the raw date ranges (e.g. "2026-05-09..2026-05-16") — no
// "last 7 days" / "prior 7 days" prose. The dates carry the window
// length on their own and avoid the calendar-week vs rolling-7d
// ambiguity that "last week" invites.
func defaultDiffWindows(by string, now time.Time) (time.Time, time.Time, time.Time, time.Time, string, string, error) {
	var d time.Duration
	switch by {
	case "week":
		d = 7 * 24 * time.Hour
	case "month":
		d = 30 * 24 * time.Hour
	default:
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, "", "",
			fmt.Errorf("--by: must be week or month, got %q", by)
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	bEnd := midnight
	bStart := midnight.Add(-d)
	aEnd := bStart
	aStart := bStart.Add(-d)
	fmtRange := func(s, e time.Time) string {
		return s.Format("2006-01-02") + ".." + e.Format("2006-01-02")
	}
	return aStart, aEnd, bStart, bEnd, fmtRange(aStart, aEnd), fmtRange(bStart, bEnd), nil
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
	// Honor CLAUDE_CONFIG_DIR the same way Claude Code itself does: when
	// set, every ~/.claude path is rerouted under it. Users on dotfiles
	// setups, non-default drives, or sandboxed configs rely on this.
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// listJSONL walks root for .jsonl files. When earliest is non-zero,
// files whose mtime is before earliest are skipped — they can't
// contain any turn newer than earliest, so opening them is wasted I/O.
// This is a big lever on time-windowed `report --since=…` / `diff` /
// `watch` queries against large ~/.claude/projects trees.
func listJSONL(root string, earliest time.Time) ([]string, error) {
	var out []string
	filter := !earliest.IsZero()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate transient errors during walk
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if filter {
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			if info.ModTime().Before(earliest) {
				return nil
			}
		}
		out = append(out, path)
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
				if cerr := f.Close(); cerr != nil && err == nil {
					err = cerr
				}
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
