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

// TestBuildAssetManifest_UpgradeRotatesDependents is the upgrade
// regression: a release that changes only a dependency (router.js, a
// stylesheet) while index.html and app.js stay byte-identical must still
// rotate the shell's ETag and app.js's hashed URL. Otherwise a returning
// browser gets 304 for the old shell, reuses the immutable app.<hash>.js
// that imports the old router URL, and either runs stale routing or 404s
// once that URL is evicted.
func TestBuildAssetManifest_UpgradeRotatesDependents(t *testing.T) {
	shell := []byte(`<link href="{{asset "app.css"}}"><script type="module" src="{{asset "app.js"}}"></script>`)
	app := []byte(`import { route } from './router.js';
import './util.js';
route();`)
	util := []byte(`import './app.js'; export const u = 1;`)
	build := func(router, css string) *assetManifest {
		t.Helper()
		m, err := buildAssetManifest(fstest.MapFS{
			"web/index.html": &fstest.MapFile{Data: shell},
			"web/app.js":     &fstest.MapFile{Data: app},
			"web/util.js":    &fstest.MapFile{Data: util},
			"web/router.js":  &fstest.MapFile{Data: []byte(router)},
			"web/app.css":    &fstest.MapFile{Data: []byte(css)},
			"web/other.js":   &fstest.MapFile{Data: []byte(`export const o = 1;`)},
		}, "web")
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return m
	}
	before := build(`export function route() { return 'old'; }`, `.a{}`)
	afterRouter := build(`export function route() { return 'new'; }`, `.a{}`)
	afterCSS := build(`export function route() { return 'old'; }`, `.b{}`)

	for _, name := range []string{"index.html", "app.js", "util.js", "router.js"} {
		if before.bySourceName[name].hash == afterRouter.bySourceName[name].hash {
			t.Errorf("router.js changed but %s hash stayed %q", name, before.bySourceName[name].hash)
		}
	}
	if before.bySourceName["index.html"].hash == afterCSS.bySourceName["index.html"].hash {
		t.Errorf("app.css changed but index.html hash stayed %q", before.bySourceName["index.html"].hash)
	}
	if before.bySourceName["app.js"].hash != afterCSS.bySourceName["app.js"].hash {
		t.Errorf("app.js does not depend on app.css, yet its hash rotated")
	}
	if before.bySourceName["other.js"].hash != afterRouter.bySourceName["other.js"].hash {
		t.Errorf("other.js is outside router.js's dependents, yet its hash rotated")
	}

	newRouter := afterRouter.bySourceName["router.js"].hashedName
	if !strings.Contains(string(afterRouter.bySourceName["app.js"].body), "./"+newRouter) {
		t.Errorf("upgraded app.js does not import %s:\n%s", newRouter, afterRouter.bySourceName["app.js"].body)
	}
	newApp := afterRouter.bySourceName["app.js"].hashedName
	if !strings.Contains(string(afterRouter.bySourceName["index.html"].body), "/_claudit/web/"+newApp) {
		t.Errorf("upgraded shell does not reference %s", newApp)
	}
	if _, ok := afterRouter.byHashedName[before.bySourceName["app.js"].hashedName]; ok {
		t.Errorf("upgraded manifest still serves the pre-upgrade app.js URL")
	}
}

// rebuildFixture is a minimal SPA tree for rebuild tests: the shell
// references app.css and app.js, and app.js imports router.js. Each
// argument overrides one file's source so two calls model two builds.
func rebuildFixture(t *testing.T, css, router string) *assetManifest {
	t.Helper()
	m, err := buildAssetManifest(fstest.MapFS{
		"web/index.html": &fstest.MapFile{Data: []byte(`<link rel="stylesheet" href="{{asset "app.css"}}"><script type="module" src="{{asset "app.js"}}"></script>{{version}}`)},
		"web/app.js":     &fstest.MapFile{Data: []byte(`import { route } from './router.js'; route();`)},
		"web/router.js":  &fstest.MapFile{Data: []byte(router)},
		"web/app.css":    &fstest.MapFile{Data: []byte(css)},
	}, "web")
	if err != nil {
		t.Fatalf("buildAssetManifest: %v", err)
	}
	return m
}

const (
	fixtureCSS    = `.a { color: red; }`
	fixtureRouter = `export function route() { return 'old'; }`
)

func TestBuildAssetManifest_StylesheetOnlyChangeRotatesShellETag(t *testing.T) {
	before := rebuildFixture(t, fixtureCSS, fixtureRouter)
	after := rebuildFixture(t, `.a { color: blue; }`, fixtureRouter)

	if b, a := before.bySourceName["index.html"].etag, after.bySourceName["index.html"].etag; b == a {
		t.Errorf("app.css changed but the shell ETag stayed %s", b)
	}
	newCSS := after.bySourceName["app.css"].hashedName
	if !strings.Contains(string(after.bySourceName["index.html"].body), "/_claudit/web/"+newCSS) {
		t.Errorf("rebuilt shell does not name %s:\n%s", newCSS, after.bySourceName["index.html"].body)
	}
}

func TestBuildAssetManifest_ImportedModuleChangeRotatesImporterAndShell(t *testing.T) {
	before := rebuildFixture(t, fixtureCSS, fixtureRouter)
	after := rebuildFixture(t, fixtureCSS, `export function route() { return 'new'; }`)

	if b, a := before.bySourceName["app.js"].hashedName, after.bySourceName["app.js"].hashedName; b == a {
		t.Errorf("router.js changed but app.js (source unchanged) kept URL %s", b)
	}
	if b, a := before.bySourceName["index.html"].etag, after.bySourceName["index.html"].etag; b == a {
		t.Errorf("router.js changed but the shell ETag stayed %s", b)
	}
}

func TestBuildAssetManifest_IdenticalBuildsKeepEveryURLAndShellETag(t *testing.T) {
	first := rebuildFixture(t, fixtureCSS, fixtureRouter)
	second := rebuildFixture(t, fixtureCSS, fixtureRouter)

	if len(first.bySourceName) != len(second.bySourceName) {
		t.Fatalf("entry count differs: %d vs %d", len(first.bySourceName), len(second.bySourceName))
	}
	for name, e := range first.bySourceName {
		if got := second.bySourceName[name].hashedName; got != e.hashedName {
			t.Errorf("%s: hashed URL %s rotated to %s on a no-op rebuild", name, e.hashedName, got)
		}
	}
	if b, a := first.bySourceName["index.html"].etag, second.bySourceName["index.html"].etag; b != a {
		t.Errorf("shell ETag rotated on a no-op rebuild: %s vs %s", b, a)
	}
}

func TestSubstituteVersionPlaceholder_RotatesShellETag(t *testing.T) {
	v1 := rebuildFixture(t, fixtureCSS, fixtureRouter)
	v2 := rebuildFixture(t, fixtureCSS, fixtureRouter)
	substituteVersionPlaceholder(v1, "v1.0.0")
	substituteVersionPlaceholder(v2, "v1.0.1")

	if b, a := v1.bySourceName["index.html"].etag, v2.bySourceName["index.html"].etag; b == a {
		t.Errorf("version label changed but the shell ETag stayed %s", b)
	}
}
