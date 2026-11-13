package aggregate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/v2/internal/parse"
	"github.com/kurofune/claudit/v2/internal/parse/codex"
	"github.com/kurofune/claudit/v2/internal/pricing"
)

func findHotspot(hs []Hotspot, kind HotspotKind, titleSub string) *Hotspot {
	for i := range hs {
		if hs[i].Kind == kind && strings.Contains(hs[i].Title, titleSub) {
			return &hs[i]
		}
	}
	return nil
}

// TestHotspotsRankByContextCost: `git status` runs in the turn that costs
// the most, but its tiny result is barely re-read; `go test` runs in a
// cheap turn whose big result every later turn re-reads. Context cost
// puts `go test` first.
func TestHotspotsRankByContextCost(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{OutputTokens: 1_000_000},
		parse.ToolUse{ID: "tu-git", Name: "Bash", Detail: "git status", Input: "git status"}))
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 100},
		parse.ToolUse{ID: "tu-test", Name: "Bash", Detail: "go test", Input: "go test ./..."}))
	agg.Add(ctxTurn("f.jsonl", "s", "C", "m", t0.Add(2*time.Second), parse.Usage{CacheCreate5mTokens: 10_000}))
	for i := 0; i < 5; i++ {
		agg.Add(ctxTurn("f.jsonl", "s", fmt.Sprintf("R%d", i), "m", t0.Add(time.Duration(3+i)*time.Second),
			parse.Usage{CacheReadTokens: 10_100}))
	}
	agg.AddToolResults([]parse.ToolResult{
		{ToolUseID: "tu-git", OutputBytes: 10},
		{ToolUseID: "tu-test", OutputBytes: 1000},
	})

	bash := agg.ByToolDetail()["Bash"]
	turnCost := map[string]float64{}
	for _, r := range bash {
		turnCost[r.Detail] = r.CostUSD
	}
	if turnCost["git status"] <= turnCost["go test"] {
		t.Fatalf("fixture: turn cost git status %v must exceed go test %v", turnCost["git status"], turnCost["go test"])
	}

	hs := agg.Hotspots(10)
	if len(hs) < 2 {
		t.Fatalf("Hotspots = %+v, want at least two", hs)
	}
	if hs[0].Title != "Bash: `go test`" || hs[1].Title != "Bash: `git status`" {
		t.Errorf("order = %q, %q; want go test then git status", hs[0].Title, hs[1].Title)
	}
	ctx := map[string]float64{}
	for _, r := range agg.ContextCostByToolDetail()["Bash"] {
		ctx[r.Detail] = r.ContextCostUSD
	}
	total := agg.ContextCostTotal().ContextCostUSD
	for _, h := range hs[:2] {
		want := ctx[h.Context["pattern"].(string)]
		if !approx(h.CostUSD, want) {
			t.Errorf("%s CostUSD = %v, want context cost %v", h.Title, h.CostUSD, want)
		}
		if !approx(h.PctOfTotal, 100*want/total) {
			t.Errorf("%s PctOfTotal = %v, want %v", h.Title, h.PctOfTotal, 100*want/total)
		}
	}
}

