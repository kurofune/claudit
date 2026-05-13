// Package render formats an Aggregator into markdown or JSON for stdout.
package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// Options tweaks markdown rendering. Zero value is fine.
type Options struct {
	// MinProjectCost hides by-project rows below this USD cost (default 0).
	// Useful for filtering out the long tail of $0.00 worktree directories.
	MinProjectCost float64

	// DrillTop limits each per-tool drill-down sub-table to the N most
	// expensive rows. 0 means "no drill-down section" (render skipped).
	DrillTop int

	// AgentTop limits the "Top subagent invocations" section. 0 disables it.
	AgentTop int

	// AgentTypeFilter, when non-empty, restricts the invocation section to
	// runs whose subagent type matches exactly (e.g. "general-purpose").
	AgentTypeFilter string

	// Hotspots controls the count of "top cost hotspots" rows at the top
	// of the report (each with a copyable LLM prompt). 0 disables it.
	Hotspots int

	// CacheTop limits the per-dimension tables in the cache efficiency
	// section. 0 disables the section entirely.
	CacheTop int

	// PromptTop limits the "Top expensive prompts" section. 0 disables
	// the section entirely (e.g. when no PromptIndex was attached).
	PromptTop int
}

// Markdown writes the full report to w with default options.
func Markdown(w io.Writer, a *aggregate.Aggregator) error {
	return MarkdownWithOptions(w, a, Options{})
}

// pct returns "12.3%" or "—" when the denominator is zero. Centralizing this
// keeps row formatting consistent across sections.
func pct(part, total float64) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*part/total)
}

// pctOf formats an already-computed percentage like 12.3 as "12.3%".
func pctOf(v float64) string { return fmt.Sprintf("%.1f%%", v) }

