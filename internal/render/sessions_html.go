package render

import (
	"html"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// timeLocal is the location used to format timestamps in the Sessions
// drill-down. Matches the JS contract (new Date(ts) renders in the
// browser's local TZ) — for SSR we use the server's local zone. Tests
// override this to a fixed location for deterministic goldens.
var timeLocal = time.Local

// renderSessionsHTML server-side renders the Sessions drill-down view's
// inner markup (the children of #session-list). Returns an empty
// template.HTML for an empty input; the template's empty-state element
// handles the no-sessions-in-window message.
//
// Phase 1 of the SSR migration: ports the renderSessions() JS IIFE
// from report.html.tmpl. See the markup contract there for the exact
// structure each session card carries.
//
// All caller-supplied strings (CWD, session ID, prompt text, model
// name, tool name/detail) are HTML-escaped via html.EscapeString so a
// prompt containing "<script>" or a CWD containing quotes can't break
// the surrounding markup.
func renderSessionsHTML(sessions []aggregate.SessionTimeline) template.HTML {
	var b strings.Builder
	for i, s := range sessions {
		renderSessionCard(&b, s, (i%5)+1)
	}
	return template.HTML(b.String())
}

// renderSessionCard emits one <details class="session-card"> with its
// summary header and a nested session-body containing the prompt
// blocks. colorSlot is the 1..5 round-robin index — passed in (not
// hashed) so adjacent cards always differ.
func renderSessionCard(b *strings.Builder, s aggregate.SessionTimeline, colorSlot int) {
	sid := html.EscapeString(s.SessionID)
	cwdEsc := html.EscapeString(s.CWD)
	promptN := len(s.Prompts)
	b.WriteString(`<details class="session-card" id="session-`)
	b.WriteString(sid)
	b.WriteString(`" data-session="`)
	b.WriteString(sid)
	b.WriteString(`">`)
	b.WriteString(`<summary>`)
	b.WriteString(`<span class="s-id" data-c="`)
	b.WriteString(strconv.Itoa(colorSlot))
	b.WriteString(`" title="`)
	b.WriteString(sid)
	b.WriteString(`">`)
	b.WriteString(sid)
	b.WriteString(`</span>`)
	b.WriteString(`<span class="s-cwd" title="`)
	b.WriteString(cwdEsc)
	b.WriteString(`">`)
	if s.CWD == "" {
		b.WriteString(`—`)
	} else {
		b.WriteString(cwdEsc)
	}
	b.WriteString(`</span>`)
	b.WriteString(`<span class="s-stats">`)
	writePluralCount(b, promptN, "prompt")
	writePluralCount(b, s.Turns, "turn")
	b.WriteString(`<span class="s-cost">`)
	b.WriteString(money(s.CostUSD))
	b.WriteString(`</span>`)
	b.WriteString(`<a class="anchor-link" href="#sessions/session-`)
	b.WriteString(sid)
	b.WriteString(`" title="Copy link to this session" aria-label="Copy link to session">#</a>`)
	b.WriteString(`</span>`)
	preview := firstPromptPreview(s)
	if preview != "" {
		pe := html.EscapeString(preview)
		b.WriteString(`<span class="s-preview" title="`)
		b.WriteString(pe)
		b.WriteString(`">`)
		b.WriteString(pe)
		b.WriteString(`</span>`)
	} else {
		b.WriteString(`<span class="s-preview is-empty">(no recognizable user prompts)</span>`)
	}
	b.WriteString(`<span class="s-time">`)
	b.WriteString(html.EscapeString(formatTimeRange(s.StartedAt, s.EndedAt)))
	b.WriteString(`</span>`)
	b.WriteString(`</summary>`)
	b.WriteString(`<div class="session-body">`)
	for _, p := range s.Prompts {
		renderPromptBlock(b, p)
	}
	b.WriteString(`</div>`)
	b.WriteString(`</details>`)
}

// writePluralCount writes <span>N noun</span> or <span>N nouns</span>
// based on count. Centralizes the "1 prompt" / "2 prompts" toggle so
// both .s-stats slots stay in lockstep.
func writePluralCount(b *strings.Builder, n int, noun string) {
	b.WriteString(`<span>`)
	b.WriteString(strconv.Itoa(n))
	b.WriteString(` `)
	b.WriteString(noun)
	if n != 1 {
		b.WriteString(`s`)
	}
	b.WriteString(`</span>`)
}

// promptKeysFromTimelines returns the deduplicated set of non-empty
// PromptTimeline.Key values across all sessions, in first-occurrence
// order. The JS uses this slice to check cross-link availability in
// O(1) without needing the full session_timelines payload.
//
// Returns an empty (non-nil) slice when no usable keys exist so
// json.Marshal emits "[]" rather than "null" — keeps the JS
// consumer's "for (const k of D.prompt_keys)" loop simple.
func promptKeysFromTimelines(sessions []aggregate.SessionTimeline) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, s := range sessions {
		for _, p := range s.Prompts {
			if p.Key == "" {
				continue
			}
			if _, ok := seen[p.Key]; ok {
				continue
			}
			seen[p.Key] = struct{}{}
			out = append(out, p.Key)
		}
	}
	return out
}

