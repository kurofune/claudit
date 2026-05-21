package render

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/pricing"
)

// htmlSetup builds the smallest valid aggregator the HTML renderer
// will accept — one Opus turn so totals are non-zero and every
// section has at least one row to iterate over.
func htmlSetup(t *testing.T) *aggregate.Aggregator {
	t.Helper()
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	a := aggregate.New(prices)
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	a.Add(mkTurn("claude-opus-4-7", "/p/x", 1_000_000, 200_000, t0))
	return a
}

// TestHTML_OneShotOmitsServeOnlyChrome guards against accidentally
// shipping the auto-reload script in a one-shot `claudit report --html`
// output. The script polls /_claudit/status — useless in a static
// file, and a hint that the renderer leaked serve-only state into
// the default path.
func TestHTML_OneShotOmitsServeOnlyChrome(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if strings.Contains(body, "claudit-reload-toast") {
		t.Errorf("one-shot HTML render leaked the serve-only reload toast")
	}
	if strings.Contains(body, "/_claudit/status") {
		t.Errorf("one-shot HTML render leaked the serve-only status path")
	}
}

func TestHTML_ServeModeInjectsReload(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{
			Enabled:    true,
			Generation: 42,
			StatusPath: "/custom/status",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, "claudit-reload-toast") {
		t.Errorf("serve render missing reload toast")
	}
	// html/template escapes "/" as "\/" inside a JS string literal —
	// JavaScript-equivalent, but the bytes differ from "/custom/status".
	// Match the escaped form so the assertion survives the auto-escape.
	if !strings.Contains(body, `"\/custom\/status"`) {
		t.Errorf("custom status path not interpolated (looked for JS-escaped form)")
	}
	// Generation: html/template pads numeric output with whitespace
	// inside a script context — accept any horizontal whitespace around
	// the literal so the test doesn't break if Go's formatting shifts.
	if !regexp.MustCompile(`INITIAL_GEN\s*=\s*42\b`).MatchString(body) {
		t.Errorf("generation value missing or wrong; want INITIAL_GEN = 42")
	}
}

