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
// Two bounds hold together: an entry-count cap (default 16) and a
// resident-byte budget maxBytes (default 256 MiB, counting plain+gzip
// per entry). Bodies can run to hundreds of MB, so counting entries
// alone is no memory bound at all. On top of that, every store at
// generation G eagerly sweeps entries with generation < G: lookups
// always pass the current snapshot generation and generation is one
// global monotonic counter, so older entries are unreachable dead
// weight.
//
// We don't try to be clever otherwise: full mutex around list+map. The
// whole point is to avoid the multi-second aggregate+render path on
// repeat requests — a few hundred ns of lock contention is in the
// noise.
type renderLRU struct {
	mu       sync.Mutex
	cap      int
	maxBytes int64 // budget for bytes below
	bytes    int64 // resident bytes: sum over entries of len(plain)+len(gzip)
	ll       *list.List
	m        map[renderKey]*list.Element
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

func newRenderLRU(cap int, maxBytes int64) *renderLRU {
	if cap <= 0 {
		cap = 16
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxCachedRenderBytes
	}
	return &renderLRU{
		cap:      cap,
		maxBytes: maxBytes,
		ll:       list.New(),
		m:        map[renderKey]*list.Element{},
	}
}

// defaultMaxCachedRenderBytes is the render cache's byte budget when
// Options.MaxCachedRenderBytes is zero: 256 MiB.
const defaultMaxCachedRenderBytes = 256 << 20

// remove drops el from both the list and the map and settles the byte
// accounting. Callers hold c.mu.
func (c *renderLRU) remove(el *list.Element) {
	e := el.Value.(*renderEntry)
	c.ll.Remove(el)
	delete(c.m, e.key)
	c.bytes -= int64(len(e.plain) + len(e.gzip))
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

// storeCached inserts (or updates in place) the plain and optionally
// gzip-encoded bytes for the (q, section, gen) key, then eagerly
// prunes every entry — any query, any section — whose generation is
// older than gen, and finally LRU-evicts until both the entry cap and
// the byte budget hold. The just-stored entry is exempt from eviction
// even when it alone exceeds the budget.
//
// The generation sweep is safe because lookups always pass the CURRENT
// snapshot generation and generation is one global monotonic counter:
// an entry keyed at an older generation can never be hit again, so
// keeping it is pure dead weight (a single JSON payload can run to
// hundreds of MB). A racy late store at an older generation may
// briefly re-add a stale entry; the next store at the newer generation
// sweeps it.
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
		c.bytes += int64(len(plain)) - int64(len(e.plain))
		e.plain = plain
		if gz != nil {
			c.bytes += int64(len(gz)) - int64(len(e.gzip))
			e.gzip = gz
		}
		c.ll.MoveToFront(el)
	} else {
		e := &renderEntry{key: k, plain: plain, gzip: gz}
		c.m[k] = c.ll.PushFront(e)
		c.bytes += int64(len(plain) + len(gz))
	}
	// Sweep unreachable older-generation entries (see doc comment).
	var next *list.Element
	for el := c.ll.Front(); el != nil; el = next {
		next = el.Next()
		if el.Value.(*renderEntry).key.generation < gen {
			c.remove(el)
		}
	}
	// LRU-evict until both the entry cap and the byte budget hold. The
	// just-stored (front) entry is never evicted, even when it alone
	// blows the budget — otherwise an oversized payload could never be
	// cached at all.
	for (c.ll.Len() > c.cap || c.bytes > c.maxBytes) && c.ll.Len() > 1 {
		c.remove(c.ll.Back())
	}
}

// cacheBytes reports the current resident-byte total (plain + gzip
// across all entries); exposed for tests.
func (s *Server) cacheBytes() int64 {
	if s.renderCache == nil {
		return 0
	}
	s.renderCache.mu.Lock()
	defer s.renderCache.mu.Unlock()
	return s.renderCache.bytes
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
