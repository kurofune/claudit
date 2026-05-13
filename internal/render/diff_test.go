package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func mkTurn(model, cwd string, in, out int, ts time.Time) parse.Turn {
	return parse.Turn{
		Model: model, CWD: cwd, Timestamp: ts,
		Usage: parse.Usage{InputTokens: in, OutputTokens: out},
	}
}

func TestModelMovers_KeysOnEitherSide(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	a := aggregate.New(prices)
	a.Add(mkTurn("claude-opus-4-7", "/p/x", 1_000_000, 0, t0))
	a.Add(mkTurn("claude-sonnet-4-6", "/p/x", 0, 1_000_000, t0))

	b := aggregate.New(prices)
	b.Add(mkTurn("claude-opus-4-7", "/p/x", 2_000_000, 0, t0))
	b.Add(mkTurn("claude-haiku-4-5-20251001", "/p/x", 100, 200, t0))

	rows := ModelMovers(a, b)
	got := map[string]DiffMover{}
	for _, r := range rows {
		got[r.Key] = r
	}
	if len(got) != 3 {
		t.Fatalf("want 3 unique models (opus,sonnet,haiku), got %d: %+v", len(got), got)
	}
	// Opus exists in both — costs should differ.
	if got["claude-opus-4-7"].CostA == 0 || got["claude-opus-4-7"].CostB == 0 {
		t.Errorf("opus should be in both: %+v", got["claude-opus-4-7"])
	}
	// Sonnet only in A.
	if got["claude-sonnet-4-6"].CostA == 0 || got["claude-sonnet-4-6"].CostB != 0 {
		t.Errorf("sonnet only-in-A wrong: %+v", got["claude-sonnet-4-6"])
	}
	// Haiku only in B.
	if got["claude-haiku-4-5-20251001"].CostA != 0 || got["claude-haiku-4-5-20251001"].CostB == 0 {
		t.Errorf("haiku only-in-B wrong: %+v", got["claude-haiku-4-5-20251001"])
	}
}

func TestRankMovers_ByAbsoluteDelta(t *testing.T) {
	rows := []DiffMover{
		{Key: "a", CostA: 10, CostB: 12},  // delta +2
		{Key: "b", CostA: 100, CostB: 0},  // delta -100  (largest)
		{Key: "c", CostA: 5, CostB: 80},   // delta +75
		{Key: "d", CostA: 50, CostB: 50},  // delta 0
	}
	ranked := rankMovers(rows, 0)
	wantOrder := []string{"b", "c", "a", "d"}
	for i, k := range wantOrder {
		if ranked[i].Key != k {
			t.Errorf("rank[%d] = %q, want %q", i, ranked[i].Key, k)
		}
	}
	// Top-N trims.
	if got := rankMovers(rows, 2); len(got) != 2 || got[0].Key != "b" || got[1].Key != "c" {
		t.Errorf("top-2 wrong: %+v", got)
	}
}

func TestDeltaFormatting(t *testing.T) {
	if got := deltaMoney(100, 130); got != "+$30.00" {
		t.Errorf("deltaMoney positive: %s", got)
	}
	if got := deltaMoney(100, 70); got != "-$30.00" {
		t.Errorf("deltaMoney negative: %s", got)
	}
	if got := deltaMoney(100, 100); got != "$0.00" {
		t.Errorf("deltaMoney zero: %s", got)
	}
	if got := deltaInt(5, 10); got != "+5" {
		t.Errorf("deltaInt positive: %s", got)
	}
	if got := deltaInt(10, 5); got != "-5" {
		t.Errorf("deltaInt negative: %s", got)
	}
	// hit-ratio delta in percentage points
	if got := deltaRatio(0.50, 0.62); got != "+12.0pp" {
		t.Errorf("deltaRatio positive: %s", got)
	}
	if got := deltaRatio(0.62, 0.50); got != "-12.0pp" {
		t.Errorf("deltaRatio negative: %s", got)
	}
	if got := deltaRatio(0, 0); got != "—" {
		t.Errorf("deltaRatio zero: %s", got)
	}
	// Reuses existing deltaPct convention from markdown.go.
	if got := deltaPct(100, 130); got != "+30.0%" {
		t.Errorf("deltaPct positive: %s", got)
	}
}

func TestDiffMarkdown_TopMoversAndNewHotspots(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	a := aggregate.New(prices)
	a.Add(mkTurn("claude-opus-4-7", "/p/old", 1_000_000, 0, t0))

	b := aggregate.New(prices)
	b.Add(mkTurn("claude-opus-4-7", "/p/old", 1_000_000, 0, t0))
	b.Add(mkTurn("claude-opus-4-7", "/p/new", 5_000_000, 0, t0)) // big new project

	var buf bytes.Buffer
	if err := DiffMarkdown(&buf, a, b, DiffOptions{LabelA: "A", LabelB: "B", TopMovers: 5, Hotspots: 5}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// Header + sections.
	for _, want := range []string{
		"# claudit diff",
		"## Totals",
		"## Top movers — By project",
		"## Top movers — By model",
		"/p/new",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Δ$ formatting present.
	if !strings.Contains(out, "+$") {
		t.Errorf("expected a +$ delta somewhere:\n%s", out)
	}
}

func TestDiffMarkdown_EmptySections(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	a := aggregate.New(prices)
	b := aggregate.New(prices)

	var buf bytes.Buffer
	if err := DiffMarkdown(&buf, a, b, DiffOptions{LabelA: "A", LabelB: "B"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "(no rows)") {
		t.Errorf("empty mover sections should say (no rows):\n%s", out)
	}
}

func TestDiffJSON_Shape(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	a := aggregate.New(prices)
	a.Add(mkTurn("claude-opus-4-7", "/p/x", 1_000_000, 0, t0))
	b := aggregate.New(prices)
	b.Add(mkTurn("claude-opus-4-7", "/p/x", 2_000_000, 0, t0))

	var buf bytes.Buffer
	if err := DiffJSON(&buf, a, b, DiffOptions{LabelA: "A", LabelB: "B", TopMovers: 5}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`"label_a": "A"`, `"label_b": "B"`,
		`"totals_a"`, `"totals_b"`,
		`"model_movers"`, `"project_movers"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in JSON:\n%s", want, out)
		}
	}
}
