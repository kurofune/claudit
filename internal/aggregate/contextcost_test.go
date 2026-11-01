package aggregate

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/parse/codex"
	"github.com/kurofune/claudit/internal/pricing"
)

const ctxEps = 1e-12

func ctxTable() *pricing.Table {
	return &pricing.Table{Models: map[string]pricing.ModelPrice{
		"m": {
			Rate: pricing.Rate{Input: 5, Output: 25, CacheRead: 2, CacheWrite5m: 6, CacheWrite1h: 10},
			Rates: []pricing.RatePeriod{{
				Until: time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
				Rate:  pricing.Rate{Input: 5, Output: 25, CacheRead: 1, CacheWrite5m: 4, CacheWrite1h: 8},
			}},
		},
		"m2": {Rate: pricing.Rate{CacheRead: 3, CacheWrite5m: 7, CacheWrite1h: 12}},
	}}
}

func ctxTurn(file, session, id, model string, ts time.Time, u parse.Usage, tools ...parse.ToolUse) parse.Turn {
	return parse.Turn{
		SessionID:  session,
		MessageID:  id,
		UUID:       "uuid-" + id,
		Timestamp:  ts,
		Model:      model,
		Usage:      u,
		ToolUses:   tools,
		SourceFile: file,
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func findCtxTool(rows []ToolContextCost, name string) *ToolContextCost {
	for i := range rows {
		if rows[i].Tool == name {
			return &rows[i]
		}
	}
	return nil
}

func checkSum(t *testing.T, label string, c ContextCost) {
	t.Helper()
	if !approx(c.ContextCostUSD, c.WriteCostUSD+c.ReadCostUSD) {
		t.Errorf("%s: ContextCostUSD %v != Write %v + Read %v", label, c.ContextCostUSD, c.WriteCostUSD, c.ReadCostUSD)
	}
}

func TestContextCost_SplitsCacheCreationByOutputBytes(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{},
		parse.ToolUse{ID: "tu-bash", Name: "Bash", Detail: "git status"},
		parse.ToolUse{ID: "tu-read", Name: "Read", Detail: ".go"}))
	agg.AddToolResults([]parse.ToolResult{
		{ToolUseID: "tu-bash", OutputBytes: 900},
		{ToolUseID: "tu-read", OutputBytes: 100},
	})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 1000}))

	rows := agg.ContextCostByTool()
	bash, read := findCtxTool(rows, "Bash"), findCtxTool(rows, "Read")
	if bash == nil || read == nil {
		t.Fatalf("ContextCostByTool = %+v, want Bash and Read rows", rows)
	}
	if bash.ContextTokens != 900 || read.ContextTokens != 100 {
		t.Errorf("ContextTokens Bash=%d Read=%d, want 900 and 100", bash.ContextTokens, read.ContextTokens)
	}
	if want := 900 * 6.0 / 1e6; !approx(bash.WriteCostUSD, want) {
		t.Errorf("Bash WriteCostUSD = %v, want %v", bash.WriteCostUSD, want)
	}
	if bash.RereadTurns != 0 || bash.ReadCostUSD != 0 {
		t.Errorf("Bash rereads = %d / $%v, want none (no later turns)", bash.RereadTurns, bash.ReadCostUSD)
	}
	if rows[0].Tool != "Bash" {
		t.Errorf("rows[0] = %q, want Bash (highest context cost first)", rows[0].Tool)
	}
	checkSum(t, "Bash", bash.ContextCost)
}

