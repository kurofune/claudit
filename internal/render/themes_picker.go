package render

// Inherited-theme bridge between `claudit serve` and the standalone
// `claudit report` / `claudit diff` exports.
//
// The SPA's picker writes the selected slug to ~/.config/claudit/theme
// (via the POST /api/theme endpoint added in internal/serve). When the
// CLI later renders a static report or diff, loadInheritedTheme()
// reads that file, validates the slug, and returns the matching
// theme-{slug}.css contents. The static templates inline that single
// stylesheet under :root[data-theme="{slug}"] and stamp the same
// attribute onto <html>, so the export looks exactly like serve did
// when the user last chose a theme. No picker UI in the static files;
// the slug is the one source of truth and serve is the only place to
// change it.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	webassets "github.com/kurofune/claudit"
)

// themeCatalogEntry mirrors one row of THEMES in web/themes.js. The
// Go-side slug list is intentionally redundant with the JS one — it's
// the validation surface for what the SPA can persist and what the CLI
// will accept. TestThemeCatalogParity asserts they stay in lockstep.
type themeCatalogEntry struct {
	Slug, Label, Scheme string
}

var themeCatalog = []themeCatalogEntry{
	{Slug: "auto", Label: "Auto (system)", Scheme: "auto"},
	// Light — alphabetical
	{Slug: "ayu-light", Label: "Ayu Light", Scheme: "light"},
	{Slug: "catppuccin-latte", Label: "Catppuccin Latte", Scheme: "light"},
	{Slug: "gruvbox-light", Label: "Gruvbox Light", Scheme: "light"},
	{Slug: "one-light", Label: "One Light", Scheme: "light"},
	{Slug: "papercolor-light", Label: "PaperColor Light", Scheme: "light"},
	{Slug: "solarized-light", Label: "Solarized Light", Scheme: "light"},
	// Dark — alphabetical
	{Slug: "catppuccin-mocha", Label: "Catppuccin Mocha", Scheme: "dark"},
	{Slug: "dracula", Label: "Dracula", Scheme: "dark"},
	{Slug: "github-dark", Label: "GitHub Dark", Scheme: "dark"},
	{Slug: "gruvbox-dark", Label: "Gruvbox Dark", Scheme: "dark"},
	{Slug: "monokai-pro", Label: "Monokai Pro", Scheme: "dark"},
	{Slug: "night-owl", Label: "Night Owl", Scheme: "dark"},
	{Slug: "nord", Label: "Nord", Scheme: "dark"},
	{Slug: "one-dark", Label: "One Dark", Scheme: "dark"},
	{Slug: "solarized-dark", Label: "Solarized Dark", Scheme: "dark"},
	{Slug: "tokyo-night", Label: "Tokyo Night", Scheme: "dark"},
}

// ValidThemeSlug reports whether s is a known theme slug. Used by the
// serve endpoint to reject garbage before it touches the config file.
// "auto" is rejected — the convention is to delete the file rather
// than persist the auto sentinel.
func ValidThemeSlug(s string) bool {
	for _, t := range themeCatalog {
		if t.Scheme == "auto" {
			continue
		}
		if t.Slug == s {
			return true
		}
	}
	return false
}

// inheritedThemePath returns the absolute path to the persisted theme
// file. Convention matches pricing.DefaultPath — ~/.config/claudit.
// Exported so the serve package can write to the same location.
func InheritedThemePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claudit", "theme"), nil
}

// loadInheritedTheme reads ~/.config/claudit/theme and returns the
// validated slug + the contents of the matching web/theme-{slug}.css.
// All three return values are zero when the file is absent, empty, or
// holds an unknown slug — callers treat that as "no inherited theme,
// fall back to the OS prefers-color-scheme default."
func loadInheritedTheme() (slug, css string) {
	path, err := InheritedThemePath()
	if err != nil {
		return "", ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "" // missing file is the common, expected case
	}
	candidate := strings.TrimSpace(string(raw))
	if !ValidThemeSlug(candidate) {
		return "", ""
	}
	b, err := fs.ReadFile(webassets.WebFS, "web/theme-"+candidate+".css")
	if err != nil {
		// File was valid against the catalog but missing from the
		// embed — should be impossible, but don't crash the report.
		return "", ""
	}
	return candidate, fmt.Sprintf("/* inherited theme: %s */\n%s", candidate, string(b))
}
