package serve

// handleAPITheme persists the SPA picker's chosen theme to
// ~/.config/claudit/theme so the standalone `claudit report` /
// `claudit diff` commands can pick it up. POST body is JSON
// {"slug":"<theme-slug>"}; the slug is validated against
// render.ValidThemeSlug before any file I/O. The empty-slug case
// (user reverted to Auto) removes the file rather than persisting
// the "auto" sentinel — exports then fall back to the OS
// prefers-color-scheme default.
//
// This is the *only* write path on the daemon. Loopback-only by
// default (serve binds 127.0.0.1) keeps the attack surface low; a
// hostile localhost neighbor could trigger a single 1-byte write to
// the config file, which is bounded and recoverable.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kurofune/claudit/internal/render"
)

type themePostBody struct {
	Slug string `json:"slug"`
}

func (s *Server) handleAPITheme(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body themePostBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	path, err := render.InheritedThemePath()
	if err != nil {
		http.Error(w, "no home directory", http.StatusInternalServerError)
		return
	}
	// Empty slug or the auto sentinel → remove the file so exports
	// drop back to system default. ENOENT is fine — desired state
	// is "no file."
	if body.Slug == "" || body.Slug == "auto" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			http.Error(w, "remove theme file: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !render.ValidThemeSlug(body.Slug) {
		http.Error(w, "unknown theme slug", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(path, []byte(body.Slug+"\n"), 0o644); err != nil {
		http.Error(w, "write theme file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