func TestContextCost_ReadAndWriteCostAtEachTurnsRate(t *testing.T) {
	// B (April, old rate) writes N=1000 split 750 5m / 250 1h; C1 (April),
	// C2 (May, current rate) and C3 (model m2) each re-read it.
	apr := time.Date(2026, 4, 29, 10, 0, 0, 0, time.UTC)
	may := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", apr, parse.Usage{},
		parse.ToolUse{ID: "tu-1", Name: "Bash", Detail: "ls"}))
	agg.AddToolResults([]parse.ToolResult{{ToolUseID: "tu-1", OutputBytes: 4000}})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", apr.Add(time.Minute),
		parse.Usage{CacheCreate5mTokens: 750, CacheCreate1hTokens: 250}))
	agg.Add(ctxTurn("f.jsonl", "s", "C1", "m", apr.Add(2*time.Minute), parse.Usage{CacheReadTokens: 5000}))
	agg.Add(ctxTurn("f.jsonl", "s", "C2", "m", may, parse.Usage{CacheReadTokens: 5000}))
	agg.Add(ctxTurn("f.jsonl", "s", "C3", "m2", may.Add(time.Minute), parse.Usage{CacheReadTokens: 5000}))

	const n = 1000.0
	wantWrite := n * (0.75*4 + 0.25*8) / 1e6
	wantRead := n*1/1e6 + n*2/1e6 + n*3/1e6

	check := func(label string, c ContextCost) {
		t.Helper()
		if c.ContextTokens != 1000 {
			t.Errorf("%s ContextTokens = %d, want 1000", label, c.ContextTokens)
		}
		if c.RereadTurns != 3 {
			t.Errorf("%s RereadTurns = %d, want 3", label, c.RereadTurns)
		}
		if !approx(c.WriteCostUSD, wantWrite) {
			t.Errorf("%s WriteCostUSD = %v, want %v", label, c.WriteCostUSD, wantWrite)
		}
		if !approx(c.ReadCostUSD, wantRead) {
			t.Errorf("%s ReadCostUSD = %v, want %v", label, c.ReadCostUSD, wantRead)
		}
		checkSum(t, label, c)
	}

	tools := agg.ContextCostByTool()
	if len(tools) != 1 {
		t.Fatalf("ContextCostByTool rows = %d, want 1", len(tools))
	}
	check("by tool", tools[0].ContextCost)

	details := agg.ContextCostByToolDetail()["Bash"]
	if len(details) != 1 || details[0].Tool != "Bash" || details[0].Detail != "ls" {
		t.Fatalf("ContextCostByToolDetail[Bash] = %+v, want one ls row", details)
	}
	check("by detail", details[0].ContextCost)

	sessions := agg.ContextCostBySession()
	if len(sessions) != 1 || sessions[0].SessionID != "s" {
		t.Fatalf("ContextCostBySession = %+v, want one row for s", sessions)
	}
	check("by session", sessions[0].ContextCost)
}

func TestContextCost_RereadsStayInTheirSessionFile(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{}, parse.ToolUse{ID: "tu-1", Name: "Bash"}))
	agg.AddToolResults([]parse.ToolResult{{ToolUseID: "tu-1", OutputBytes: 10}})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 100}))
	agg.Add(ctxTurn("g.jsonl", "s2", "X", "m", t0.Add(2*time.Second), parse.Usage{CacheReadTokens: 5000}))

	rows := agg.ContextCostByTool()
	if len(rows) != 1 || rows[0].RereadTurns != 0 {
		t.Errorf("rows = %+v, want one Bash row with no rereads (other file's turn ignored)", rows)
	}
}

func TestContextCost_CapsReadsAtTurnCacheRead(t *testing.T) {
	// The cache expired before C: it re-read only 100 tokens of the 1000 live
	// ones, so only a tenth of the result's read cost is attributed there.
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{}, parse.ToolUse{ID: "tu-1", Name: "Bash"}))
	agg.AddToolResults([]parse.ToolResult{{ToolUseID: "tu-1", OutputBytes: 10}})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 1000}))
	agg.Add(ctxTurn("f.jsonl", "s", "C", "m", t0.Add(2*time.Second), parse.Usage{CacheReadTokens: 100}))
	agg.Add(ctxTurn("f.jsonl", "s", "D", "m", t0.Add(3*time.Second), parse.Usage{CacheCreate5mTokens: 1000}))

	rows := agg.ContextCostByTool()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want 1", rows)
	}
	if want := 100 * 2.0 / 1e6; !approx(rows[0].ReadCostUSD, want) {
		t.Errorf("ReadCostUSD = %v, want %v (capped at C's 100 cache-read tokens)", rows[0].ReadCostUSD, want)
	}
	if rows[0].RereadTurns != 1 {
		t.Errorf("RereadTurns = %d, want 1 (D read nothing from cache)", rows[0].RereadTurns)
	}
}

func loadFixture(t *testing.T, path string) parse.Result {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := parse.ParseFile(f, path)
	if err != nil {
		t.Fatalf("ParseFile %s: %v", path, err)
	}
	return res
}

func feed(agg *Aggregator, res parse.Result, boundaries bool) {
	for _, tn := range res.Turns {
		agg.Add(tn)
	}
	agg.AddToolResults(res.ToolResults)
	if boundaries {
		agg.AddCompactBoundaries(res.CompactBoundaries)
	}
}

