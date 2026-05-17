package main

import (
	"fmt"
	"strings"

	"github.com/kurofune/claudit/internal/watch/term"
)

// All color decisions for the watch UI live in this file so palette
// tweaks touch one place. The intent of each helper is named — Bold /
// Dim / Cyan etc. on term.Style remain low-level primitives.

// dotSep is the inter-field separator used inside status lines. Dim
// middle-dot groups fields visually without competing with the data.
func dotSep(st term.Style) string { return " " + st.Dim("·") + " " }

// money formats a USD amount in bold. Used for headline numbers (the
// rolling totals, the combined live spend). For per-turn amounts —
// where magnitude matters for alarm-worthiness — use moneyByMag.
func money(st term.Style, usd float64, digits int) string {
	return st.Bold(fmt.Sprintf("$%.*f", digits, usd))
}

// moneyByMag colors a per-turn USD amount by magnitude so the eye is
// drawn to expensive turns without reading the number itself.
//
//	< $0.01  dim (negligible)
//	< $0.05  default (typical)
//	< $0.50  yellow (notable)
//	>= $0.50 bold red (alarming)
func moneyByMag(st term.Style, usd float64, digits int) string {
	s := fmt.Sprintf("$%.*f", digits, usd)
	switch {
	case usd < 0.01:
		return st.Dim(s)
	case usd < 0.05:
		return s
	case usd < 0.50:
		return st.Yellow(s)
	default:
		return st.Bold(st.Red(s))
	}
}

// deltaMoney is the "+$0.0727" suffix on session rows. Like moneyByMag
// but always carries a sign and is one shade quieter — this is a
// recurring incremental, not a running total.
func deltaMoney(st term.Style, usd float64) string {
	s := fmt.Sprintf("+$%.4f", usd)
	switch {
	case usd < 0.05:
		return st.Dim(s)
	case usd < 0.50:
		return st.Yellow(s)
	default:
		return st.Red(s)
	}
}

// label dims a non-numeric label like "today" or "turns".
func label(st term.Style, s string) string { return st.Dim(s) }

// toolsLabel formats the comma-joined tool names with cyan emphasis.
func toolsLabel(st term.Style, tools []string) string {
	if len(tools) == 0 {
		return st.Dim("-")
	}
	return st.Cyan(strings.Join(tools, "+"))
}

// project formats a project name as bold-cyan (matches Claude Code's
// own accent color for path/file references in tool output).
func project(st term.Style, name string) string {
	return st.Bold(st.Cyan(name))
}

// rollingPanelLine renders the totals row inside the "totals" panel.
// today / week / month sit on one line separated by dim dots.
func rollingPanelLine(st term.Style, today, week, month float64) string {
	return strings.Join([]string{
		label(st, "today") + " " + money(st, today, 2),
		label(st, "week") + " " + money(st, week, 2),
		label(st, "month") + " " + money(st, month, 2),
	}, dotSep(st))
}

// liveHeader is the title-hint string that appears in the live
// panel's top border: "$18.18 across 2 active sessions". "active" is
// explicit because we hide idle sessions from the visible count.
func liveHeader(st term.Style, total float64, n int) string {
	sess := "active session"
	if n != 1 {
		sess = "active sessions"
	}
	return fmt.Sprintf("%s %s %d %s", money(st, total, 4), label(st, "across"), n, label(st, sess))
}

// singleSessionLine renders the one-row body for `claudit watch` (one
// session). Layout: total · turns · hit% · last turn: tool (+cost).
// The per-turn cost is parenthesized inside the same cell as the tool
// name so the eye reads them as a single thought — what the last turn
// did, and what it cost.
func singleSessionLine(st term.Style, total float64, turns int, hitRatio float64, tools []string, lastCost float64) string {
	hit := st.Dim("—")
	if hitRatio > 0 {
		hit = fmt.Sprintf("%.1f%%", 100*hitRatio)
	}
	return strings.Join([]string{
		money(st, total, 2),
		fmt.Sprintf("%d %s", turns, label(st, "turns")),
		fmt.Sprintf("%s %s", hit, label(st, "cache")),
		fmt.Sprintf("%s %s (%s)", label(st, "last turn:"), toolsLabel(st, tools), deltaMoney(st, lastCost)),
	}, dotSep(st))
}