// firstPromptPreview returns the first non-orphan prompt's text,
// collapsed to a single line and truncated to 80 chars with an ellipsis.
// A "[redacted N chars]" body passes through unchanged so the user can
// see redaction took place. Returns "" when no usable prompt exists.
func firstPromptPreview(s aggregate.SessionTimeline) string {
	for _, p := range s.Prompts {
		if p.UUID == "" {
			continue
		}
		raw := collapseWhitespace(p.Text)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "[redacted ") {
			return raw
		}
		if len([]rune(raw)) > 80 {
			return string([]rune(raw)[:80]) + "…"
		}
		return raw
	}
	return ""
}

// renderPromptBlock emits the <details class="prompt-block"> for a single
// PromptTimeline. Mirrors the JS contract at report.html.tmpl:3461-3508.
func renderPromptBlock(b *strings.Builder, p aggregate.PromptTimeline) {
	text := p.Text
	isRedacted := strings.HasPrefix(text, "[redacted ")
	isOrphan := p.UUID == ""
	turnN := len(p.Turns)

	b.WriteString(`<details class="prompt-block"`)
	if isOrphan {
		b.WriteString(` data-orphan="1"`)
	}
	if p.Key != "" {
		b.WriteString(` data-prompt-key="`)
		b.WriteString(html.EscapeString(p.Key))
		b.WriteString(`"`)
	}
	b.WriteString(`>`)
	b.WriteString(`<summary>`)
	switch {
	case isOrphan:
		b.WriteString(`<span class="p-text p-redacted">(orphan turns — no recognized originating prompt)</span>`)
	case isRedacted:
		b.WriteString(`<span class="p-text p-redacted">`)
		b.WriteString(html.EscapeString(text))
		b.WriteString(`</span>`)
	default:
		b.WriteString(`<span class="p-text">`)
		b.WriteString(html.EscapeString(text))
		b.WriteString(`</span>`)
	}
	b.WriteString(`<span class="p-stats">`)
	writePluralCount(b, turnN, "turn")
	b.WriteString(`<span class="p-cost">`)
	b.WriteString(money(p.CostUSD))
	b.WriteString(`</span>`)
	b.WriteString(`</span>`)
	b.WriteString(`</summary>`)
	if p.Truncated {
		b.WriteString(`<div class="p-truncated">(prompt truncated — re-run with a higher --sessions cap or check the raw JSONL for the full text)</div>`)
	}
	b.WriteString(`<ul class="turn-list">`)
	for _, t := range p.Turns {
		renderTurnRow(b, t)
	}
	b.WriteString(`</ul>`)
	b.WriteString(`</details>`)
}

