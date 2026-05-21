package render

import (
	"html/template"
	"strconv"
	"strings"

	"github.com/kurofune/claudit/internal/aggregate"
)

// renderTotalsHTML server-side renders the inner markup of the #totals
// element — the headline cost tile + three metric tiles (Sessions,
// Assistant turns, Cache hit ratio) with optional period-over-period
// delta chips beneath each value.
//
// Phase 3 of the SSR migration: ports the totals JS IIFE
// (report.html.tmpl, around line 2616) so the page paints with the
// headline numbers on the first byte.
func renderTotalsHTML(totals aggregate.Totals, overallHitRatio float64, period aggregate.Period, trendTotals []aggregate.TrendPoint) template.HTML {
	// Period-over-period deltas only render with --by AND ≥2 buckets.
	var costDelta, sessionsDelta, turnsDelta, hitDelta string
	if period.Valid() && len(trendTotals) >= 2 {
		latest := trendTotals[len(trendTotals)-1]
		prior := trendTotals[len(trendTotals)-2]
		costDelta = deltaBlock(latest.CostUSD, prior.CostUSD, "bad", false, period)
		sessionsDelta = deltaBlock(float64(latest.Sessions), float64(prior.Sessions), "neutral", false, period)
		turnsDelta = deltaBlock(float64(latest.Turns), float64(prior.Turns), "neutral", false, period)
		hitDelta = deltaBlock(latest.HitRatio(), prior.HitRatio(), "good", true, period)
	}

	var b strings.Builder
	b.WriteString(`<div class="headline">`)
	b.WriteString(`<div class="label">`)
	b.WriteString(labelIconSVG("cost"))
	b.WriteString(`Total cost</div>`)
	b.WriteString(`<div class="value">`)
	b.WriteString(money(totals.CostUSD))
	b.WriteString(`</div>`)
	b.WriteString(costDelta)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="metric">`)
	b.WriteString(`<div class="label">`)
	b.WriteString(labelIconSVG("sessions"))
	b.WriteString(`Sessions</div>`)
	b.WriteString(`<div class="value">`)
	b.WriteString(num(int64(totals.Sessions)))
	b.WriteString(`</div>`)
	b.WriteString(sessionsDelta)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="metric">`)
	b.WriteString(`<div class="label">`)
	b.WriteString(labelIconSVG("turns"))
	b.WriteString(`Assistant turns</div>`)
	b.WriteString(`<div class="value">`)
	b.WriteString(num(int64(totals.Turns)))
	b.WriteString(`</div>`)
	b.WriteString(turnsDelta)
	b.WriteString(`</div>`)
	b.WriteString(`<div class="metric">`)
	b.WriteString(`<div class="label">`)
	b.WriteString(labelIconSVG("gauge"))
	b.WriteString(`Cache hit ratio</div>`)
	b.WriteString(`<div class="value">`)
	b.WriteString(hitRatioPill(overallHitRatio, ""))
	b.WriteString(`</div>`)
	b.WriteString(hitDelta)
	b.WriteString(`</div>`)
	return template.HTML(b.String())
}

// deltaBlock mirrors the JS deltaBlock(latest, prior, semantics,
// asPP) helper. Returns "" when no comparison is possible (zero
// prior in relative mode, or non-finite math). Otherwise emits one
// of {delta-bad, delta-good, delta-neutral, delta-flat} chip.
//
//   - semantics "bad": rising values are bad (e.g. cost).
//   - semantics "good": rising values are good (e.g. hit ratio).
//   - semantics "neutral": directional but value-neutral (e.g. turns).
//   - asPP=true: value is a 0..1 ratio; we compare in percentage
//     POINTS rather than relative %. "50% → 55%" reads as "+5pp".
func deltaBlock(latest, prior float64, semantics string, asPP bool, period aggregate.Period) string {
	// Mirror the JS `!isFinite` guard. NaN, +Inf, -Inf all fall out
	// here. We don't take NaN directly in Go, but a 0/0 division
	// in the relative branch is handled by the prior-zero check
	// below.
	if !asPP && prior == 0 {
		return ""
	}
	var v float64
	if asPP {
		v = (latest - prior) * 100
	} else {
		v = (latest - prior) / prior * 100
	}
	periodTail := "vs prior"
	if period.Valid() {
		periodTail = "vs prior " + string(period)
	}
	tail := `<span class="delta-tail">` + periodTail + `</span>`
	eps := 0.1
	if asPP {
		eps = 0.5
	}
	if absFloat(v) < eps {
		return `<div class="delta delta-flat">flat <span class="delta-tail">` + periodTail + `</span></div>`
	}
	isUp := v > 0
	cls := "delta-neutral"
	switch semantics {
	case "bad":
		if isUp {
			cls = "delta-bad"
		} else {
			cls = "delta-good"
		}
	case "good":
		if isUp {
			cls = "delta-good"
		} else {
			cls = "delta-bad"
		}
	}
	iconID := "chevron-down"
	if isUp {
		iconID = "chevron-up"
	}
	arrow := `<svg class="icon" aria-hidden="true"><use href="#icon-` + iconID + `"/></svg>`
	unit := "%"
	if asPP {
		unit = "pp"
	}
	mag := strconv.FormatFloat(absFloat(v), 'f', 1, 64)
	return `<div class="delta ` + cls + `">` + arrow + mag + unit + ` ` + tail + `</div>`
}

// absFloat is math.Abs without the import. Inlined to keep the
// totals-renderer's deps minimal — same shape as the JS Math.abs.
func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// hitRatioPill mirrors the JS hitRatioPill(v, extraClass) helper:
// renders a colored tier badge (good/ok/bad) with a percentage. Pass
// extraClass="tier-sm" for the compact nav-metric variant.
func hitRatioPill(v float64, extraClass string) string {
	if v <= 0 {
		// JS treats 0 / NaN / null identically — "—"; our zero path
		// also catches "no cacheable traffic".
		return "—"
	}
	tier := "bad"
	switch {
	case v >= 0.70:
		tier = "good"
	case v >= 0.40:
		tier = "ok"
	}
	cls := "tier tier-" + tier
	if extraClass != "" {
		cls += " " + extraClass
	}
	return `<span class="` + cls + `">` + fmtPctOne(v) + `</span>`
}

// fmtPctOne mirrors the JS `fmtPct1` helper: renders a 0..1 ratio as
// "N.N%". Negative / zero returns "—" so the call site doesn't need
// to special-case empty data.
func fmtPctOne(v float64) string {
	if v <= 0 {
		return "—"
	}
	// One decimal, matches JS .toFixed(1).
	return pctOf(100 * v)
}

// labelIconSVG mirrors the JS `labelIcon(id)` helper: a small Lucide-
// style icon by sprite ID.
func labelIconSVG(id string) string {
	return `<svg class="icon" aria-hidden="true"><use href="#icon-` + id + `"/></svg>`
}
