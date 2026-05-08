package aggregate

import "sort"

// CacheableTokens returns the total tokens that contribute to the
// cache-hit denominator: input + cache_create_5m + cache_create_1h +
// cache_read. Output tokens are excluded — they're generation, not
// context, and have nothing to do with cache reuse.
func (t Tokens) CacheableTokens() int64 {
	return t.InputTokens + t.CacheCreate5mTokens + t.CacheCreate1hTokens + t.CacheReadTokens
}

// MissTokens is the cacheable traffic that did NOT come from cache:
// input + cache_create_5m + cache_create_1h. Bigger = more dollars
// went to fresh upload + cache writes that should have been cache hits.
func (t Tokens) MissTokens() int64 {
	return t.InputTokens + t.CacheCreate5mTokens + t.CacheCreate1hTokens
}

// HitRatio is cache_read / (cache_read + miss). Returns 0 when there's
// no cacheable traffic at all (the row is irrelevant to cache analysis).
// Higher = better.
func (t Tokens) HitRatio() float64 {
	denom := t.CacheableTokens()
	if denom <= 0 {
		return 0
	}
	return float64(t.CacheReadTokens) / float64(denom)
}

// SessionBucket is the per-session row used by the cache-efficiency
// section. Sessions don't have their own dimension elsewhere because
// the canonical cost view is by-project; here they're useful because a
// single bad session can hide inside an otherwise healthy project.
type SessionBucket struct {
	SessionID string
	Project   string
	Tokens
	CostUSD float64
	Turns   int
}

// CacheRow is a denormalized row used by the cache-efficiency renderer.
// One CacheRow is one (dimension, key) pair — e.g. (project, "/p/foo")
// or (tool, "Bash"). The renderer iterates these directly so the
// markdown/HTML code doesn't have to know about each source bucket type.
type CacheRow struct {
	Key      string // project path / session id / tool name
	Subtitle string // optional second-line context (e.g. project for a session row)
	Tokens          // embedded so r.CacheReadTokens etc. just work
	CostUSD  float64
	Turns    int
	HitRatio float64
	Miss     int64
}

// rankCacheRows sorts by miss tokens descending so the worst absolute
// offender is first. Two rows with the same miss count fall back to
// hit-ratio ascending (worse first), then key for stability.
func rankCacheRows(rows []CacheRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Miss != rows[j].Miss {
			return rows[i].Miss > rows[j].Miss
		}
		if rows[i].HitRatio != rows[j].HitRatio {
			return rows[i].HitRatio < rows[j].HitRatio
		}
		return rows[i].Key < rows[j].Key
	})
}

// CacheByProject returns one CacheRow per project, ranked by miss tokens.
// Projects with zero cacheable traffic are excluded.
func (a *Aggregator) CacheByProject() []CacheRow {
	out := make([]CacheRow, 0, len(a.byProject))
	for _, p := range a.byProject {
		if p.CacheableTokens() == 0 {
			continue
		}
		out = append(out, CacheRow{
			Key:      p.Project,
			Tokens:   p.Tokens,
			CostUSD:  p.CostUSD,
			Turns:    p.Turns,
			HitRatio: p.Tokens.HitRatio(),
			Miss:     p.Tokens.MissTokens(),
		})
	}
	rankCacheRows(out)
	return out
}

// CacheBySession returns one CacheRow per session. Each session's
// project is shown in Subtitle so the reader can place it without
// cross-referencing.
func (a *Aggregator) CacheBySession() []CacheRow {
	out := make([]CacheRow, 0, len(a.bySession))
	for _, s := range a.bySession {
		if s.CacheableTokens() == 0 {
			continue
		}
		out = append(out, CacheRow{
			Key:      s.SessionID,
			Subtitle: s.Project,
			Tokens:   s.Tokens,
			CostUSD:  s.CostUSD,
			Turns:    s.Turns,
			HitRatio: s.Tokens.HitRatio(),
			Miss:     s.Tokens.MissTokens(),
		})
	}
	rankCacheRows(out)
	return out
}

// CacheBySubagent returns one CacheRow per subagent type. Subagents are
// notably bad cache citizens — each invocation starts with a fresh
// context — so this view often surfaces a structural cost driver
// distinct from any individual project.
func (a *Aggregator) CacheBySubagent() []CacheRow {
	out := make([]CacheRow, 0, len(a.bySub))
	for _, s := range a.bySub {
		if s.CacheableTokens() == 0 {
			continue
		}
		out = append(out, CacheRow{
			Key:      s.Type,
			Tokens:   s.Tokens,
			CostUSD:  s.CostUSD,
			Turns:    s.Turns,
			HitRatio: s.Tokens.HitRatio(),
			Miss:     s.Tokens.MissTokens(),
		})
	}
	rankCacheRows(out)
	return out
}

// CacheByInvocation returns one CacheRow per subagent run. The Key is
// the agent description (or "(no description)"); Subtitle is the
// subagent type so the reader can tell two same-typed runs apart.
func (a *Aggregator) CacheByInvocation() []CacheRow {
	out := make([]CacheRow, 0, len(a.byInvocation))
	for _, inv := range a.byInvocation {
		if inv.CacheableTokens() == 0 {
			continue
		}
		key := inv.Description
		if key == "" {
			key = "(no description)"
		}
		out = append(out, CacheRow{
			Key:      key,
			Subtitle: inv.SubagentType,
			Tokens:   inv.Tokens,
			CostUSD:  inv.CostUSD,
			Turns:    inv.Turns,
			HitRatio: inv.Tokens.HitRatio(),
			Miss:     inv.Tokens.MissTokens(),
		})
	}
	rankCacheRows(out)
	return out
}

// OverallHitRatio is the report-wide cache hit ratio — the headline
// number for the cache-efficiency section.
func (a *Aggregator) OverallHitRatio() float64 {
	return a.totals.Tokens.HitRatio()
}