func TestContextCost_CompactionBoundaryEndsRereads(t *testing.T) {
	res := loadFixture(t, filepath.Join("..", "parse", "testdata", "compact_session.jsonl"))
	if len(res.Turns) != 6 || len(res.CompactBoundaries) != 1 {
		t.Fatalf("fixture: %d turns, %d boundaries; want 6 and 1", len(res.Turns), len(res.CompactBoundaries))
	}

	agg := New(ctxTable())
	feed(agg, res, true)
	rows := agg.ContextCostByTool()

	// Bash ran on turn 1, so its result was produced before turn 3.
	bash := findCtxTool(rows, "Bash")
	if bash == nil {
		t.Fatalf("no Bash row in %+v", rows)
	}
	if bash.RereadTurns > 2 {
		t.Errorf("Bash RereadTurns = %d, want <= 2 (compaction between turns 3 and 4)", bash.RereadTurns)
	}
	if bash.ContextTokens != 1000 || bash.RereadTurns != 1 {
		t.Errorf("Bash = %+v, want 1000 tokens re-read once (turn 3)", bash.ContextCost)
	}
	if read := findCtxTool(rows, "Read"); read == nil || read.ContextTokens != 400 || read.RereadTurns != 0 {
		t.Errorf("Read = %+v, want 400 tokens, no rereads past the boundary", read)
	}
	if grep := findCtxTool(rows, "Grep"); grep != nil {
		t.Errorf("Grep = %+v, want no row: its result was compacted away before turn 4", grep)
	}
	if glob := findCtxTool(rows, "Glob"); glob == nil || glob.ContextTokens != 80 {
		t.Errorf("Glob = %+v, want 80 tokens attributed after the boundary", glob)
	}

	// Without the boundary the same fixture keeps Bash live to session end.
	plain := New(ctxTable())
	feed(plain, res, false)
	if b := findCtxTool(plain.ContextCostByTool(), "Bash"); b == nil || b.RereadTurns != 3 {
		t.Errorf("Bash without boundary = %+v, want 3 rereads (fixture exercises the boundary)", b)
	}
}

// TestContextCostReconciles: per fixture, the attributed context cost never
// exceeds the prompt spend it explains — cache reads and writes, plus a
// Codex turn's uncached input, which is what a Codex result's tokens add —
// under a flat table and the bundled one. Codex fixtures go through
// codex.ParseFile unpatched.
func TestContextCostReconciles(t *testing.T) {
	claude, err := filepath.Glob(filepath.Join("..", "parse", "testdata", "*.jsonl"))
	if err != nil || len(claude) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	cx, err := filepath.Glob(filepath.Join("..", "parse", "codex", "testdata", "*.jsonl"))
	if err != nil || len(cx) == 0 {
		t.Fatalf("no codex fixtures: %v", err)
	}
	flatRate := pricing.Rate{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10}
	defaults, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// budget is what tables price the turns' prompt-side spend that context
	// cost can explain.
	budget := func(tbl *pricing.Table, turns []parse.Turn) float64 {
		var b float64
		for _, tn := range turns {
			u := tn.Usage
			r, _ := tbl.EffectiveRate(tn.Provider, tn.Model, tn.Timestamp,
				u.InputTokens+u.CacheCreate5mTokens+u.CacheCreate1hTokens+u.CacheReadTokens, u.Speed)
			b += (float64(u.CacheReadTokens)*r.CacheRead +
				float64(u.CacheCreate5mTokens)*r.CacheWrite5m +
				float64(u.CacheCreate1hTokens)*r.CacheWrite1h) / 1e6
			if tn.Provider == parse.ProviderCodex {
				b += float64(u.InputTokens) * r.Input / 1e6
			}
		}
		return b
	}
	attributed := func(tbl *pricing.Table, res parse.Result) float64 {
		agg := New(tbl)
		feed(agg, res, true)
		var sum float64
		for _, r := range agg.ContextCostByTool() {
			sum += r.ContextCostUSD
		}
		return sum
	}
	check := func(path string, res parse.Result) (flatSum, defaultSum float64) {
		flat := &pricing.Table{Models: map[string]pricing.ModelPrice{}, Providers: map[string]pricing.ProviderPrices{}}
		for _, tn := range res.Turns {
			prov := pricing.PriceProvider(tn.Provider)
			if flat.Providers[prov].Models == nil {
				flat.Providers[prov] = pricing.ProviderPrices{Models: map[string]pricing.ModelPrice{}}
			}
			flat.Providers[prov].Models[tn.Model] = pricing.ModelPrice{Rate: flatRate}
		}
		flatSum = attributed(flat, res)
		if b := budget(flat, res.Turns); flatSum > b+ctxEps {
			t.Errorf("%s: attributed $%v > prompt budget $%v", path, flatSum, b)
		}
		defaultSum = attributed(defaults, res)
		if b := budget(defaults, res.Turns); defaultSum > b+ctxEps {
			t.Errorf("%s (default prices): attributed $%v > prompt budget $%v", path, defaultSum, b)
		}
		return flatSum, defaultSum
	}

	var claudeSum float64
	for _, path := range claude {
		s, _ := check(path, loadFixture(t, path))
		claudeSum += s
	}
	if claudeSum == 0 {
		t.Error("no Claude Code fixture attributed any context cost; the reconciliation is vacuous")
	}
	// tools.jsonl writes no cache, so only uncached-input attribution can
	// make its sums positive; a cache-write fixture would mask its loss.
	for _, path := range cx {
		res := loadCodexFixture(t, path)
		flatSum, defaultSum := check(path, res)
		if filepath.Base(path) != "tools.jsonl" {
			continue
		}
		for _, tn := range res.Turns {
			if u := tn.Usage; u.CacheCreate5mTokens+u.CacheCreate1hTokens != 0 {
				t.Fatalf("%s: turn %s writes cache; the fixture no longer isolates uncached-input attribution", path, tn.UUID)
			}
		}
		if flatSum <= 0 || defaultSum <= 0 {
			t.Errorf("%s: attributed flat $%v, default $%v; want both positive from uncached input alone", path, flatSum, defaultSum)
		}
	}
}

