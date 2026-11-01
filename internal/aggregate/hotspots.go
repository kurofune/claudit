package aggregate

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

// HotspotKind identifies what a hotspot card is about. Each kind has its own
// prompt template in the render package, because the change that shrinks a
// Bash command's output differs from the one that stops a session
// re-writing its cache.
type HotspotKind string

const (
	HotspotBashPattern HotspotKind = "bash_pattern"
	HotspotFileExt     HotspotKind = "file_ext"  // Read/Edit/Write/NotebookEdit by extension
	HotspotGrepGlob    HotspotKind = "grep_glob" // Grep/Glob patterns
	HotspotWebHost     HotspotKind = "web_host"
	HotspotToolPattern HotspotKind = "tool_pattern" // any other tool's (tool, detail)
	// HotspotRepeatedCacheWrite is a session that keeps re-writing its prompt
	// cache instead of reading it back.
	HotspotRepeatedCacheWrite HotspotKind = "repeated_cache_write"
)

// Repeated-cache-write thresholds: a session qualifies once it has written
// at least repeatedWriteMinTokens cache tokens and those writes exceed
// repeatedWriteMinShare of its cache reads.
const (
	repeatedWriteMinTokens = int64(1_000_000)
	repeatedWriteMinShare  = 0.25
	hotspotExamples        = 5
)

// Hotspot is one ranked context-cost target. Title is what the renderer
// shows; Context is the substitution data for the prompt template.
type Hotspot struct {
	Kind  HotspotKind
	Title string // e.g. "Bash: `bd show`"
	// CostUSD is the hotspot's context cost: for a tool pattern, what its
	// results cost to write into and re-read from context; for a repeated
	// cache write, the session's cache-write cost.
	CostUSD float64
	// PctOfTotal is CostUSD as a percentage of the window total PctBasis
	// names: ContextCostTotal for a tool pattern, every session's
	// cache-write cost for a repeated cache write.
	PctOfTotal float64
	PctBasis   string
	// Examples are up to five invocations behind the card, biggest context
	// first.
	Examples []ContextExample

	// Context carries everything the prompt template needs, per kind.
	// Tool patterns: "tool", "pattern", "calls", "context_tokens",
	// "rereads", "write_cost", "read_cost". Repeated cache write:
	// "session_id", "project", "write_tokens", "read_tokens", "write_cost",
	// "write_pct_of_read", "turns".
	Context map[string]any
}

// PctBasis values: what a hotspot's PctOfTotal is a share of.
const (
	PctOfContextCost    = "context cost"
	PctOfCacheWriteCost = "cache-write cost"
)

// HotspotKey joins a hotspot across two windows.
type HotspotKey struct {
	Kind  HotspotKind
	Title string
}

// Hotspots returns the top-N context-cost hotspots, highest first, each
// with up to five example invocations.
func (a *Aggregator) Hotspots(top int) []Hotspot {
	if top <= 0 {
		return nil
	}
	caches := a.sessionCaches()
	cands := append(a.toolHotspots(), repeatedCacheWrites(caches, a.bySession)...)
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].CostUSD != cands[j].CostUSD {
			return cands[i].CostUSD > cands[j].CostUSD
		}
		return cands[i].Title < cands[j].Title
	})
	if len(cands) > top {
		cands = cands[:top]
	}
	contextTotal := a.ContextCostTotal().ContextCostUSD
	var cacheWriteTotal float64
	for _, sc := range caches {
		cacheWriteTotal += sc.writeCost
	}
	evidence := a.contextEvidence(hotspotExamples)
	for i := range cands {
		h := &cands[i]
		total := contextTotal
		h.PctBasis = PctOfContextCost
		if h.Kind == HotspotRepeatedCacheWrite {
			total = cacheWriteTotal
			h.PctBasis = PctOfCacheWriteCost
		}
		if total > 0 {
			h.PctOfTotal = 100 * h.CostUSD / total
		}
		switch h.Kind {
		case HotspotRepeatedCacheWrite:
			h.Examples = a.cacheWriteExamples(h.Context["session_id"].(string), hotspotExamples)
		default:
			if ev := evidence[toolDetailKey{h.Context["tool"].(string), h.Context["pattern"].(string)}]; ev != nil {
				h.Examples = ev.examples
				h.Context["calls"] = ev.calls
			}
		}
	}
	return cands
}

// HotspotCosts returns, by key and uncapped, the cost of everything that
// could be a hotspot card in another window, so that window can show each
// card's change against this one. A session's cache-write cost is included
// whether or not it crosses the repeated-cache-write thresholds here.
func (a *Aggregator) HotspotCosts() map[HotspotKey]float64 {
	out := map[HotspotKey]float64{}
	for _, h := range a.toolHotspots() {
		out[HotspotKey{h.Kind, h.Title}] = h.CostUSD
	}
	for id, sc := range a.sessionCaches() {
		if sc.writeCost > 0 {
			out[HotspotKey{HotspotRepeatedCacheWrite, repeatedCacheWriteTitle(id)}] = sc.writeCost
		}
	}
	return out
}

// skipHotspotTool lists tools with no output-shrinking change to ask for:
// a Skill or slash command's cost is its instructions, an Agent's is its
// own session.
var skipHotspotTool = map[string]bool{"Skill": true, "SlashCommand": true, "Agent": true}

