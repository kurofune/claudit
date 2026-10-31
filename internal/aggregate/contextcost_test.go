package aggregate

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
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

func TestContextCostReconciles(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "parse", "testdata", "*.jsonl"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	flatRate := pricing.Rate{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10}
	defaults, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var attributed float64
	for _, path := range paths {
		res := loadFixture(t, path)

		flat := &pricing.Table{Models: map[string]pricing.ModelPrice{}}
		for _, tn := range res.Turns {
			flat.Models[tn.Model] = pricing.ModelPrice{Rate: flatRate}
		}
		agg := New(flat)
		feed(agg, res, true)
		tot := agg.Totals()
		budget := (float64(tot.CacheReadTokens)*flatRate.CacheRead +
			float64(tot.CacheCreate5mTokens)*flatRate.CacheWrite5m +
			float64(tot.CacheCreate1hTokens)*flatRate.CacheWrite1h) / 1e6
		var sum float64
		for _, r := range agg.ContextCostByTool() {
			sum += r.ContextCostUSD
		}
		if sum > budget+ctxEps {
			t.Errorf("%s: attributed $%v > cache budget $%v", path, sum, budget)
		}
		attributed += sum

		dflt := New(defaults)
		feed(dflt, res, true)
		var realBudget float64
		for _, tn := range res.Turns {
			u := tn.Usage
			r, _ := defaults.EffectiveRate(tn.Model, tn.Timestamp,
				u.InputTokens+u.CacheCreate5mTokens+u.CacheCreate1hTokens+u.CacheReadTokens, u.Speed)
			realBudget += (float64(tn.Usage.CacheReadTokens)*r.CacheRead +
				float64(tn.Usage.CacheCreate5mTokens)*r.CacheWrite5m +
				float64(tn.Usage.CacheCreate1hTokens)*r.CacheWrite1h) / 1e6
		}
		var realSum float64
		for _, r := range dflt.ContextCostByTool() {
			realSum += r.ContextCostUSD
		}
		if realSum > realBudget+ctxEps {
			t.Errorf("%s (default prices): attributed $%v > cache budget $%v", path, realSum, realBudget)
		}
	}
	if attributed == 0 {
		t.Error("no fixture attributed any context cost; the reconciliation is vacuous")
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
		bIn, _ := tbl.CostAt("m", t0, bu.InputTokens, 0, 0, 0, 0, bu.Speed)
		bAll, _ := tbl.CostAt("m", t0, bu.InputTokens, 0, bu.CacheCreate5mTokens, 0, 0, bu.Speed)
		cIn, _ := tbl.CostAt("m", t0, cu.InputTokens, 0, 0, 0, 0, cu.Speed)
		cAll, _ := tbl.CostAt("m", t0, cu.InputTokens, 0, 0, 0, cu.CacheReadTokens, cu.Speed)
		if !approx(rows[0].ContextCostUSD, (bAll-bIn)+(cAll-cIn)) {
			t.Errorf("%s: ContextCostUSD = %v, want CostAt's cache spend %v", c.name, rows[0].ContextCostUSD, (bAll-bIn)+(cAll-cIn))
		}
	}
}
