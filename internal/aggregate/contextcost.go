package aggregate

import (
	"sort"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// ContextCost is what the bytes a tool result put into context went on to
// cost: the tokens they added on the turn that first carried them, plus the
// cache reads of every later turn in the same session file that re-read them
// before the session ended or was compacted.
type ContextCost struct {
	// ContextTokens is the result's share, by OutputBytes among the tool
	// results the carrying turn carried, of the tokens that turn added to
	// context: its cache-creation tokens, or for Codex, which writes no
	// prompt cache, its uncached input tokens.
	ContextTokens int64
	// RereadTurns counts the distinct later turns that read those tokens
	// from cache; a turn re-reading several of a row's results counts once.
	RereadTurns int
	// WriteCostUSD is ContextTokens priced as what added them: the cache
	// write rate, or for Codex the input rate.
	WriteCostUSD   float64
	ReadCostUSD    float64
	ContextCostUSD float64 // WriteCostUSD + ReadCostUSD
}

// contextCostSum sums results' ContextCost. RereadTurns is not summed: a
// row's distinct re-reading turns come from the walk's rereadCounts.
type contextCostSum struct {
	ContextCost
}

func (c *contextCostSum) add(r resultContextCost) {
	c.ContextTokens += r.ContextTokens
	c.WriteCostUSD += r.WriteCostUSD
	c.ReadCostUSD += r.ReadCostUSD
	c.ContextCostUSD += r.ContextCostUSD
}

// rereadCounts is, per aggregate row key, how many distinct turns re-read at
// least one of that row's results. The walk counts each turn once per key
// it re-reads, so the counts cost one entry per key, never one per
// result-to-turn pair.
type rereadCounts struct {
	byTool    map[string]int
	byDetail  map[toolDetailKey]int
	bySession map[string]int
	total     int
}

// liveKeys is the set of row keys among the live results that hold tokens:
// exactly the rows a re-reading turn counts once.
type liveKeys struct {
	tools    map[string]struct{}
	details  map[toolDetailKey]struct{}
	sessions map[string]struct{}
	any      bool
}

func newLiveKeys() liveKeys {
	return liveKeys{tools: map[string]struct{}{}, details: map[toolDetailKey]struct{}{}, sessions: map[string]struct{}{}}
}

func (l *liveKeys) add(r *resultContextCost) {
	if r.ContextTokens == 0 {
		return
	}
	l.any = true
	l.tools[r.tool] = struct{}{}
	if r.detail != "" {
		l.details[toolDetailKey{r.tool, r.detail}] = struct{}{}
	}
	if r.sessionID != "" {
		l.sessions[r.sessionID] = struct{}{}
	}
}

// countTurn counts one re-reading turn against every live key.
func (c *rereadCounts) countTurn(l *liveKeys) {
	c.total++
	for k := range l.tools {
		c.byTool[k]++
	}
	for k := range l.details {
		c.byDetail[k]++
	}
	for k := range l.sessions {
		c.bySession[k]++
	}
}

// contextWalk is computeContextCosts' result: every attributed tool result
// and the distinct re-reading turn count of every aggregate row.
type contextWalk struct {
	results []resultContextCost
	rereads rereadCounts
}

// ToolContextCost is one tool's summed ContextCost.
type ToolContextCost struct {
	Tool string
	ContextCost
}

// DetailContextCost is one (tool, detail) pattern's summed ContextCost.
type DetailContextCost struct {
	Tool   string
	Detail string
	ContextCost
}

// SessionContextCost is one session's summed ContextCost.
type SessionContextCost struct {
	SessionID string
	ContextCost
}

// contextTurn is the slice of a counted turn the attribution walk needs.
type contextTurn struct {
	sessionID string
	uuid      string
	ts        time.Time
	endTS     time.Time
	provider  string
	model     string
	usage     parse.Usage
	toolUses  []parse.ToolUse
}

// resultContextCost is one tool result's attributed cost, its keys, and the
// evidence a hotspot card shows for it.
type resultContextCost struct {
	tool, detail, sessionID string
	// turnUUID is the turn that carried the result into context.
	turnUUID          string
	input, sourceFile string
	provider          string
	ContextCost
}

// recordContextTurn buffers a counted turn under its session file. Only
// turns that survived the filter and replay dedup arrive here, so a
// replayed tool_use is attributed once, on its canonical copy.
func (a *Aggregator) recordContextTurn(t parse.Turn) {
	a.invalidateContextCosts()
	a.ctxTurns[t.SourceFile] = append(a.ctxTurns[t.SourceFile], contextTurn{
		sessionID: t.SessionID,
		uuid:      t.UUID,
		ts:        t.Timestamp,
		endTS:     t.EndTimestamp,
		provider:  pricing.PriceProvider(t.Provider),
		model:     t.Model,
		usage:     t.Usage,
		toolUses:  t.ToolUses,
	})
}

// AddToolResults registers tool results so ContextCost* and ToolCallStats
// can join them to the tool_use that produced them by ToolUseID. A
// ToolUseID seen again (a replayed transcript) keeps its first
// registration. The content snippet is dropped: nothing here reads it.
func (a *Aggregator) AddToolResults(results []parse.ToolResult) {
	a.invalidateContextCosts()
	for _, r := range results {
		if r.ToolUseID == "" {
			continue
		}
		if _, ok := a.ctxResults[r.ToolUseID]; !ok {
			r.Content = ""
			a.ctxResults[r.ToolUseID] = r
		}
	}
}

// AddCompactBoundaries registers compaction points. Results produced before
// a boundary stop accruing re-read cost on turns after it.
func (a *Aggregator) AddCompactBoundaries(bs []parse.CompactBoundary) {
	a.invalidateContextCosts()
	for _, b := range bs {
		if b.Timestamp.IsZero() {
			continue
		}
		a.ctxBoundaries[b.SourceFile] = append(a.ctxBoundaries[b.SourceFile], b.Timestamp)
	}
}

// pendingResult is a tool result waiting for the turn that first carries it.
type pendingResult struct {
	tool, detail, sessionID, input, provider string
	bytes                                    int
}

func (a *Aggregator) invalidateContextCosts() {
	a.ctxMu.Lock()
	a.ctxMemo, a.ctxMemoOK = contextWalk{}, false
	a.ctxMu.Unlock()
}

// contextCosts is computeContextCosts memoized until the next Add or
// registration, so every ContextCost* accessor and Hotspots share one walk.
// Callers must treat the returned walk as read-only.
func (a *Aggregator) contextCosts() contextWalk {
	a.ctxMu.Lock()
	defer a.ctxMu.Unlock()
	if !a.ctxMemoOK {
		a.ctxMemo, a.ctxMemoOK = a.computeContextCosts(), true
	}
	return a.ctxMemo
}

// computeContextCosts walks each session file's turns in time order. A tool
// result is carried by the first turn after the one that called the tool
// whose timestamp is not before the result's arrival; a result with no
// arrival time is carried by the next turn. The tokens that carrying turn
// added (addedContext) split across the results it carries by OutputBytes,
// at its effective rate for what added them. A compaction boundary between
// a result's arrival (or, lacking one, its calling turn) and its carrying
// turn drops the result: it never reached a prompt. Each later turn re-reads
// every live result at its own effective cache-read rate, and a compaction
// boundary ends every live result's re-reads. A turn's attributed reads are
// scaled down to its actual CacheReadTokens, and its added tokens are what
// it paid for, so attribution never exceeds the prompt spend it explains (an
// expired cache re-reads less than what is live). A turn that re-reads
// counts once toward every row key among the live results holding tokens.
func (a *Aggregator) computeContextCosts() contextWalk {
	var out []resultContextCost
	counts := rereadCounts{byTool: map[string]int{}, byDetail: map[toolDetailKey]int{}, bySession: map[string]int{}}
	for file, turns := range a.ctxTurns {
		turns = append([]contextTurn(nil), turns...)
		sort.SliceStable(turns, func(i, j int) bool { return turns[i].ts.Before(turns[j].ts) })
		bounds := append([]time.Time(nil), a.ctxBoundaries[file]...)
		sort.Slice(bounds, func(i, j int) bool { return bounds[i].Before(bounds[j]) })
		compactedBetween := func(from, to time.Time) bool {
			k := sort.Search(len(bounds), func(k int) bool { return !bounds[k].Before(from) })
			return k < len(bounds) && !bounds[k].After(to)
		}

		carried := make([][]pendingResult, len(turns))
		for i, c := range turns {
			for _, tu := range c.toolUses {
				r, ok := JoinToolResult(tu, a.ctxResults)
				if !ok {
					continue
				}
				j, from := i+1, c.ts.Add(time.Nanosecond)
				if !r.Timestamp.IsZero() {
					j = i + 1 + sort.Search(len(turns)-i-1, func(k int) bool { return !turns[i+1+k].ts.Before(r.Timestamp) })
					from = r.Timestamp
				}
				if j >= len(turns) || compactedBetween(from, turns[j].ts) {
					continue
				}
				carried[j] = append(carried[j], pendingResult{tool: tu.Name, detail: tu.Detail, sessionID: c.sessionID, input: tu.Input, provider: c.provider, bytes: r.OutputBytes})
			}
		}

		var live []int // indexes into out
		keys := newLiveKeys()
		for i, c := range turns {
			if i > 0 && compactedBetween(turns[i-1].ts.Add(time.Nanosecond), c.ts) {
				live, keys = nil, newLiveKeys()
			}
			u := c.usage
			prompt := u.InputTokens + u.CacheCreate5mTokens + u.CacheCreate1hTokens + u.CacheReadTokens
			rate, _ := a.prices.EffectiveRate(c.provider, c.model, c.ts, prompt, u.Speed)
			if chargeRereads(out, live, c, rate.CacheRead) {
				counts.countTurn(&keys)
			}

			added, writePerTok := addedContext(c, rate)
			totalBytes := 0
			for _, p := range carried[i] {
				totalBytes += p.bytes
			}
			for _, p := range carried[i] {
				var n int64
				if totalBytes > 0 {
					n = added * int64(p.bytes) / int64(totalBytes)
				}
				write := float64(n) * writePerTok
				live = append(live, len(out))
				out = append(out, resultContextCost{
					tool:       p.tool,
					detail:     p.detail,
					sessionID:  p.sessionID,
					turnUUID:   c.uuid,
					input:      p.input,
					sourceFile: file,
					provider:   p.provider,
					ContextCost: ContextCost{
						ContextTokens:  n,
						WriteCostUSD:   write,
						ContextCostUSD: write,
					},
				})
				keys.add(&out[len(out)-1])
			}
		}
	}
	return contextWalk{results: out, rereads: counts}
}

// addedContext is how many tokens turn c added to context, which the tool
// results it carried split, and what each cost (USD per token) at rate. A
// provider with a prompt cache adds what it wrote to that cache, at the
// cache-write rates in the turn's 5m/1h proportion. Codex writes no cache:
// a token is billed once as uncached input and afterwards as a cached
// re-read, so what it adds is its uncached input at the input rate. This
// branches on the provider, not on a zero cache write: a Claude Code turn
// that wrote no cache added nothing to it, whatever its uncached input.
func addedContext(c contextTurn, rate pricing.Rate) (tokens int64, usdPerTok float64) {
	u := c.usage
	if c.provider == parse.ProviderCodex {
		return int64(u.InputTokens), rate.Input / 1e6
	}
	cc := u.CacheCreate5mTokens + u.CacheCreate1hTokens
	if cc <= 0 {
		return 0, 0
	}
	return int64(cc), (float64(u.CacheCreate5mTokens)*rate.CacheWrite5m +
		float64(u.CacheCreate1hTokens)*rate.CacheWrite1h) / float64(cc) / 1e6
}

// chargeRereads bills turn c's re-read of every live result at readRate
// (USD per MTok), scaled so the total never exceeds c's CacheReadTokens,
// and reports whether c re-read anything.
func chargeRereads(out []resultContextCost, live []int, c contextTurn, readRate float64) bool {
	var liveTokens int64
	for _, idx := range live {
		liveTokens += out[idx].ContextTokens
	}
	if liveTokens == 0 || c.usage.CacheReadTokens <= 0 {
		return false
	}
	scale := 1.0
	if cr := int64(c.usage.CacheReadTokens); cr < liveTokens {
		scale = float64(cr) / float64(liveTokens)
	}
	for _, idx := range live {
		r := &out[idx]
		if r.ContextTokens == 0 {
			continue
		}
		read := float64(r.ContextTokens) * scale * readRate / 1e6
		r.ReadCostUSD += read
		r.ContextCostUSD += read
		r.RereadTurns++
	}
	return true
}

// ContextCostByTool returns per-tool context cost, highest first.
func (a *Aggregator) ContextCostByTool() []ToolContextCost {
	walk := a.contextCosts()
	byTool := map[string]*contextCostSum{}
	for _, r := range walk.results {
		row := byTool[r.tool]
		if row == nil {
			row = &contextCostSum{}
			byTool[r.tool] = row
		}
		row.add(r)
	}
	out := make([]ToolContextCost, 0, len(byTool))
	for tool, row := range byTool {
		row.RereadTurns = walk.rereads.byTool[tool]
		out = append(out, ToolContextCost{Tool: tool, ContextCost: row.ContextCost})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContextCostUSD != out[j].ContextCostUSD {
			return out[i].ContextCostUSD > out[j].ContextCostUSD
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}

// ContextCostByToolDetail returns, per tool, its (tool, detail) rows highest
// first. Results whose tool_use carried no Detail are absent, as in
// ByToolDetail.
func (a *Aggregator) ContextCostByToolDetail() map[string][]DetailContextCost {
	walk := a.contextCosts()
	byKey := map[toolDetailKey]*contextCostSum{}
	for _, r := range walk.results {
		if r.detail == "" {
			continue
		}
		k := toolDetailKey{r.tool, r.detail}
		row := byKey[k]
		if row == nil {
			row = &contextCostSum{}
			byKey[k] = row
		}
		row.add(r)
	}
	out := map[string][]DetailContextCost{}
	for k, row := range byKey {
		row.RereadTurns = walk.rereads.byDetail[k]
		out[k.tool] = append(out[k.tool], DetailContextCost{Tool: k.tool, Detail: k.detail, ContextCost: row.ContextCost})
	}
	for _, rows := range out {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].ContextCostUSD != rows[j].ContextCostUSD {
				return rows[i].ContextCostUSD > rows[j].ContextCostUSD
			}
			return rows[i].Detail < rows[j].Detail
		})
	}
	return out
}

