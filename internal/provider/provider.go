// Package provider is the seam between claudit and each agent tool whose
// logs it reads. A Source finds one tool's log files and parses them into
// the per-turn surface the corpus and aggregator consume; the corpus holds a
// list of Sources and never needs to know which tool wrote a file.
package provider

import (
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// Source is one provider's logs.
type Source interface {
	// Name is the provider name every turn the Source parses carries in
	// parse.Turn.Provider.
	Name() string
	// Walk invokes fn for every log file the Source owns. When earliest is
	// non-zero, files whose mtime predates it may be skipped.
	Walk(earliest time.Time, fn func(path string, mod time.Time, size int64)) error
	// ParseFile reads one log file Walk yielded.
	ParseFile(r io.Reader, path string) (parse.Result, error)
}

var _ Source = (*ClaudeCode)(nil)

// ClaudeCode reads Claude Code session JSONL under a projects root
// (~/.claude/projects by default).
type ClaudeCode struct {
	root string
}

// NewClaudeCode returns the Claude Code Source rooted at root.
func NewClaudeCode(root string) *ClaudeCode { return &ClaudeCode{root: root} }

// Name is parse.ProviderClaudeCode.
func (c *ClaudeCode) Name() string { return parse.ProviderClaudeCode }

// Root is the projects root the Source walks.
func (c *ClaudeCode) Root() string { return c.root }

// ParseFile is parse.ParseFile.
func (c *ClaudeCode) ParseFile(r io.Reader, path string) (parse.Result, error) {
	return parse.ParseFile(r, path)
}

// Walk invokes fn for every .jsonl file under the root. When earliest is
// non-zero, files whose mtime predates it are skipped. Tolerates per-entry
// walk errors so a transient FS hiccup mid-walk doesn't abort the whole scan.
func (c *ClaudeCode) Walk(earliest time.Time, fn func(path string, mod time.Time, size int64)) error {
	filter := !earliest.IsZero()
	return filepath.WalkDir(c.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
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
