package render

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildPayload_MatchesInlineDataJSON locks the invariant that the
// bytes the JSON endpoint will serve are the same bytes the legacy
// (fat-HTML) template inlines via <script id="claudit-data">. Phase 9
// split the static one-shot path off to a separate SPA-shell template
// with its own section-keyed bundle (covered by TestBuildStaticBundle_*),
// so this test now targets the /legacy serve-mode render explicitly.
func TestBuildPayload_MatchesInlineDataJSON(t *testing.T) {
	a := htmlSetup(t)
	want, err := BuildPayload(context.Background(), a, HTMLOptions{})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var buf bytes.Buffer
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{Enabled: true},
	}); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	body := buf.String()
	const open = `<script id="claudit-data" type="application/json">`
	const closeTag = `</script>`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("inline data script tag not found")
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, closeTag)
	if j < 0 {
		t.Fatal("inline data script closing tag not found")
	}
	got := rest[:j]
	if got != string(want) {
		t.Errorf("inline JSON differs from BuildPayload output\n inline (first 200): %s\n payload (first 200): %s",
			truncFor(got, 200), truncFor(string(want), 200))
	}
}

func truncFor(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// TestHTML_ServeModeDeferDataOmitsInlineJSON asserts that when
// ServeOptions.DeferData is true, the rendered HTML omits the
// <script id="claudit-data" type="application/json"> blob entirely —
// the page will fetch /_claudit/data.json instead and the inline copy
// would just be wasted bytes.
func TestHTML_ServeModeDeferDataOmitsInlineJSON(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{
			Enabled:    true,
			DeferData:  true,
			DataPath:   "/_claudit/data.json",
			StatusPath: "/_claudit/status",
		},
	})
	if err != nil {
		t.Fatalf("HTMLWithOptions: %v", err)
	}
	body := buf.String()
	if strings.Contains(body, `<script id="claudit-data" type="application/json">`) {
		t.Errorf("serve-mode HTML should NOT inline the data script tag (DeferData=true)")
	}
}

// TestHTML_ServeModeEmitsFetchPreamble asserts that the deferred-data
// path installs a fetch promise on window.__claudit_data targeting the
// configured DataPath. The main consumer script awaits this promise.
func TestHTML_ServeModeEmitsFetchPreamble(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{
			Enabled:   true,
			DeferData: true,
			DataPath:  "/_claudit/data.json",
		},
	})
	if err != nil {
		t.Fatalf("HTMLWithOptions: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "window.__claudit_data") {
		t.Errorf("serve-mode HTML missing window.__claudit_data preamble")
	}
	// html/template escapes "/" as "\/" inside a JS string literal,
	// matching the StatusPath assertion style used elsewhere.
	if !strings.Contains(body, `"\/_claudit\/data.json"`) {
		t.Errorf("serve-mode HTML missing JS-escaped DataPath fetch URL; first 1000 of body:\n%s", truncFor(body, 1000))
	}
	if !strings.Contains(body, "fetch(") {
		t.Errorf("serve-mode HTML missing fetch(...) call for data preamble")
	}
}

// TestHTML_ServeModeFetchPreambleHonorsCustomDataPath asserts the
// preamble uses the caller-provided path verbatim, not the default,
// so the integration is decoupled from the URL the serve daemon
// chooses to expose.
func TestHTML_ServeModeFetchPreambleHonorsCustomDataPath(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{
			Enabled:   true,
			DeferData: true,
			DataPath:  "/custom/data.json",
		},
	})
	if err != nil {
		t.Fatalf("HTMLWithOptions: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `"\/custom\/data.json"`) {
		t.Errorf("custom data path not interpolated (JS-escaped form not found)")
	}
}

// TestHTML_ServeModeFetchPreambleForwardsLocationSearch asserts the
// fetch URL appends window.location.search so URL query params on the
// page (e.g. ?since=2026-03-02) reach the data endpoint. Without this,
// the data handler parses an empty query, applyDefaults runs with no
// URL inputs, and the canonical-query cache key diverges from what
// handleReport stored — turning the JSON fetch into a cache miss that
// re-aggregates from scratch (~1.9s on real corpora). The unfiltered
// case is unchanged because location.search is "" when no params exist.
func TestHTML_ServeModeFetchPreambleForwardsLocationSearch(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{
			Enabled:   true,
			DeferData: true,
			DataPath:  "/_claudit/data.json",
		},
	})
	if err != nil {
		t.Fatalf("HTMLWithOptions: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "+ window.location.search") {
		t.Errorf("fetch URL must append window.location.search so filters propagate to the data endpoint; first 1000 of body:\n%s", truncFor(body, 1000))
	}
}