// toolHotspots is every tool-pattern hotspot without examples or share.
func (a *Aggregator) toolHotspots() []Hotspot {
	var cands []Hotspot
	for tool, rows := range a.ContextCostByToolDetail() {
		if skipHotspotTool[tool] {
			continue
		}
		for _, r := range rows {
			if r.ContextCostUSD <= 0 {
				continue
			}
			kind, title := classifyDetail(tool, r.Detail)
			cands = append(cands, Hotspot{
				Kind:    kind,
				Title:   title,
				CostUSD: r.ContextCostUSD,
				Context: map[string]any{
					"tool":           tool,
					"pattern":        r.Detail,
					"context_tokens": r.ContextTokens,
					"rereads":        r.RereadTurns,
					"write_cost":     r.WriteCostUSD,
					"read_cost":      r.ReadCostUSD,
				},
			})
		}
	}
	return cands
}

// sessionCache is one session's cache traffic over its counted turns.
type sessionCache struct {
	write, read int64
	writeCost   float64
	turns       int
}

// sessionCaches returns each session's cache traffic over its counted turns.
func (a *Aggregator) sessionCaches() map[string]*sessionCache {
	bySession := map[string]*sessionCache{}
	for _, turns := range a.ctxTurns {
		for _, c := range turns {
			if c.sessionID == "" {
				continue
			}
			sc := bySession[c.sessionID]
			if sc == nil {
				sc = &sessionCache{}
				bySession[c.sessionID] = sc
			}
			sc.write += int64(c.usage.CacheCreate5mTokens + c.usage.CacheCreate1hTokens)
			sc.read += int64(c.usage.CacheReadTokens)
			sc.writeCost += a.cacheWriteCost(c)
			sc.turns++
		}
	}
	return bySession
}

// repeated reports whether sc's cache writes are at least
// repeatedWriteMinTokens and exceed repeatedWriteMinShare of its cache
// reads — a cache that keeps being rebuilt instead of re-read.
func (sc *sessionCache) repeated() bool {
	return sc.write >= repeatedWriteMinTokens && float64(sc.write) > repeatedWriteMinShare*float64(sc.read)
}

func repeatedCacheWriteTitle(session string) string {
	return "Repeated cache writes: session " + session
}

// repeatedCacheWrites returns a card per session in caches whose writes are
// repeated.
func repeatedCacheWrites(caches map[string]*sessionCache, sessions map[string]*SessionBucket) []Hotspot {
	var out []Hotspot
	for id, sc := range caches {
		if !sc.repeated() {
			continue
		}
		var project string
		if sb := sessions[id]; sb != nil {
			project = sb.Project
		}
		pctOfRead := 0.0
		if sc.read > 0 {
			pctOfRead = 100 * float64(sc.write) / float64(sc.read)
		}
		out = append(out, Hotspot{
			Kind:    HotspotRepeatedCacheWrite,
			Title:   repeatedCacheWriteTitle(id),
			CostUSD: sc.writeCost,
			Context: map[string]any{
				"session_id":        id,
				"project":           project,
				"write_tokens":      sc.write,
				"read_tokens":       sc.read,
				"write_cost":        sc.writeCost,
				"write_pct_of_read": pctOfRead,
				"turns":             sc.turns,
			},
		})
	}
	return out
}

// cacheWriteCost prices c's cache-creation tokens at its effective rate.
func (a *Aggregator) cacheWriteCost(c contextTurn) float64 {
	u := c.usage
	if u.CacheCreate5mTokens == 0 && u.CacheCreate1hTokens == 0 {
		return 0
	}
	prompt := u.InputTokens + u.CacheCreate5mTokens + u.CacheCreate1hTokens + u.CacheReadTokens
	rate, _ := a.prices.EffectiveRate(c.provider, c.model, c.ts, prompt, u.Speed)
	return (float64(u.CacheCreate5mTokens)*rate.CacheWrite5m + float64(u.CacheCreate1hTokens)*rate.CacheWrite1h) / 1e6
}

// cacheWriteExamples returns session's n biggest cache-writing turns, each
// described with how long after the previous turn it came — a gap past
// the cache TTL is why a prefix gets written again.
func (a *Aggregator) cacheWriteExamples(session string, n int) []ContextExample {
	var top []ContextExample
	for file, turns := range a.ctxTurns {
		turns = append([]contextTurn(nil), turns...)
		sort.SliceStable(turns, func(i, j int) bool { return turns[i].ts.Before(turns[j].ts) })
		for i, c := range turns {
			if c.sessionID != session {
				continue
			}
			w := int64(c.usage.CacheCreate5mTokens + c.usage.CacheCreate1hTokens)
			if w == 0 {
				continue
			}
			when := "at session start"
			if i > 0 && !c.ts.IsZero() && !turns[i-1].ts.IsZero() {
				when = c.ts.Sub(turns[i-1].ts).Round(time.Second).String() + " after the previous turn"
			}
			top = insertTopExample(top, ContextExample{
				Input:          fmt.Sprintf("turn at %s wrote %s cache tokens, %s", c.ts.UTC().Format("2006-01-02 15:04:05 UTC"), groupThousands(w), when),
				SessionID:      session,
				SourceFile:     file,
				Provider:       c.provider,
				ContextTokens:  w,
				ContextCostUSD: a.cacheWriteCost(c),
			}, n)
		}
	}
	return top
}

// groupThousands renders n with comma thousands separators.
func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := s[0] == '-'
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

// classifyDetail picks a Hotspot kind and title from (tool, detail) so the
// renderer can pick the right prompt template.
func classifyDetail(tool, detail string) (HotspotKind, string) {
	switch tool {
	case "Bash", "Monitor":
		return HotspotBashPattern, tool + ": `" + detail + "`"
	case "Read", "Edit", "Write", "NotebookEdit":
		return HotspotFileExt, tool + " of `" + detail + "` files"
	case "Grep", "Glob":
		return HotspotGrepGlob, tool + ": `" + detail + "`"
	case "WebFetch":
		return HotspotWebHost, "WebFetch: " + detail
	}
	return HotspotToolPattern, tool + ": `" + detail + "`"
}