// renderTurnRow emits a single <li class="turn-row"> for one TurnSummary.
// Mirrors the JS contract at report.html.tmpl:3481-3506.
func renderTurnRow(b *strings.Builder, t aggregate.TurnSummary) {
	modelEsc := html.EscapeString(t.Model)
	b.WriteString(`<li class="turn-row">`)
	b.WriteString(`<span class="t-time">`)
	b.WriteString(formatHMS(t.Timestamp))
	if dur := formatDuration(t.DurationMs); dur != "" {
		b.WriteString(` <span class="t-dur" title="Time to next turn within this prompt">`)
		b.WriteString(html.EscapeString(dur))
		b.WriteString(`</span>`)
	}
	b.WriteString(`</span>`)
	b.WriteString(`<span class="t-model" title="`)
	b.WriteString(modelEsc)
	b.WriteString(`">`)
	if t.Model == "" {
		b.WriteString(`—`)
	} else {
		b.WriteString(modelEsc)
	}
	b.WriteString(`</span>`)
	b.WriteString(`<span class="t-tokens">`)
	b.WriteString(formatTokens(t.Tokens))
	if cache := formatCacheChip(t.Tokens); cache != "" {
		b.WriteString(` · `)
		b.WriteString(cache)
	}
	b.WriteString(`</span>`)
	b.WriteString(`<span class="t-cost">`)
	b.WriteString(money(t.CostUSD))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="t-tools">`)
	for _, tool := range t.Tools {
		renderToolChip(b, tool)
	}
	if t.Sidechain {
		b.WriteString(`<span class="t-side">sidechain</span>`)
	}
	b.WriteString(`</span>`)
	b.WriteString(`</li>`)
}

// renderToolChip emits one <span class="t-tool" …> for a tool
// invocation. The data-c slot comes from toolColorSlot so the same
// tool name gets the same color across the whole report. Detail (when
// present) renders as a nested .t-tool-detail span and is also
// reflected in the title= attribute.
func renderToolChip(b *strings.Builder, t aggregate.ToolInvocation) {
	slot := toolColorSlot(t.Name)
	nameEsc := html.EscapeString(t.Name)
	detailEsc := html.EscapeString(t.Detail)
	b.WriteString(`<span class="t-tool" data-c="`)
	b.WriteString(strconv.Itoa(slot))
	b.WriteString(`"`)
	if t.Detail != "" {
		b.WriteString(` title="`)
		b.WriteString(nameEsc)
		b.WriteString(` · `)
		b.WriteString(detailEsc)
		b.WriteString(`"`)
	}
	b.WriteString(`>`)
	b.WriteString(nameEsc)
	if t.Detail != "" {
		b.WriteString(` · <span class="t-tool-detail">`)
		b.WriteString(detailEsc)
		b.WriteString(`</span>`)
	}
	b.WriteString(`</span>`)
}

// toolColorSlot is the FNV-1a hash → 1..5 mapping used to color a
// tool chip. Same name → same color across the whole report, so users
// can scan for "all the Bash chips" by hue alone. Mirrors the JS
// algorithm at report.html.tmpl:3381-3389.
func toolColorSlot(name string) int {
	if name == "" {
		return 0
	}
	var h uint32 = 0x811c9dc5
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 0x01000193
	}
	return int(h%5) + 1
}

// formatDuration renders an inter-turn gap (in milliseconds) using
// the JS IIFE's rules:
//   - <= 0: ""
//   - < 1s:    "Nms"
//   - < 10s:   "N.Ns" (one decimal)
//   - < 60s:   "Ns" (integer seconds, rounded)
//   - else:    "MmSs" or "Mm" if seconds round to zero
func formatDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	sec := float64(ms) / 1000
	if sec < 60 {
		if sec < 10 {
			return strconv.FormatFloat(sec, 'f', 1, 64) + "s"
		}
		return strconv.Itoa(int(sec+0.5)) + "s"
	}
	m := int(sec) / 60
	s := int(sec+0.5) - m*60
	if s == 0 {
		return strconv.Itoa(m) + "m"
	}
	return strconv.Itoa(m) + "m" + strconv.Itoa(s) + "s"
}

// formatHMS renders "HH:MM:SS" in the package's timeLocal. Returns ""
// for the zero time. Mirrors fmtHMS in the JS IIFE.
func formatHMS(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(timeLocal).Format("15:04:05")
}

