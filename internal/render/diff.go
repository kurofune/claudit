package render

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"sort"

	"github.com/kurofune/claudit/internal/aggregate"
)

//go:embed diff.html.tmpl
var diffHTMLTemplate string

// diffTpl is parsed once at init time. Template funcs handle all numeric
// formatting so the template stays readable.
var diffTpl = template.Must(template.New("diff").Funcs(template.FuncMap{
	"money":          money,
	"deltaMoney":     deltaMoney,
	"deltaPct":       deltaPct,
	"deltaInt":       deltaInt,
	"deltaRatio":     deltaRatio,
	"ratioPctOrDash": ratioPctOrDash,
	"pctOf":          pctOf,
	"truncate":       truncate,
	"barPct":         barPct,
	"deltaSign":      deltaSign,
}).Parse(diffHTMLTemplate))

// barPct returns the width percent (0..100) of a value relative to a
// section's max. Returns 0 when max is zero. Bars below 1% bump to 1
// so a nonzero row never renders as an invisible sliver.
func barPct(v, max float64) string {
	if max <= 0 || v <= 0 {
		return "0"
	}
	p := 100 * v / max
	if p < 1 {
		p = 1
	}
	if p > 100 {
		p = 100
	}
	return fmt.Sprintf("%.2f", p)
}

// deltaSign classifies a B−A delta as "up" / "down" / "zero" so the
// template can map to coral / green / muted without re-doing the math.
func deltaSign(a, b float64) string {
	switch {
	case b > a:
		return "up"
	case b < a:
		return "down"
	default:
		return "zero"
	}
}

// diffHTMLSection bundles one movers table with its display title and the
// per-section bar max. Computing max once on the server side beats asking
// the browser to do it for every row.
type diffHTMLSection struct {
	Title   string
	Rows    []DiffMover
	Max     float64
	Empty   bool
}

// diffHTMLData is the full payload passed to diff.html.tmpl.
type diffHTMLData struct {
	LabelA, LabelB  string
	TotalsA         aggregate.Totals
	TotalsB         aggregate.Totals
	HitRatioA       float64
	HitRatioB       float64
	Sections        []diffHTMLSection
	NewHotspots     []aggregate.Hotspot
	NewHotspotsShow bool // true when the section is enabled (Hotspots > 0)
}

// DiffHTML writes a self-contained HTML diff to w. Server-side rendered;
// no client JS. Reuses the design tokens from the main report so the two
// surfaces feel like one product.
func DiffHTML(w io.Writer, a, b *aggregate.Aggregator, opt DiffOptions) error {
	opt.defaults()

	mkSection := func(title string, rows []DiffMover) diffHTMLSection {
		rows = rankMovers(rows, opt.TopMovers)
		max := 0.0
		for _, r := range rows {
			if r.CostA > max {
				max = r.CostA
			}
			if r.CostB > max {
				max = r.CostB
			}
		}
		return diffHTMLSection{
			Title: title,
			Rows:  rows,
			Max:   max,
			Empty: len(rows) == 0,
		}
	}

	data := diffHTMLData{
		LabelA:    opt.LabelA,
		LabelB:    opt.LabelB,
		TotalsA:   a.Totals(),
		TotalsB:   b.Totals(),
		HitRatioA: a.Totals().Tokens.HitRatio(),
		HitRatioB: b.Totals().Tokens.HitRatio(),
		Sections: []diffHTMLSection{
			mkSection("By model", ModelMovers(a, b)),
			mkSection("By project", ProjectMovers(a, b)),
			mkSection("By tool", ToolMovers(a, b)),
			mkSection("By skill / slash command", SkillMovers(a, b)),
			mkSection("By subagent type", SubagentMovers(a, b)),
		},
	}
	if opt.Hotspots > 0 {
		data.NewHotspots = newHotspots(a, b, opt.Hotspots)
		data.NewHotspotsShow = true
	}
	return diffTpl.Execute(w, data)
}

