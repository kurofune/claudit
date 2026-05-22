package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestApp_ServesSPAShell verifies the /app route returns the rewritten
// SPA shell. The shell must:
//  1. carry a 200 OK status and HTML content type,
//  2. reference the hashed app.js URL — proof that the asset
//     placeholder was resolved at startup, not at request time,
//  3. NOT contain raw {{asset ...}} placeholders.
//
// Phase 5 of the serve-API plan: this is the user-visible /app entry
// point that runs alongside the legacy / until Phase 8 cuts over.
func TestApp_ServesSPAShell(t *testing.T) {
	s := fixtureServer(t)

	req := httptest.NewRequest(http.MethodGet, "/app", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html...", ct)
	}
	body := rec.Body.String()
	if strings.Contains(body, "{{asset") {
		t.Errorf("response still contains unresolved {{asset ...}} placeholder:\n%s", body)
	}
	if !strings.Contains(body, "/_claudit/web/app.") || !strings.Contains(body, ".js") {
		t.Errorf("response missing hashed /_claudit/web/app.<hash>.js script tag:\n%s", body[:min(500, len(body))])
	}
}

// TestRoot_ServesSPAShell is the Phase 8 cutover assertion: GET / now
// returns the same SPA shell that /app serves. The shell carries a
// hashed app.<hash>.js script tag and no raw {{asset}} placeholders.
// Before Phase 8 this URL served the fat HTML; that body has moved to
// /legacy (see TestLegacy_ServesFatHTML).
func TestRoot_ServesSPAShell(t *testing.T) {
	s := fixtureServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/ status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html...", ct)
	}
	body := rec.Body.String()
	if strings.Contains(body, "{{asset") {
		t.Errorf("/ still contains unresolved {{asset ...}} placeholder:\n%s", body)
	}
	if !strings.Contains(body, "/_claudit/web/app.") || !strings.Contains(body, ".js") {
		t.Errorf("/ missing hashed /_claudit/web/app.<hash>.js script tag:\n%s", body[:min(500, len(body))])
	}
	// Fat-HTML's hallmark is the SSR'd #totals block. The SPA shell
	// has no such element pre-rendered, so its absence proves we hit
	// the shell handler and not the legacy renderer.
	if strings.Contains(body, `id="totals"`) {
		t.Errorf("/ still serves legacy fat HTML (#totals found in body)")
	}
}

// TestLegacy_ServesFatHTML asserts the fat HTML is reachable at /legacy
// — the one-minor-release escape hatch the Phase 8 cutover provides. A
// caller (a bookmark, an external script) that still wants the
// self-contained fat report can hit /legacy until the deprecation
// window closes in Phase 10.
func TestLegacy_ServesFatHTML(t *testing.T) {
	s := fixtureServer(t)
	req := httptest.NewRequest(http.MethodGet, "/legacy", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/legacy status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="totals"`) {
		t.Errorf("/legacy not serving fat HTML (missing #totals):\n%s", body[:min(800, len(body))])
	}
	if !strings.Contains(body, "claudit-reload-toast") {
		t.Errorf("/legacy missing serve-mode auto-reload toast")
	}
}

// TestWebAsset_ServesHashedJSWithImmutableCache exercises one
// /_claudit/web/<name>.<hash>.<ext> request end-to-end:
//  1. /app returns the shell with a hashed script src,
//  2. fetching that hashed URL returns the rewritten JS body,
//  3. headers carry the public/immutable/long-max-age tuple that the
//     plan calls out as the win for content-hashed naming.
func TestWebAsset_ServesHashedJSWithImmutableCache(t *testing.T) {
	s := fixtureServer(t)

	// Fetch /app to find the hashed URL the browser would request.
	shellReq := httptest.NewRequest(http.MethodGet, "/app", nil)
	shellRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(shellRec, shellReq)
	if shellRec.Code != http.StatusOK {
		t.Fatalf("/app status = %d", shellRec.Code)
	}
	hashedURL := extractHashedAssetURL(shellRec.Body.String(), "app.", ".js")
	if hashedURL == "" {
		t.Fatalf("could not locate hashed app.<hash>.js in shell:\n%s", shellRec.Body.String())
	}

	// Now fetch the hashed URL and validate.
	req := httptest.NewRequest(http.MethodGet, hashedURL, nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body=%s", hashedURL, rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("Content-Type = %q, want application/javascript...", ct)
	}
	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=") {
		t.Errorf("Cache-Control = %q, want immutable + max-age", cc)
	}
	// app.js imports ./view-overview.js — after rewrite, the body
	// should reference the hashed sibling. This guards against the
	// startup pipeline silently shipping un-rewritten imports.
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "./view-overview.") {
		t.Errorf("served app.js missing rewritten ./view-overview.<hash>.js import:\n%s", string(body))
	}
}

// TestWebAsset_404OnBadHash protects against a request for the
// un-hashed source name (which a stale cached HTML might still hold).
// Returning 404 forces clients to fetch the shell again and pick up
// the new hashed URL.
func TestWebAsset_404OnBadHash(t *testing.T) {
	s := fixtureServer(t)
	req := httptest.NewRequest(http.MethodGet, "/_claudit/web/app.js", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /_claudit/web/app.js (unhashed) status = %d, want 404", rec.Code)
	}
}

// extractHashedAssetURL pulls the first occurrence of
// /_claudit/web/<prefix><8 hex chars><suffix> from an HTML body.
// Used only in tests to follow the shell-to-asset chain without
// hardcoding the hash (which changes whenever the source file's
// bytes change). The 8-hex constraint matches the manifest's hash
// width and disambiguates "app." → app.js vs the app.css that lives
// next to it in the shell.
func extractHashedAssetURL(body, prefix, suffix string) string {
	re := regexp.MustCompile(`/_claudit/web/` + regexp.QuoteMeta(prefix) + `[0-9a-f]{8}` + regexp.QuoteMeta(suffix))
	return re.FindString(body)
}
