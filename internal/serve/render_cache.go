package serve

import (
	"container/list"
	"sync"
)

// renderLRU is a tiny bounded cache of rendered HTML responses keyed
// on (canonical query string, snapshot generation). Both the plain
// and gzipped bytes are stored so the serve-time wire path is a pure
// memcpy.
//
// We don't try to be clever: small fixed capacity (default 16), full
// mutex around list+map. The whole point is to avoid the multi-second
// aggregate+render path on repeat requests — a few hundred ns of
// lock contention is in the noise.
type renderLRU struct {
	mu   sync.Mutex
	cap  int
	ll   *list.List
	m    map[renderKey]*list.Element
}

type renderKey struct {
	query      string
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
// only the plain bytes are stored (no good way to gzip without
// blowing the cache-key contract for size); the caller is responsible
// for compressing-on-miss and re-storing via storeCached.
func (s *Server) lookupCached(q Query, gen int64, wantGzip bool) ([]byte, bool) {
	if s.renderCache == nil {
		return nil, false
	}
	c := s.renderCache
	c.mu.Lock()
	defer c.mu.Unlock()
	k := renderKey{query: q.rawQuery, generation: gen}
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
// the (q, gen) key. Evicts the least-recently-used entry once the
// cap is exceeded. Also prunes any entries with smaller generation —
// snapshot generations only ever increase, so older entries are
// guaranteed garbage.
func (s *Server) storeCached(q Query, gen int64, plain, gz []byte) {
	if s.renderCache == nil {
		return
	}
	c := s.renderCache
	c.mu.Lock()
	defer c.mu.Unlock()

	// Prune older generations. Cheap: we expect cap ≈ 16.
	for el := c.ll.Back(); el != nil; {
		prev := el.Prev()
		e := el.Value.(*renderEntry)
		if e.key.generation < gen {
			delete(c.m, e.key)
			c.ll.Remove(el)
		}
		el = prev
	}

	k := renderKey{query: q.rawQuery, generation: gen}
	if el, ok := c.m[k]; ok {
		// Same key already present; update bytes (the gzip variant
		// may have been added after the plain).
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

// cacheLen reports the current count; exposed for tests.
func (s *Server) cacheLen() int {
	if s.renderCache == nil {
		return 0
	}
	s.renderCache.mu.Lock()
	defer s.renderCache.mu.Unlock()
	return s.renderCache.ll.Len()
}
