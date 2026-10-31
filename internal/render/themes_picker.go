package render

// Theme catalog and the persisted theme path. The SPA's picker writes the
// selected slug to ~/.config/claudit/theme via POST /api/theme
// (internal/serve), which validates it with ValidThemeSlug.

import (
	"os"
	"path/filepath"
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
