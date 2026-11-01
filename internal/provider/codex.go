package provider

import (
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/parse/codex"
)

var _ Source = (*Codex)(nil)

// Codex reads Codex CLI rollouts under a sessions root (~/.codex/sessions by
// default), laid out as YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl.
type Codex struct {
	root string
}

// NewCodex returns the Codex Source rooted at root.
func NewCodex(root string) *Codex { return &Codex{root: root} }

// Name is parse.ProviderCodex.
func (c *Codex) Name() string { return parse.ProviderCodex }

// Root is the sessions root the Source walks.
func (c *Codex) Root() string { return c.root }

// ParseFile is codex.ParseFile.
func (c *Codex) ParseFile(r io.Reader, path string) (parse.Result, error) {
	return codex.ParseFile(r, path)
}

// Walk invokes fn for every rollout-*.jsonl three date directories below the
// root. When earliest is non-zero, files whose mtime predates it are
// skipped. Per-entry walk errors are tolerated, and a missing root yields
// nothing.
func (c *Codex) Walk(earliest time.Time, fn func(path string, mod time.Time, size int64)) error {
	filter := !earliest.IsZero()
	return filepath.WalkDir(c.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(c.root, path)
		if rerr != nil || rel == "." {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if d.IsDir() {
			if len(parts) > 3 || !digits(parts[len(parts)-1], datePartWidths[len(parts)-1]) {
				return filepath.SkipDir
			}
			return nil
		}
		name := parts[len(parts)-1]
		if len(parts) != 4 || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if filter && info.ModTime().Before(earliest) {
			return nil
		}
		fn(path, info.ModTime(), info.Size())
		return nil
	})
}

// datePartWidths are the widths of the YYYY, MM and DD directory names.
var datePartWidths = [3]int{4, 2, 2}

func digits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
