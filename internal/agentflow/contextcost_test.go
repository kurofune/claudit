package agentflow

import (
	"math"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/provider"
)

// ctxTurn is a cache-writing, cache-reading turn that calls tools.
func ctxTurn(uuid, msgID, sid, src string, ts time.Time, tools ...string) parse.Turn {
	t := mkTurn(uuid, sid, src, ts)
	t.MessageID = msgID
	t.Provider = parse.ProviderClaudeCode
	t.Usage = parse.Usage{InputTokens: 10, CacheCreate5mTokens: 4_000, CacheReadTokens: 20_000, OutputTokens: 50}
	for _, id := range tools {
		t.ToolUses = append(t.ToolUses, parse.ToolUse{ID: id, Name: "Bash", Detail: "go", Input: "go test " + id})
	}
	return t
}

func ctxResult(id string, bytes int, ts time.Time) parse.ToolResult {
	return parse.ToolResult{ToolUseID: id, OutputBytes: bytes, Timestamp: ts}
}

// contextCorpus holds what the per-file attribution walk is sensitive to:
// a result that arrives after a later turn, a compaction boundary, a
// sub-agent file, a resumed session whose replays of its original lose to
// the original's smaller path, a resume whose smaller path wins the
// replays from its original, and a Codex sub-agent thread.
func contextCorpus() *corpus.Snapshot {
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	a, sub, b := "/p/a.jsonl", "/p/a/subagents/agent-x.jsonl", "/p/b.jsonl"
	c, z := "/p/c.jsonl", "/p/0z.jsonl"
	cx, cxChild := "/codex/rollout-P.jsonl", "/codex/rollout-T.jsonl"

	turns := []parse.Turn{
		// Session A: tA1's result arrives after a2, so a3 carries it.
		ctxTurn("a1", "m-a1", "A", a, at(0), "tA1"),
		ctxTurn("a2", "m-a2", "A", a, at(2), "tA2"),
		ctxTurn("a3", "m-a3", "A", a, at(6)),
		// Compaction at 7: tA4 is carried by a5 after it, tA3's re-reads end.
		ctxTurn("a4", "m-a4", "A", a, at(6.5), "tA4"),
		ctxTurn("a5", "m-a5", "A", a, at(9)),
		ctxTurn("a6", "m-a6", "A", a, at(10), "tA6"),
		ctxTurn("a7", "m-a7", "A", a, at(11)),
		// A's sub-agent.
		ctxTurn("x1", "m-x1", "A", sub, at(3), "tX1"),
		ctxTurn("x2", "m-x2", "A", sub, at(4)),
		// Session B resumes A: its replays of a1, a2 lose to /p/a.jsonl.
		ctxTurn("a1", "m-a1", "B", b, at(0), "tA1"),
		ctxTurn("a2", "m-a2", "B", b, at(2), "tA2"),
		ctxTurn("b1", "m-b1", "B", b, at(20), "tB1"),
		ctxTurn("b2", "m-b2", "B", b, at(21)),
		// Session C, resumed as Z, whose smaller path keeps the shared turns.
		ctxTurn("c1", "m-c1", "C", c, at(30), "tC1"),
		ctxTurn("c2", "m-c2", "C", c, at(31)),
		ctxTurn("c3", "m-c3", "C", c, at(32), "tC3"),
		ctxTurn("c4", "m-c4", "C", c, at(33)),
		ctxTurn("c1", "m-c1", "Z", z, at(30), "tC1"),
		ctxTurn("c2", "m-c2", "Z", z, at(31)),
		ctxTurn("z1", "m-z1", "Z", z, at(40), "tZ1"),
		ctxTurn("z2", "m-z2", "Z", z, at(41)),
	}
	codexTurn := func(uuid, sid, src string, s float64, side bool, tools ...string) parse.Turn {
		t := ctxTurn(uuid, "", sid, src, at(s), tools...)
		t.Provider, t.Model, t.Sidechain = parse.ProviderCodex, "gpt-6-astra", side
		t.Usage = parse.Usage{InputTokens: 3_000, CacheReadTokens: 9_000, OutputTokens: 40}
		return t
	}
	turns = append(turns,
		codexTurn("p1", "P", cx, 50, false, "tP1"),
		codexTurn("p2", "P", cx, 52, false),
		codexTurn("t1", "T", cxChild, 51, true, "tT1"),
		codexTurn("t2", "T", cxChild, 53, true, "tT2"),
		codexTurn("t3", "T", cxChild, 55, true),
	)
	results := []parse.ToolResult{
		ctxResult("tA1", 900, at(5)), ctxResult("tA2", 300, at(3)),
		ctxResult("tA4", 500, at(6.8)), ctxResult("tA6", 700, time.Time{}),
		ctxResult("tX1", 200, at(3.5)),
		ctxResult("tA1", 900, at(5)), ctxResult("tA2", 300, at(3)),
		ctxResult("tB1", 400, at(20.5)),
		ctxResult("tC1", 800, at(30.5)), ctxResult("tC3", 100, at(32.5)),
		ctxResult("tC1", 800, at(30.5)), ctxResult("tZ1", 600, at(40.5)),
		ctxResult("tP1", 1_200, at(51.5)), ctxResult("tT1", 250, at(52)), ctxResult("tT2", 350, at(54)),
	}
	return &corpus.Snapshot{
		Turns:             turns,
		ToolResults:       results,
		Links:             []parse.ParentLink{{UUID: "T", ParentUUID: "P"}},
		CompactBoundaries: []parse.CompactBoundary{{SessionID: "A", SourceFile: a, Timestamp: at(7)}},
	}
}

