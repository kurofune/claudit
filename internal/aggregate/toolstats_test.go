package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// toolStatsFixture: one turn calls Bash three times and Read once. The
// turn's last line lands at end. Bash `go test` errors after 300ms, Bash
// `git status` succeeds after 100ms, Bash `ls` has no result line at all,
// and Read succeeds after 2s.
func toolStatsFixture(t *testing.T) *Aggregator {
	t.Helper()
	start := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Second)
	a := New(ctxTable())
	turn := ctxTurn("f", "s", "t1", "m", start, parse.Usage{},
		parse.ToolUse{ID: "b1", Name: "Bash", Detail: "go test"},
		parse.ToolUse{ID: "b2", Name: "Bash", Detail: "git status"},
		parse.ToolUse{ID: "b3", Name: "Bash", Detail: "ls"},
		parse.ToolUse{ID: "r1", Name: "Read", Detail: ".go"},
	)
	turn.EndTimestamp = end
	a.Add(turn)
	a.AddToolResults([]parse.ToolResult{
		{ToolUseID: "b1", IsError: true, Timestamp: end.Add(300 * time.Millisecond)},
		{ToolUseID: "b2", Timestamp: end.Add(100 * time.Millisecond)},
		{ToolUseID: "r1", Timestamp: end.Add(2 * time.Second)},
	})
	return a
}

func TestToolCallStats_ErrorCountsJoinedErrors(t *testing.T) {
	byTool, _ := toolStatsFixture(t).ToolCallStats()
	bash := byTool["Bash"]
	if bash.Calls != 3 {
		t.Errorf("Bash calls = %d, want 3", bash.Calls)
	}
	if bash.ErrorCount != 1 {
		t.Errorf("Bash error_count = %d, want 1", bash.ErrorCount)
	}
	if read := byTool["Read"]; read.ErrorCount != 0 || read.Calls != 1 {
		t.Errorf("Read = %+v, want 1 call, 0 errors", read)
	}
}

func TestToolCallStats_UnjoinedCallLeavesErrorRateDenominator(t *testing.T) {
	byTool, byDetail := toolStatsFixture(t).ToolCallStats()
	bash := byTool["Bash"]
	if bash.Results != 2 {
		t.Errorf("Bash results = %d, want 2 (the unjoined `ls` is excluded)", bash.Results)
	}
	if !approx(bash.ErrorRate, 0.5) {
		t.Errorf("Bash error_rate = %v, want 0.5 (1 error / 2 joined results)", bash.ErrorRate)
	}
	ls := byDetail["Bash"]["ls"]
	if ls.Calls != 1 || ls.Results != 0 || ls.ErrorRate != 0 {
		t.Errorf("Bash·ls = %+v, want 1 call, 0 results, rate 0", ls)
	}
	if gt := byDetail["Bash"]["go test"]; gt.ErrorCount != 1 || !approx(gt.ErrorRate, 1) {
		t.Errorf("Bash·go test = %+v, want 1 error, rate 1", gt)
	}
}

func TestToolCallStats_DurationFromTurnEndToResult(t *testing.T) {
	byTool, byDetail := toolStatsFixture(t).ToolCallStats()
	bash := byTool["Bash"]
	if bash.TimedCalls != 2 {
		t.Errorf("Bash timed calls = %d, want 2 (the unjoined `ls` contributes no duration)", bash.TimedCalls)
	}
	// Durations 100ms and 300ms; nearest-rank median is the 1st of 2.
	if bash.DurationMedianMs != 100 || bash.DurationP95Ms != 300 {
		t.Errorf("Bash median/p95 = %d/%d, want 100/300", bash.DurationMedianMs, bash.DurationP95Ms)
	}
	if read := byTool["Read"]; read.DurationMedianMs != 2000 || read.DurationP95Ms != 2000 {
		t.Errorf("Read median/p95 = %d/%d, want 2000/2000 (result − turn EndTimestamp)", read.DurationMedianMs, read.DurationP95Ms)
	}
	if ls := byDetail["Bash"]["ls"]; ls.TimedCalls != 0 || ls.DurationMedianMs != 0 || ls.DurationP95Ms != 0 {
		t.Errorf("Bash·ls = %+v, want no duration", ls)
	}
	if gs := byDetail["Bash"]["git status"]; gs.DurationMedianMs != 100 {
		t.Errorf("Bash·git status median = %d, want 100", gs.DurationMedianMs)
	}
}

// A result with no timestamp joins (it counts toward error rate) but has no
// end to measure from, so it adds no duration.
func TestToolCallStats_UntimedResultCountsButHasNoDuration(t *testing.T) {
	start := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := New(ctxTable())
	a.Add(ctxTurn("f", "s", "t1", "m", start, parse.Usage{}, parse.ToolUse{ID: "g1", Name: "Grep"}))
	a.AddToolResults([]parse.ToolResult{{ToolUseID: "g1", IsError: true}})
	g := mustStats(t, a, "Grep")
	if g.Results != 1 || g.ErrorCount != 1 || g.TimedCalls != 0 {
		t.Errorf("Grep = %+v, want 1 result, 1 error, 0 timed", g)
	}
}

func TestToolCallStats_PercentilesNearestRank(t *testing.T) {
	start := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := New(ctxTable())
	var results []parse.ToolResult
	for i := 1; i <= 20; i++ {
		id := string(rune('a'+i)) + "x"
		turn := ctxTurn("f", "s", "t"+id, "m", start.Add(time.Duration(i)*time.Minute), parse.Usage{}, parse.ToolUse{ID: id, Name: "Bash"})
		a.Add(turn)
		results = append(results, parse.ToolResult{ToolUseID: id, Timestamp: turn.Timestamp.Add(time.Duration(i) * 10 * time.Millisecond)})
	}
	a.AddToolResults(results)
	b := mustStats(t, a, "Bash")
	if b.DurationMedianMs != 100 || b.DurationP95Ms != 190 {
		t.Errorf("median/p95 = %d/%d, want 100/190 over 10..200ms", b.DurationMedianMs, b.DurationP95Ms)
	}
}

// A result line stamped before the calling turn's last line (the tool ran
// while the turn was still streaming) clamps to 0, never negative.
func TestToolCallStats_ResultBeforeTurnEndClampsToZero(t *testing.T) {
	start := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	a := New(ctxTable())
	turn := ctxTurn("f", "s", "t1", "m", start, parse.Usage{}, parse.ToolUse{ID: "x", Name: "Bash"})
	turn.EndTimestamp = start.Add(time.Second)
	a.Add(turn)
	a.AddToolResults([]parse.ToolResult{{ToolUseID: "x", Timestamp: start.Add(500 * time.Millisecond)}})
	if b := mustStats(t, a, "Bash"); b.TimedCalls != 1 || b.DurationMedianMs != 0 {
		t.Errorf("Bash = %+v, want 1 timed call at 0ms", b)
	}
}

func mustStats(t *testing.T, a *Aggregator, tool string) ToolCallStats {
	t.Helper()
	byTool, _ := a.ToolCallStats()
	s, ok := byTool[tool]
	if !ok {
		t.Fatalf("no stats for %s", tool)
	}
	return s
}