// DiffOptions controls diff output. Zero value is fine — sensible
// defaults are filled in.
type DiffOptions struct {
	// LabelA / LabelB are the human-readable range tags shown in the
	// header (e.g. "2026-04-25..2026-05-01").
	LabelA, LabelB string

	// TopMovers caps each per-dimension movers table. Default 10.
	TopMovers int

	// Hotspots is the size of the hotspot pool used to derive the
	// "new in B" section. Default 10. Zero disables that section.
	Hotspots int
}

func (o *DiffOptions) defaults() {
	if o.TopMovers <= 0 {
		o.TopMovers = 10
	}
}

// DiffMover is one ranked row in a per-dimension movers table.
type DiffMover struct {
	Key   string  `json:"key"`
	CostA float64 `json:"cost_a"`
	CostB float64 `json:"cost_b"`
}

// AbsDelta returns |B - A|. Used for sort ranking.
func (d DiffMover) AbsDelta() float64 { return math.Abs(d.CostB - d.CostA) }

// DiffMarkdown writes a side-by-side delta report comparing two
// aggregators (A = baseline, B = current).
func DiffMarkdown(w io.Writer, a, b *aggregate.Aggregator, opt DiffOptions) error {
	opt.defaults()
	totA := a.Totals()
	totB := b.Totals()

	fmt.Fprintln(w, "# claudit diff")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "_Comparing baseline **A** (`%s`) against current **B** (`%s`). Δ$ is B − A; Δ%% uses A as the denominator._\n\n", opt.LabelA, opt.LabelB)

	// Totals row.
	fmt.Fprintln(w, "## Totals")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Metric | A | B | Δ |")
	fmt.Fprintln(w, "|---|---:|---:|---:|")
	fmt.Fprintf(w, "| Total cost | %s | %s | %s (%s) |\n",
		money(totA.CostUSD), money(totB.CostUSD),
		deltaMoney(totA.CostUSD, totB.CostUSD), deltaPct(totA.CostUSD, totB.CostUSD))
	fmt.Fprintf(w, "| Sessions | %d | %d | %s |\n", totA.Sessions, totB.Sessions, deltaInt(totA.Sessions, totB.Sessions))
	fmt.Fprintf(w, "| Turns | %d | %d | %s |\n", totA.Turns, totB.Turns, deltaInt(totA.Turns, totB.Turns))
	fmt.Fprintf(w, "| Overall hit ratio | %s | %s | %s |\n",
		ratioPctOrDash(totA.Tokens.HitRatio()), ratioPctOrDash(totB.Tokens.HitRatio()),
		deltaRatio(totA.Tokens.HitRatio(), totB.Tokens.HitRatio()))
	fmt.Fprintln(w)

	writeMoversTable(w, "By model", ModelMovers(a, b), opt.TopMovers)
	writeMoversTable(w, "By project", ProjectMovers(a, b), opt.TopMovers)
	writeMoversTable(w, "By tool", ToolMovers(a, b), opt.TopMovers)
	writeMoversTable(w, "By skill / slash command", SkillMovers(a, b), opt.TopMovers)
	writeMoversTable(w, "By subagent type", SubagentMovers(a, b), opt.TopMovers)

	// New hotspots in B.
	if opt.Hotspots > 0 {
		newH := newHotspots(a, b, opt.Hotspots)
		fmt.Fprintln(w, "## New hotspots in B")
		fmt.Fprintln(w)
		if len(newH) == 0 {
			fmt.Fprintln(w, "_(B's top hotspots all appear in A's top hotspots — no new headline movers.)_")
			fmt.Fprintln(w)
		} else {
			fmt.Fprintf(w, "_Hotspots that appear in B's top %d but not in A's top %d._\n\n", opt.Hotspots, opt.Hotspots)
			fmt.Fprintln(w, "| Hotspot | Cost in B | % of B total |")
			fmt.Fprintln(w, "|---|---:|---:|")
			for _, h := range newH {
				fmt.Fprintf(w, "| %s | %s | %s |\n", h.Title, money(h.CostUSD), pctOf(h.PctOfTotal))
			}
			fmt.Fprintln(w)
		}
	}

	return nil
}