// projectHeading is the heading line that introduces a project group
// under --all. When a project has multiple active sessions, the
// heading carries the aggregate ("3 sessions · 412 turns · $66 total")
// so the reader can compare projects without summing per-session
// rows. With one session the heading is just the project name —
// the detail row below has the only meaningful numbers.
func projectHeading(st term.Style, name string, sessionCount, turns int, cost float64) string {
	if sessionCount <= 1 {
		return project(st, name)
	}
	return fmt.Sprintf("%s%s%d %s%s%d %s%s%s %s",
		project(st, name),
		dotSep(st),
		sessionCount, label(st, "sessions"),
		dotSep(st),
		turns, label(st, "turns"),
		dotSep(st),
		moneyByMag(st, cost, 4), label(st, "total"))
}

// projectDetailRow is the indented one-line detail under a project
// heading: "<n> turns   $X total   last turn: <tool> (+$Y)".
//
// Layout is column-aligned by visible width (turnCol/totalCol) so
// multiple sessions / multiple projects stack cleanly. The "last turn"
// cell is the rightmost — no trailing padding needed, the cost lives
// inside the same parens-grouped cell.
func projectDetailRow(st term.Style, turns int, cost float64, tools []string, lastCost float64, turnCol, totalCol int) string {
	turnCell := fmt.Sprintf("%d %s", turns, label(st, "turns"))
	totalCell := fmt.Sprintf("%s %s", moneyByMag(st, cost, 4), label(st, "total"))
	lastCell := fmt.Sprintf("%s %s (%s)", label(st, "last turn:"), toolsLabel(st, tools), deltaMoney(st, lastCost))

	return fmt.Sprintf("    %s%s   %s%s   %s",
		turnCell, padTo(turnCell, turnCol),
		totalCell, padTo(totalCell, totalCol),
		lastCell)
}

// padTo returns a run of spaces to right-pad s to `col` visible columns.
// Negative or zero padding becomes the empty string. Used by the
// detail-row builder so multiple rows align even when ANSI-colored
// content has different visible widths.
func padTo(s string, col int) string {
	w := term.VisibleWidth(s)
	if w >= col {
		return ""
	}
	return strings.Repeat(" ", col-w)
}

// styleSpikeSingle is the single-session SPIKE alert.
func styleSpikeSingle(st term.Style, turn int, cost, ratio float64, window int, median float64, tools string) string {
	return fmt.Sprintf("%s  turn %d  %s  %s  %s  %s",
		st.BoldRed("SPIKE"),
		turn,
		moneyByMag(st, cost, 4),
		st.Yellow(fmt.Sprintf("%.1fx", ratio)),
		st.Dim(fmt.Sprintf("median $%.4f over %d", median, window)),
		st.Dim("last: ")+st.Cyan(tools))
}

// styleSpikeMulti is the cross-session SPIKE alert.
func styleSpikeMulti(st term.Style, projectName string, turn int, cost, median float64, tools string) string {
	return fmt.Sprintf("%s  %s · turn %d  %s  %s  %s  %s",
		st.BoldRed("SPIKE"),
		project(st, projectName), turn,
		moneyByMag(st, cost, 4),
		st.Yellow(fmt.Sprintf("%.1fx", cost/median)),
		st.Dim(fmt.Sprintf("median $%.4f", median)),
		st.Dim("last: ")+st.Cyan(tools))
}

// styleBudgetSingle is the single-session budget-cross alert.
func styleBudgetSingle(st term.Style, total, budget float64) string {
	return fmt.Sprintf("%s  %s ≥ %s",
		st.BoldRed("BUDGET"),
		st.Bold(fmt.Sprintf("$%.2f", total)),
		st.Bold(fmt.Sprintf("$%.2f", budget)))
}

// styleBudgetMulti is the cross-session budget-cross alert.
func styleBudgetMulti(st term.Style, total, budget float64) string {
	return fmt.Sprintf("%s  combined %s ≥ %s",
		st.BoldRed("BUDGET"),
		st.Bold(fmt.Sprintf("$%.2f", total)),
		st.Bold(fmt.Sprintf("$%.2f", budget)))
}
