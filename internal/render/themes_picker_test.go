package render

import (
	"io/fs"
	"regexp"
	"sort"
	"testing"

	webassets "github.com/kurofune/claudit"
)

// TestThemeCatalogParity guards the three hand-maintained theme slug
// lists against drift:
//
//   - themeCatalog        (Go, this package — validates serve writes + CLI reads)
//   - THEMES              (web/themes.js — drives the SPA picker UI)
//   - SLUGS               (web/index.html — the FOUC-prevention allowlist)
//
// and asserts every non-"auto" catalog slug has a matching
// web/theme-<slug>.css in the embed. If any of these fall out of sync,
// a theme either won't render, won't be pickable, or flashes the wrong
// colors before the SPA boots — all silent in normal builds, hence the
// test. Slug-only on purpose: labels/scheme are the SPA's concern and
// don't affect the CLI surface this test protects.
func TestThemeCatalogParity(t *testing.T) {
	var goSlugs []string
	for _, e := range themeCatalog {
		goSlugs = append(goSlugs, e.Slug)
	}

	// web/themes.js THEMES entries: `slug: "x"`. The colon distinguishes
	// catalog entries from the `data-theme-slug="..."` attribute string
	// (which uses `=`, not `:`) elsewhere in the module.
	jsSlugs := extractQuoted(t, mustEmbed(t, "web/themes.js"), `slug:\s*"([a-z0-9-]+)"`)
	if diff := setDiff(goSlugs, jsSlugs); diff != "" {
		t.Errorf("themeCatalog vs web/themes.js THEMES slugs differ:\n%s", diff)
	}

	// web/index.html FOUC list excludes "auto" (the sentinel means "no
	// data-theme", so there's nothing to allowlist). Narrow the scan to
	// the `SLUGS = [ ... ]` block so class names / ids elsewhere in the
	// shell don't masquerade as slugs.
	foucBlock := extractBlock(t, mustEmbed(t, "web/index.html"), `(?s)SLUGS\s*=\s*\[(.*?)\]`)
	htmlSlugs := extractQuoted(t, foucBlock, `"([a-z0-9-]+)"`)
	goNonAuto := without(goSlugs, "auto")
	if diff := setDiff(goNonAuto, htmlSlugs); diff != "" {
		t.Errorf("themeCatalog (non-auto) vs web/index.html SLUGS differ:\n%s", diff)
	}

	// Every non-auto slug must back a real stylesheet in the embed,
	// otherwise loadInheritedTheme silently drops to the system default.
	for _, s := range goNonAuto {
		if _, err := fs.ReadFile(webassets.WebFS, "web/theme-"+s+".css"); err != nil {
			t.Errorf("catalog slug %q has no web/theme-%s.css in the embed: %v", s, s, err)
		}
	}
}

func mustEmbed(t *testing.T, path string) []byte {
	t.Helper()
	b, err := fs.ReadFile(webassets.WebFS, path)
	if err != nil {
		t.Fatalf("read embed %s: %v", path, err)
	}
	return b
}

// extractBlock returns capture group 1 of the first match of pattern.
func extractBlock(t *testing.T, src []byte, pattern string) []byte {
	t.Helper()
	m := regexp.MustCompile(pattern).FindSubmatch(src)
	if m == nil {
		t.Fatalf("pattern %q matched nothing", pattern)
	}
	return m[1]
}

// extractQuoted returns every capture-group-1 match of pattern in src.
func extractQuoted(t *testing.T, src []byte, pattern string) []string {
	t.Helper()
	var out []string
	for _, m := range regexp.MustCompile(pattern).FindAllSubmatch(src, -1) {
		out = append(out, string(m[1]))
	}
	if len(out) == 0 {
		t.Fatalf("pattern %q matched no slugs", pattern)
	}
	return out
}

func without(in []string, drop string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// setDiff returns a human-readable description of how two slug lists
// differ as sets (order-insensitive), or "" when they match.
func setDiff(a, b []string) string {
	as, bs := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	inA := map[string]bool{}
	inB := map[string]bool{}
	for _, s := range as {
		inA[s] = true
	}
	for _, s := range bs {
		inB[s] = true
	}
	var onlyA, onlyB []string
	for _, s := range as {
		if !inB[s] {
			onlyA = append(onlyA, s)
		}
	}
	for _, s := range bs {
		if !inA[s] {
			onlyB = append(onlyB, s)
		}
	}
	if len(onlyA) == 0 && len(onlyB) == 0 {
		return ""
	}
	out := ""
	if len(onlyA) > 0 {
		out += "  only in first:  " + join(onlyA) + "\n"
	}
	if len(onlyB) > 0 {
		out += "  only in second: " + join(onlyB) + "\n"
	}
	return out
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}
