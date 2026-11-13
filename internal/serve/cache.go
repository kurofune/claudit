package serve

import (
	"github.com/kurofune/claudit/v2/internal/corpus"
	"github.com/kurofune/claudit/v2/internal/provider"
)

// The data layer lives in internal/corpus now — one loader shared by
// serve and watch. These aliases keep the serve package's
// existing call sites (NewCache/Cache/Snapshot/hostInfo) unchanged while
// the implementation lives in one place.
type (
	// Cache walks the projects root and keeps a polled, parsed copy.
	Cache = corpus.Cache
	// Snapshot is an immutable view of the parsed corpus.
	Snapshot = corpus.Snapshot
	// hostInfo is the diagnostic vitals struct surfaced by /snapshot.
	hostInfo = corpus.HostInfo
)

// NewCache returns a cache over the Claude Code projects root. Thin
// wrapper over corpus.New so existing serve call sites keep working.
func NewCache(root string) *Cache { return corpus.New(provider.NewClaudeCode(root)) }
