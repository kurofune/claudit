package render

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	webassets "github.com/kurofune/claudit"
)

// BuildSPABundle returns the HTML fragment that inlines the SPA's
// vanilla ES module source (web/*.js) into a self-contained static
// report. The fragment is two parts back-to-back:
//
//  1. One <script type="text/x-claudit-mod" data-name="X.js">…</script>
//     per JS module. The mime type is intentionally non-executable so
//     the browser stashes the raw source in textContent without
//     trying to run it as a script.
//  2. One executable <script> bootstrap that walks every mod-tag,
//     rewrites relative imports (./foo.js) to per-module blob: URLs,
//     and dynamic-imports the entry module (app.js). The browser
//     handles real ES module loading from blob URLs, so name
//     collisions (paint/reset across view-*.js) and the import graph
//     work exactly as they do in serve mode — no build-step rewriter.
//
// Strategy G in the Phase-9 design doc: the browser does the heavy
// lifting, so the Go side only embeds source bytes and rewrites the
// literal `</script>` substring (the one thing that would prematurely
// close the storage tag and corrupt the page).
//
// Returns an error only if a file read from fsys fails. CSS, HTML, and
// other non-.js siblings are skipped — they're inlined separately by
// the static report template.
func BuildSPABundle(fsys fs.FS) ([]byte, error) {
	names, sources, err := collectJSModules(fsys)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for _, name := range names {
		// Closing-script-tag escaping. Only the literal token
		// "</script>" ends a script element per HTML5; partial
		// matches do not. Browsers reconstruct the original text
		// from `<\/script>` when they read textContent back (the
		// backslash is a no-op outside a JS string literal, and
		// inside one it's still a no-op escape for /). Without
		// this, a JS string like '</script>' embedded in module
		// source would close the storage tag mid-module.
		safe := strings.ReplaceAll(sources[name], "</script>", `<\/script>`)
		fmt.Fprintf(&buf, `<script type="text/x-claudit-mod" data-name=%q>`, name)
		buf.WriteString(safe)
		buf.WriteString("</script>\n")
	}
	buf.WriteString(bootstrapScript)
	return buf.Bytes(), nil
}

// collectJSModules walks fsys looking for web/*.js entries and
// returns them sorted by name (so the emitted bundle is byte-stable
// across runs). Source bytes are returned in the second map.
func collectJSModules(fsys fs.FS) (names []string, sources map[string]string, err error) {
	if fsys == nil {
		return nil, nil, fmt.Errorf("BuildSPABundle: nil fs")
	}
	sources = map[string]string{}
	walkErr := fs.WalkDir(fsys, "web", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(path.Ext(p)) != ".js" {
			return nil
		}
		name := strings.TrimPrefix(p, "web/")
		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return rerr
		}
		sources[name] = string(data)
		names = append(names, name)
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	sort.Strings(names)
	return names, sources, nil
}

// productionWebFS returns the embed shipped with the binary. Kept
// separate from BuildSPABundle's signature so tests can pass a
// fstest.MapFS without dragging in the embed.
func productionWebFS() fs.FS {
	return webassets.WebFS
}

// bootstrapScript drives the runtime that turns the embedded
// module-text tags into a running SPA. It runs once on page load and:
//
//  1. Collects every <script type="text/x-claudit-mod"> tag's
//     textContent into a name→source map.
//  2. Parses each source's `import ... from './X.js'` statements to
//     build a dependency graph. ASCII order alone would mis-order
//     the web/ tree (app.js is alphabetically before its
//     dependencies router.js, sse.js, view-*.js) — topological
//     sort is the only correctness-preserving order.
//  3. Walks modules in topological order; for each one, rewrites
//     its relative imports to point at the blob: URLs created for
//     dependencies, then creates a blob URL from the rewritten
//     source. Dependencies are always processed first so the URLs
//     are known by the time a dependent rewrite runs.
//  4. Dynamic-imports app.js's final blob URL.
//
// Browsers handle the actual ES module parsing — including export/
// import scoping and live-binding semantics — so name collisions
// like `paint` across view-*.js modules work without rewriting.
//
// Defensive guards:
//   - the bootstrap is idempotent: if it has already run, a second
//     invocation is a no-op.
//   - createObjectURL output is NOT revoked: the browser would
//     unload the modules and crash later dynamic-import attempts.
//     For a single-page static report this leak is bounded.
//   - import cycles are reported and bailed; static reports should
//     never carry one (the SPA's web/ tree is acyclic), but a
//     malformed embed shouldn't lock the bootstrap into infinite
//     recursion.
const bootstrapScript = `<script>
(function () {
  if (window.__claudit_spa_booted) return;
  window.__claudit_spa_booted = true;
  var tags = document.querySelectorAll('script[type="text/x-claudit-mod"]');
  if (!tags.length) return;
  var sources = {};
  var names = [];
  for (var i = 0; i < tags.length; i++) {
    var name = tags[i].getAttribute('data-name');
    if (!name) continue;
    sources[name] = tags[i].textContent;
    names.push(name);
  }
  // Parse import statements to build the dep graph. Same regex
  // shape as the rewrite pass below; here we only need the
  // referenced sibling name.
  var depRE = /(?:from|import)\s+['"]\.\/([a-zA-Z0-9_-]+\.js)['"]/g;
  var deps = {};
  for (var k = 0; k < names.length; k++) {
    var src = sources[names[k]];
    var ds = [];
    var seen = {};
    depRE.lastIndex = 0;
    var m;
    while ((m = depRE.exec(src)) !== null) {
      if (!seen[m[1]]) { seen[m[1]] = true; ds.push(m[1]); }
    }
    deps[names[k]] = ds;
  }
  // Topological sort — DFS post-order. Dependencies emit before
  // dependents, so the rewrite pass below always finds its
  // dependency URLs already populated.
  var order = [];
  var visited = {};
  var visiting = {};
  function visit(n) {
    if (visited[n]) return;
    if (visiting[n]) {
      console.error('claudit SPA bundle: import cycle through', n);
      return;
    }
    visiting[n] = true;
    var ds = deps[n] || [];
    for (var x = 0; x < ds.length; x++) visit(ds[x]);
    visiting[n] = false;
    visited[n] = true;
    order.push(n);
  }
  for (var y = 0; y < names.length; y++) visit(names[y]);
  // Rewrite + blob in topological order.
  var urls = {};
  var importRE = /(from|import)(\s+)(['"])\.\/([a-zA-Z0-9_-]+\.js)(['"])/g;
  for (var z = 0; z < order.length; z++) {
    var n2 = order[z];
    var rewritten = sources[n2].replace(importRE, function (match, kw, ws, q1, base, q2) {
      var u = urls[base];
      if (!u) {
        console.error('claudit SPA bundle: unresolved import', base, 'from', n2);
        return match;
      }
      return kw + ws + q1 + u + q2;
    });
    urls[n2] = URL.createObjectURL(new Blob([rewritten], { type: 'application/javascript' }));
  }
  var entry = urls['app.js'];
  if (!entry) {
    console.error('claudit SPA bundle: missing app.js entry');
    return;
  }
  import(entry).catch(function (e) {
    console.error('claudit SPA bundle: entry import failed', e);
  });
})();
</script>
`
