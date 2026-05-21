package render

import (
	"html"
	"html/template"
	"strconv"
	"strings"

	"github.com/kurofune/claudit/internal/aggregate"
)

// renderModelBarsHTML server-side renders the inner markup of the
// #bars-model element — a horizontal-bar list, one row per model
// with a fill scaled to the row's share of total cost.
//
// Phase 3 of the SSR migration: ports the modelBars() JS IIFE
// (report.html.tmpl, around line 3028).
//
// Returns the empty-state hint when no model has positive cost.
func renderModelBarsHTML(rows []aggregate.ModelBucket, totalCost float64) template.HTML {
	// JS-side: rows = D.by_model.filter(r => r.CostUSD > 0).
	priced := make([]aggregate.ModelBucket, 0, len(rows))
	for _, r := range rows {
		if r.CostUSD > 0 {
			priced = append(priced, r)
		}
	}
	if len(priced) == 0 {
		return template.HTML(`<p class="small empty-state">No priced model data. Add prices for any unknown models in <code>~/.config/claudit/prices.yaml</code>.</p>`)
	}
	var b strings.Builder
	for _, r := range priced {
		writeHbar(&b, r.Model, r.CostUSD, totalCost, "", false)
	}
	return template.HTML(b.String())
}

// renderProjectBarsHTML server-side renders the inner markup of the
// #bars-project element — a horizontal-bar list of the top 20
// projects by cost. Each row's label gets visibly truncated to 70
// chars; the title= attribute keeps the full path so hover reveals
// it.
//
// Phase 3 of the SSR migration: ports the projectBars() JS IIFE
// (report.html.tmpl, around line 3056).
func renderProjectBarsHTML(rows []aggregate.ProjectBucket, totalCost float64) template.HTML {
	limit := len(rows)
	if limit > 20 {
		limit = 20
	}
	var b strings.Builder
	for _, r := range rows[:limit] {
		writeHbar(&b, r.Project, r.CostUSD, totalCost, "", true)
	}
	return template.HTML(b.String())
}

// renderToolBarsHTML server-side renders the inner markup of the
// #bars-tool element. Differs from model/project bars: the .val
// cell reads "money · NN calls" rather than "money (pct)".
//
// Phase 3 of the SSR migration: ports the toolBars() JS IIFE
// (report.html.tmpl, around line 3134).
func renderToolBarsHTML(rows []aggregate.ToolBucket, totalCost float64) template.HTML {
	var b strings.Builder
	for _, r := range rows {
		extra := ` · ` + num(int64(r.Count)) + ` calls`
		writeHbar(&b, r.Name, r.CostUSD, totalCost, extra, false)
	}
	return template.HTML(b.String())
}

// writeHbar emits one .hbar row: a fill scaled to the share of
// totalCost, a label (HTML-escaped), and a value cell. The label
// text and title= attribute both reflect the supplied name; if
// truncateLabel is true, the visible label uses the markdown
// truncate() helper (keeps the last N chars + leading "…").
//
// extraVal is appended to the .val cell after the money/percent;
// pass " · 1,234 calls" for the tool-bars variant.
func writeHbar(b *strings.Builder, name string, cost, totalCost float64, extraVal string, truncateLabel bool) {
	pctW := "0.0"
	if totalCost > 0 {
		// JS: (100 * r.CostUSD / totalCost).toFixed(1)
		pctW = fmtFloat1(100 * cost / totalCost)
	}
	nameEsc := html.EscapeString(name)
	visible := nameEsc
	if truncateLabel {
		visible = html.EscapeString(truncate(name, 70))
	}
	b.WriteString(`<div class="hbar">`)
	b.WriteString(`<div class="fill" style="width:`)
	b.WriteString(pctW)
	b.WriteString(`%"></div>`)
	b.WriteString(`<div class="lbl" title="`)
	b.WriteString(nameEsc)
	b.WriteString(`">`)
	b.WriteString(visible)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="val">`)
	b.WriteString(money(cost))
	if extraVal != "" {
		b.WriteString(extraVal)
	} else {
		b.WriteString(` (`)
		b.WriteString(pctOfDenom(cost, totalCost))
		b.WriteString(`)`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`</div>`)
}

// pctOfDenom mirrors the JS fmtPct(part, total): renders "N.N%" or
// "—" when total <= 0.
func pctOfDenom(part, total float64) string {
	if total <= 0 {
		return "—"
	}
	return fmtFloat1(100*part/total) + "%"
}

// fmtFloat1 formats a float with exactly one decimal place. Mirrors
// JS .toFixed(1).
func fmtFloat1(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}
