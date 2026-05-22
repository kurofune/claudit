package serve

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestBuildAssetManifest_HashesAndRewritesImports is the Phase-5 contract
// test for the SPA assets pipeline. Given a tiny in-memory FS, the
// manifest must:
//  1. Compute a stable hash for each asset (independent of contents'
//     order on disk).
//  2. Rewrite `from './sibling.js'` imports in .js files to point at the
//     hashed sibling filename, so the browser can cache each asset
//     forever (the hash changes when the content changes).
//  3. Rewrite `{{asset "name.ext"}}` references in .html files the same
//     way — the SPA shell uses this to point <script src> at the hashed
//     app.js URL.
//
// Failure modes this guards against: an import that didn't get rewritten
// (browser fetches the un-hashed URL → 404 because we only serve hashed
// URLs), or a hash that changes spuriously when only the file's mtime
// shifts (would defeat the immutable cache).
func TestBuildAssetManifest_HashesAndRewritesImports(t *testing.T) {
	fs := fstest.MapFS{
		"web/index.html": &fstest.MapFile{
			Data: []byte(`<!doctype html>
<link rel="stylesheet" href="{{asset "tokens.css"}}">
<script type="module" src="{{asset "app.js"}}"></script>`),
		},
		"web/app.js": &fstest.MapFile{
			Data: []byte(`import { fetchOverview } from './api.js';
fetchOverview();`),
		},
		"web/api.js": &fstest.MapFile{
			Data: []byte(`export function fetchOverview() { return fetch('/_claudit/api/overview'); }`),
		},
		"web/tokens.css": &fstest.MapFile{
			Data: []byte(`:root { --x: 0; }`),
		},
	}

	m, err := buildAssetManifest(fs, "web")
	if err != nil {
		t.Fatalf("buildAssetManifest: %v", err)
	}

	// Every original file must be present under its source name.
	for _, name := range []string{"index.html", "app.js", "api.js", "tokens.css"} {
		if _, ok := m.bySourceName[name]; !ok {
			t.Errorf("manifest missing entry for %q", name)
		}
	}

	// Hashes must be deterministic + non-empty + 8 chars.
	for name, e := range m.bySourceName {
		if len(e.hash) != 8 {
			t.Errorf("asset %q: hash len = %d, want 8", name, len(e.hash))
		}
	}

	// app.js must have its `./api.js` import rewritten to the hashed URL.
	appEntry := m.bySourceName["app.js"]
	apiEntry := m.bySourceName["api.js"]
	wantImport := "./api." + apiEntry.hash + ".js"
	if !strings.Contains(string(appEntry.body), wantImport) {
		t.Errorf("app.js body did not get import rewritten\n--- body ---\n%s\n--- want substring ---\n%s",
			string(appEntry.body), wantImport)
	}
	// And the un-rewritten path must be gone.
	if strings.Contains(string(appEntry.body), "'./api.js'") || strings.Contains(string(appEntry.body), `"./api.js"`) {
		t.Errorf("app.js still contains un-rewritten ./api.js import:\n%s", string(appEntry.body))
	}

	// index.html must have both {{asset ...}} placeholders rewritten.
	htmlEntry := m.bySourceName["index.html"]
	htmlBody := string(htmlEntry.body)
	tokensEntry := m.bySourceName["tokens.css"]
	if !strings.Contains(htmlBody, "/_claudit/web/tokens."+tokensEntry.hash+".css") {
		t.Errorf("index.html missing rewritten tokens.css URL:\n%s", htmlBody)
	}
	if !strings.Contains(htmlBody, "/_claudit/web/app."+appEntry.hash+".js") {
		t.Errorf("index.html missing rewritten app.js URL:\n%s", htmlBody)
	}
	if strings.Contains(htmlBody, "{{asset") {
		t.Errorf("index.html still contains unresolved {{asset ...}} placeholder:\n%s", htmlBody)
	}

	// byHashedName must let us look up assets by their served URL component.
	hashedAppName := "app." + appEntry.hash + ".js"
	if got, ok := m.byHashedName[hashedAppName]; !ok || got != appEntry {
		t.Errorf("byHashedName[%q] lookup mismatch (ok=%v)", hashedAppName, ok)
	}
}

// TestBuildAssetManifest_HashStable verifies the same input produces
// the same hash on repeated calls — guards against accidentally folding
// time, randomness, or map iteration order into the hash.
func TestBuildAssetManifest_HashStable(t *testing.T) {
	makeFS := func() fstest.MapFS {
		return fstest.MapFS{
			"web/app.js": &fstest.MapFile{Data: []byte(`console.log('x');`)},
		}
	}
	m1, err := buildAssetManifest(makeFS(), "web")
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	m2, err := buildAssetManifest(makeFS(), "web")
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if m1.bySourceName["app.js"].hash != m2.bySourceName["app.js"].hash {
		t.Errorf("hash differs between builds: %q vs %q",
			m1.bySourceName["app.js"].hash, m2.bySourceName["app.js"].hash)
	}
}

// TestBuildAssetManifest_HashChangesWithContent verifies that editing a
// file's contents changes its hash — the immutable-cache contract relies
// on this. If two contents produced the same hash, the browser would
// cache the old bytes forever after an update.
func TestBuildAssetManifest_HashChangesWithContent(t *testing.T) {
	fs1 := fstest.MapFS{
		"web/app.js": &fstest.MapFile{Data: []byte(`console.log('one');`)},
	}
	fs2 := fstest.MapFS{
		"web/app.js": &fstest.MapFile{Data: []byte(`console.log('two');`)},
	}
	m1, err := buildAssetManifest(fs1, "web")
	if err != nil {
		t.Fatalf("m1: %v", err)
	}
	m2, err := buildAssetManifest(fs2, "web")
	if err != nil {
		t.Fatalf("m2: %v", err)
	}
	if m1.bySourceName["app.js"].hash == m2.bySourceName["app.js"].hash {
		t.Errorf("hash unchanged across different contents: %q", m1.bySourceName["app.js"].hash)
	}
}