func TestContextCost_StaggeredResultsAttributeOnTheTurnAfterArrival(t *testing.T) {
	// A calls two tools; only tu-1's result is back before B. tu-2's result
	// arrives between B and C, so C's cache write carries it, and B's
	// carries tu-1 alone.
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{},
		parse.ToolUse{ID: "tu-1", Name: "Bash", Detail: "ls"},
		parse.ToolUse{ID: "tu-2", Name: "Read", Detail: ".go"}))
	agg.AddToolResults([]parse.ToolResult{
		{ToolUseID: "tu-1", OutputBytes: 900, Timestamp: t0.Add(1 * time.Second)},
		{ToolUseID: "tu-2", OutputBytes: 100, Timestamp: t0.Add(3 * time.Second)},
	})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(2*time.Second), parse.Usage{CacheCreate5mTokens: 1000}))
	agg.Add(ctxTurn("f.jsonl", "s", "C", "m", t0.Add(4*time.Second), parse.Usage{CacheCreate5mTokens: 500, CacheReadTokens: 5000}))
	agg.Add(ctxTurn("f.jsonl", "s", "D", "m", t0.Add(5*time.Second), parse.Usage{CacheReadTokens: 5000}))

	rows := agg.ContextCostByTool()
	bash, read := findCtxTool(rows, "Bash"), findCtxTool(rows, "Read")
	if bash == nil || read == nil {
		t.Fatalf("ContextCostByTool = %+v, want Bash and Read rows", rows)
	}
	if bash.ContextTokens != 1000 || bash.RereadTurns != 2 {
		t.Errorf("Bash = %+v, want all 1000 of B's tokens, re-read by C and D", bash.ContextCost)
	}
	if want := 1000*6.0/1e6 + 2*1000*2.0/1e6; !approx(bash.ContextCostUSD, want) {
		t.Errorf("Bash ContextCostUSD = %v, want %v", bash.ContextCostUSD, want)
	}
	if read.ContextTokens != 500 || read.RereadTurns != 1 {
		t.Errorf("Read = %+v, want C's 500 tokens, re-read by D only", read.ContextCost)
	}
	if want := 500*6.0/1e6 + 500*2.0/1e6; !approx(read.ContextCostUSD, want) {
		t.Errorf("Read ContextCostUSD = %v, want %v", read.ContextCostUSD, want)
	}
	checkSum(t, "Bash", bash.ContextCost)
	checkSum(t, "Read", read.ContextCost)
}

func TestContextCost_ResultArrivingBeforeCompactionIsDropped(t *testing.T) {
	// tu-1's result arrives, then the session compacts before B: B's cache
	// write is the summary, not the result. tu-2's result arrives after the
	// boundary, so B carries it.
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{},
		parse.ToolUse{ID: "tu-1", Name: "Bash"},
		parse.ToolUse{ID: "tu-2", Name: "Read"}))
	agg.AddToolResults([]parse.ToolResult{
		{ToolUseID: "tu-1", OutputBytes: 900, Timestamp: t0.Add(1 * time.Second)},
		{ToolUseID: "tu-2", OutputBytes: 100, Timestamp: t0.Add(3 * time.Second)},
	})
	agg.AddCompactBoundaries([]parse.CompactBoundary{{SourceFile: "f.jsonl", Timestamp: t0.Add(2 * time.Second)}})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(4*time.Second), parse.Usage{CacheCreate5mTokens: 1000}))

	rows := agg.ContextCostByTool()
	if b := findCtxTool(rows, "Bash"); b != nil {
		t.Errorf("Bash = %+v, want no row (compacted away before B)", b)
	}
	if r := findCtxTool(rows, "Read"); r == nil || r.ContextTokens != 1000 {
		t.Errorf("Read = %+v, want all of B's 1000 tokens", r)
	}
}

