// Package render formats an Aggregator into markdown or JSON for stdout.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/nategross/claudit/internal/aggregate"
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
	fmt.Fprintln(w, "## Top-line totals")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- **Total cost:** %s\n", money(tot.CostUSD))
	fmt.Fprintf(w, "- **Sessions:** %d\n", tot.Sessions)
	fmt.Fprintf(w, "- **Assistant turns:** %d\n", tot.Turns)
	fmt.Fprintf(w, "- **Date range:** %s\n", dateRange)
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

	// By model.
	fmt.Fprintln(w, "## By model")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Model | Cost | % | Turns | Input | Output | Cache create (5m) | Cache create (1h) | Cache read |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|---:|---:|")
	for _, m := range a.ByModel() {
		fmt.Fprintf(w, "| %s | %s | %s | %d | %s | %s | %s | %s | %s |\n",
			m.Model, money(m.CostUSD), pct(m.CostUSD, tot.CostUSD), m.Turns,
			num(m.InputTokens), num(m.OutputTokens),
			num(m.CacheCreate5mTokens), num(m.CacheCreate1hTokens),
			num(m.CacheReadTokens))
	}
	fmt.Fprintln(w)

	// By project.
	fmt.Fprintln(w, "## By project")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Project | Cost | % | Sessions | Turns | Dominant model |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---|")
	rows := a.ByProject()
	hidden := 0
	for _, p := range rows {
		if p.CostUSD < opt.MinProjectCost {
			hidden++
			continue
		}
		fmt.Fprintf(w, "| %s | %s | %s | %d | %d | %s |\n",
			truncate(p.Project, 60), money(p.CostUSD), pct(p.CostUSD, tot.CostUSD),
			p.Sessions, p.Turns, p.DominantModel)
	}
	if hidden > 0 {
		fmt.Fprintf(w, "\n_…and %d project(s) below $%.2f hidden (use --min-cost 0 to show all)._\n",
			hidden, opt.MinProjectCost)
	}
	fmt.Fprintln(w)

	// By tool.
	fmt.Fprintln(w, "## By tool")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Tool | Calls | Turns | Cost | % | Output tokens |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|")
	for _, b := range a.ByTool() {
		fmt.Fprintf(w, "| %s | %d | %d | %s | %s | %s |\n",
			b.Name, b.Count, b.TurnCount,
			money(b.CostUSD), pct(b.CostUSD, tot.CostUSD), num(b.OutputTokens))
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

// escapePipes prevents detail strings (which may contain "|" or backticks)
// from breaking the markdown table format.
func escapePipes(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return "`" + s + "`"
}
