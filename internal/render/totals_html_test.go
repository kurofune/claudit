package render

import (
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/aggregate"
)

// totalsInput bundles every input renderTotalsHTML needs. Kept as a
// single struct so adding a new field doesn't churn every test
// signature.
type totalsInput struct {
	totals          aggregate.Totals
	overallHitRatio float64
	period          aggregate.Period
	trendTotals     []aggregate.TrendPoint
}

// baseTotalsInput is the minimum-valid input — one session, one turn,
// non-zero cost. Tests tweak fields as needed.
func baseTotalsInput() totalsInput {
	return totalsInput{
		totals: aggregate.Totals{
			CostUSD:  4.5,
			Sessions: 1,
			Turns:    2,
		},
		overallHitRatio: 0.0,
	}
}

// TestRenderTotalsHTML_HeadlineCostShown: the cost headline carries the
// formatted dollar value alongside the "Total cost" label. This is the
// minimum proof the SSR path is wired up.
func TestRenderTotalsHTML_HeadlineCostShown(t *testing.T) {
	in := baseTotalsInput()
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	if !strings.Contains(got, `<div class="headline">`) {
		t.Errorf("missing headline div; got: %s", got)
	}
	if !strings.Contains(got, `Total cost`) {
		t.Errorf("missing 'Total cost' label; got: %s", got)
	}
	if !strings.Contains(got, `$4.50`) {
		t.Errorf("missing formatted cost value $4.50; got: %s", got)
	}
}

// TestRenderTotalsHTML_CostDeltaBadOnRise: with --by=day and two
// trend buckets where the latest cost is higher than the prior, a
// `<div class="delta delta-bad">` chip is emitted next to the
// headline cost value (rising cost is bad semantics).
func TestRenderTotalsHTML_CostDeltaBadOnRise(t *testing.T) {
	in := baseTotalsInput()
	in.period = aggregate.PeriodDay
	in.trendTotals = []aggregate.TrendPoint{
		{CostUSD: 1.00},
		{CostUSD: 1.50}, // +50%
	}
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	if !strings.Contains(got, `class="delta delta-bad"`) {
		t.Errorf("rising cost should render delta-bad; got: %s", got)
	}
	if !strings.Contains(got, `50.0%`) {
		t.Errorf("delta should show 50.0%% magnitude; got: %s", got)
	}
	if !strings.Contains(got, `vs prior day`) {
		t.Errorf("delta tail should reference period name; got: %s", got)
	}
}

// TestRenderTotalsHTML_NoPeriodNoDeltas: with no --by period set, no
// `<div class="delta">` blocks render. The deltas only make sense
// when there's a meaningful "vs prior bucket" comparison.
func TestRenderTotalsHTML_NoPeriodNoDeltas(t *testing.T) {
	in := baseTotalsInput()
	// period zero, trend empty
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	if strings.Contains(got, `class="delta`) {
		t.Errorf("no period set should produce no delta blocks; got: %s", got)
	}
}

// TestRenderTotalsHTML_SessionsDeltaNeutral: a rising Sessions count
// renders a delta-neutral chip (sessions are directional but not
// value-laden — "more sessions" isn't good or bad).
func TestRenderTotalsHTML_SessionsDeltaNeutral(t *testing.T) {
	in := baseTotalsInput()
	in.period = aggregate.PeriodWeek
	in.trendTotals = []aggregate.TrendPoint{
		{Sessions: 2},
		{Sessions: 4}, // +100%
	}
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	if !strings.Contains(got, `class="delta delta-neutral"`) {
		t.Errorf("rising sessions should render delta-neutral; got: %s", got)
	}
	if !strings.Contains(got, `vs prior week`) {
		t.Errorf("delta tail should reference 'week'; got: %s", got)
	}
}

// TestRenderTotalsHTML_HitDeltaGoodInPP: rising hit ratio renders a
// delta-good chip with `pp` (percentage points) unit, not `%`. JS
// asPP=true treats 0.50→0.55 as +5pp, not +10%.
func TestRenderTotalsHTML_HitDeltaGoodInPP(t *testing.T) {
	in := baseTotalsInput()
	in.period = aggregate.PeriodDay
	// Construct trend with distinct cacheable distributions so
	// pointHitRatio() returns 0.5 then 0.7.
	in.trendTotals = []aggregate.TrendPoint{
		{Tokens: aggregate.Tokens{InputTokens: 50, CacheReadTokens: 50}}, // 50%
		{Tokens: aggregate.Tokens{InputTokens: 30, CacheReadTokens: 70}}, // 70%
	}
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	// Rising hit ratio is good.
	if !strings.Contains(got, `class="delta delta-good"`) {
		t.Errorf("rising hit ratio should render delta-good; got: %s", got)
	}
	// 70% - 50% = 20pp.
	if !strings.Contains(got, `20.0pp`) {
		t.Errorf("hit-ratio delta should report 20.0pp; got: %s", got)
	}
}