// MarkdownWithOptions writes the full report with custom render options.
func MarkdownWithOptions(w io.Writer, a *aggregate.Aggregator, opt Options) error {
	tot := a.Totals()
	dateRange := "—"
	if !tot.First.IsZero() {
		dateRange = fmt.Sprintf("%s → %s",
			tot.First.UTC().Format("2006-01-02"),
			tot.Last.UTC().Format("2006-01-02"))
	}

	fmt.Fprintln(w, "# claudit report")
	fmt.Fprintln(w)

	// Top-of-report: cost hotspots, each with a tailored LLM prompt the user
	// can copy and paste into Claude / GPT / etc. for tool & workflow advice.
	if opt.Hotspots > 0 {
		hs := a.Hotspots(opt.Hotspots)
		if len(hs) > 0 {
			fmt.Fprintln(w, "## Top cost hotspots")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "_The highest-cost optimization targets in your data. Each row expands to show a tailored prompt — copy it into an LLM (Claude, GPT, etc.) for specific advice on tools, MCPs, CLIs, and workflow patterns. The prompts explicitly forbid the trivial \"use a cheaper model\" answer._")
			fmt.Fprintln(w)
			for i, h := range hs {
				fmt.Fprintf(w, "### %d. %s — %s (%s of total)\n\n",
					i+1, h.Title, money(h.CostUSD), pctOf(h.PctOfTotal))
				prompt, err := HotspotPrompt(h)
				if err != nil {
					fmt.Fprintf(w, "_(no prompt template available for kind %q)_\n\n", h.Kind)
					continue
				}
				fmt.Fprintln(w, "<details><summary>📋 Copy this prompt to ask an LLM how to address it</summary>")
				fmt.Fprintln(w)
				fmt.Fprintln(w, "```")
				fmt.Fprint(w, prompt)
				fmt.Fprintln(w, "```")
				fmt.Fprintln(w)
				fmt.Fprintln(w, "</details>")
				fmt.Fprintln(w)
			}
		}
	}

	fmt.Fprintln(w, "## Top-line totals")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- **Total cost:** %s\n", money(tot.CostUSD))
	fmt.Fprintf(w, "- **Sessions:** %d\n", tot.Sessions)
	fmt.Fprintf(w, "- **Assistant turns:** %d\n", tot.Turns)
	fmt.Fprintf(w, "- **Date range:** %s\n", dateRange)
	if tot.Tokens.CacheableTokens() > 0 {
		fmt.Fprintf(w, "- **Cache hit ratio:** %s _(read / (read + input + cache_create))_\n",
			ratioPct(tot.Tokens.HitRatio()))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Bucket | Tokens |")
	fmt.Fprintln(w, "|---|---:|")
	fmt.Fprintf(w, "| input | %s |\n", num(tot.InputTokens))
	fmt.Fprintf(w, "| output | %s |\n", num(tot.OutputTokens))
	fmt.Fprintf(w, "| cache create (5m) | %s |\n", num(tot.CacheCreate5mTokens))
	fmt.Fprintf(w, "| cache create (1h) | %s |\n", num(tot.CacheCreate1hTokens))
	fmt.Fprintf(w, "| cache read | %s |\n", num(tot.CacheReadTokens))
	fmt.Fprintln(w)

	if uk := a.UnknownModels(); len(uk) > 0 {
		fmt.Fprintln(w, "> **Warning:** unpriced models seen — add them to ~/.config/claudit/prices.yaml:")
		for _, m := range uk {
			fmt.Fprintf(w, "> - `%s`\n", m)
		}
		fmt.Fprintln(w)
	}

	period := a.Period()
	trendTotals := a.TrendTotals()
	if period.Valid() && len(trendTotals) > 0 {
		renderTrendSection(w, period, trendTotals)
	}

	trendByModel := a.TrendByModel()
	trendByProject := a.TrendByProject()
	trendByTool := a.TrendByTool()

	// By model.
	fmt.Fprintln(w, "## By model")
	fmt.Fprintln(w)
	if period.Valid() {
		fmt.Fprintln(w, "| Model | Cost | % | Turns | Trend | Input | Output | Cache create (5m) | Cache create (1h) | Cache read |")
		fmt.Fprintln(w, "|---|---:|---:|---:|---|---:|---:|---:|---:|---:|")
	} else {
		fmt.Fprintln(w, "| Model | Cost | % | Turns | Input | Output | Cache create (5m) | Cache create (1h) | Cache read |")
		fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|---:|---:|")
	}
	for _, m := range a.ByModel() {
		if period.Valid() {
			fmt.Fprintf(w, "| %s | %s | %s | %d | `%s` | %s | %s | %s | %s | %s |\n",
				m.Model, money(m.CostUSD), pct(m.CostUSD, tot.CostUSD), m.Turns,
				sparkline(trendByModel[m.Model], 30),
				num(m.InputTokens), num(m.OutputTokens),
				num(m.CacheCreate5mTokens), num(m.CacheCreate1hTokens),
				num(m.CacheReadTokens))
		} else {
			fmt.Fprintf(w, "| %s | %s | %s | %d | %s | %s | %s | %s | %s |\n",
				m.Model, money(m.CostUSD), pct(m.CostUSD, tot.CostUSD), m.Turns,
				num(m.InputTokens), num(m.OutputTokens),
				num(m.CacheCreate5mTokens), num(m.CacheCreate1hTokens),
				num(m.CacheReadTokens))
		}
	}
	fmt.Fprintln(w)

	// By project.
	fmt.Fprintln(w, "## By project")
	fmt.Fprintln(w)
	if period.Valid() {
		fmt.Fprintln(w, "| Project | Cost | % | Sessions | Turns | Trend | Dominant model |")
		fmt.Fprintln(w, "|---|---:|---:|---:|---:|---|---|")
	} else {
		fmt.Fprintln(w, "| Project | Cost | % | Sessions | Turns | Dominant model |")
		fmt.Fprintln(w, "|---|---:|---:|---:|---:|---|")
	}
	rows := a.ByProject()
	hidden := 0
	for _, p := range rows {
		if p.CostUSD < opt.MinProjectCost {
			hidden++
			continue
		}
		if period.Valid() {
			fmt.Fprintf(w, "| %s | %s | %s | %d | %d | `%s` | %s |\n",
				truncate(p.Project, 60), money(p.CostUSD), pct(p.CostUSD, tot.CostUSD),
				p.Sessions, p.Turns, sparkline(trendByProject[p.Project], 30), p.DominantModel)
		} else {
			fmt.Fprintf(w, "| %s | %s | %s | %d | %d | %s |\n",
				truncate(p.Project, 60), money(p.CostUSD), pct(p.CostUSD, tot.CostUSD),
				p.Sessions, p.Turns, p.DominantModel)
		}
	}
	if hidden > 0 {
		fmt.Fprintf(w, "\n_…and %d project(s) below $%.2f hidden (use --min-cost 0 to show all)._\n",
			hidden, opt.MinProjectCost)
	}
	fmt.Fprintln(w)

	if opt.CacheTop > 0 {
		renderCacheSection(w, a, opt.CacheTop)
	}

	if opt.PromptTop > 0 {
		renderPromptSection(w, a, opt.PromptTop, tot.CostUSD)
	}

	// By tool.
	fmt.Fprintln(w, "## By tool")
	fmt.Fprintln(w)
	if period.Valid() {
		fmt.Fprintln(w, "| Tool | Calls | Turns | Cost | % | Trend | Output tokens |")
		fmt.Fprintln(w, "|---|---:|---:|---:|---:|---|---:|")
	} else {
		fmt.Fprintln(w, "| Tool | Calls | Turns | Cost | % | Output tokens |")
		fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|")
	}
	for _, b := range a.ByTool() {
		if period.Valid() {
			fmt.Fprintf(w, "| %s | %d | %d | %s | %s | `%s` | %s |\n",
				b.Name, b.Count, b.TurnCount,
				money(b.CostUSD), pct(b.CostUSD, tot.CostUSD),
				sparkline(trendByTool[b.Name], 30), num(b.OutputTokens))
		} else {
			fmt.Fprintf(w, "| %s | %d | %d | %s | %s | %s |\n",
				b.Name, b.Count, b.TurnCount,
				money(b.CostUSD), pct(b.CostUSD, tot.CostUSD), num(b.OutputTokens))
		}
	}
	fmt.Fprintln(w)

	// Drill-down: per-tool sub-tables. We render in the same order as the
	// main By-tool list (cost descending) so the first sub-block is also
	// the highest-cost tool.
	if opt.DrillTop > 0 {
		fmt.Fprintln(w, "## Drill-down by tool")
		fmt.Fprintln(w)
		fmt.Fprintf(w, "_Top %d rows per tool by cost. Each row is a (tool, argument-pattern) pair — e.g. Bash + \"git status\"._\n\n", opt.DrillTop)
		details := a.ByToolDetail()
		for _, tool := range a.ByTool() {
			rows := details[tool.Name]
			if len(rows) == 0 {
				continue
			}
			fmt.Fprintf(w, "### %s — $%s across %d call(s)\n\n", tool.Name, fmt.Sprintf("%.2f", tool.CostUSD), tool.Count)
			fmt.Fprintln(w, "| Pattern | Calls | Turns | Cost | % of "+tool.Name+" | Output tokens |")
			fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|")
			limit := opt.DrillTop
			if limit > len(rows) {
				limit = len(rows)
			}
			for _, r := range rows[:limit] {
				fmt.Fprintf(w, "| %s | %d | %d | %s | %s | %s |\n",
					escapePipes(truncate(r.Detail, 60)), r.Count, r.TurnCount,
					money(r.CostUSD), pct(r.CostUSD, tool.CostUSD), num(r.OutputTokens))
			}
			if len(rows) > limit {
				var rest float64
				for _, r := range rows[limit:] {
					rest += r.CostUSD
				}
				fmt.Fprintf(w, "| _(%d more rows totaling %s)_ | | | | |\n",
					len(rows)-limit, money(rest))
			}
			fmt.Fprintln(w)
		}
	}

	// By skill / slash command.
	fmt.Fprintln(w, "## By skill & slash command")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Key | Calls | Turns | Cost | % | Output tokens |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|")
	for _, b := range a.BySkill() {
		fmt.Fprintf(w, "| %s | %d | %d | %s | %s | %s |\n",
			b.Key, b.Count, b.TurnCount,
			money(b.CostUSD), pct(b.CostUSD, tot.CostUSD), num(b.OutputTokens))
	}
	fmt.Fprintln(w)

	// Sidechain split.
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()
	fmt.Fprintln(w, "## Main vs sidechain (subagent) turns")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Bucket | Cost | % | Turns | Input | Output | Cache create (5m) | Cache create (1h) | Cache read |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|---:|---:|")
	fmt.Fprintf(w, "| main | %s | %s | %d | %s | %s | %s | %s | %s |\n",
		money(mainCost), pct(mainCost, tot.CostUSD), mainTurns,
		num(mainTok.InputTokens), num(mainTok.OutputTokens),
		num(mainTok.CacheCreate5mTokens), num(mainTok.CacheCreate1hTokens), num(mainTok.CacheReadTokens))
	fmt.Fprintf(w, "| sidechain | %s | %s | %d | %s | %s | %s | %s | %s |\n",
		money(sideCost), pct(sideCost, tot.CostUSD), sideTurns,
		num(sideTok.InputTokens), num(sideTok.OutputTokens),
		num(sideTok.CacheCreate5mTokens), num(sideTok.CacheCreate1hTokens), num(sideTok.CacheReadTokens))
	fmt.Fprintln(w)

	// By subagent. % is of sidechain cost (the meaningful denominator —
	// "review-lens is X% of sidechain spend" beats "X% of total").
	fmt.Fprintln(w, "## By subagent type (sidechain only)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Subagent | Cost | % of sidechain | Turns | Input | Output | Cache read |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|")
	for _, s := range a.BySubagent() {
		fmt.Fprintf(w, "| %s | %s | %s | %d | %s | %s | %s |\n",
			s.Type, money(s.CostUSD), pct(s.CostUSD, sideCost), s.Turns,
			num(s.InputTokens), num(s.OutputTokens), num(s.CacheReadTokens))
	}
	fmt.Fprintln(w)

	// Per-invocation drill-down. Each row is one subagent run — useful for
	// "show me the 20 most expensive general-purpose invocations and what
	// they were trying to do."
	if opt.AgentTop > 0 {
		invs := a.AgentInvocations(opt.AgentTypeFilter)
		title := "## Top subagent invocations"
		if opt.AgentTypeFilter != "" {
			title = fmt.Sprintf("## Top `%s` invocations", opt.AgentTypeFilter)
		}
		fmt.Fprintln(w, title)
		fmt.Fprintln(w)
		if len(invs) == 0 {
			fmt.Fprintln(w, "_(no invocations match — likely older sessions without `agent-*.meta.json` siblings)_")
			return nil
		}
		fmt.Fprintf(w, "_Each row is one subagent run (one `agent-<id>.jsonl` file). Showing top %d by cost._\n\n", opt.AgentTop)
		fmt.Fprintln(w, "| Subagent | Description | Cost | Turns | Project | Started |")
		fmt.Fprintln(w, "|---|---|---:|---:|---|---|")
		limit := opt.AgentTop
		if limit > len(invs) {
			limit = len(invs)
		}
		for _, inv := range invs[:limit] {
			subType := inv.SubagentType
			if subType == "" {
				subType = "_(unknown)_"
			}
			desc := inv.Description
			if desc == "" {
				desc = "_(no description in meta.json)_"
			}
			started := "—"
			if !inv.First.IsZero() {
				started = inv.First.UTC().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "| %s | %s | %s | %d | %s | %s |\n",
				subType,
				escapePipes(truncate(desc, 80)),
				money(inv.CostUSD),
				inv.Turns,
				truncate(inv.Project, 50),
				started)
		}
		if len(invs) > limit {
			var rest float64
			for _, inv := range invs[limit:] {
				rest += inv.CostUSD
			}
			fmt.Fprintf(w, "| _(%d more invocations totaling %s)_ | | | | | |\n",
				len(invs)-limit, money(rest))
		}
	}

	return nil
}