func TestHTML_ServeModeAddsDateRangeButton(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{Serve: ServeOptions{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// Button wraps the date range; inner span keeps the existing
	// #date-range id so the JS that fills the text doesn't need to
	// change.
	if !regexp.MustCompile(`<button[^>]+id="date-range-button"`).MatchString(body) {
		t.Errorf("ServeMode render missing date-range-button")
	}
	if !regexp.MustCompile(`<span[^>]+id="date-range"`).MatchString(body) {
		t.Errorf("ServeMode render: #date-range should be a <span> inside the button")
	}
	if !strings.Contains(body, `id="icon-calendar"`) {
		t.Errorf("ServeMode render missing calendar icon symbol")
	}
	if !strings.Contains(body, `href="#icon-calendar"`) {
		t.Errorf("ServeMode render missing calendar icon <use>")
	}
}

func TestHTML_OneShotKeepsPlainDateRangeDiv(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// One-shot mode has no server to navigate to — the picker would
	// be a dead control. Keep the static <div>.
	if !regexp.MustCompile(`<div[^>]+id="date-range"`).MatchString(body) {
		t.Errorf("one-shot render: #date-range should remain a <div>")
	}
	if strings.Contains(body, `id="date-range-button"`) {
		t.Errorf("one-shot render leaked the serve-only date-range button")
	}
}

func TestHTML_ServeModeDefaultStatusPath(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{Serve: ServeOptions{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	// Same JS-escape note as above.
	if !strings.Contains(buf.String(), `"\/_claudit\/status"`) {
		t.Errorf("default status path missing (looked for JS-escaped form)")
	}
}

// TestHTML_JSONPayloadDropsSessionTimelines asserts that
// session_timelines no longer ships in the inline JSON island — that
// data is now server-rendered into #session-list and the payload
// trimmed accordingly. Guards the Phase 1 SSR win against regression.
func TestHTML_JSONPayloadDropsSessionTimelines(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	st := []aggregate.SessionTimeline{{
		SessionID: "abc-123",
		Prompts: []aggregate.PromptTimeline{
			{UUID: "u1", Key: "k1", Text: "hi"},
		},
	}}
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		SessionTimelines: st,
	}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// The JSON island lives between this script tag and its closing.
	const open = `<script id="claudit-data" type="application/json">`
	const closeTag = `</script>`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("missing claudit-data script tag")
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, closeTag)
	if j < 0 {
		t.Fatal("missing claudit-data closing tag")
	}
	jsonBlob := rest[:j]
	if strings.Contains(jsonBlob, `"session_timelines"`) {
		t.Errorf("JSON payload should NOT contain session_timelines; first 400 chars: %s", trim400(jsonBlob))
	}
}

// TestHTML_JSONPayloadHasPromptKeys asserts that the JSON payload
// includes a prompt_keys array carrying each non-orphan prompt's Key
// (deduped) — the JS uses it to check cross-link availability in O(1).
func TestHTML_JSONPayloadHasPromptKeys(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	st := []aggregate.SessionTimeline{
		{
			SessionID: "s1",
			Prompts: []aggregate.PromptTimeline{
				{UUID: "u1", Key: "k-alpha", Text: "hi"},
				{UUID: "u2", Key: "k-beta", Text: "yo"},
				{UUID: "", Key: "", Text: "orphan"}, // ignored
			},
		},
		{
			SessionID: "s2",
			Prompts: []aggregate.PromptTimeline{
				{UUID: "u3", Key: "k-alpha", Text: "hi again"}, // dup of session 1
				{UUID: "u4", Key: "k-gamma", Text: "diff"},
			},
		},
	}
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		SessionTimelines: st,
	}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// Pull the JSON blob.
	const open = `<script id="claudit-data" type="application/json">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("missing claudit-data script tag")
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, `</script>`)
	jsonBlob := rest[:j]
	// Should contain all three unique keys but not duplicate k-alpha.
	for _, want := range []string{`"k-alpha"`, `"k-beta"`, `"k-gamma"`} {
		if !strings.Contains(jsonBlob, want) {
			t.Errorf("prompt_keys missing %s in payload: %s", want, trim400(jsonBlob))
		}
	}
	// k-alpha should appear exactly once in the prompt_keys array.
	if strings.Count(jsonBlob, `"k-alpha"`) != 1 {
		t.Errorf("k-alpha should be de-duplicated; count=%d", strings.Count(jsonBlob, `"k-alpha"`))
	}
	if !strings.Contains(jsonBlob, `"prompt_keys"`) {
		t.Errorf("missing prompt_keys field; payload: %s", trim400(jsonBlob))
	}
}

func trim400(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

// TestHTML_SessionListIsSSR asserts that #session-list now carries
// the server-rendered session-card markup inline, instead of being an
// empty div populated by the JS IIFE.
func TestHTML_SessionListIsSSR(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	st := []aggregate.SessionTimeline{{
		SessionID: "abc-uuid",
		CWD:       "/p/x",
		Prompts: []aggregate.PromptTimeline{
			{UUID: "u1", Key: "k1", Text: "hi"},
		},
	}}
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		SessionTimelines: st,
	}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// The session-list div should now contain at least one session-card.
	idx := strings.Index(body, `id="session-list"`)
	if idx < 0 {
		t.Fatal("missing #session-list element")
	}
	// Look for a session-card inside the div (within a generous window).
	tail := body[idx:]
	cardIdx := strings.Index(tail, `class="session-card"`)
	closeIdx := strings.Index(tail, `</div>`)
	if cardIdx < 0 || (closeIdx >= 0 && cardIdx > closeIdx) {
		t.Errorf("session-list should contain a session-card; window: %s", trim400(tail))
	}
	if !strings.Contains(body, `id="session-abc-uuid"`) {
		t.Errorf("expected SSR session card with id; body excerpt: %s", trim400(tail))
	}
}

// TestHTML_RenderSessionsJSIIFE_Removed: the renderSessions JS IIFE
// is no longer in the template — sessions are server-rendered.
func TestHTML_RenderSessionsJSIIFE_Removed(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// The old IIFE was named renderSessions; if it still appears, the
	// migration left dead code behind.
	if strings.Contains(body, `function renderSessions(`) {
		t.Errorf("renderSessions JS IIFE should be removed from the template")
	}
}

// TestHTML_BuildPromptKeySet_ReadsFromPromptKeys: the
// buildPromptKeySet helper now consumes D.prompt_keys (a flat string
// array), not the legacy nested D.session_timelines walk.
func TestHTML_BuildPromptKeySet_ReadsFromPromptKeys(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, "D.prompt_keys") {
		t.Errorf("buildPromptKeySet should reference D.prompt_keys")
	}
	if strings.Contains(body, "D.session_timelines") {
		t.Errorf("the JS should no longer reference D.session_timelines")
	}
}

// TestHTML_RedactNotice_ShownWhenAnyRedacted: when at least one
// prompt in any session has the "[redacted N chars]" prefix, the
// #session-redact-notice element is server-rendered with the
// explainer text — the JS no longer toggles it.
func TestHTML_RedactNotice_ShownWhenAnyRedacted(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	st := []aggregate.SessionTimeline{{
		SessionID: "s1",
		Prompts: []aggregate.PromptTimeline{
			{UUID: "u1", Text: "[redacted 100 chars]"},
		},
	}}
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		SessionTimelines: st,
	}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, `<p class="small" id="session-redact-notice">Prompts in this report were redacted at generation time (--redact). Costs and tokens are unaffected.</p>`) {
		t.Errorf("expected SSR redact notice; not found in body")
	}
}

// TestHTML_RedactNotice_EmptyWhenNoRedaction: with no redacted
// prompts, the element exists but is empty.
func TestHTML_RedactNotice_EmptyWhenNoRedaction(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	st := []aggregate.SessionTimeline{{
		SessionID: "s1",
		Prompts: []aggregate.PromptTimeline{
			{UUID: "u1", Text: "do the thing"},
		},
	}}
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		SessionTimelines: st,
	}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, `<p class="small" id="session-redact-notice"></p>`) {
		t.Errorf("expected empty redact notice element when no redaction")
	}
}

// TestHTML_SessionEmptyVisibleWhenNoSessions: the #session-empty
// fallback renders without the `hidden` attribute when no sessions
// were supplied. The JS no longer toggles this — it's SSR.
func TestHTML_SessionEmptyVisibleWhenNoSessions(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		// SessionTimelines empty.
	}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// Empty state should NOT have the hidden attribute.
	if !strings.Contains(body, `<div id="session-empty" class="empty-note">No sessions in this window`) {
		t.Errorf("session-empty should be visible (no hidden attr) when no sessions; got body excerpt around tag")
	}
	// And session-list should be empty.
	if !strings.Contains(body, `<div id="session-list" class="session-list"></div>`) {
		t.Errorf("session-list should be empty when no sessions supplied")
	}
}

// TestHTML_TotalsIsSSR asserts that the #totals element is now
// populated by server-rendered markup (a headline + three metric
// blocks), not an empty div the JS would fill in. Also checks the
// totals-building JS IIFE is removed from the template.
func TestHTML_TotalsIsSSR(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// #totals should contain headline markup, not be empty.
	idx := strings.Index(body, `id="totals"`)
	if idx < 0 {
		t.Fatal("missing #totals element")
	}
	// Window from the open tag through the next closing wrap.
	tail := body[idx:]
	headIdx := strings.Index(tail, `<div class="headline">`)
	if headIdx < 0 {
		t.Errorf("#totals should be SSR (contain .headline); body tail: %s", trim400(tail))
	}
	if !strings.Contains(body, `Assistant turns`) {
		t.Errorf("expected SSR 'Assistant turns' label in body")
	}
	// The old JS line `document.getElementById('totals').innerHTML = ` should be gone.
	if strings.Contains(body, `document.getElementById('totals').innerHTML`) {
		t.Errorf("totals JS IIFE should be removed; still found innerHTML assignment")
	}
}

// TestHTML_HotspotsAreSSR: #hotspots paints the empty-state on
// first byte (or, with rows, the card stack) — and the hotspots()
// JS IIFE is removed from the template.
func TestHTML_HotspotsAreSSR(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	idx := strings.Index(body, `id="hotspots"`)
	if idx < 0 {
		t.Fatal("missing #hotspots element")
	}
	// htmlSetup has trivial data so we get the empty-state. The
	// durable check across data shapes is the old IIFE being gone.
	tail := body[idx:]
	if !strings.Contains(tail[:600], `empty-state`) && !strings.Contains(tail[:600], `class="hotspot"`) {
		t.Errorf("#hotspots should be SSR-populated; got: %s", trim400(tail))
	}
	if strings.Contains(body, `(function hotspots()`) {
		t.Errorf("hotspots() JS IIFE should be removed from template")
	}
}

// TestHTML_ProjectAndToolBarsAreSSR: #bars-project carries SSR'd
// .hbar rows (htmlSetup builds one project), and both projectBars()
// and toolBars() JS IIFEs are gone from the template. The
// #bars-tool element may be empty because the fixture has no tool
// calls — the absence of the JS IIFE is the durable check there.
func TestHTML_ProjectAndToolBarsAreSSR(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	idx := strings.Index(body, `id="bars-project"`)
	if idx < 0 {
		t.Fatal("missing #bars-project element")
	}
	window := body[idx : idx+1200]
	if !strings.Contains(window, `class="hbar"`) {
		t.Errorf("#bars-project should contain SSR .hbar row; window: %s", trim400(window))
	}
	for _, jsFn := range []string{`function projectBars()`, `function toolBars()`} {
		if strings.Contains(body, jsFn) {
			t.Errorf("%s should be removed from template", jsFn)
		}
	}
}

// TestHTML_ModelBarsAreSSR asserts the #bars-model element now
// contains server-rendered .hbar rows on first byte, and the
// modelBars() JS IIFE is removed from the template.
func TestHTML_ModelBarsAreSSR(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	idx := strings.Index(body, `id="bars-model"`)
	if idx < 0 {
		t.Fatal("missing #bars-model element")
	}
	tail := body[idx:]
	if !strings.Contains(tail[:600], `class="hbar"`) {
		t.Errorf("#bars-model should contain SSR hbar rows; got: %s", trim400(tail))
	}
	if strings.Contains(body, `function modelBars()`) {
		t.Errorf("modelBars() JS IIFE should be removed from template")
	}
}

// TestHTMLWithOptions_CanceledContextReturnsError ensures a disconnected
// HTTP client can short-circuit the render path before the JSON marshal
// + template execute do real work. We assert ctx.Err() is returned and
// nothing meaningful was written to the buffer.
func TestHTMLWithOptions_CanceledContextReturnsError(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := HTMLWithOptions(ctx, &buf, a, HTMLOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("want empty buffer on cancellation, got %d bytes", buf.Len())
	}
}