// TestHTML_ConsumerScriptAwaitsDataPromise asserts the main consumer
// IIFE is wrapped as an async function awaiting window.__claudit_data.
// This is the contract that lets both the inline-resolve and the
// fetch paths converge on the same downstream code.
// Legacy template path (Phase 9 note).
func TestHTML_ConsumerScriptAwaitsDataPromise(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{Enabled: true},
	}); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "(async function()") {
		t.Errorf("main consumer should be wrapped in (async function() ...) IIFE")
	}
	if !strings.Contains(body, "await window.__claudit_data") {
		t.Errorf("main consumer should `await window.__claudit_data` to obtain D")
	}
}

// TestHTML_ServeModeFetchPreambleHasCatch asserts the fetch promise
// has a .catch() handler that surfaces failures to the console. Without
// it, a 5xx from /_claudit/data.json would surface as an unhandled
// promise rejection and the page would just sit broken — no clue why.
func TestHTML_ServeModeFetchPreambleHasCatch(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{
			Enabled:   true,
			DeferData: true,
			DataPath:  "/_claudit/data.json",
		},
	})
	if err != nil {
		t.Fatalf("HTMLWithOptions: %v", err)
	}
	body := buf.String()
	// Look for a catch() call hung off the window.__claudit_data
	// promise. Tolerate whitespace and intermediate then() chains.
	if !strings.Contains(body, "__claudit_data.catch(") &&
		!strings.Contains(body, "__claudit_data\n  .catch(") {
		// Fall back to a relaxed structural check: ".catch(" must
		// appear after window.__claudit_data is established.
		idx := strings.Index(body, "window.__claudit_data")
		if idx < 0 || !strings.Contains(body[idx:], ".catch(") {
			t.Errorf("serve-mode fetch preamble missing .catch() failure handler")
		}
	}
}

// TestLegacyHTML_InlinesDataJSON guards the legacy fat-HTML render
// path (used at /legacy in serve mode and by any caller passing
// Serve.Enabled=true without DeferData): the JSON island must be
// inline. Phase 9 moved the one-shot static path to a separate
// SPA-shell template (see TestStatic_InlinesSectionData), so this
// test is now scoped to the legacy template only.
func TestLegacyHTML_InlinesDataJSON(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		Serve: ServeOptions{Enabled: true},
	}); err != nil {
		t.Fatalf("HTML: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `<script id="claudit-data" type="application/json">`) {
		t.Errorf("legacy HTML missing inline claudit-data script tag")
	}
	// The inline path also installs window.__claudit_data as a resolved
	// promise so the main consumer can `await` it uniformly.
	if !strings.Contains(body, "window.__claudit_data") {
		t.Errorf("legacy HTML missing window.__claudit_data preamble")
	}
}

// TestBuildPayload_ReturnsValidJSONWithExpectedKeys is the entry test for
// the data-payload extraction work. BuildPayload is the new exported
// function that returns the JSON bytes currently inlined inside
// HTMLWithOptions's <script id="claudit-data"> tag.
func TestBuildPayload_ReturnsValidJSONWithExpectedKeys(t *testing.T) {
	a := htmlSetup(t)
	got, err := BuildPayload(context.Background(), a, HTMLOptions{})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("BuildPayload returned empty bytes")
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("payload not valid JSON: %v; first 200 chars: %s", err, string(got[:min(200, len(got))]))
	}

	wantKeys := []string{
		"totals", "by_model", "by_project", "by_tool",
		"by_tool_detail", "by_skill", "main", "sidechain", "by_subagent",
		"agent_invocations", "unknown_models", "period",
		"trend_totals", "trend_by_model", "trend_by_project",
		"trend_by_tool", "trend_by_session", "trend_by_subagent",
		"overall_hit_ratio", "cache_by_project", "cache_by_session",
		"cache_by_subagent", "cache_by_invocation", "by_prompt",
		"anomalies", "prompt_keys", "forecast",
	}
	for _, k := range wantKeys {
		if _, ok := obj[k]; !ok {
			t.Errorf("payload missing top-level key %q", k)
		}
	}
}

// Hotspots are server-rendered into the #hotspots div by
// renderHotspotsHTML. No JS code reads D.hotspots, so the field
// shouldn't bloat the deferred-fetch JSON. Guard against accidental
// reintroduction.
func TestBuildPayload_OmitsHotspots(t *testing.T) {
	a := htmlSetup(t)
	got, err := BuildPayload(context.Background(), a, HTMLOptions{})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if _, ok := obj["hotspots"]; ok {
		t.Errorf("payload still contains \"hotspots\" key — should be SSR-only now")
	}
}
