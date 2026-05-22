package serve

import (
	"container/list"
	"sync"
)

// renderLRU is a tiny bounded cache of rendered bytes keyed on
// (canonical query string, section, snapshot generation). Both the
// plain and gzipped bytes are stored so the serve-time wire path is a
// pure memcpy.
//
// "Section" names what the entry represents — "html" for the rendered
// report, "data" for the JSON payload, with room for one-per-API-tab
// keys in later phases. Keying by section means the HTML and JSON
// halves of one pageload coexist without churning each other, which is
// what the pre-Phase-1 code achieved by maintaining two separate LRUs.
//
// We don't try to be clever: small fixed capacity (default 16), full
// mutex around list+map. The whole point is to avoid the multi-second
// aggregate+render path on repeat requests — a few hundred ns of lock
// contention is in the noise.
type renderLRU struct {
	mu  sync.Mutex
	cap int
	ll  *list.List
	m   map[renderKey]*list.Element
}

type renderKey struct {
	query      string
	section    string
	generation int64
}

type renderEntry struct {
	key   renderKey
	plain []byte
	gzip  []byte // nil if not yet computed
}

func newRenderLRU(cap int) *renderLRU {
	if cap <= 0 {
		cap = 16
	}
	return &renderLRU{
		cap: cap,
		ll:  list.New(),
		m:   map[renderKey]*list.Element{},
	}
}

// lookupCached returns the appropriate body for the encoding the
// client wanted, plus ok=true on hit. gzip-wanting clients miss when
// only the plain bytes are stored; the caller is responsible for
// compressing-on-miss and re-storing via storeCached.
func (s *Server) lookupCached(q Query, section string, gen int64, wantGzip bool) ([]byte, bool) {
	if s.renderCache == nil {
		return nil, false
	}
	c := s.renderCache
	c.mu.Lock()
	defer c.mu.Unlock()
	k := renderKey{query: q.rawQuery, section: section, generation: gen}
	el, ok := c.m[k]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	e := el.Value.(*renderEntry)
	if wantGzip {
		if e.gzip == nil {
			return nil, false
		}
		return e.gzip, true
	}
	return e.plain, true
}

// storeCached inserts plain (and optionally gzip-encoded) bytes for
// the (q, section, gen) key. Evicts the least-recently-used entry
// once the cap is exceeded.
//
// Note: there is no eviction by generation. The key includes
// generation, so a post-bump request for the same (query, section)
// won't hit an old entry — it'll miss and store a new one. Older
// entries linger until LRU pushes them out. Pre-Phase-1, an eager
// prune loop here purged every entry with a smaller generation on
// every store; that was correct only when the cache held one
// monolithic blob per (query, gen). Once sections enter the key, a
// generation bump may invalidate only some sections, and the
// section's own (q, section, gen+1) miss naturally replaces it.
func (s *Server) storeCached(q Query, section string, gen int64, plain, gz []byte) {
	if s.renderCache == nil {
		return
	}
	c := s.renderCache
	c.mu.Lock()
	defer c.mu.Unlock()

	k := renderKey{query: q.rawQuery, section: section, generation: gen}
	if el, ok := c.m[k]; ok {
		e := el.Value.(*renderEntry)
		e.plain = plain
		if gz != nil {
			e.gzip = gz
		}
		c.ll.MoveToFront(el)
		return
	}
	e := &renderEntry{key: k, plain: plain, gzip: gz}
	c.m[k] = c.ll.PushFront(e)
	for c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest == nil {
			break
		}
		c.ll.Remove(oldest)
		delete(c.m, oldest.Value.(*renderEntry).key)
	}
}

// cacheLen reports the current total count across all sections;
// exposed for tests.
func (s *Server) cacheLen() int {
	if s.renderCache == nil {
		return 0
	}
	s.renderCache.mu.Lock()
	defer s.renderCache.mu.Unlock()
	return s.renderCache.ll.Len()
}

// sectionCacheLen reports the count of entries with the given section
// label; exposed for tests so an assertion can target the HTML cache
// or the JSON cache independently without re-deriving the count from
// section-agnostic data.
func (s *Server) sectionCacheLen(section string) int {
	if s.renderCache == nil {
		return 0
	}
	s.renderCache.mu.Lock()
	defer s.renderCache.mu.Unlock()
	n := 0
	for k := range s.renderCache.m {
		if k.section == section {
			n++
		}
	}
	return n
}
