package aggregate

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func TestAggregate_CountsCoalescedMessageOnce(t *testing.T) {
	// A streamed message fans out into 5 JSONL lines (thinking/text/3 tools),
	// each repeating the same cumulative usage. Once ParseFile coalesces them,
	// the aggregator must count the message's tokens/cost/turn exactly once,
	// not 5x. Opus 1M input = $5, so the dollar figure is exact and the old
	// per-line bug (which billed $25) is unambiguous.
	blocks := []string{
		`{"type":"thinking","thinking":"a"}`,
		`{"type":"text","text":"b"}`,
		`{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}`,
		`{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/x"}}`,
		`{"type":"tool_use","id":"t3","name":"Edit","input":{"file_path":"/y"}}`,
	}
	var b strings.Builder
	for i, blk := range blocks {
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"a%d","timestamp":"2026-04-10T10:00:0%dZ","message":{"id":"msg_1","model":"claude-opus-4-7","role":"assistant","usage":{"input_tokens":1000000,"output_tokens":0},"content":[%s]}}`+"\n", i, i, blk)
	}
	res, err := parse.ParseFile(strings.NewReader(b.String()), "synthetic")
	if err != nil {
		t.Fatal(err)
	}

	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	for _, tn := range res.Turns {
		agg.Add(tn)
	}
	tot := agg.Totals()
	if tot.Turns != 1 {
		t.Errorf("Turns = %d, want 1 (message counted once, not per-line)", tot.Turns)
	}
	if tot.InputTokens != 1_000_000 {
		t.Errorf("InputTokens = %d, want 1_000_000 once (not 5_000_000)", tot.InputTokens)
	}
	if tot.CostUSD < 4.99 || tot.CostUSD > 5.01 {
		t.Errorf("CostUSD = %v, want ~$5.00 once (not ~$25)", tot.CostUSD)
	}
}

func TestAggregate_DedupsDuplicateMessageIDAcrossFiles(t *testing.T) {
	// A resumed/forked session replays the prior transcript into a NEW file:
	// same message.id, usage, and uuid — only the sessionId and file path
	// differ. Both files land in the corpus, so the aggregator sees the
	// same generation twice. coalesceTurns only dedups within one file, so the
	// aggregator must drop the cross-file duplicate — counting both would
	// double-bill the tokens and cost. Opus 1M input = $5, so $10 would be the
	// unambiguous double-count signature.
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	t0 := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	orig := turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", t0)
	orig.MessageID = "msg_dup"
	orig.UUID = "uuid-a"
	orig.SessionID = "session-a"
	orig.SourceFile = "a.jsonl"

	replay := orig // same message.id + identical usage
	replay.UUID = "uuid-b"
	replay.SessionID = "session-b" // fork re-stamps uuid + sessionId
	replay.SourceFile = "b.jsonl"

	agg.Add(orig)
	agg.Add(replay)

	tot := agg.Totals()
	if tot.Turns != 1 {
		t.Errorf("Turns = %d, want 1 (duplicate generation counted once)", tot.Turns)
	}
	if tot.InputTokens != 1_000_000 {
		t.Errorf("InputTokens = %d, want 1_000_000 once (not 2_000_000)", tot.InputTokens)
	}
	if tot.CostUSD < 4.99 || tot.CostUSD > 5.01 {
		t.Errorf("CostUSD = %v, want ~$5.00 once (not ~$10)", tot.CostUSD)
	}

	// A genuinely distinct generation (different message.id) must still count.
	other := turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", t0)
	other.MessageID = "msg_other"
	agg.Add(other)
	if got := agg.Totals().InputTokens; got != 2_000_000 {
		t.Errorf("InputTokens after distinct id = %d, want 2_000_000", got)
	}
}

func TestAggregate_ReplaySet_CreditsCanonicalSessionRegardlessOfAddOrder(t *testing.T) {
	// A resumed/forked session replays the original transcript verbatim into a
	// new file (same message.id + usage; different sessionId + path). The
	// drill-downs credit the canonical copy — the lexicographically smallest
	// source file — so a turn is never billed twice. The headline per-session
	// rollup must agree, deterministically, no matter which copy Add() sees
	// first. Without WithReplaySet the first-Add-order copy wins (here the
	// replay, since it's added first), which both contradicts the drill-down
	// and is non-deterministic across runs. With it, the canonical "a.jsonl"
	// (session-a) is always credited.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	orig := turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", t0)
	orig.MessageID = "msg_dup"
	orig.UUID = "uuid-a"
	orig.SessionID = "session-a"
	orig.SourceFile = "a.jsonl" // canonical: lexicographically smallest

	replay := orig
	replay.UUID = "uuid-b"
	replay.SessionID = "session-b"
	replay.SourceFile = "b.jsonl"

	turns := []parse.Turn{replay, orig} // replay added FIRST on purpose
	agg := New(prices).WithReplaySet(BuildReplaySet(turns))
	for _, tn := range turns {
		agg.Add(tn)
	}

	rows := agg.CacheBySession()
	if len(rows) != 1 {
		t.Fatalf("CacheBySession rows = %d, want 1 (replay copy skipped)", len(rows))
	}
	if rows[0].Key != "session-a" {
		t.Errorf("credited session = %q, want %q (canonical min source file)", rows[0].Key, "session-a")
	}
	if rows[0].CostUSD < 4.99 || rows[0].CostUSD > 5.01 {
		t.Errorf("session cost = %v, want ~$5.00 once", rows[0].CostUSD)
	}
}

func TestAggregate_ReplaySet_CountsDuplicateOnceInTotals(t *testing.T) {
	// The replay-set dedup path must not double-bill the headline totals: a
	// generation present in two files (resume/fork) is one billed call, so
	// Turns, tokens, and cost all reflect a single occurrence. Opus 1M input =
	// $5, so $10 / 2M tokens / 2 turns would be the double-count signature.
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	orig := turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", t0)
	orig.MessageID = "msg_dup"
	orig.SessionID = "session-a"
	orig.SourceFile = "a.jsonl"

	replay := orig
	replay.SessionID = "session-b"
	replay.SourceFile = "b.jsonl"

	turns := []parse.Turn{replay, orig}
	agg := New(prices).WithReplaySet(BuildReplaySet(turns))
	for _, tn := range turns {
		agg.Add(tn)
	}

	tot := agg.Totals()
	if tot.Turns != 1 {
		t.Errorf("Turns = %d, want 1 (duplicate counted once)", tot.Turns)
	}
	if tot.InputTokens != 1_000_000 {
		t.Errorf("InputTokens = %d, want 1_000_000 (not 2_000_000)", tot.InputTokens)
	}
	if tot.CostUSD < 4.99 || tot.CostUSD > 5.01 {
		t.Errorf("CostUSD = %v, want ~$5.00 (not ~$10)", tot.CostUSD)
	}
}

func turn(model string, in, out int, sidechain bool, cwd string, ts time.Time, tools ...parse.ToolUse) parse.Turn {
	return parse.Turn{
		Model:     model,
		Sidechain: sidechain,
		Timestamp: ts,
		CWD:       cwd,
		Usage: parse.Usage{
			InputTokens:  in,
			OutputTokens: out,
		},
		ToolUses: tools,
	}
}

func TestAggregate_Basic(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	t0 := time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC)

	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/foo", t0))
	agg.Add(turn("claude-sonnet-4-6", 0, 1_000_000, false, "/p/foo", t0))
	agg.Add(turn("claude-haiku-4-5-20251001", 100, 200, true, "/p/bar", t0))

	tot := agg.Totals()
	// Note: tot.Sessions may be 0 here because the turn helper doesn't
	// set SessionID, and we only count sessions with an ID.
	if tot.InputTokens != 1_000_100 {
		t.Errorf("input total: %d", tot.InputTokens)
	}
	if tot.OutputTokens != 1_000_200 {
		t.Errorf("output total: %d", tot.OutputTokens)
	}
	// $5 (opus 1M input) + $15 (sonnet 1M output) = $20, plus tiny haiku
	if tot.CostUSD < 19.99 || tot.CostUSD > 20.10 {
		t.Errorf("cost: %v", tot.CostUSD)
	}

	byModel := agg.ByModel()
	if len(byModel) != 3 {
		t.Errorf("by model: %d entries", len(byModel))
	}

	byProj := agg.ByProject()
	if len(byProj) != 2 {
		t.Errorf("by project: %d entries", len(byProj))
	}

	side := agg.SidechainSplit()
	if side.Main.OutputTokens != 1_000_000 || side.Sidechain.OutputTokens != 200 {
		t.Errorf("sidechain split wrong: %+v", side)
	}
}

func TestAggregate_Tools(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	ts := time.Now()
	agg.Add(turn("claude-opus-4-7", 0, 100, false, "/p/foo", ts,
		parse.ToolUse{Name: "Bash"}, parse.ToolUse{Name: "Bash"}))
	agg.Add(turn("claude-opus-4-7", 0, 200, false, "/p/foo", ts,
		parse.ToolUse{Name: "Read"}))
	agg.Add(turn("claude-opus-4-7", 0, 50, false, "/p/foo", ts)) // no tool use

	bt := agg.ByTool()
	bash := findTool(bt, "Bash")
	read := findTool(bt, "Read")
	if bash == nil || read == nil {
		t.Fatal("missing tool entries")
	}
	if bash.Count != 2 {
		t.Errorf("bash count: %d", bash.Count)
	}
	// Bash turn produced 100 output, the other Bash turn was the same as that turn
	// — actually only one turn used Bash twice. So output_tokens for the Bash bucket
	// should be the Bash-using turn's output tokens (100), not 200.
	if bash.OutputTokens != 100 {
		t.Errorf("bash output tokens: %d (expected 100, the turn's output)", bash.OutputTokens)
	}
	if read.OutputTokens != 200 {
		t.Errorf("read output tokens: %d", read.OutputTokens)
	}
}

func TestAggregate_Skill(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	ts := time.Now()
	agg.Add(turn("claude-opus-4-7", 0, 100, false, "/p/foo", ts,
		parse.ToolUse{Name: "Skill", SkillName: "tdd"}))
	agg.Add(turn("claude-opus-4-7", 0, 200, false, "/p/foo", ts,
		parse.ToolUse{Name: "SlashCommand", SlashCommand: "/review"}))
	agg.Add(turn("claude-opus-4-7", 0, 300, false, "/p/foo", ts,
		parse.ToolUse{Name: "Skill", SkillName: "tdd"}))

	bs := agg.BySkill()
	tdd := findSkill(bs, "skill:tdd")
	rev := findSkill(bs, "command:/review")
	if tdd == nil || rev == nil {
		t.Fatal("missing skill entries")
	}
	if tdd.Count != 2 {
		t.Errorf("tdd count: %d", tdd.Count)
	}
}

// TestAggregate_SkillTokens: skill buckets carry the full token tuple
// (not just output), so the Tokens tab can break a skill's spend into
// input / output / cache categories the same way Cost does.
func TestAggregate_SkillTokens(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	ts := time.Now()
	// Two invocations of the same skill, each with input + output.
	agg.Add(turn("claude-opus-4-7", 500, 100, false, "/p/foo", ts,
		parse.ToolUse{Name: "Skill", SkillName: "tdd"}))
	agg.Add(turn("claude-opus-4-7", 700, 300, false, "/p/foo", ts,
		parse.ToolUse{Name: "Skill", SkillName: "tdd"}))

	tdd := findSkill(agg.BySkill(), "skill:tdd")
	if tdd == nil {
		t.Fatal("missing skill:tdd entry")
	}
	if tdd.InputTokens != 1200 {
		t.Errorf("tdd input tokens: %d, want 1200", tdd.InputTokens)
	}
	if tdd.OutputTokens != 400 {
		t.Errorf("tdd output tokens: %d, want 400", tdd.OutputTokens)
	}
}

func TestAggregate_DateFilter(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	since := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	agg := New(prices).WithFilter(Filter{Since: since, Until: until})

	agg.Add(turn("claude-opus-4-7", 100, 0, false, "/p/foo",
		time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))) // before
	agg.Add(turn("claude-opus-4-7", 200, 0, false, "/p/foo",
		time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC))) // in
	agg.Add(turn("claude-opus-4-7", 400, 0, false, "/p/foo",
		time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC))) // after

	if agg.Totals().InputTokens != 200 {
		t.Errorf("filter mismatch: %d", agg.Totals().InputTokens)
	}
}

func TestAggregate_ProjectFilter(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithFilter(Filter{ProjectSubstring: "claudit"})
	ts := time.Now()
	agg.Add(turn("claude-opus-4-7", 100, 0, false, "/Users/x/Projects/claudit", ts))
	agg.Add(turn("claude-opus-4-7", 999, 0, false, "/Users/x/Projects/other", ts))
	if agg.Totals().InputTokens != 100 {
		t.Errorf("project filter: %d", agg.Totals().InputTokens)
	}
}

func TestAggregate_ToolDetail(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	ts := time.Now()
	// Turn 1: two `git status` Bashes (cost should be counted once).
	agg.Add(turn("claude-opus-4-7", 0, 100, false, "/p", ts,
		parse.ToolUse{Name: "Bash", Detail: "git status"},
		parse.ToolUse{Name: "Bash", Detail: "git status"}))
	// Turn 2: a `git log` and a Read.
	agg.Add(turn("claude-opus-4-7", 0, 200, false, "/p", ts,
		parse.ToolUse{Name: "Bash", Detail: "git log"},
		parse.ToolUse{Name: "Read", Detail: ".go"}))
	// Turn 3: another git status (different turn, contributes again).
	agg.Add(turn("claude-opus-4-7", 0, 50, false, "/p", ts,
		parse.ToolUse{Name: "Bash", Detail: "git status"}))

	det := agg.ByToolDetail()
	bashRows := det["Bash"]
	if len(bashRows) != 2 {
		t.Fatalf("Bash rows: %+v", bashRows)
	}
	// Sorted by cost desc — git status appears in 2 turns (100+50=150 output)
	// vs git log in 1 turn (200 output). git log has higher output so should be first.
	if bashRows[0].Detail != "git log" {
		t.Errorf("expected git log first, got %+v", bashRows)
	}
	gs := bashRows[1]
	if gs.Detail != "git status" || gs.Count != 3 || gs.TurnCount != 2 {
		t.Errorf("git status row: %+v", gs)
	}
	if gs.OutputTokens != 150 {
		t.Errorf("git status output tokens: %d", gs.OutputTokens)
	}

	readRows := det["Read"]
	if len(readRows) != 1 || readRows[0].Detail != ".go" {
		t.Errorf("read rows: %+v", readRows)
	}
}

func TestAggregate_AgentInvocations(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	t0 := time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC)

	// Two sidechain turns from one invocation file
	tA1 := turn("claude-opus-4-7", 0, 100, true, "/p/foo", t0)
	tA1.SourceFile = "/sub/agent-aaa.jsonl"
	tA2 := turn("claude-opus-4-7", 0, 200, true, "/p/foo", t0.Add(time.Minute))
	tA2.SourceFile = "/sub/agent-aaa.jsonl"
	// One sidechain turn from a different invocation file
	tB1 := turn("claude-opus-4-7", 0, 50, true, "/p/foo", t0.Add(time.Hour))
	tB1.SourceFile = "/sub/agent-bbb.jsonl"
	// And a non-sidechain turn that must NOT show up
	tMain := turn("claude-opus-4-7", 0, 9999, false, "/p/foo", t0)
	tMain.SourceFile = "/sub/agent-aaa.jsonl"

	lookup := func(tn parse.Turn) (string, string) {
		switch tn.SourceFile {
		case "/sub/agent-aaa.jsonl":
			return "general-purpose", "Find every reference to widget"
		case "/sub/agent-bbb.jsonl":
			return "Explore", "Map the engine package"
		}
		return "", ""
	}
	agg.AddWithSubagent(tA1, lookup)
	agg.AddWithSubagent(tA2, lookup)
	agg.AddWithSubagent(tB1, lookup)
	agg.AddWithSubagent(tMain, lookup)

	all := agg.AgentInvocations("")
	if len(all) != 2 {
		t.Fatalf("invocations: %+v", all)
	}
	// Higher-cost invocation first (aaa: 300 output, bbb: 50)
	if all[0].SourceFile != "/sub/agent-aaa.jsonl" {
		t.Errorf("expected aaa first, got %s", all[0].SourceFile)
	}
	if all[0].Turns != 2 || all[0].SubagentType != "general-purpose" {
		t.Errorf("aaa metadata wrong: %+v", all[0])
	}
	if all[0].Description != "Find every reference to widget" {
		t.Errorf("description: %q", all[0].Description)
	}
	if !all[0].First.Equal(t0) || !all[0].Last.Equal(t0.Add(time.Minute)) {
		t.Errorf("ts range: %v..%v", all[0].First, all[0].Last)
	}

	// Filter by type
	gp := agg.AgentInvocations("general-purpose")
	if len(gp) != 1 || gp[0].SourceFile != "/sub/agent-aaa.jsonl" {
		t.Errorf("filter: %+v", gp)
	}
}

// Turns are priced at the rate in effect when they ran, not today's rate:
// two identical Sonnet 5 turns on either side of the 2026-08-31 intro-rate
// cliff cost different amounts.
func TestAggregate_PricesTurnsAtTheirOwnDateRate(t *testing.T) {
	prices, _ := pricing.LoadDefault()

	intro := New(prices)
	intro.Add(turn("claude-sonnet-5", 1_000_000, 1_000_000, false, "/p/foo",
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)))
	if got := intro.Totals().CostUSD; math.Abs(got-12.0) > 0.001 {
		t.Errorf("intro-period turn cost %v, want 12 ($2/$10)", got)
	}

	std := New(prices)
	std.Add(turn("claude-sonnet-5", 1_000_000, 1_000_000, false, "/p/foo",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)))
	if got := std.Totals().CostUSD; math.Abs(got-18.0) > 0.001 {
		t.Errorf("post-cliff turn cost %v, want 18 ($3/$15)", got)
	}
}

// A turn with no usable timestamp prices at the model's current rate.
func TestAggregate_ZeroTimestampUsesCurrentRate(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	agg.Add(turn("claude-sonnet-5", 1_000_000, 1_000_000, false, "/p/foo", time.Time{}))
	if got := agg.Totals().CostUSD; math.Abs(got-18.0) > 0.001 {
		t.Errorf("zero-timestamp turn cost %v, want current rate 18", got)
	}
}

func TestAggregate_UnknownModel(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	agg.Add(turn("not-a-model-2099", 1000, 0, false, "/p/foo", time.Now()))
	if len(agg.UnknownModels()) != 1 || agg.UnknownModels()[0] != "not-a-model-2099" {
		t.Errorf("unknown models: %v", agg.UnknownModels())
	}
}

func findTool(s []ToolBucket, name string) *ToolBucket {
	for i := range s {
		if s[i].Name == name {
			return &s[i]
		}
	}
	return nil
}

func findSkill(s []SkillBucket, key string) *SkillBucket {
	for i := range s {
		if s[i].Key == key {
			return &s[i]
		}
	}
	return nil
}