// renderPromptSection writes the "Top expensive prompts" table. Empty
// (or PromptIndex-less) aggregators silently produce no section.
func renderPromptSection(w io.Writer, a *aggregate.Aggregator, top int, totalCost float64) {
	rows := a.ByPrompt()
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(w, "## Top expensive prompts")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "_User prompts ranked by the total cost of the assistant turn chain each one kicked off. Prompts cluster on the first 120 chars of normalized text — trivially-different repeats of the same ask appear in the same row. \"Invocations\" is the number of distinct prompt instances; \"Sessions\" is how many sessions issued this prompt; \"Turns\" counts downstream assistant turns attributed via parentUuid._")
	fmt.Fprintln(w)
	limit := top
	if limit > len(rows) {
		limit = len(rows)
	}
	fmt.Fprintln(w, "| # | Snippet | Invocations | Sessions | Turns | Cost | % |")
	fmt.Fprintln(w, "|---:|---|---:|---:|---:|---:|---:|")
	for i, r := range rows[:limit] {
		snippet := r.Sample
		if snippet == "" {
			snippet = r.Key
		}
		fmt.Fprintf(w, "| %d | %s | %d | %d | %d | %s | %s |\n",
			i+1, escapePipes(truncateHead(snippet, 80)),
			r.Invocations, r.Sessions, r.TurnCount,
			money(r.CostUSD), pct(r.CostUSD, totalCost))
	}
	if len(rows) > limit {
		var rest float64
		for _, r := range rows[limit:] {
			rest += r.CostUSD
		}
		fmt.Fprintf(w, "| _(%d more prompts totaling %s)_ | | | | | | |\n", len(rows)-limit, money(rest))
	}
	fmt.Fprintln(w)
}

