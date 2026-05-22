package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"

	webassets "github.com/kurofune/claudit"
)

// webFS is the SPA source tree shipped inside the binary. The actual
// //go:embed directive lives in the repo-root webassets package
// because `embed` only resolves paths under the embedding source
// file's directory — internal/serve/ can't reach up to ../web/.
var webFS fs.FS = webassets.WebFS

// webRoot is the directory prefix the embed walk strips. Hoisted into
// a constant so changes to the on-disk layout land in one place.
const webRoot = "web"

// webAssetURLPrefix is the URL path under which hashed assets are
// served. Browser requests look like /_claudit/web/app.<hash>.js.
const webAssetURLPrefix = "/_claudit/web/"

// rootPath is the SPA's canonical entry URL after the Phase 8 cutover.
// appPath is kept as an alias so /app bookmarks from the Phase 5/6/7
// A/B window still resolve. legacyPath is the one-minor-release escape
// hatch for callers that still want the self-contained fat HTML.
const (
	rootPath   = "/"
	appPath    = "/app"
	legacyPath = "/legacy"
)

// assetEntry is one rewritten file ready to be served. body has already
// had its import URLs (.js) or {{asset "..."}} placeholders (.html)
// substituted with the hashed sibling names — at request time the
// handler just writes the bytes.
//
// hash is the first 8 hex chars of sha256(original-source-bytes). It
// drives both the served URL (/_claudit/web/<base>.<hash>.<ext>) and the
// import-rewrite step below. Picking 8 chars (32 bits of collision
// resistance) is plenty for a self-served single-binary app — the
// browser only ever sees URLs we emitted into the same page.
type assetEntry struct {
	sourceName  string // e.g. "app.js"
	hashedName  string // e.g. "app.<hash>.js"
	contentType string
	hash        string
	body        []byte
}

// assetManifest indexes the embedded SPA assets two ways: by source
// name (for the import-rewrite pass, which resolves `./api.js` →
// `app.bySourceName["api.js"].hashedName`) and by hashed name (for the
// /_claudit/web/* handler, which gets the URL component back from the
// browser).
type assetManifest struct {
	bySourceName map[string]*assetEntry
	byHashedName map[string]*assetEntry
}

// importRewriteRE matches ES-module relative imports of sibling files:
//
//	from './name.js'    /  from "./name.js"
//	import './name.js'  /  import "./name.js"
//
// Only flat siblings are matched on purpose — nested paths would require
// resolving "./" relative to each importer's directory, which adds
// failure modes (a typo'd subdir silently becomes a 404) without buying
// us anything for Phase 5's flat web/ layout.
var importRewriteRE = regexp.MustCompile(`(from|import)(\s+)(['"])\./([a-zA-Z0-9_-]+)\.(js)(['"])`)

// assetPlaceholderRE matches {{asset "name.ext"}} in HTML source.
// Whitespace inside the braces is tolerated; the quoted name must be
// a plain filename (no path separators) so a typo can't accidentally
// reach into an arbitrary directory.
var assetPlaceholderRE = regexp.MustCompile(`\{\{\s*asset\s+"([a-zA-Z0-9_.-]+)"\s*\}\}`)