func TestContextCost_UsesEffectiveRate(t *testing.T) {
	// Each case: A calls a tool, B carries its 1000-token result, C re-reads
	// it. B and C run at the case's speed and prompt size; the attributed
	// write and read must use the rate CostAt would pick for that turn.
	std := pricing.Rate{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite5m: 6, CacheWrite1h: 10}
	fast := pricing.Rate{Input: 10, Output: 50, CacheRead: 1, CacheWrite5m: 12, CacheWrite1h: 20}
	long := pricing.Rate{Input: 7, Output: 30, CacheRead: 0.7, CacheWrite5m: 9, CacheWrite1h: 14}
	tbl := &pricing.Table{Models: map[string]pricing.ModelPrice{
		"m": {Rate: std, Fast: &fast, LongPrompt: &pricing.LongPromptRate{ThresholdTokens: 10_000, Rate: long}},
	}}
	cases := []struct {
		name  string
		speed string
		input int // pushes the prompt past the threshold when large
		want  pricing.Rate
	}{
		{"standard", "", 0, std},
		{"fast", "fast", 0, fast},
		{"long prompt", "standard", 20_000, long},
		{"fast over long prompt", "fast", 20_000, fast},
	}
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	for _, c := range cases {
		agg := New(tbl)
		agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{}, parse.ToolUse{ID: "tu-1", Name: "Bash"}))
		agg.AddToolResults([]parse.ToolResult{{ToolUseID: "tu-1", OutputBytes: 10}})
		bu := parse.Usage{Speed: c.speed, InputTokens: c.input, CacheCreate5mTokens: 1000}
		cu := parse.Usage{Speed: c.speed, InputTokens: c.input, CacheReadTokens: 1000}
		agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), bu))
		agg.Add(ctxTurn("f.jsonl", "s", "C", "m", t0.Add(2*time.Second), cu))

		rows := agg.ContextCostByTool()
		if len(rows) != 1 {
			t.Fatalf("%s: rows = %+v, want 1", c.name, rows)
		}
		if want := 1000 * c.want.CacheWrite5m / 1e6; !approx(rows[0].WriteCostUSD, want) {
			t.Errorf("%s: WriteCostUSD = %v, want %v", c.name, rows[0].WriteCostUSD, want)
		}
		if want := 1000 * c.want.CacheRead / 1e6; !approx(rows[0].ReadCostUSD, want) {
			t.Errorf("%s: ReadCostUSD = %v, want %v", c.name, rows[0].ReadCostUSD, want)
		}
		// Reconcile with what CostAt bills those cache tokens on each turn.
		bIn, _ := tbl.CostAt(pricing.ClaudeCode, "m", t0, bu.InputTokens, 0, 0, 0, 0, bu.Speed)
		bAll, _ := tbl.CostAt(pricing.ClaudeCode, "m", t0, bu.InputTokens, 0, bu.CacheCreate5mTokens, 0, 0, bu.Speed)
		cIn, _ := tbl.CostAt(pricing.ClaudeCode, "m", t0, cu.InputTokens, 0, 0, 0, 0, cu.Speed)
		cAll, _ := tbl.CostAt(pricing.ClaudeCode, "m", t0, cu.InputTokens, 0, 0, 0, cu.CacheReadTokens, cu.Speed)
		if !approx(rows[0].ContextCostUSD, (bAll-bIn)+(cAll-cIn)) {
			t.Errorf("%s: ContextCostUSD = %v, want CostAt's cache spend %v", c.name, rows[0].ContextCostUSD, (bAll-bIn)+(cAll-cIn))
		}
	}
}

