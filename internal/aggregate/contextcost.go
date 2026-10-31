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

// AddToolResults registers tool results so ContextCost* can join them to
// the tool_use that produced them by ToolUseID. A ToolUseID seen again (a
// replayed transcript) keeps its first registration.
func (a *Aggregator) AddToolResults(results []parse.ToolResult) {
	for _, r := range results {
		if r.ToolUseID == "" {
			continue
		}
		if _, ok := a.ctxResultBytes[r.ToolUseID]; !ok {
			a.ctxResultBytes[r.ToolUseID] = r.OutputBytes
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

// contextCosts walks each session file's turns in time order. A result of
// turn A's tool_use is attributed on the next turn B: B's cache-creation
// tokens split across A's results by OutputBytes, written at B's cache-write
// rates in B's 5m/1h proportion. Each later turn re-reads every live result
// at its own cache-read rate. A turn's attributed reads are scaled down to
// its actual CacheReadTokens, so attribution never exceeds the cache spend
// it explains (an expired cache re-reads less than what is live).
func (a *Aggregator) contextCosts() []resultContextCost {
	var out []resultContextCost
	for file, turns := range a.ctxTurns {
		turns = append([]contextTurn(nil), turns...)
		sort.SliceStable(turns, func(i, j int) bool { return turns[i].ts.Before(turns[j].ts) })
		bounds := append([]time.Time(nil), a.ctxBoundaries[file]...)
		sort.Slice(bounds, func(i, j int) bool { return bounds[i].Before(bounds[j]) })

		var live []int // indexes into out
		bi := 0
		for i, c := range turns {
			compacted := false
			for bi < len(bounds) && !bounds[bi].After(c.ts) {
				compacted = compacted || i > 0
				bi++
			}
			if i == 0 {
				continue
			}
			if compacted {
				live = nil
				continue
			}
			rate, _ := a.prices.RateAt(c.model, c.ts)
			a.chargeRereads(out, live, c, rate.CacheRead)

			cc := c.usage.CacheCreate5mTokens + c.usage.CacheCreate1hTokens
			writePerTok := 0.0
			if cc > 0 {
				writePerTok = (float64(c.usage.CacheCreate5mTokens)*rate.CacheWrite5m +
					float64(c.usage.CacheCreate1hTokens)*rate.CacheWrite1h) / float64(cc) / 1e6
			}
			prev := turns[i-1]
			totalBytes := 0
			for _, tu := range prev.toolUses {
				totalBytes += a.ctxResultBytes[tu.ID]
			}
			for _, tu := range prev.toolUses {
				bytes, ok := a.ctxResultBytes[tu.ID]
				if !ok {
					continue
				}
				var n int64
				if totalBytes > 0 {
					n = int64(cc) * int64(bytes) / int64(totalBytes)
				}
				write := float64(n) * writePerTok
				live = append(live, len(out))
				out = append(out, resultContextCost{
					tool:      tu.Name,
					detail:    tu.Detail,
					sessionID: prev.sessionID,
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
