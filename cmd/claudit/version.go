package main

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// versionString returns "claudit vX.Y.Z (commit abc1234)" for go-install
// builds and "claudit (devel) (commit abc1234, dirty)" for local builds.
// Both forms answer the same diagnostic question — "is the binary on my
// PATH actually built from the source/tag I expect" — which is exactly
// the trap we hit when a stale go-module-proxy cache served v1.1.1 in
// response to @latest after v1.2.0 was already tagged.
func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "claudit (unknown)"
	}
	version := info.Main.Version
	if version == "" {
		version = "(devel)"
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = ", dirty"
			}
		}
	}
	if rev != "" {
		return fmt.Sprintf("claudit %s (commit %s%s)", version, rev, dirty)
	}
	return fmt.Sprintf("claudit %s", version)
}

// versionShort returns a compact label for the report sidebar.
//
//	tagged go-install:  "v1.2.0"
//	local build past a tag:  "v1.2.1-dev abc1234 (dirty)"
//	  — base parsed from the pseudo-version (v1.2.1-0.<ts>-<sha>+dirty)
//	    that go synthesizes; the parsed base is the *next* version this
//	    commit would become, with "-dev" + sha making "future-tag, not
//	    actually released" explicit
//	first-commit / no parent tag:  "(devel) abc1234 (dirty)"
//	no build info at all:  ""
//
// Distinct from versionString — that one answers a diagnostic question
// ("which binary is on PATH?"), this one chrome-tags the UI.
func versionShort() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	version := info.Main.Version
	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			} else {
				rev = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = true
			}
		}
	}
	dirtySuffix := ""
	if dirty {
		dirtySuffix = " (dirty)"
	}

	// Strip the "+dirty" suffix Go appends to pseudo-versions for
	// modified working trees — we surface that signal via dirtySuffix.
	clean := version
	if i := strings.Index(clean, "+"); i >= 0 {
		clean = clean[:i]
	}

	// Pseudo-version base extraction. Go's pseudo-version format is
	// "<base>-0.<yyyymmddhhmmss>-<12hexchars>" for commits past a tag,
	// e.g. v1.2.1-0.20260519211036-0eb38df485a5. The "-0." infix is the
	// reliable marker. Everything before it is the version that the
	// *next* tag would become — we display that with a "-dev" suffix.
	if i := strings.Index(clean, "-0."); i > 0 {
		base := clean[:i]
		label := base + "-dev"
		if rev != "" {
			label += " " + rev
		}
		return label + dirtySuffix
	}

	// Not a pseudo-version with a parsable base. Could be a clean tag
	// (v1.2.0), the literal "(devel)" Go uses when no module info is
	// available, or the v0.0.0-<ts>-<sha> form for repos with no
	// reachable tag. Treat the last two as devel.
	if clean != "" && clean != "(devel)" && !strings.HasPrefix(clean, "v0.0.0-") {
		return clean + dirtySuffix
	}
	if rev != "" {
		return "(devel) " + rev + dirtySuffix
	}
	return "(devel)" + dirtySuffix
}