// TestContextCost_LongSessionRetainsLinearMemory: n turns each carry the
// previous turn's result and re-read every earlier one, with no
// compaction — about n²/2 result-to-reread pairs. The walk must count
// distinct re-reading turns without keeping a reference per pair, while
// still billing every pair's tokens and dollars.
func TestContextCost_LongSessionRetainsLinearMemory(t *testing.T) {
	const n = 4000
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	details := []string{"ls", "git status", "go test"}
	results := make([]parse.ToolResult, 0, n)
	for i := 0; i < n; i++ {
		id := "tu-" + strconv.Itoa(i)
		u := parse.Usage{CacheReadTokens: 1 << 30}
		if i > 0 {
			u.CacheCreate5mTokens = 100
		}
		agg.Add(ctxTurn("f.jsonl", "s", "T"+strconv.Itoa(i), "m", t0.Add(time.Duration(i)*time.Second), u,
			parse.ToolUse{ID: id, Name: "Bash", Detail: details[i%len(details)]}))
		results = append(results, parse.ToolResult{ToolUseID: id, OutputBytes: 100})
	}
	agg.AddToolResults(results)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	agg.contextCosts()
	runtime.ReadMemStats(&after)
	// Linear state is a few hundred bytes per result; one 24-byte reference
	// per pair would be ~190 MB here.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 16<<20 {
		t.Errorf("attribution walk allocated %d MB for %d turns, want linear (< 16 MB)", alloc>>20, n)
	}

	// Turn j carries result j-1 (100 tokens) and re-reads results 0..j-2.
	pairs := int64(0)
	for j := 2; j < n; j++ {
		pairs += int64(j - 1)
	}
	wantTokens := int64(100 * (n - 1))
	wantWrite := float64(wantTokens) * 6 / 1e6
	wantRead := float64(pairs) * 100 * 2 / 1e6
	check := func(label string, c ContextCost, tokens int64, rereads int, write, read float64) {
		t.Helper()
		if c.ContextTokens != tokens || c.RereadTurns != rereads {
			t.Errorf("%s = %d tokens / %d rereads, want %d / %d", label, c.ContextTokens, c.RereadTurns, tokens, rereads)
		}
		if math.Abs(c.WriteCostUSD-write) > 1e-6 || math.Abs(c.ReadCostUSD-read) > 1e-6 {
			t.Errorf("%s = $%v write / $%v read, want $%v / $%v", label, c.WriteCostUSD, c.ReadCostUSD, write, read)
		}
		checkSum(t, label, c)
	}
	check("total", agg.ContextCostTotal(), wantTokens, n-2, wantWrite, wantRead)
	tools := agg.ContextCostByTool()
	if len(tools) != 1 {
		t.Fatalf("ContextCostByTool rows = %d, want 1", len(tools))
	}
	check("Bash", tools[0].ContextCost, wantTokens, n-2, wantWrite, wantRead)
	sessions := agg.ContextCostBySession()
	if len(sessions) != 1 {
		t.Fatalf("ContextCostBySession rows = %d, want 1", len(sessions))
	}
	check("session", sessions[0].ContextCost, wantTokens, n-2, wantWrite, wantRead)

	// Detail d (result index d, d+3, ...) is first live on turn d+2, so
	// turns d+2..n-1 re-read it.
	byDetail := map[string]DetailContextCost{}
	for _, r := range agg.ContextCostByToolDetail()["Bash"] {
		byDetail[r.Detail] = r
	}
	for d, name := range details {
		var tokens int64
		var reads float64
		for k := d; k < n-1; k += len(details) {
			tokens += 100
			reads += float64(n-2-k) * 100 * 2 / 1e6
		}
		check("Bash·"+name, byDetail[name].ContextCost, tokens, n-2-d, float64(tokens)*6/1e6, reads)
	}
}

// codexCtxTable prices model "cx" for provider codex only. Its cache-write
// rates are set so a walk that priced a Codex turn's added tokens as a cache
// write would show it.
func codexCtxTable() *pricing.Table {
	return &pricing.Table{Providers: map[string]pricing.ProviderPrices{parse.ProviderCodex: {Models: map[string]pricing.ModelPrice{
		"cx": {Rate: pricing.Rate{Input: 10, Output: 40, CacheRead: 1, CacheWrite5m: 99, CacheWrite1h: 99}},
	}}}}
}

func codexCtxTurn(id string, ts time.Time, u parse.Usage, tools ...parse.ToolUse) parse.Turn {
	t := ctxTurn("rollout.jsonl", "s", id, "cx", ts, u, tools...)
	t.Provider = parse.ProviderCodex
	return t
}

func findCtxDetail(rows map[string][]DetailContextCost, tool, detail string) *DetailContextCost {
	for i := range rows[tool] {
		if rows[tool][i].Detail == detail {
			return &rows[tool][i]
		}
	}
	return nil
}