// TestRenderTotalsHTML_PriorZeroSkipsRelativeDelta: in relative mode
// (asPP=false), a prior value of 0 makes "% change" undefined, so the
// delta block is suppressed for that tile.
func TestRenderTotalsHTML_PriorZeroSkipsRelativeDelta(t *testing.T) {
	in := baseTotalsInput()
	in.period = aggregate.PeriodDay
	// Cost delta: prior=0. Sessions also 0/2 so both relative tiles
	// should skip. (Turns same shape.) Hit ratio stays pp-mode so
	// can still render.
	in.trendTotals = []aggregate.TrendPoint{
		{CostUSD: 0, Sessions: 0, Turns: 0},
		{CostUSD: 5, Sessions: 2, Turns: 1},
	}
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	if strings.Contains(got, "delta-bad") || strings.Contains(got, "delta-good") || strings.Contains(got, "delta-neutral") {
		t.Errorf("prior=0 should suppress relative-mode deltas; got: %s", got)
	}
}

// TestRenderTotalsHTML_FlatDelta: when the relative change magnitude
// is < 0.1% (or asPP change < 0.5pp), the chip renders "flat".
func TestRenderTotalsHTML_FlatDelta(t *testing.T) {
	in := baseTotalsInput()
	in.period = aggregate.PeriodDay
	in.trendTotals = []aggregate.TrendPoint{
		{CostUSD: 100.0},
		{CostUSD: 100.05}, // 0.05% — below 0.1 eps
	}
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	if !strings.Contains(got, `class="delta delta-flat"`) {
		t.Errorf("sub-eps change should render delta-flat; got: %s", got)
	}
}

// TestRenderTotalsHTML_HitRatioTierPill: the Cache-hit-ratio metric's
// value is a tier pill whose class reflects the threshold tier
// (≥0.70=good, ≥0.40=ok, else=bad). Mirrors the JS hitRatioPill
// rules at report.html.tmpl:2564-2569.
func TestRenderTotalsHTML_HitRatioTierPill(t *testing.T) {
	cases := []struct {
		ratio float64
		want  string
	}{
		{0.85, `<span class="tier tier-good">`}, // ≥0.70
		{0.50, `<span class="tier tier-ok">`},   // ≥0.40
		{0.10, `<span class="tier tier-bad">`},  // below
	}
	for _, c := range cases {
		in := baseTotalsInput()
		in.overallHitRatio = c.ratio
		got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
		if !strings.Contains(got, c.want) {
			t.Errorf("ratio=%v: want %q in output; got: %s", c.ratio, c.want, got)
		}
	}
}

// TestRenderTotalsHTML_ThreeMetricBlocks: the totals carries three
// <div class="metric"> blocks — one each for Sessions, Assistant
// turns, Cache hit ratio. Each carries the corresponding label text.
func TestRenderTotalsHTML_ThreeMetricBlocks(t *testing.T) {
	in := baseTotalsInput()
	got := string(renderTotalsHTML(in.totals, in.overallHitRatio, in.period, in.trendTotals))
	for _, label := range []string{"Sessions", "Assistant turns", "Cache hit ratio"} {
		if !strings.Contains(got, label) {
			t.Errorf("missing metric label %q; got: %s", label, got)
		}
	}
	// Three .metric blocks.
	if c := strings.Count(got, `<div class="metric">`); c != 3 {
		t.Errorf("want 3 metric blocks; got %d. body: %s", c, got)
	}
	// Session count and turn count come from totals.
	if !strings.Contains(got, `>1</div>`) { // Sessions=1
		t.Errorf("missing sessions count 1 value; got: %s", got)
	}
	if !strings.Contains(got, `>2</div>`) { // Turns=2
		t.Errorf("missing turns count 2 value; got: %s", got)
	}
}
