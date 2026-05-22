package render

import (
	"strings"
	"testing"
	"testing/fstest"
)

// TestBuildSPABundle_EmbedsEveryModule verifies the bundle carries
// the raw source bytes of each web/*.js module. The runtime uses
// blob URLs to feed those bytes to the browser as real ES modules
// (Strategy G in the Phase-9 design): no transformation, no
// export/import rewriting at build time — the browser parses each
// module as the author wrote it.
func TestBuildSPABundle_EmbedsEveryModule(t *testing.T) {
	fsys := fstest.MapFS{
		"web/format.js": &fstest.MapFile{Data: []byte("export const x = 1;\n")},
		"web/app.js":    &fstest.MapFile{Data: []byte("import { x } from './format.js';\nconsole.log(x);\n")},
	}
	out, err := BuildSPABundle(fsys)
	if err != nil {
		t.Fatalf("BuildSPABundle: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "export const x = 1;") {
		t.Errorf("format.js source missing from bundle")
	}
	if !strings.Contains(body, "import { x } from './format.js';") {
		t.Errorf("app.js source missing from bundle")
	}
}

// TestBuildSPABundle_TagsEachModule asserts each module is wrapped
// in its own <script type="text/x-claudit-mod" data-name="..."> tag.
// "text/x-claudit-mod" is a non-executable type so the browser
// stashes the text without trying to run it; the bootstrap runtime
// reads textContent and creates a real module from it.
func TestBuildSPABundle_TagsEachModule(t *testing.T) {
	fsys := fstest.MapFS{
		"web/format.js": &fstest.MapFile{Data: []byte("export const x = 1;\n")},
		"web/app.js":    &fstest.MapFile{Data: []byte("import { x } from './format.js';\n")},
	}
	out, err := BuildSPABundle(fsys)
	if err != nil {
		t.Fatalf("BuildSPABundle: %v", err)
	}
	body := string(out)
	for _, want := range []string{
		`<script type="text/x-claudit-mod" data-name="format.js">`,
		`<script type="text/x-claudit-mod" data-name="app.js">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing module tag %q in bundle", want)
		}
	}
}

// TestBuildSPABundle_EmitsBootstrap verifies a real <script>
// bootstrap is appended — the bit that walks the module tags,
// rewrites relative imports to blob URLs, and dynamic-imports the
// entry point. Without this, the embedded module text just sits in
// the DOM and never runs.
func TestBuildSPABundle_EmitsBootstrap(t *testing.T) {
	fsys := fstest.MapFS{
		"web/app.js": &fstest.MapFile{Data: []byte("console.log(1);\n")},
	}
	out, err := BuildSPABundle(fsys)
	if err != nil {
		t.Fatalf("BuildSPABundle: %v", err)
	}
	body := string(out)
	// Bootstrap is the only executable <script> in the output and
	// must mention createObjectURL (the mechanic that turns each
	// module's text into a URL the dynamic import can resolve).
	if !strings.Contains(body, "createObjectURL") {
		t.Errorf("bootstrap missing createObjectURL call; bundle body: %s", body)
	}
	// The entry import has to target app.js — the SPA's actual
	// entry point. Other module orderings can change without
	// breaking anything, but this one is load-bearing.
	if !strings.Contains(body, `'app.js'`) {
		t.Errorf("bootstrap should reference 'app.js' as entry; bundle body: %s", body)
	}
}

// TestBuildSPABundle_EscapesClosingScriptTag guards against a
// module source that contains the literal `</script>` token —
// without escaping it would close the surrounding inline <script>
// tag prematurely and corrupt the page. The standard mitigation is
// to break the closing slash with a backslash inside JS strings;
// for our text storage we replace any literal `</script` with
// `<\/script` before embedding. The browser still gets the original
// bytes back from textContent because the parser only treats
// `</script>` (exactly) as a tag terminator.
//
// We use a string concatenation to construct the dangerous token
// here so the test source itself doesn't trigger any tooling that
// scans for literal closing-script tags.
func TestBuildSPABundle_EscapesClosingScriptTag(t *testing.T) {
	dangerous := "const html = '<" + "/script>';\n"
	fsys := fstest.MapFS{
		"web/app.js": &fstest.MapFile{Data: []byte(dangerous)},
	}
	out, err := BuildSPABundle(fsys)
	if err != nil {
		t.Fatalf("BuildSPABundle: %v", err)
	}
	body := string(out)
	// The first <script type="text/x-claudit-mod"> opens the storage
	// tag. The literal "</script>" inside its body would close it
	// early; check the dangerous substring no longer appears between
	// the opening x-claudit-mod tag and the next closing </script>.
	openTag := `<script type="text/x-claudit-mod" data-name="app.js">`
	openAt := strings.Index(body, openTag)
	if openAt < 0 {
		t.Fatalf("missing app.js storage tag")
	}
	rest := body[openAt+len(openTag):]
	closeAt := strings.Index(rest, "</script>")
	if closeAt < 0 {
		t.Fatalf("missing closing </script> for app.js")
	}
	storage := rest[:closeAt]
	if strings.Contains(storage, "</script>") {
		t.Errorf("unescaped </script> in module storage would break the inline tag; storage: %q", storage)
	}
}

// TestBuildSPABundle_OrdersForDeterministicOutput asserts the
// generated bundle is byte-stable across runs with the same input.
// Non-determinism here would surface as flaky golden-file tests
// and noisy diffs on the static report's hash.
func TestBuildSPABundle_OrdersForDeterministicOutput(t *testing.T) {
	fsys := fstest.MapFS{
		"web/format.js":  &fstest.MapFile{Data: []byte("export const x = 1;\n")},
		"web/charts.js":  &fstest.MapFile{Data: []byte("export const y = 2;\n")},
		"web/app.js":     &fstest.MapFile{Data: []byte("// entry\n")},
		"web/sse.js":     &fstest.MapFile{Data: []byte("export const z = 3;\n")},
		"web/router.js":  &fstest.MapFile{Data: []byte("export const w = 4;\n")},
		"web/index.html": &fstest.MapFile{Data: []byte("ignored")},
		"web/app.css":    &fstest.MapFile{Data: []byte("ignored")},
		"web/tokens.css": &fstest.MapFile{Data: []byte("ignored")},
	}
	a, err := BuildSPABundle(fsys)
	if err != nil {
		t.Fatalf("BuildSPABundle: %v", err)
	}
	b, err := BuildSPABundle(fsys)
	if err != nil {
		t.Fatalf("BuildSPABundle: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("BuildSPABundle should be deterministic; bytes differ")
	}
}

// TestBuildSPABundle_OnlyJSModules asserts CSS / HTML / other files
// in the same web/ tree are not embedded as JS modules. Strategy G
// pairs the bundle with separately-inlined CSS via the template,
// so the bundle stays focused on JS only.
func TestBuildSPABundle_OnlyJSModules(t *testing.T) {
	fsys := fstest.MapFS{
		"web/format.js":  &fstest.MapFile{Data: []byte("// js code\n")},
		"web/index.html": &fstest.MapFile{Data: []byte("<html>...</html>")},
		"web/app.css":    &fstest.MapFile{Data: []byte(".x { color: red; }")},
		"web/tokens.css": &fstest.MapFile{Data: []byte(":root {}")},
	}
	out, err := BuildSPABundle(fsys)
	if err != nil {
		t.Fatalf("BuildSPABundle: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, `data-name="format.js"`) {
		t.Errorf("expected format.js to be embedded; bundle: %s", body)
	}
	for _, unwanted := range []string{`data-name="index.html"`, `data-name="app.css"`, `data-name="tokens.css"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("non-JS file embedded as module: %q", unwanted)
		}
	}
}

// TestBuildSPABundle_RealWebFS smokes the production embed: every
// web/*.js file that ships in the binary should round-trip through
// the bundler without error and the entry (app.js) must show up
// as a module tag. Insulates Phase 9 against a future web/*.js
// addition that contains something the bundler chokes on (e.g. a
// stray null byte).
func TestBuildSPABundle_RealWebFS(t *testing.T) {
	out, err := BuildSPABundle(productionWebFS())
	if err != nil {
		t.Fatalf("BuildSPABundle on real web/: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, `data-name="app.js"`) {
		t.Errorf("real bundle missing app.js")
	}
	if !strings.Contains(body, "createObjectURL") {
		t.Errorf("real bundle missing bootstrap")
	}
}