// formatTokens renders "{in} in · {out} out". Mirrors fmtTokens +
// fmtCount in the JS IIFE — ≥1000 collapses to "N.Nk" (one decimal).
func formatTokens(tk aggregate.Tokens) string {
	return formatCount(tk.InputTokens) + ` in · ` + formatCount(tk.OutputTokens) + ` out`
}

// formatCacheChip renders the .t-cache <span> with a totals number
// and a title= breakdown of read / 5m create / 1h create. Returns ""
// when no cache tokens are present. Mirrors fmtCache in the JS IIFE.
func formatCacheChip(tk aggregate.Tokens) string {
	r := tk.CacheReadTokens
	c5 := tk.CacheCreate5mTokens
	c1 := tk.CacheCreate1hTokens
	total := r + c5 + c1
	if total == 0 {
		return ""
	}
	var parts []string
	if r > 0 {
		parts = append(parts, formatCount(r)+" read")
	}
	if c5 > 0 {
		parts = append(parts, formatCount(c5)+" create 5m")
	}
	if c1 > 0 {
		parts = append(parts, formatCount(c1)+" create 1h")
	}
	return `<span class="t-cache" title="` + strings.Join(parts, ", ") + `">` + formatCount(total) + ` cache</span>`
}

// formatCount renders tokens as either an integer or "N.Nk" once it
// crosses 1000. Negative values are printed as-is (won't appear in
// real data; symmetric to JS String(n)).
func formatCount(n int64) string {
	if n >= 1000 {
		// Force one decimal place to match JS .toFixed(1).
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + "k"
	}
	return strconv.FormatInt(n, 10)
}

// formatTimeRange renders "{start} → {end}" or "{start} → {end} ({span})"
// when the span is non-empty. Mirrors the JS template literal at
// report.html.tmpl:3422-3425.
func formatTimeRange(start, end time.Time) string {
	left := formatTime(start)
	right := formatTime(end)
	span := formatSpan(start, end)
	if span == "" {
		return left + " → " + right
	}
	return left + " → " + right + " (" + span + ")"
}

// formatTime renders "YYYY-MM-DD HH:MM" in the package's timeLocal.
// Returns "" for the zero time. Mirrors fmtTime in the JS IIFE.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(timeLocal).Format("2006-01-02 15:04")
}

// formatSpan renders the wall-clock gap between start and end as a
// natural-language duration. Mirrors fmtSpan in the JS IIFE.
//
// Rules:
//   - returns "" if either timestamp is zero, or the diff is <1s
//   - <60s: "Ns"
//   - else build a list of {d,h,m,s} pieces and join with " "
//   - h and m are always emitted when h>0 or m>0
//   - s is emitted only when h>0 and s>0 (the JS comment: "only
//     surface seconds when h is present")
func formatSpan(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	diff := end.Sub(start)
	if diff < 0 {
		diff = 0
	}
	secs := int(diff.Round(time.Second).Seconds())
	if secs == 0 {
		return ""
	}
	if secs < 60 {
		return strconv.Itoa(secs) + "s"
	}
	d := secs / 86400
	secs -= d * 86400
	h := secs / 3600
	secs -= h * 3600
	m := secs / 60
	secs -= m * 60
	var parts []string
	if d > 0 {
		parts = append(parts, strconv.Itoa(d)+"d")
	}
	if h > 0 {
		parts = append(parts, strconv.Itoa(h)+"h")
	}
	if h > 0 || m > 0 {
		parts = append(parts, strconv.Itoa(m)+"m")
	}
	if h > 0 && secs > 0 {
		parts = append(parts, strconv.Itoa(secs)+"s")
	}
	return strings.Join(parts, " ")
}

// collapseWhitespace replaces every run of whitespace with a single
// space and trims the result. Mirrors the JS regex /\s+/g.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // suppress leading whitespace via trim-like start
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	out := b.String()
	// Trim trailing space if any.
	return strings.TrimRight(out, " ")
}