// assertContextCostMatches compares, for every session of snap, the
// session-local per-turn context cost with the whole-corpus Aggregator's
// and returns how many sessions and non-zero turns it compared.
func assertContextCostMatches(t *testing.T, snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter) (sessions, nonZero int) {
	t.Helper()
	replays := aggregate.BuildReplaySet(snap.Turns)
	agg := aggregate.New(prices).WithFilter(f).WithReplaySet(replays)
	for _, turn := range snap.Turns {
		agg.Add(turn)
	}
	agg.AddToolResults(snap.ToolResults)
	agg.AddCompactBoundaries(snap.CompactBoundaries)

	l := NewLayout(snap, &replays)
	for _, id := range l.Sessions() {
		got := SessionContextCost(snap, prices, f, id, l)
		want := agg.ContextCostByTurnWhere(InSession(snap, id))
		keys := map[string]bool{}
		for k := range got {
			keys[k] = true
		}
		for k := range want {
			keys[k] = true
		}
		for k := range keys {
			g, gok := got[k]
			w, wok := want[k]
			if gok != wok || math.Abs(g-w) > 1e-9 {
				t.Errorf("session %s turn %s: session-local %v (%v), aggregator %v (%v)", id, k, g, gok, w, wok)
			}
			if w > 0 {
				nonZero++
			}
		}
		sessions++
	}
	return sessions, nonZero
}

// TestSessionContextCost_MatchesAggregator: every session's session-local
// per-turn context cost equals the whole-corpus Aggregator's, key for key,
// over a synthetic corpus and, with CLAUDIT_CORPUS set, a real projects
// root.
func TestSessionContextCost_MatchesAggregator(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	snap := contextCorpus()
	for _, f := range []aggregate.Filter{{}, {Since: time.Date(2026, 5, 1, 12, 0, 2, 0, time.UTC)}} {
		sessions, nonZero := assertContextCostMatches(t, snap, prices, f)
		if sessions != 5 {
			t.Errorf("filter %+v: compared %d sessions, want A B C Z and P (T folds into P)", f, sessions)
		}
		if nonZero < 8 {
			t.Errorf("filter %+v: only %d non-zero turns compared; the fixture lost its signal", f, nonZero)
		}
	}

	root := os.Getenv("CLAUDIT_CORPUS")
	if root == "" {
		return
	}
	real, err := corpus.LoadConcurrent([]provider.Source{provider.NewClaudeCode(root)}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	sessions, nonZero := assertContextCostMatches(t, real, prices, aggregate.Filter{})
	t.Logf("CLAUDIT_CORPUS: %d sessions, %d non-zero turns compared", sessions, nonZero)
	if sessions == 0 {
		t.Errorf("CLAUDIT_CORPUS %s held no sessions", root)
	}
}

// TestLayout_GroupsFilesBySession: a session's files are its own logs, its
// sub-agents' and its Codex sub-agent threads', sorted; a turn-less id is
// unknown.
func TestLayout_GroupsFilesBySession(t *testing.T) {
	l := NewLayout(contextCorpus(), nil)
	got := map[string][]string{}
	for _, id := range l.Sessions() {
		got[id] = l.Files(id)
	}
	want := map[string][]string{
		"A": {"/p/a.jsonl", "/p/a/subagents/agent-x.jsonl"},
		"B": {"/p/b.jsonl"},
		"C": {"/p/c.jsonl"},
		"Z": {"/p/0z.jsonl"},
		"P": {"/codex/rollout-P.jsonl", "/codex/rollout-T.jsonl"},
	}
	ids := make([]string, 0, len(got))
	for id := range got {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(got) != len(want) {
		t.Fatalf("sessions = %v, want A B C P Z", ids)
	}
	for id, files := range want {
		if g := got[id]; len(g) != len(files) || (len(g) > 0 && g[0] != files[0]) || (len(g) > 1 && g[1] != files[1]) {
			t.Errorf("files of %s = %v, want %v", id, g, files)
		}
	}
	if !l.Has("P") || l.Has("T") || l.Has("nope") {
		t.Errorf("Has: P %v, T %v, nope %v; want true false false", l.Has("P"), l.Has("T"), l.Has("nope"))
	}
}