// TestContextCost_CodexUncachedInputIsTheWrite: Codex writes no prompt
// cache, so the tokens a result added are the carrying response's uncached
// input, split by OutputBytes and priced at the input rate; later responses
// re-read them at the cached-input rate.
func TestContextCost_CodexUncachedInputIsTheWrite(t *testing.T) {
	t0 := time.Date(2026, 10, 23, 7, 0, 0, 0, time.UTC)
	agg := New(codexCtxTable())
	agg.Add(codexCtxTurn("A", t0, parse.Usage{InputTokens: 25_000},
		parse.ToolUse{ID: "tu-1", Name: "Bash", Detail: "cat"},
		parse.ToolUse{ID: "tu-2", Name: "Bash", Detail: "ls"}))
	agg.AddToolResults([]parse.ToolResult{
		{ToolUseID: "tu-1", OutputBytes: 900},
		{ToolUseID: "tu-2", OutputBytes: 100},
		{ToolUseID: "tu-3", OutputBytes: 4000},
	})
	// B carries tu-1 and tu-2 and calls tu-3; C carries tu-3 with no
	// uncached input and re-reads B's results; D re-reads with less cached
	// input than is live.
	agg.Add(codexCtxTurn("B", t0.Add(time.Second), parse.Usage{InputTokens: 1000, CacheReadTokens: 25_000},
		parse.ToolUse{ID: "tu-3", Name: "Read", Detail: ".go"}))
	agg.Add(codexCtxTurn("C", t0.Add(2*time.Second), parse.Usage{InputTokens: 0, CacheReadTokens: 26_000}))
	agg.Add(codexCtxTurn("D", t0.Add(3*time.Second), parse.Usage{InputTokens: 50, CacheReadTokens: 500}))

	rows := agg.ContextCostByToolDetail()
	cat, ls := findCtxDetail(rows, "Bash", "cat"), findCtxDetail(rows, "Bash", "ls")
	if cat == nil || ls == nil {
		t.Fatalf("ContextCostByToolDetail = %+v, want Bash cat and ls", rows)
	}
	if cat.ContextTokens != 900 || ls.ContextTokens != 100 {
		t.Errorf("ContextTokens cat=%d ls=%d, want 900 and 100 (B's 1000 uncached input by bytes)", cat.ContextTokens, ls.ContextTokens)
	}
	if !approx(cat.WriteCostUSD, 900*10/1e6) || !approx(ls.WriteCostUSD, 100*10/1e6) {
		t.Errorf("WriteCostUSD cat=%v ls=%v, want %v and %v at the $10 input rate", cat.WriteCostUSD, ls.WriteCostUSD, 900*10/1e6, 100*10/1e6)
	}
	// C re-reads all 1000 live tokens; D's 500 cached tokens scale its
	// re-read to half.
	if want := (900 + 900*0.5) * 1 / 1e6; !approx(cat.ReadCostUSD, want) {
		t.Errorf("cat ReadCostUSD = %v, want %v", cat.ReadCostUSD, want)
	}
	if want := (100 + 100*0.5) * 1 / 1e6; !approx(ls.ReadCostUSD, want) {
		t.Errorf("ls ReadCostUSD = %v, want %v", ls.ReadCostUSD, want)
	}
	if cat.RereadTurns != 2 || ls.RereadTurns != 2 {
		t.Errorf("RereadTurns cat=%d ls=%d, want 2 (C and D)", cat.RereadTurns, ls.RereadTurns)
	}
	checkSum(t, "cat", cat.ContextCost)
	if read := findCtxDetail(rows, "Read", ".go"); read != nil && read.ContextTokens != 0 {
		t.Errorf("Read = %+v, want 0 tokens: C had no uncached input", read.ContextCost)
	}
}

// TestContextCost_ClaudeUncachedInputAttributesNothing: a Claude Code turn
// that writes no cache adds nothing, however much uncached input it has.
func TestContextCost_ClaudeUncachedInputAttributesNothing(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{}, parse.ToolUse{ID: "tu-1", Name: "Bash", Detail: "ls"}))
	agg.AddToolResults([]parse.ToolResult{{ToolUseID: "tu-1", OutputBytes: 900}})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), parse.Usage{InputTokens: 5000, CacheReadTokens: 1000}))
	agg.Add(ctxTurn("f.jsonl", "s", "C", "m", t0.Add(2*time.Second), parse.Usage{InputTokens: 5000, CacheReadTokens: 6000}))
	if got := agg.ContextCostTotal(); got.ContextTokens != 0 || got.ContextCostUSD != 0 {
		t.Errorf("ContextCostTotal = %+v, want nothing attributed", got)
	}
}

