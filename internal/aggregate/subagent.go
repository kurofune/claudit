package aggregate

import (
	"sync"

	"github.com/kurofune/claudit/internal/parse"
)

// NewMemoizedSubagentLookup returns a SubagentLookup that lazily reads
// the sibling .meta.json once per source file. Safe for concurrent use.
// A failed read caches the zero SubagentMeta so a missing meta file is
// only stat'd once.
func NewMemoizedSubagentLookup() SubagentLookup {
	var cache sync.Map
	return func(t parse.Turn) (string, string) {
		if !parse.IsSubagentFile(t.SourceFile) {
			return "", ""
		}
		if v, ok := cache.Load(t.SourceFile); ok {
			m := v.(parse.SubagentMeta)
			return m.AgentType, m.Description
		}
		m, _ := parse.ReadSubagentMeta(t.SourceFile)
		cache.Store(t.SourceFile, m)
		return m.AgentType, m.Description
	}
}
