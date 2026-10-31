package aggregate

import (
	"sort"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// ContextCost is what the bytes a tool result put into context went on to
// cost: the cache write on the turn that first carried them, plus the cache
// reads of every later turn in the same session file that re-read them
// before the session ended or was compacted.
type ContextCost struct {
	// ContextTokens is the share of the attributing turn's cache-creation
	// tokens, split across the tool results it carried by OutputBytes.
	ContextTokens int64
	// RereadTurns counts the later turns that read those tokens from cache.
	RereadTurns    int
	WriteCostUSD   float64
	ReadCostUSD    float64
	ContextCostUSD float64 // WriteCostUSD + ReadCostUSD
}

func (c *ContextCost) add(o ContextCost) {
	c.ContextTokens += o.ContextTokens
	c.RereadTurns += o.RereadTurns
	c.WriteCostUSD += o.WriteCostUSD
	c.ReadCostUSD += o.ReadCostUSD
	c.ContextCostUSD += o.ContextCostUSD
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
	ts        time.Time
	model     string
	usage     parse.Usage
	toolUses  []parse.ToolUse
}

// resultContextCost is one tool result's attributed cost and its keys.
type resultContextCost struct {
	tool, detail, sessionID string
	ContextCost
}

// recordContextTurn buffers a counted turn under its session file. Only
// turns that survived the filter and replay dedup arrive here, so a
// replayed tool_use is attributed once, on its canonical copy.
func (a *Aggregator) recordContextTurn(t parse.Turn) {
	a.ctxTurns[t.SourceFile] = append(a.ctxTurns[t.SourceFile], contextTurn{
		sessionID: t.SessionID,
		ts:        t.Timestamp,
		model:     t.Model,
		usage:     t.Usage,
		toolUses:  t.ToolUses,
	})
}

// contextResult is a registered tool result: its size and the moment it
// arrived (zero when its line carried no timestamp).
type contextResult struct {
	bytes   int
	arrived time.Time
}

// AddToolResults registers tool results so ContextCost* can join them to
// the tool_use that produced them by ToolUseID. A ToolUseID seen again (a
// replayed transcript) keeps its first registration.
func (a *Aggregator) AddToolResults(results []parse.ToolResult) {
	for _, r := range results {
		if r.ToolUseID == "" {
			continue
		}
		if _, ok := a.ctxResults[r.ToolUseID]; !ok {
			a.ctxResults[r.ToolUseID] = contextResult{bytes: r.OutputBytes, arrived: r.Timestamp}
		}
	}
}

// AddCompactBoundaries registers compaction points. Results produced before
// a boundary stop accruing re-read cost on turns after it.
func (a *Aggregator) AddCompactBoundaries(bs []parse.CompactBoundary) {
	for _, b := range bs {
		if b.Timestamp.IsZero() {
			continue
		}
		a.ctxBoundaries[b.SourceFile] = append(a.ctxBoundaries[b.SourceFile], b.Timestamp)
	}
}

// pendingResult is a tool result waiting for the turn that first carries it.
type pendingResult struct {
	tool, detail, sessionID string
	bytes                   int
}

// contextCosts walks each session file's turns in time order. A tool
// result is carried by the first turn after the one that called the tool
// whose timestamp is not before the result's arrival; a result with no
// arrival time is carried by the next turn. That carrying turn's
// cache-creation tokens split across the results it carries by OutputBytes,
// written at its effective cache-write rates in its 5m/1h proportion. A
// compaction boundary between a result's arrival (or, lacking one, its
// calling turn) and its carrying turn drops the result: it never reached a
// cache write. Each later turn re-reads every live result at its own
// effective cache-read rate, and a compaction boundary ends every live
// result's re-reads. A turn's attributed reads are scaled down to its actual
// CacheReadTokens, so attribution never exceeds the cache spend it explains
// (an expired cache re-reads less than what is live).
func (a *Aggregator) contextCosts() []resultContextCost {
	var out []resultContextCost
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
				r, ok := a.ctxResults[tu.ID]
				if !ok {
					continue
				}
				j, from := i+1, c.ts.Add(time.Nanosecond)
				if !r.arrived.IsZero() {
					j = i + 1 + sort.Search(len(turns)-i-1, func(k int) bool { return !turns[i+1+k].ts.Before(r.arrived) })
					from = r.arrived
				}
				if j >= len(turns) || compactedBetween(from, turns[j].ts) {
					continue
				}
				carried[j] = append(carried[j], pendingResult{tool: tu.Name, detail: tu.Detail, sessionID: c.sessionID, bytes: r.bytes})
			}
		}

		var live []int // indexes into out
		for i, c := range turns {
			if i > 0 && compactedBetween(turns[i-1].ts.Add(time.Nanosecond), c.ts) {
				live = nil
			}
			u := c.usage
			prompt := u.InputTokens + u.CacheCreate5mTokens + u.CacheCreate1hTokens + u.CacheReadTokens
			rate, _ := a.prices.EffectiveRate(c.model, c.ts, prompt, u.Speed)
			a.chargeRereads(out, live, c, rate.CacheRead)

			cc := u.CacheCreate5mTokens + u.CacheCreate1hTokens
			writePerTok := 0.0
			if cc > 0 {
				writePerTok = (float64(u.CacheCreate5mTokens)*rate.CacheWrite5m +
					float64(u.CacheCreate1hTokens)*rate.CacheWrite1h) / float64(cc) / 1e6
			}
			totalBytes := 0
			for _, p := range carried[i] {
				totalBytes += p.bytes
			}
			for _, p := range carried[i] {
				var n int64
				if totalBytes > 0 {
					n = int64(cc) * int64(p.bytes) / int64(totalBytes)
				}
				write := float64(n) * writePerTok
				live = append(live, len(out))
				out = append(out, resultContextCost{
					tool:      p.tool,
					detail:    p.detail,
					sessionID: p.sessionID,
					ContextCost: ContextCost{
						ContextTokens:  n,
						WriteCostUSD:   write,
						ContextCostUSD: write,
					},
				})
			}
		}
	}
	return out
}