func loadCodexFixture(t *testing.T, path string) parse.Result {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := codex.ParseFile(f, path)
	if err != nil {
		t.Fatalf("codex.ParseFile %s: %v", path, err)
	}
	return res
}

// TestContextCost_CodexCompactedRecordEndsRereads: the `cat` output carried
// before the rollout's compacted record is re-read only by the compaction
// response; the two responses after the record re-read only what they
// themselves carried.
func TestContextCost_CodexCompactedRecordEndsRereads(t *testing.T) {
	res := loadCodexFixture(t, filepath.Join("..", "parse", "codex", "testdata", "compact.jsonl"))
	if len(res.CompactBoundaries) != 1 {
		t.Fatalf("fixture boundaries = %d, want 1", len(res.CompactBoundaries))
	}
	tbl := &pricing.Table{Providers: map[string]pricing.ProviderPrices{parse.ProviderCodex: {Models: map[string]pricing.ModelPrice{
		"gpt-6-astra": {Rate: pricing.Rate{Input: 10, Output: 60, CacheRead: 1}},
	}}}}

	agg := New(tbl)
	feed(agg, res, true)
	rows := agg.ContextCostByToolDetail()
	cat, ls := findCtxDetail(rows, "Bash", "cat"), findCtxDetail(rows, "Bash", "ls")
	if cat == nil || ls == nil {
		t.Fatalf("ContextCostByToolDetail = %+v, want Bash cat and ls", rows)
	}
	// The second response's uncached input: 244,851 − 240,512.
	if cat.ContextTokens != 4339 || !approx(cat.WriteCostUSD, 4339*10/1e6) {
		t.Errorf("cat = %+v, want 4339 tokens written at $10", cat.ContextCost)
	}
	if cat.RereadTurns != 1 || !approx(cat.ReadCostUSD, 4339*1/1e6) {
		t.Errorf("cat = %+v, want one re-read (the compaction response) and none after the record", cat.ContextCost)
	}
	// The last response's uncached input: 36,056 − 33,536.
	if ls.ContextTokens != 2520 || ls.ReadCostUSD != 0 {
		t.Errorf("ls = %+v, want 2520 tokens, no re-reads", ls.ContextCost)
	}

	plain := New(tbl)
	feed(plain, res, false)
	if c := findCtxDetail(plain.ContextCostByToolDetail(), "Bash", "cat"); c == nil || c.RereadTurns != 3 {
		t.Errorf("cat without the boundary = %+v, want 3 re-reads (fixture exercises the boundary)", c)
	}
}

func TestContextCostByTurn_CarryingTurnGetsTheWriteCost(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{},
		parse.ToolUse{ID: "tu-bash", Name: "Bash", Detail: "git status"},
		parse.ToolUse{ID: "tu-read", Name: "Read", Detail: ".go"}))
	agg.AddToolResults([]parse.ToolResult{
		{ToolUseID: "tu-bash", OutputBytes: 900},
		{ToolUseID: "tu-read", OutputBytes: 100},
		{ToolUseID: "tu-grep", OutputBytes: 50},
	})
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 1000, CacheReadTokens: 500},
		parse.ToolUse{ID: "tu-grep", Name: "Grep", Detail: "x"}))
	agg.Add(ctxTurn("f.jsonl", "s", "C", "m", t0.Add(2*time.Second), parse.Usage{CacheCreate1hTokens: 200, CacheReadTokens: 1000}))
	agg.Add(ctxTurn("g.jsonl", "other", "D", "m", t0, parse.Usage{CacheCreate5mTokens: 10}))

	by := agg.ContextCostByTurn("s")
	if got := by["uuid-A"]; got != 0 {
		t.Errorf("turn A = %v, want 0 (it carried no result)", got)
	}
	if want := 1000 * 6.0 / 1e6; !approx(by["uuid-B"], want) {
		t.Errorf("turn B = %v, want %v (A's results' write cost)", by["uuid-B"], want)
	}
	if want := 200 * 10.0 / 1e6; !approx(by["uuid-C"], want) {
		t.Errorf("turn C = %v, want %v (B's grep result's write cost)", by["uuid-C"], want)
	}
	if _, ok := by["uuid-D"]; ok {
		t.Errorf("turn D of another session present: %v", by)
	}
	var sum float64
	for _, v := range by {
		sum += v
	}
	var want float64
	for _, row := range agg.ContextCostBySession() {
		if row.SessionID == "s" {
			want = row.WriteCostUSD
		}
	}
	if want == 0 || !approx(sum, want) {
		t.Errorf("per-turn sum = %v, want session WriteCostUSD %v", sum, want)
	}
}
