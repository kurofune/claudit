// Package claudit is a root-level shim whose sole purpose is to embed
// the SPA's source tree under web/ into the Go binary. Why a separate
// package at repo root: //go:embed paths must resolve relative to the
// embedding file's directory, and internal/serve/ can't reach up to
// ../web/. Keeping web/ at repo root (where humans expect it) means
// the embedding declaration has to live alongside it.
//
// The file is otherwise empty — internal/serve/assets.go does the
// hashing, rewriting, and serving against this FS.
package claudit

import "embed"

// WebFS is the SPA's static source tree (HTML, CSS, ES modules)
// bundled into the binary at build time. Consumers should treat it
// as a read-only fs.FS — the assets pipeline in internal/serve walks
// it once at startup and never again.
//
//go:embed all:web
var WebFS embed.FS