// renderCacheSection writes the "Cache efficiency" deep dive — one
// table per dimension, ranked by miss tokens descending. Skips
// dimensions that have no rows w/ cacheable traffic.
func renderCacheSection(w io.Writer, a *aggregate.Aggregator, top int) {
	projRows := a.CacheByProject()
	sessRows := a.CacheBySession()
	if len(projRows) == 0 && len(sessRows) == 0 {
		return
	}

	fmt.Fprintln(w, "## Cache efficiency")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "_Hit ratio = `cache_read / (cache_read + input + cache_create_5m + cache_create_1h)`. Higher is better. Rows are ranked by **miss tokens** (input + cache_create) — the volume of context you're paying to upload or freshly cache that should have been a cache hit. Tool dimension is omitted because tool-level cache hit rate is dominated by surrounding-turn context, not the tool itself._")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "**Overall hit ratio:** %s · **Total miss tokens:** %s\n\n",
		ratioPct(a.OverallHitRatio()), num(a.Totals().Tokens.MissTokens()))

	if len(projRows) > 0 {
		fmt.Fprintln(w, "### Worst projects by miss tokens")
		fmt.Fprintln(w)
		writeCacheTable(w, projRows, top, "Project")
	}
	if len(sessRows) > 0 {
		fmt.Fprintln(w, "### Worst sessions by miss tokens")
		fmt.Fprintln(w)
		writeCacheTable(w, sessRows, top, "Session")
	}
	if subRows := a.CacheBySubagent(); len(subRows) > 0 {
		fmt.Fprintln(w, "### Worst subagent types by miss tokens")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "_Subagents start with a cold cache by definition (each invocation is a fresh context). The miss-token volume below is the structural tax for using each subagent type in this report's window._")
		fmt.Fprintln(w)
		writeCacheTable(w, subRows, top, "Subagent type")
	}
	if invRows := a.CacheByInvocation(); len(invRows) > 0 {
		fmt.Fprintln(w, "### Worst single subagent invocations by miss tokens")
		fmt.Fprintln(w)
		writeCacheTable(w, invRows, top, "Description")
	}
}