// buildAssetManifest walks fsys under root, hashes each file's
// contents, and rewrites JS imports + HTML asset placeholders so the
// final served bytes refer to the hashed sibling names.
//
// Two-pass on purpose:
//  1. First pass: read every file, compute hash on the *raw* source.
//     The hash drives both the served URL and the rewriter's
//     substitutions in pass 2 — and it must reflect the source the
//     user authored, not the post-rewrite output (which would create a
//     hash-depends-on-hash circular dependency the second a file
//     references itself or a tight import cycle).
//  2. Second pass: walk the same files, run the regex substitution
//     against now-known hashed names from pass 1, and stash the
//     resulting body bytes plus content-type on each entry.
//
// Errors from the FS bubble up so a missing file during init fails
// loudly at server start rather than at first request.
func buildAssetManifest(fsys fs.FS, root string) (*assetManifest, error) {
	// Pass 1: collect raw bytes + hash.
	rawByName := make(map[string][]byte)
	hashByName := make(map[string]string)
	if err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return rerr
		}
		// Source name = path relative to root (e.g. "web/app.js" → "app.js").
		name := strings.TrimPrefix(p, root+"/")
		rawByName[name] = data
		sum := sha256.Sum256(data)
		hashByName[name] = hex.EncodeToString(sum[:])[:8]
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk assets: %w", err)
	}

	// Stable iteration order so deterministic logging / errors don't
	// depend on map iteration order. The substitutions themselves are
	// per-file, but a future enhancement (e.g. precomputed manifest
	// log lines) would benefit.
	names := make([]string, 0, len(rawByName))
	for n := range rawByName {
		names = append(names, n)
	}
	sort.Strings(names)

	m := &assetManifest{
		bySourceName: make(map[string]*assetEntry, len(names)),
		byHashedName: make(map[string]*assetEntry, len(names)),
	}

	// Pass 2: rewrite + index.
	for _, name := range names {
		h := hashByName[name]
		ext := strings.ToLower(path.Ext(name))
		hashedName := strings.TrimSuffix(name, ext) + "." + h + ext
		body := rawByName[name]

		switch ext {
		case ".js":
			body = importRewriteRE.ReplaceAllFunc(body, func(m []byte) []byte {
				// Submatches: 1=from|import, 2=ws, 3=open quote,
				// 4=basename, 5=ext (js), 6=close quote.
				sub := importRewriteRE.FindSubmatch(m)
				base := string(sub[4]) // e.g. "api"
				srcKey := base + "." + string(sub[5])
				targetHash, ok := hashByName[srcKey]
				if !ok {
					// Unknown sibling — leave the import alone so the
					// 404 surfaces at runtime where it's findable
					// rather than silently mangling the source.
					return m
				}
				replaced := base + "." + targetHash + "." + string(sub[5])
				// Rebuild: from ./<replaced>"
				return []byte(string(sub[1]) + string(sub[2]) +
					string(sub[3]) + "./" + replaced + string(sub[6]))
			})
		case ".html":
			body = assetPlaceholderRE.ReplaceAllFunc(body, func(m []byte) []byte {
				sub := assetPlaceholderRE.FindSubmatch(m)
				srcKey := string(sub[1])
				targetHash, ok := hashByName[srcKey]
				if !ok {
					// Same logic as JS imports — surface the typo
					// loudly. Leaving the placeholder in the body
					// means the page is visibly broken.
					return m
				}
				targetExt := strings.ToLower(path.Ext(srcKey))
				baseNoExt := strings.TrimSuffix(srcKey, targetExt)
				return []byte("/_claudit/web/" + baseNoExt + "." + targetHash + targetExt)
			})
		}

		e := &assetEntry{
			sourceName:  name,
			hashedName:  hashedName,
			contentType: contentTypeForExt(ext),
			hash:        h,
			body:        body,
		}
		m.bySourceName[name] = e
		m.byHashedName[hashedName] = e
	}

	return m, nil
}

// handleApp serves the SPA shell at "/" and "/app". The shell bytes
// are baked at startup (see buildAssetManifest) with the
// {{asset "name.ext"}} placeholders already resolved to hashed
// /_claudit/web/... URLs, so the request path is a flat memcpy.
//
// Cache-Control: no-cache, must-revalidate — the shell itself is
// tiny and its hashed-asset references rotate whenever the source
// rotates, so we want the browser to revalidate (ETag → 304) on each
// load rather than serve a stale shell that points at evicted hashed
// URLs.
//
// Routes "/" through this handler as a catch-all post-Phase-8: the
// only paths that should answer with the shell are "/" and the
// historical "/app" alias. Everything else 404s here rather than
// silently rendering the shell under a typo'd URL.
func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != rootPath && r.URL.Path != appPath {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.assets == nil {
		http.Error(w, "assets unavailable", http.StatusInternalServerError)
		return
	}
	entry, ok := s.assets.bySourceName["index.html"]
	if !ok {
		http.Error(w, "missing index.html", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("ETag", `W/"asset-`+entry.hash+`"`)
	if match := r.Header.Get("If-None-Match"); match != "" && ifNoneMatchHit(match, `W/"asset-`+entry.hash+`"`) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := w.Write(entry.body); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write app shell failed",
			slog.Any("err", err),
			slog.String("path", r.URL.Path))
	}
}

// handleWebAsset serves the hashed /_claudit/web/<base>.<hash>.<ext>
// URLs. Returns 404 for unknown hashes — including the un-hashed
// source name — so a browser holding a stale shell can't accidentally
// fetch an out-of-date file under the bare URL.
//
// Cache-Control: public, max-age=31536000, immutable — the URL
// changes whenever the bytes change, so the browser can hold the
// response forever.
func (s *Server) handleWebAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.assets == nil {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, webAssetURLPrefix)
	if rel == "" || strings.Contains(rel, "/") {
		// Nested paths are not supported in the flat layout —
		// rejecting them keeps the manifest lookup unambiguous.
		http.NotFound(w, r)
		return
	}
	entry, ok := s.assets.byHashedName[rel]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(entry.body)))
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := w.Write(entry.body); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write web asset failed",
			slog.Any("err", err),
			slog.String("path", r.URL.Path))
	}
}

// contentTypeForExt returns the MIME type for a given asset extension.
// Limited to the file kinds the SPA actually ships — anything else
// would be a typo'd reference, not a real asset.
func contentTypeForExt(ext string) string {
	switch ext {
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}
