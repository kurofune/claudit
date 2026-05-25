// Tests for Phase 9's static-mode HTML output: a self-contained
// single file that is the SPA shell + inlined CSS + inlined SPA
// bundle + inlined per-section JSON. The /legacy (serve-mode)
// template stays separate and is not under test here.
//
// Convention: tests in this file describe behavior the SPA-shell
// static report must satisfy. Tests for the legacy fat-HTML
// template live in html_test.go and are gated on Serve.Enabled=true.
package render

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/aggregate"
)

// TestStatic_UsesSPAShellStructure: the static report mirrors the
// SPA shell at web/index.html — nav with six tabs, main panel with
// one <section class="view"> per tab. The SPA bundle relies on
// these IDs to mount.
func TestStatic_UsesSPAShellStructure(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	for _, want := range []string{
		`id="view-overview"`,
		`id="view-cost"`,
		`id="view-tokens"`,
		`id="view-sessions"`,
		`id="view-cache"`,
		`id="view-tools"`,
		`id="view-subagents"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("static report missing SPA view container %q", want)
		}
	}
}

// TestStatic_InlinesSPABundle: the SPA's ES modules are embedded as
// text/x-claudit-mod storage tags + a bootstrap that creates blob
// URLs. Without this the page is a dead shell offline.
func TestStatic_InlinesSPABundle(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, `type="text/x-claudit-mod"`) {
		t.Errorf("static report missing module storage tags")
	}
	if !strings.Contains(body, `data-name="app.js"`) {
		t.Errorf("static report missing app.js module")
	}
	if !strings.Contains(body, "createObjectURL") {
		t.Errorf("static report missing SPA bootstrap (createObjectURL)")
	}
}

// TestStatic_InlinesSectionData: the static-mode data island is the
// section-keyed StaticBundle (not the legacy flat BuildPayload
// shape). The SPA's api.js reads from window.__claudit_static_data
// when offline.
func TestStatic_InlinesSectionData(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// The data island lives in a non-executable script tag the
	// bootstrap reads textContent from.
	const open = `<script id="claudit-static-data" type="application/json">`
	if !strings.Contains(body, open) {
		t.Fatalf("static report missing #claudit-static-data island")
	}
	// And the offline-handoff bridge — a tiny inline script that
	// parses the island into a global the SPA's api.js checks.
	if !strings.Contains(body, "window.__claudit_static_data") {
		t.Errorf("static report missing __claudit_static_data bridge")
	}
}

// TestStatic_NoFatHTMLInlineIIFEs: the old report.html.tmpl
// shipped ~2,900 lines of inline JS (modelBars(), projectBars(),
// hotspots(), renderSessions(), the auto-reload + date picker
// IIFE). The static-mode SPA path replaces all of them with the
// bundle, so none should appear.
func TestStatic_NoFatHTMLInlineIIFEs(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// The bans target legacy IIFE source the static path replaced
	// with the SPA bundle. Identifier-style tokens (`function X()`,
	// `D.prompt_keys`) read as fat-HTML artifacts; element-id
	// tokens (`id="claudit-reload-toast"`, `id="date-range-button"`)
	// catch the legacy DOM hooks. CSS class names are intentionally
	// not banned — `.claudit-reload-toast` lives in app.css for the
	// SPA's serve mode and rides along whether the static template
	// renders the element or not.
	for _, banned := range []string{
		"function modelBars()",
		"function projectBars()",
		"function toolBars()",
		"function renderSessions(",
		"D.prompt_keys",
		`id="claudit-reload-toast"`,
		`id="date-range-button"`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("static report leaked fat-HTML artifact %q", banned)
		}
	}
}

// TestStatic_OfflineGuardsPresent: api.js and app.js must carry
// the runtime guards that short-circuit fetch() and skip SSE when
// window.__claudit_static_data is set. The SPA bundle inlines
// both modules verbatim, so the guard tokens land in the static
// HTML if and only if the source modules carry them. Without the
// guards, a downloaded report would hammer fetch() at /_claudit/
// api/* (404 in file:// context) and fail.
//
// We check for substrings the offline branch must contain rather
// than text the bundle inherently inherits (e.g. the literal
// `new EventSource('/events')` that lives inside sse.js's
// online path) — those would always be present and prove nothing.
func TestStatic_OfflineGuardsPresent(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// api.js's offline branch checks the global.
	if !strings.Contains(body, "window.__claudit_static_data") {
		t.Errorf("api.js or app.js offline guard missing from inlined bundle")
	}
	// app.js's SSE bypass shows up as `if (!window.__claudit_static_data)`.
	if !strings.Contains(body, "!window.__claudit_static_data") {
		t.Errorf("app.js SSE skip guard missing — startSSE would still fire offline")
	}
}

// TestStatic_InlinesCSS: the static report is self-contained, so
// tokens.css + app.css must inline into the document <style>.
// Without them the SPA mounts into an unstyled DOM.
func TestStatic_InlinesCSS(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, "--bg:") || !strings.Contains(body, ":root") {
		t.Errorf("static report missing inlined tokens.css")
	}
	// One distinctive selector from app.css.
	if !strings.Contains(body, ".nav-item") {
		t.Errorf("static report missing inlined app.css")
	}
	// And no <link rel=\"stylesheet\" href=\"...{{asset...}}\"> placeholders.
	if strings.Contains(body, `{{asset`) {
		t.Errorf("static report leaked unrendered {{asset}} placeholder")
	}
}

// TestStatic_DataBundleParsesAsJSON: the inlined data island must
// be valid JSON the SPA can JSON.parse() with no preprocessing.
func TestStatic_DataBundleParsesAsJSON(t *testing.T) {
	a := htmlSetup(t)
	st := []aggregate.SessionTimeline{{
		SessionID: "sess-a",
		Prompts: []aggregate.PromptTimeline{
			{UUID: "u1", Key: "k1", Text: "hi"},
		},
	}}
	var buf bytes.Buffer
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{SessionTimelines: st}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	const open = `<script id="claudit-static-data" type="application/json">`
	const closeTag = `</script>`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("missing static-data island")
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, closeTag)
	if j < 0 {
		t.Fatal("missing static-data closing tag")
	}
	jsonBlob := rest[:j]
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonBlob), &m); err != nil {
		t.Fatalf("data island isn't valid JSON: %v\n  first 400 chars: %s", err, trim400(jsonBlob))
	}
	// And the session_timelines map must include our session.
	var sub struct {
		ST map[string]json.RawMessage `json:"session_timelines"`
	}
	if err := json.Unmarshal([]byte(jsonBlob), &sub); err != nil {
		t.Fatalf("session_timelines decode: %v", err)
	}
	if _, ok := sub.ST["sess-a"]; !ok {
		t.Errorf("session_timelines missing sess-a; got keys=%v", mapKeys(sub.ST))
	}
}

// TestStatic_PreservesURLContract: bookmarks of the form #cost,
// #sessions/<id>, etc. continue to work after the cutover. The
// SPA's router parses location.hash and toggles the matching
// view. Asserting structural presence — the actual routing logic
// is covered by the SPA's runtime tests in serve mode.
func TestStatic_PreservesURLContract(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	for _, want := range []string{
		`href="#overview"`,
		`href="#cost"`,
		`href="#tokens"`,
		`href="#sessions"`,
		`href="#cache"`,
		`href="#tools"`,
		`href="#subagents"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("static report missing nav link %q", want)
		}
	}
}

// TestStatic_NoReloadToastInOneShot: the SPA-shell static report
// omits the reload-toast element entirely (no SSE source to push
// generation events offline). The CSS class for the toast still
// rides along inside app.css because the SPA's serve mode uses
// it; the test asserts the *element-id markup* is absent, not the
// CSS class.
func TestStatic_NoReloadToastInOneShot(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if strings.Contains(body, `id="claudit-reload-toast"`) {
		t.Errorf("static report leaked legacy fat-HTML reload toast element")
	}
	if strings.Contains(body, "/_claudit/status") {
		t.Errorf("static report leaked /_claudit/status reference")
	}
}
