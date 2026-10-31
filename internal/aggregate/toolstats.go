package aggregate

import (
	"math"
	"sort"
	"time"
)

// ToolCallStats is what a tool's (or pattern's) calls did once joined to
// their tool_result: how often they failed and how long they ran.
type ToolCallStats struct {
	Calls int
	// Results counts calls whose tool_result joined — the denominator of
	// ErrorRate. A call with no joined result is neither ok nor an error.
	Results    int
	ErrorCount int
	// ErrorRate is ErrorCount / Results, 0 when no result joined.
	ErrorRate float64
	// TimedCalls counts calls with a measurable duration: a joined result
	// that carried a timestamp. Duration is the result's arrival minus the
	// calling turn's EndTimestamp (its last streamed line), clamped at 0.
	TimedCalls int
	// DurationMedianMs and DurationP95Ms are nearest-rank percentiles over
	// the timed calls; 0 when TimedCalls is 0.
	DurationMedianMs int64
	DurationP95Ms    int64
}

type callAccum struct {
	calls, results, errors int
	durations              []int64
}

func (c *callAccum) add(ms int64, joined, timed, isErr bool) {
	c.calls++
	if !joined {
		return
	}
	c.results++
	if isErr {
		c.errors++
	}
	if timed {
		c.durations = append(c.durations, ms)
	}
}

func (c *callAccum) stats() ToolCallStats {
	s := ToolCallStats{Calls: c.calls, Results: c.results, ErrorCount: c.errors, TimedCalls: len(c.durations)}
	if c.results > 0 {
		s.ErrorRate = float64(c.errors) / float64(c.results)
	}
	if len(c.durations) > 0 {
		sort.Slice(c.durations, func(i, j int) bool { return c.durations[i] < c.durations[j] })
		s.DurationMedianMs = nearestRank(c.durations, 0.50)
		s.DurationP95Ms = nearestRank(c.durations, 0.95)
	}
	return s
}

// nearestRank returns the ⌈p·n⌉-th smallest of sorted.
func nearestRank(sorted []int64, p float64) int64 {
	k := int(math.Ceil(p * float64(len(sorted))))
	if k < 1 {
		k = 1
	}
	return sorted[k-1]
}

// ToolCallStats returns error and duration stats per tool and per (tool,
// detail) pattern over every counted call. Calls whose tool_use carried no
// Detail appear only in the per-tool map, as in ByToolDetail.
func (a *Aggregator) ToolCallStats() (map[string]ToolCallStats, map[string]map[string]ToolCallStats) {
	// One map for both levels: a tool's own row is keyed with detail "".
	acc := map[toolDetailKey]*callAccum{}
	add := func(k toolDetailKey, ms int64, joined, timed, isErr bool) {
		c := acc[k]
		if c == nil {
			c = &callAccum{}
			acc[k] = c
		}
		c.add(ms, joined, timed, isErr)
	}
	for _, turns := range a.ctxTurns {
		for _, t := range turns {
			end := t.endTS
			if end.IsZero() {
				end = t.ts
			}
			for _, tu := range t.toolUses {
				r, joined := JoinToolResult(tu, a.ctxResults)
				timed := joined && !r.Timestamp.IsZero() && !end.IsZero()
				var ms int64
				if timed {
					ms = max(r.Timestamp.Sub(end), time.Duration(0)).Milliseconds()
				}
				add(toolDetailKey{tu.Name, ""}, ms, joined, timed, r.IsError)
				if tu.Detail != "" {
					add(toolDetailKey{tu.Name, tu.Detail}, ms, joined, timed, r.IsError)
				}
			}
		}
	}
	tools := map[string]ToolCallStats{}
	details := map[string]map[string]ToolCallStats{}
	for k, c := range acc {
		if k.detail == "" {
			tools[k.tool] = c.stats()
			continue
		}
		if details[k.tool] == nil {
			details[k.tool] = map[string]ToolCallStats{}
		}
		details[k.tool][k.detail] = c.stats()
	}
	return tools, details
}