// writeCacheTable emits one ranked sub-table. keyLabel is the column
// header for the first column (e.g. "Project" / "Session").
func writeCacheTable(w io.Writer, rows []aggregate.CacheRow, top int, keyLabel string) {
	limit := top
	if limit > len(rows) {
		limit = len(rows)
	}
	fmt.Fprintf(w, "| %s | Hit ratio | Miss tokens | Cache read | Turns | Cost |\n", keyLabel)
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|")
	for _, r := range rows[:limit] {
		key := r.Key
		if r.Subtitle != "" {
			// Append the project so a session row reads on its own.
			key = key + " · " + truncate(r.Subtitle, 50)
		} else {
			key = truncate(key, 70)
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %d | %s |\n",
			key, ratioPct(r.HitRatio), num(r.Miss),
			num(r.CacheReadTokens), r.Turns, money(r.CostUSD))
	}
	if len(rows) > limit {
		var restMiss int64
		for _, r := range rows[limit:] {
			restMiss += r.Miss
		}
		fmt.Fprintf(w, "| _(%d more rows totaling %s miss tokens)_ | | | | | |\n",
			len(rows)-limit, num(restMiss))
	}
	fmt.Fprintln(w)
}

// ratioPct formats a 0..1 ratio as a percent string, "—" when zero.
func ratioPct(r float64) string {
	if r <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*r)
}

