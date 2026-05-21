package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/render"
)

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
