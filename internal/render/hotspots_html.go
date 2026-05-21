package render

import (
	"html"
	"html/template"
	"strconv"
	"strings"

	"github.com/kurofune/claudit/internal/aggregate"
)

// renderHotspotsHTML server-side renders the inner markup of the
// #hotspots element — N <details class="hotspot"> cards, each with
// a rank, kind chip, title, cost, percent, optional cross-link to
// the originating session, a "Copy prompt" button, and the prompt
// text in a <pre>.
//
// Phase 3 of the SSR migration: ports the hotspots() JS IIFE
// (report.html.tmpl, around line 2628).
//
// promptKeySet is the set of prompt keys present in the session
// timelines. Used to decide whether prompt_pattern hotspots get a
// clickable "view session" button or the disabled "(unavailable)"
// hint.
func renderHotspotsHTML(hotspots []hotspotForJSON, promptKeySet map[string]struct{}) template.HTML {
	if len(hotspots) == 0 {
		return template.HTML(`<p class="small empty-state">No hotspots in this window. Try widening the time range with <code>--since</code>, or lowering <code>-hotspots</code>.</p>`)
	}
	var b strings.Builder
	for i, h := range hotspots {
		writeHotspotCard(&b, i, h, promptKeySet)
	}
	return template.HTML(b.String())
}

// writeHotspotCard emits one <details class="hotspot"> for a single
// hotspot. Mirrors the inner template literal of the JS hotspots()
// IIFE (report.html.tmpl, around line 2649).
func writeHotspotCard(b *strings.Builder, idx int, h hotspotForJSON, promptKeySet map[string]struct{}) {
	rank := idx + 1
	anchorID := `hotspot-` + itoa(rank)
	titleEsc := html.EscapeString(h.Title)
	kindFmt := html.EscapeString(strings.ReplaceAll(string(h.Kind), "_", " "))
	b.WriteString(`<details class="hotspot" id="`)
	b.WriteString(anchorID)
	b.WriteString(`">`)
	b.WriteString(`<summary>`)
	b.WriteString(`<span class="rank">#`)
	b.WriteString(itoa(rank))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="hotspot-kind">`)
	b.WriteString(kindFmt)
	b.WriteString(`</span>`)
	b.WriteString(`<span class="h-title" title="`)
	b.WriteString(titleEsc)
	b.WriteString(`">`)
	b.WriteString(titleEsc)
	b.WriteString(`</span>`)
	b.WriteString(`<span class="h-cost">`)
	b.WriteString(money(h.CostUSD))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="h-pct">(`)
	b.WriteString(fmtFloat1(h.PctOfTotal))
	b.WriteString(`%)</span>`)
	b.WriteString(`<a class="anchor-link" href="#overview/`)
	b.WriteString(anchorID)
	b.WriteString(`" title="Copy link to this hotspot" aria-label="Copy link to hotspot `)
	b.WriteString(itoa(rank))
	b.WriteString(`">#</a>`)
	b.WriteString(`</summary>`)
	b.WriteString(`<div class="body">`)
	b.WriteString(`<div class="actions">`)
	b.WriteString(`<button class="copy" data-hotspot="`)
	b.WriteString(itoa(idx))
	b.WriteString(`">Copy prompt</button>`)
	writeViewSessionAction(b, h, promptKeySet)
	b.WriteString(`<span class="small">Paste this into Claude or another LLM for specific advice.</span>`)
	b.WriteString(`</div>`)
	b.WriteString(`<pre>`)
	b.WriteString(html.EscapeString(h.Prompt))
	b.WriteString(`</pre>`)
	b.WriteString(`</div>`)
	b.WriteString(`</details>`)
}

// writeViewSessionAction emits the optional cross-link control on a
// prompt_pattern hotspot. Three states:
//   - non-prompt hotspot: nothing
//   - prompt_pattern with key in set: enabled "view session →" button
//   - prompt_pattern with key NOT in set: disabled "(unavailable)" span
func writeViewSessionAction(b *strings.Builder, h hotspotForJSON, promptKeySet map[string]struct{}) {
	if h.Kind != aggregate.HotspotPromptPattern || h.PromptKey == "" {
		return
	}
	if _, ok := promptKeySet[h.PromptKey]; ok {
		b.WriteString(`<button class="view-session" data-key="`)
		b.WriteString(html.EscapeString(h.PromptKey))
		b.WriteString(`" title="Open the session this prompt ran in">view session →</button>`)
		return
	}
	b.WriteString(`<span class="view-session is-disabled" title="This prompt&#39;s session is below the --sessions=N cap. Re-run with a higher --sessions to include it.">view session (unavailable)</span>`)
}

// itoa is a local alias for strconv.Itoa, kept so call sites read
// as "itoa(n)" — the integer-to-string idiom this file leans on
// heavily for HTML ID generation.
func itoa(n int) string { return strconv.Itoa(n) }