// chargeRereads bills turn c's re-read of every live result at readRate
// (USD per MTok), scaled so the total never exceeds c's CacheReadTokens.
func (a *Aggregator) chargeRereads(out []resultContextCost, live []int, c contextTurn, readRate float64) {
	var liveTokens int64
	for _, idx := range live {
		liveTokens += out[idx].ContextTokens
	}
	if liveTokens == 0 || c.usage.CacheReadTokens <= 0 {
		return
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
}

// ContextCostByTool returns per-tool context cost, highest first.
func (a *Aggregator) ContextCostByTool() []ToolContextCost {
	byTool := map[string]*ToolContextCost{}
	for _, r := range a.contextCosts() {
		row := byTool[r.tool]
		if row == nil {
			row = &ToolContextCost{Tool: r.tool}
			byTool[r.tool] = row
		}
		row.add(r.ContextCost)
	}
	out := make([]ToolContextCost, 0, len(byTool))
	for _, row := range byTool {
		out = append(out, *row)
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
	type key struct{ tool, detail string }
	byKey := map[key]*DetailContextCost{}
	for _, r := range a.contextCosts() {
		if r.detail == "" {
			continue
		}
		k := key{r.tool, r.detail}
		row := byKey[k]
		if row == nil {
			row = &DetailContextCost{Tool: r.tool, Detail: r.detail}
			byKey[k] = row
		}
		row.add(r.ContextCost)
	}
	out := map[string][]DetailContextCost{}
	for k, row := range byKey {
		out[k.tool] = append(out[k.tool], *row)
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
	bySession := map[string]*SessionContextCost{}
	for _, r := range a.contextCosts() {
		if r.sessionID == "" {
			continue
		}
		row := bySession[r.sessionID]
		if row == nil {
			row = &SessionContextCost{SessionID: r.sessionID}
			bySession[r.sessionID] = row
		}
		row.add(r.ContextCost)
	}
	out := make([]SessionContextCost, 0, len(bySession))
	for _, row := range bySession {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContextCostUSD != out[j].ContextCostUSD {
			return out[i].ContextCostUSD > out[j].ContextCostUSD
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}