// TestHotspotsExamplesRankByContextTokens: a card lists at most five
// invocations, biggest context first, each naming its input, session and
// JSONL path.
func TestHotspotsExamplesRankByContextTokens(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	for i := 0; i < 7; i++ {
		file := fmt.Sprintf("/p/s%d.jsonl", i)
		sess := fmt.Sprintf("s%d", i)
		id := fmt.Sprintf("tu-%d", i)
		agg.Add(ctxTurn(file, sess, "A"+id, "m", t0, parse.Usage{},
			parse.ToolUse{ID: id, Name: "Bash", Detail: "wc", Input: fmt.Sprintf("wc -l f%d", i)}))
		agg.Add(ctxTurn(file, sess, "B"+id, "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 1000 * (i + 1)}))
		agg.AddToolResults([]parse.ToolResult{{ToolUseID: id, OutputBytes: 100}})
	}
	h := findHotspot(agg.Hotspots(10), HotspotBashPattern, "wc")
	if h == nil {
		t.Fatal("no wc hotspot")
	}
	if len(h.Examples) != 5 {
		t.Fatalf("examples = %d, want 5", len(h.Examples))
	}
	for k, ex := range h.Examples {
		i := 6 - k
		if ex.ContextTokens != int64(1000*(i+1)) || ex.SessionID != fmt.Sprintf("s%d", i) ||
			ex.SourceFile != fmt.Sprintf("/p/s%d.jsonl", i) || ex.Input != fmt.Sprintf("wc -l f%d", i) {
			t.Errorf("example %d = %+v, want session s%d", k, ex, i)
		}
	}
	if h.Context["calls"] != 7 {
		t.Errorf("calls = %v, want 7", h.Context["calls"])
	}
}

// TestHotspotsRepeatedCacheWrite: a session that keeps re-writing its
// cache (≥ 1M write tokens, writes > 25% of reads) is a card; one that
// writes once and reads it back many times is not.
func TestHotspotsRepeatedCacheWrite(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	// churn: 4 × 300k writes (1.2M), 2M reads → writes are 60% of reads.
	for i := 0; i < 4; i++ {
		agg.Add(ctxTurn("/p/churn.jsonl", "churn", fmt.Sprintf("c%d", i), "m", t0.Add(time.Duration(i)*10*time.Minute),
			parse.Usage{CacheCreate5mTokens: 300_000, CacheReadTokens: 500_000}))
	}
	// steady: 1.2M writes, 10M reads → 12%.
	agg.Add(ctxTurn("/p/steady.jsonl", "steady", "s0", "m", t0, parse.Usage{CacheCreate5mTokens: 1_200_000}))
	agg.Add(ctxTurn("/p/steady.jsonl", "steady", "s1", "m", t0.Add(time.Minute), parse.Usage{CacheReadTokens: 10_000_000}))
	// small: 900k writes, no reads — under the volume floor.
	agg.Add(ctxTurn("/p/small.jsonl", "small", "m0", "m", t0, parse.Usage{CacheCreate5mTokens: 900_000}))

	hs := agg.Hotspots(10)
	h := findHotspot(hs, HotspotRepeatedCacheWrite, "churn")
	if h == nil {
		t.Fatalf("no repeated_cache_write card for churn in %+v", hs)
	}
	if !strings.Contains(h.Title, "churn") {
		t.Errorf("title %q does not name the session", h.Title)
	}
	if h.Context["write_tokens"] != int64(1_200_000) || h.Context["read_tokens"] != int64(2_000_000) {
		t.Errorf("context = %+v, want write 1.2M read 2M", h.Context)
	}
	if want := 1_200_000 * 6.0 / 1e6; !approx(h.Context["write_cost"].(float64), want) || !approx(h.CostUSD, want) {
		t.Errorf("write cost = %v / CostUSD %v, want %v", h.Context["write_cost"], h.CostUSD, want)
	}
	if len(h.Examples) == 0 || len(h.Examples) > 5 || h.Examples[0].SessionID != "churn" || h.Examples[0].SourceFile != "/p/churn.jsonl" {
		t.Errorf("examples = %+v, want ≤5 churn turns", h.Examples)
	}
	for _, s := range []string{"steady", "small"} {
		if findHotspot(hs, HotspotRepeatedCacheWrite, s) != nil {
			t.Errorf("%s should not be a repeated_cache_write card", s)
		}
	}
}

func TestHotspotsAtScale(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	agg := New(prices)
	const sessions, calls = 3000, 100_000
	perSession := calls / sessions
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tools := []struct{ name, detail string }{
		{"Bash", "git status"}, {"Bash", "go test"}, {"Bash", "rg"}, {"Bash", "wc"}, {"Bash", "bd show"},
		{"Read", ".go"}, {"Read", ".md"}, {"Read", ".js"}, {"Grep", "func "}, {"Glob", "**/*.go"},
		{"WebFetch", "github.com"}, {"Bash", "ls"}, {"Bash", "cat"},
	}
	var results []parse.ToolResult
	n := 0
	for s := 0; s < sessions; s++ {
		file := fmt.Sprintf("/p/s%04d.jsonl", s)
		sess := fmt.Sprintf("sess-%04d", s)
		start := t0.Add(time.Duration(s) * 30 * 24 * time.Hour / sessions)
		k := perSession
		if s < calls%sessions {
			k++
		}
		for c := 0; c <= k; c++ {
			ts := start.Add(time.Duration(c) * 20 * time.Second)
			u := parse.Usage{OutputTokens: 200, CacheCreate5mTokens: 2000, CacheReadTokens: 20_000 + 2000*c}
			var uses []parse.ToolUse
			if c < k {
				tl := tools[(s+c)%len(tools)]
				id := fmt.Sprintf("tu-%d", n)
				n++
				uses = []parse.ToolUse{{ID: id, Name: tl.name, Detail: tl.detail, Input: tl.detail + " arg"}}
				results = append(results, parse.ToolResult{ToolUseID: id, OutputBytes: 500 + (n%7)*300, Timestamp: ts.Add(time.Second)})
			}
			agg.Add(ctxTurn(file, sess, fmt.Sprintf("m-%d-%d", s, c), "claude-opus-4-7", ts, u, uses...))
		}
	}
	agg.AddToolResults(results)
	if n != calls {
		t.Fatalf("fixture built %d calls, want %d", n, calls)
	}
	start := time.Now()
	hs := agg.Hotspots(10)
	t.Logf("Hotspots(10) over %d sessions / %d calls: %v", sessions, calls, time.Since(start))
	if len(hs) != 10 {
		t.Fatalf("Hotspots(10) = %d cards, want 10", len(hs))
	}
	for _, h := range hs {
		if len(h.Examples) == 0 || len(h.Examples) > 5 {
			t.Errorf("%s: %d examples, want 1-5", h.Title, len(h.Examples))
		}
	}
}

func TestHotspotsZeroTopReturnsNil(t *testing.T) {
	agg := New(ctxTable())
	if hs := agg.Hotspots(0); hs != nil {
		t.Errorf("expected nil for top=0, got %v", hs)
	}
}

func TestHotspotsSkipsSkillSlashCommandAgent(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	agg := New(ctxTable())
	agg.Add(ctxTurn("f.jsonl", "s", "A", "m", t0, parse.Usage{},
		parse.ToolUse{ID: "a", Name: "Agent", Detail: "general-purpose"},
		parse.ToolUse{ID: "b", Name: "Skill", Detail: "tdd"}))
	agg.Add(ctxTurn("f.jsonl", "s", "B", "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 1000}))
	agg.AddToolResults([]parse.ToolResult{{ToolUseID: "a", OutputBytes: 10}, {ToolUseID: "b", OutputBytes: 10}})
	if hs := agg.Hotspots(10); len(hs) != 0 {
		t.Errorf("Hotspots = %+v, want none for Agent/Skill", hs)
	}
}

// TestHotspotsExamplesFromParsedJSONL runs real Read and Grep tool_use lines
// through parse.ParseFile, so the examples carry the inputs the parser
// extracts rather than hand-filled ones.
func TestHotspotsExamplesFromParsedJSONL(t *testing.T) {
	const path = "/p/sess.jsonl"
	lines := []string{
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-05-10T10:00:00Z","message":{"id":"m1","model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
			`{"type":"tool_use","id":"tu-read","name":"Read","input":{"file_path":"/repo/internal/parse/parse.go","limit":400}},` +
			`{"type":"tool_use","id":"tu-grep","name":"Grep","input":{"pattern":"func Parse","path":"internal","glob":"*.go","output_mode":"content"}}]}}`,
		`{"type":"user","sessionId":"s","uuid":"u1","timestamp":"2026-05-10T10:00:01Z","message":{"role":"user","content":[` +
			`{"type":"tool_result","tool_use_id":"tu-read","content":"` + strings.Repeat("r", 4000) + `"},` +
			`{"type":"tool_result","tool_use_id":"tu-grep","content":"` + strings.Repeat("g", 2000) + `"}]}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a2","timestamp":"2026-05-10T10:00:02Z","message":{"id":"m2","model":"m","role":"assistant","usage":{"input_tokens":1,"cache_creation_input_tokens":2000,"output_tokens":1}}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a3","timestamp":"2026-05-10T10:00:03Z","message":{"id":"m3","model":"m","role":"assistant","usage":{"input_tokens":1,"cache_read_input_tokens":2000,"output_tokens":1}}}`,
	}
	res, err := parse.ParseFile(strings.NewReader(strings.Join(lines, "\n")+"\n"), path)
	if err != nil {
		t.Fatal(err)
	}
	agg := New(ctxTable())
	for _, tr := range res.Turns {
		agg.Add(tr)
	}
	agg.AddToolResults(res.ToolResults)

	hs := agg.Hotspots(10)
	for _, c := range []struct {
		kind      HotspotKind
		title     string
		wantInput string
	}{
		{HotspotFileExt, "Read of `.go` files", "/repo/internal/parse/parse.go"},
		{HotspotGrepGlob, "Grep: `*.go`", "func Parse in internal (glob *.go)"},
	} {
		h := findHotspot(hs, c.kind, c.title)
		if h == nil {
			t.Fatalf("no %q card in %+v", c.title, hs)
		}
		if len(h.Examples) != 1 || h.Examples[0].Input != c.wantInput {
			t.Errorf("%s examples = %+v, want one with input %q", c.title, h.Examples, c.wantInput)
		}
		if h.Examples[0].SessionID != "s" || h.Examples[0].SourceFile != path {
			t.Errorf("%s example = %+v, want session s in %s", c.title, h.Examples[0], path)
		}
	}
	for _, h := range hs {
		for _, e := range h.Examples {
			if e.Provider != parse.ProviderClaudeCode {
				t.Errorf("%s example %+v Provider = %q, want %q", h.Title, e, e.Provider, parse.ProviderClaudeCode)
			}
		}
	}
}

// TestHotspotsExamplesFromCodexRollout runs a Codex rollout through
// internal/parse/codex, so every example — tool-pattern and cache-write —
// carries the codex provider the parser stamps on its turns.
func TestHotspotsExamplesFromCodexRollout(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "parse", "codex", "testdata", "tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The fixture writes no prompt cache; give the turn after the tool calls
	// a cache write big enough to carry context cost and to count as a
	// repeated cache write.
	src := strings.Replace(string(raw),
		`"usage":{"input_tokens":35082,"cached_input_tokens":33920,"cache_write_input_tokens":0`,
		`"usage":{"input_tokens":35082,"cached_input_tokens":33920,"cache_write_input_tokens":1200000`, 1)
	if src == string(raw) {
		t.Fatal("fixture usage line not found")
	}
	const path = "/home/u/.codex/sessions/rollout.jsonl"
	res, err := codex.ParseFile(strings.NewReader(src), path)
	if err != nil {
		t.Fatal(err)
	}
	table := ctxTable()
	table.Providers = map[string]pricing.ProviderPrices{parse.ProviderCodex: {Models: map[string]pricing.ModelPrice{
		"gpt-6.1-sol": {Rate: pricing.Rate{Input: 5, Output: 25, CacheRead: 1, CacheWrite5m: 6}},
	}}}
	agg := New(table)
	for _, tr := range res.Turns {
		agg.Add(tr)
	}
	agg.AddToolResults(res.ToolResults)

	hs := agg.Hotspots(10)
	var tool, cache int
	for _, h := range hs {
		for _, e := range h.Examples {
			if e.Provider != parse.ProviderCodex {
				t.Errorf("%s example %+v Provider = %q, want %q", h.Title, e, e.Provider, parse.ProviderCodex)
			}
		}
		if h.Kind == HotspotRepeatedCacheWrite {
			cache += len(h.Examples)
		} else {
			tool += len(h.Examples)
		}
	}
	if tool == 0 || cache == 0 {
		t.Fatalf("want tool-pattern and cache-write examples, got %d and %d in %+v", tool, cache, hs)
	}
}

// TestHotspotsFromUnpatchedCodexRollout: Codex writes no prompt cache, yet
// the real tools.jsonl rollout still ranks a tool-pattern card — its second
// response's 1,162 uncached input tokens are what the three outputs added.
func TestHotspotsFromUnpatchedCodexRollout(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "parse", "codex", "testdata", "tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := codex.ParseFile(f, "/home/u/.codex/sessions/rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	table := ctxTable()
	table.Providers = map[string]pricing.ProviderPrices{parse.ProviderCodex: {Models: map[string]pricing.ModelPrice{
		"gpt-6.1-sol": {Rate: pricing.Rate{Input: 5, Output: 25, CacheRead: 0.5}},
	}}}
	agg := New(table)
	for _, tr := range res.Turns {
		agg.Add(tr)
	}
	agg.AddToolResults(res.ToolResults)

	hs := agg.Hotspots(10)
	var found bool
	for _, h := range hs {
		if h.Kind == HotspotRepeatedCacheWrite || len(h.Examples) == 0 {
			continue
		}
		all := true
		for _, e := range h.Examples {
			if e.Provider != parse.ProviderCodex || e.ContextTokens <= 0 {
				all = false
				t.Errorf("%s example %+v, want provider %q with ContextTokens > 0", h.Title, e, parse.ProviderCodex)
			}
		}
		found = found || all
	}
	if !found {
		t.Fatalf("Hotspots(10) = %+v, want a tool-pattern card of Codex examples", hs)
	}
}

// TestHotspotsFoldCodexShellIntoBash: a Claude Code Bash `go test` and a
// Codex exec_command `go test` land on one Bash pattern card whose examples
// name both providers, and no card is keyed on a raw Codex shell tool name.
func TestHotspotsFoldCodexShellIntoBash(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "parse", "codex", "testdata", "tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(string(raw),
		`"usage":{"input_tokens":35082,"cached_input_tokens":33920,"cache_write_input_tokens":0`,
		`"usage":{"input_tokens":35082,"cached_input_tokens":33920,"cache_write_input_tokens":1200000`, 1)
	if src == string(raw) {
		t.Fatal("fixture usage line not found")
	}
	cx, err := codex.ParseFile(strings.NewReader(src), "/home/u/.codex/sessions/rollout.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	cc, err := parse.ParseFile(strings.NewReader(strings.Join([]string{
		`{"type":"assistant","sessionId":"s","uuid":"a1","timestamp":"2026-05-10T10:00:00Z","message":{"id":"m1","model":"m","role":"assistant","usage":{"input_tokens":1,"output_tokens":1},"content":[` +
			`{"type":"tool_use","id":"tu-bash","name":"Bash","input":{"command":"go test ./internal/..."}}]}}`,
		`{"type":"user","sessionId":"s","uuid":"u1","timestamp":"2026-05-10T10:00:01Z","message":{"role":"user","content":[` +
			`{"type":"tool_result","tool_use_id":"tu-bash","content":"` + strings.Repeat("o", 4000) + `"}]}}`,
		`{"type":"assistant","sessionId":"s","uuid":"a2","timestamp":"2026-05-10T10:00:02Z","message":{"id":"m2","model":"m","role":"assistant","usage":{"input_tokens":1,"cache_creation_input_tokens":200000,"output_tokens":1}}}`,
	}, "\n")+"\n"), "/p/sess.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	table := ctxTable()
	table.Providers = map[string]pricing.ProviderPrices{parse.ProviderCodex: {Models: map[string]pricing.ModelPrice{
		"gpt-6.1-sol": {Rate: pricing.Rate{Input: 5, Output: 25, CacheRead: 1, CacheWrite5m: 6}},
	}}}
	agg := New(table)
	for _, res := range []parse.Result{cc, cx} {
		for _, tr := range res.Turns {
			agg.Add(tr)
		}
		agg.AddToolResults(res.ToolResults)
	}

	hs := agg.Hotspots(10)
	var goTest []Hotspot
	for _, h := range hs {
		if tool, _ := h.Context["tool"].(string); tool == "exec" || tool == "exec_command" {
			t.Errorf("card %q keyed on raw Codex tool %q", h.Title, tool)
		}
		if h.Title == "Bash: `go test`" {
			goTest = append(goTest, h)
		}
	}
	if len(goTest) != 1 || goTest[0].Kind != HotspotBashPattern {
		t.Fatalf("Bash `go test` cards = %+v, want exactly one bash_pattern card in %+v", goTest, hs)
	}
	providers := map[string]int{}
	for _, e := range goTest[0].Examples {
		providers[e.Provider]++
	}
	if len(goTest[0].Examples) != 2 || providers[parse.ProviderClaudeCode] != 1 || providers[parse.ProviderCodex] != 1 {
		t.Errorf("Bash `go test` examples = %+v, want one claude-code and one codex", goTest[0].Examples)
	}
}

// TestHotspotsCacheWritePctBasis: a repeated-cache-write card's share is of
// every session's cache-write cost, so it stays within 0–100% whether the
// window has no attributed tool results or only a tiny one; tool-pattern
// cards keep their share of context cost.
func TestHotspotsCacheWritePctBasis(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	build := func(withTool bool) []Hotspot {
		agg := New(ctxTable())
		// churn: 1.2M writes; other: 600k writes; both priced at $6/M.
		for i := 0; i < 4; i++ {
			agg.Add(ctxTurn("/p/churn.jsonl", "churn", fmt.Sprintf("c%d", i), "m", t0.Add(time.Duration(i)*10*time.Minute),
				parse.Usage{CacheCreate5mTokens: 300_000}))
		}
		agg.Add(ctxTurn("/p/other.jsonl", "other", "o0", "m", t0, parse.Usage{CacheCreate5mTokens: 600_000}))
		if withTool {
			agg.Add(ctxTurn("/p/tool.jsonl", "tool", "t0", "m", t0, parse.Usage{},
				parse.ToolUse{ID: "tu", Name: "Bash", Detail: "ls", Input: "ls"}))
			agg.Add(ctxTurn("/p/tool.jsonl", "tool", "t1", "m", t0.Add(time.Second), parse.Usage{CacheCreate5mTokens: 100}))
			agg.AddToolResults([]parse.ToolResult{{ToolUseID: "tu", OutputBytes: 40}})
		}
		return agg.Hotspots(10)
	}
	for _, withTool := range []bool{false, true} {
		hs := build(withTool)
		h := findHotspot(hs, HotspotRepeatedCacheWrite, "churn")
		if h == nil {
			t.Fatalf("withTool=%v: no churn card in %+v", withTool, hs)
		}
		// 1.2M of the 1.8M (+100 with the tool turn) cache-write tokens.
		wantTotal := 1_800_000.0
		if withTool {
			wantTotal += 100
		}
		if want := 100 * 1_200_000 / wantTotal; !approx(h.PctOfTotal, want) || h.PctBasis != PctOfCacheWriteCost {
			t.Errorf("withTool=%v: churn pct = %v basis %q, want %v of %q", withTool, h.PctOfTotal, h.PctBasis, want, PctOfCacheWriteCost)
		}
		if !withTool {
			continue
		}
		ls := findHotspot(hs, HotspotBashPattern, "ls")
		if ls == nil {
			t.Fatalf("no ls card in %+v", hs)
		}
		if !approx(ls.PctOfTotal, 100) || ls.PctBasis != PctOfContextCost {
			t.Errorf("ls pct = %v basis %q, want 100 of %q", ls.PctOfTotal, ls.PctBasis, PctOfContextCost)
		}
	}
}