// renderTrendSection writes the "Cost by <period>" overview: a sparkline
// of the full series, then a per-bucket table with cost, turns, and
// percent-change vs the prior bucket. Used when --by is set.
func renderTrendSection(w io.Writer, period aggregate.Period, points []aggregate.TrendPoint) {
	label := string(period)
	fmt.Fprintf(w, "## Cost by %s\n\n", label)
	fmt.Fprintf(w, "_Spend bucketed by %s. The trend column on each by-row table below uses the same buckets, downsampled when wider than 30 cells._\n\n", label)
	fmt.Fprintf(w, "Overall trend: `%s`\n\n", sparkline(points, 0))

	fmt.Fprintln(w, "| Period | Cost | Δ vs prior | Turns | Hit ratio |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|")
	var prev float64
	havePrev := false
	for _, p := range points {
		delta := "—"
		if havePrev {
			delta = deltaPct(prev, p.CostUSD)
		}
		hit := "—"
		if p.Tokens.CacheableTokens() > 0 {
			hit = ratioPct(p.Tokens.HitRatio())
		}
		fmt.Fprintf(w, "| %s | %s | %s | %d | %s |\n",
			formatBucket(period, p.Time), money(p.CostUSD), delta, p.Turns, hit)
		prev = p.CostUSD
		havePrev = true
	}
	fmt.Fprintln(w)
}

// formatBucket prints a bucket time the way the reader expects for that
// period: "2026-05-06" for day, "wk of 2026-05-04" for week, "2026-05"
// for month.
func formatBucket(p aggregate.Period, t time.Time) string {
	switch p {
	case aggregate.PeriodWeek:
		return "wk of " + t.UTC().Format("2006-01-02")
	case aggregate.PeriodMonth:
		return t.UTC().Format("2006-01")
	}
	return t.UTC().Format("2006-01-02")
}

// deltaPct formats a signed percent-change. Returns "—" when prior is
// zero (any change from zero is undefined as a percentage).
func deltaPct(prev, cur float64) string {
	if prev <= 0 {
		if cur <= 0 {
			return "0%"
		}
		return "new"
	}
	d := 100 * (cur - prev) / prev
	sign := "+"
	if d < 0 {
		sign = ""
	}
	return fmt.Sprintf("%s%.1f%%", sign, d)
}

func money(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func num(v int64) string {
	s := fmt.Sprintf("%d", v)
	// thousands separators (US style)
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		if n > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max+1:]
}

// truncateHead keeps the FIRST max-1 runes and appends "…". For things
// like prompt text where the start identifies the row; truncate's
// keep-the-tail behavior is wrong for prose.
func truncateHead(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max < 1 {
		return ""
	}
	return string(runes[:max-1]) + "…"
}

// escapePipes prevents detail strings (which may contain "|" or backticks)
// from breaking the markdown table format.
func escapePipes(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return "`" + s + "`"
}
