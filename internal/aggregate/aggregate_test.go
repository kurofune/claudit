package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

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
	if tot.Sessions == 0 {
		// We don't track sessions without a sessionId on the turn, so this is OK.
	}
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