// DiffJSON writes the diff payload as JSON. Mirrors the markdown
// sections so downstream consumers don't have to scrape text.
func DiffJSON(w io.Writer, a, b *aggregate.Aggregator, opt DiffOptions) error {
	opt.defaults()
	out := struct {
		LabelA          string             `json:"label_a"`
		LabelB          string             `json:"label_b"`
		TotalsA         aggregate.Totals   `json:"totals_a"`
		TotalsB         aggregate.Totals   `json:"totals_b"`
		HitRatioA       float64            `json:"hit_ratio_a"`
		HitRatioB       float64            `json:"hit_ratio_b"`
		ModelMovers     []DiffMover        `json:"model_movers"`
		ProjectMovers   []DiffMover        `json:"project_movers"`
		ToolMovers      []DiffMover        `json:"tool_movers"`
		SkillMovers     []DiffMover        `json:"skill_movers"`
		SubagentMovers  []DiffMover        `json:"subagent_movers"`
		NewHotspotsInB  []aggregate.Hotspot `json:"new_hotspots_in_b"`
	}{
		LabelA:         opt.LabelA,
		LabelB:         opt.LabelB,
		TotalsA:        a.Totals(),
		TotalsB:        b.Totals(),
		HitRatioA:      a.Totals().Tokens.HitRatio(),
		HitRatioB:      b.Totals().Tokens.HitRatio(),
		ModelMovers:    rankMovers(ModelMovers(a, b), opt.TopMovers),
		ProjectMovers:  rankMovers(ProjectMovers(a, b), opt.TopMovers),
		ToolMovers:     rankMovers(ToolMovers(a, b), opt.TopMovers),
		SkillMovers:    rankMovers(SkillMovers(a, b), opt.TopMovers),
		SubagentMovers: rankMovers(SubagentMovers(a, b), opt.TopMovers),
		NewHotspotsInB: newHotspots(a, b, opt.Hotspots),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeMoversTable(w io.Writer, title string, rows []DiffMover, top int) {
	fmt.Fprintf(w, "## Top movers — %s\n\n", title)
	rows = rankMovers(rows, top)
	if len(rows) == 0 {
		fmt.Fprintln(w, "_(no rows)_")
		fmt.Fprintln(w)
		return
	}
	fmt.Fprintln(w, "| Key | A | B | Δ$ | Δ% |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|")
	for _, r := range rows {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n",
			truncate(r.Key, 60), money(r.CostA), money(r.CostB),
			deltaMoney(r.CostA, r.CostB), deltaPct(r.CostA, r.CostB))
	}
	fmt.Fprintln(w)
}

// rankMovers sorts by absolute delta descending and trims to top.
func rankMovers(rows []DiffMover, top int) []DiffMover {
	sort.SliceStable(rows, func(i, j int) bool {
		di, dj := rows[i].AbsDelta(), rows[j].AbsDelta()
		if di != dj {
			return di > dj
		}
		return rows[i].Key < rows[j].Key
	})
	if top > 0 && len(rows) > top {
		rows = rows[:top]
	}
	return rows
}

// ModelMovers builds (cost_a, cost_b) rows for every model seen in either
// aggregator. Rows where both sides are zero are dropped — the
// aggregator can't surface a key without at least one nonzero side.
func ModelMovers(a, b *aggregate.Aggregator) []DiffMover {
	aMap := map[string]float64{}
	for _, m := range a.ByModel() {
		aMap[m.Model] = m.CostUSD
	}
	bMap := map[string]float64{}
	for _, m := range b.ByModel() {
		bMap[m.Model] = m.CostUSD
	}
	return mergeMovers(aMap, bMap)
}

// ProjectMovers builds project-cost movers.
func ProjectMovers(a, b *aggregate.Aggregator) []DiffMover {
	aMap := map[string]float64{}
	for _, p := range a.ByProject() {
		aMap[p.Project] = p.CostUSD
	}
	bMap := map[string]float64{}
	for _, p := range b.ByProject() {
		bMap[p.Project] = p.CostUSD
	}
	return mergeMovers(aMap, bMap)
}

// ToolMovers builds tool-cost movers.
func ToolMovers(a, b *aggregate.Aggregator) []DiffMover {
	aMap := map[string]float64{}
	for _, t := range a.ByTool() {
		aMap[t.Name] = t.CostUSD
	}
	bMap := map[string]float64{}
	for _, t := range b.ByTool() {
		bMap[t.Name] = t.CostUSD
	}
	return mergeMovers(aMap, bMap)
}

// SkillMovers builds skill / slash-command cost movers.
func SkillMovers(a, b *aggregate.Aggregator) []DiffMover {
	aMap := map[string]float64{}
	for _, s := range a.BySkill() {
		aMap[s.Key] = s.CostUSD
	}
	bMap := map[string]float64{}
	for _, s := range b.BySkill() {
		bMap[s.Key] = s.CostUSD
	}
	return mergeMovers(aMap, bMap)
}

// SubagentMovers builds subagent-type cost movers.
func SubagentMovers(a, b *aggregate.Aggregator) []DiffMover {
	aMap := map[string]float64{}
	for _, s := range a.BySubagent() {
		aMap[s.Type] = s.CostUSD
	}
	bMap := map[string]float64{}
	for _, s := range b.BySubagent() {
		bMap[s.Type] = s.CostUSD
	}
	return mergeMovers(aMap, bMap)
}

func mergeMovers(aMap, bMap map[string]float64) []DiffMover {
	keys := make(map[string]struct{}, len(aMap)+len(bMap))
	for k := range aMap {
		keys[k] = struct{}{}
	}
	for k := range bMap {
		keys[k] = struct{}{}
	}
	out := make([]DiffMover, 0, len(keys))
	for k := range keys {
		ca, cb := aMap[k], bMap[k]
		if ca == 0 && cb == 0 {
			continue
		}
		out = append(out, DiffMover{Key: k, CostA: ca, CostB: cb})
	}
	return out
}

func newHotspots(a, b *aggregate.Aggregator, top int) []aggregate.Hotspot {
	if top <= 0 {
		return nil
	}
	aH := a.Hotspots(top)
	bH := b.Hotspots(top)
	seen := make(map[string]struct{}, len(aH))
	for _, h := range aH {
		seen[hotspotKey(h)] = struct{}{}
	}
	var out []aggregate.Hotspot
	for _, h := range bH {
		if _, found := seen[hotspotKey(h)]; !found {
			out = append(out, h)
		}
	}
	return out
}

func hotspotKey(h aggregate.Hotspot) string { return string(h.Kind) + "::" + h.Title }

// deltaMoney formats a signed dollar delta. Always shows a sign so the
// reader can scan a column of deltas without parsing each one.
func deltaMoney(prev, cur float64) string {
	d := cur - prev
	if d == 0 {
		return "$0.00"
	}
	if d > 0 {
		return fmt.Sprintf("+$%.2f", d)
	}
	return fmt.Sprintf("-$%.2f", -d)
}

// deltaInt formats a signed integer delta.
func deltaInt(prev, cur int) string {
	d := cur - prev
	if d > 0 {
		return fmt.Sprintf("+%d", d)
	}
	return fmt.Sprintf("%d", d)
}

// deltaRatio formats a hit-ratio delta as percentage points (pp). Returns
// "—" when both sides are zero (no cacheable traffic at all).
func deltaRatio(prev, cur float64) string {
	if prev == 0 && cur == 0 {
		return "—"
	}
	d := 100 * (cur - prev)
	if d > 0 {
		return fmt.Sprintf("+%.1fpp", d)
	}
	return fmt.Sprintf("%.1fpp", d)
}

// ratioPctOrDash is ratioPct but treats true-zero ratios as "—" so a row
// with no cacheable traffic doesn't read as "0.0%".
func ratioPctOrDash(r float64) string {
	if r <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*r)
}