// ContextCostBySession returns per-session context cost, highest first.
// Results from turns without a session id are absent.
func (a *Aggregator) ContextCostBySession() []SessionContextCost {
	walk := a.contextCosts()
	bySession := map[string]*contextCostSum{}
	for _, r := range walk.results {
		if r.sessionID == "" {
			continue
		}
		row := bySession[r.sessionID]
		if row == nil {
			row = &contextCostSum{}
			bySession[r.sessionID] = row
		}
		row.add(r)
	}
	out := make([]SessionContextCost, 0, len(bySession))
	for id, row := range bySession {
		row.RereadTurns = walk.rereads.bySession[id]
		out = append(out, SessionContextCost{SessionID: id, ContextCost: row.ContextCost})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContextCostUSD != out[j].ContextCostUSD {
			return out[i].ContextCostUSD > out[j].ContextCostUSD
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// ContextCostByTurn returns, per turn uuid of the session, the summed
// WriteCostUSD of the tool results that turn carried into context: what the
// turn added. Re-read charges stay on the session total. A turn that
// carried nothing is absent.
func (a *Aggregator) ContextCostByTurn(sessionID string) map[string]float64 {
	return a.ContextCostByTurnWhere(func(id string) bool { return id != "" && id == sessionID })
}

// ContextCostByTurnWhere is ContextCostByTurn over every session id in
// accepts: a session and its Codex sub-agent threads, say.
func (a *Aggregator) ContextCostByTurnWhere(in func(sessionID string) bool) map[string]float64 {
	out := map[string]float64{}
	for _, r := range a.contextCosts().results {
		if r.turnUUID != "" && in(r.sessionID) {
			out[r.turnUUID] += r.WriteCostUSD
		}
	}
	return out
}

// ContextCostTotal is the context cost summed over every attributed tool
// result — the denominator of a hotspot's share.
func (a *Aggregator) ContextCostTotal() ContextCost {
	walk := a.contextCosts()
	var total contextCostSum
	for _, r := range walk.results {
		total.add(r)
	}
	total.RereadTurns = walk.rereads.total
	return total.ContextCost
}

// ContextExample is one tool invocation behind a pattern: what was run,
// where, by which provider's agent, and how much context its result added.
type ContextExample struct {
	Input          string
	SessionID      string
	SourceFile     string
	Provider       string
	ContextTokens  int64
	ContextCostUSD float64
}

// detailEvidence is one (tool, detail) pattern's invocation count and its
// biggest invocations by context tokens.
type detailEvidence struct {
	calls    int
	examples []ContextExample
}

type toolDetailKey struct{ tool, detail string }

// contextEvidence returns, per (tool, detail), the invocation count and the
// top n invocations by ContextTokens (ties: session, then file).
func (a *Aggregator) contextEvidence(n int) map[toolDetailKey]*detailEvidence {
	out := map[toolDetailKey]*detailEvidence{}
	for _, r := range a.contextCosts().results {
		if r.detail == "" {
			continue
		}
		k := toolDetailKey{r.tool, r.detail}
		ev := out[k]
		if ev == nil {
			ev = &detailEvidence{}
			out[k] = ev
		}
		ev.calls++
		ev.examples = insertTopExample(ev.examples, ContextExample{
			Input:          r.input,
			SessionID:      r.sessionID,
			SourceFile:     r.sourceFile,
			Provider:       r.provider,
			ContextTokens:  r.ContextTokens,
			ContextCostUSD: r.ContextCostUSD,
		}, n)
	}
	return out
}

// insertTopExample keeps top sorted by ContextTokens descending, at most n long.
func insertTopExample(top []ContextExample, ex ContextExample, n int) []ContextExample {
	less := func(x, y ContextExample) bool {
		if x.ContextTokens != y.ContextTokens {
			return x.ContextTokens > y.ContextTokens
		}
		if x.SessionID != y.SessionID {
			return x.SessionID < y.SessionID
		}
		return x.SourceFile < y.SourceFile
	}
	if len(top) == n && !less(ex, top[n-1]) {
		return top
	}
	i := sort.Search(len(top), func(i int) bool { return less(ex, top[i]) })
	if len(top) < n {
		top = append(top, ContextExample{})
	}
	copy(top[i+1:], top[i:])
	top[i] = ex
	return top
}
