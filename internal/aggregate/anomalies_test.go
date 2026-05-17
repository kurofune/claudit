package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// Anomalies need at least anomalyWindow+1 buckets — without a full prior
// window we can't form a stable baseline.
func TestAnomalies_NoTrendOrTooShort(t *testing.T) {
	prices, _ := pricing.LoadDefault()

	noPeriod := New(prices)
	if got := noPeriod.Anomalies(); got != nil {
		t.Errorf("anomalies without period must be nil, got %v", got)
	}

	short := New(prices).WithPeriod(PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		short.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/a", t0.AddDate(0, 0, i)))
	}
	if got := short.Anomalies(); got != nil {
		t.Errorf("anomalies with 5 buckets must be nil, got %v", got)
	}
}

// Eight identical-cost days followed by a 10x-cost day must flag the
// spike on the trailing bucket.
func TestAnomalies_CostSpikeFlagged(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	// 8 steady days at 1M input.
	for i := 0; i < 8; i++ {
		agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/a", t0.AddDate(0, 0, i)))
	}
	// Spike day: 10M input — ~10x the baseline cost.
	spikeDay := t0.AddDate(0, 0, 8)
	agg.Add(turn("claude-opus-4-7", 10_000_000, 0, false, "/p/a", spikeDay))

	got := agg.Anomalies()
	if len(got) != 1 {
		t.Fatalf("want 1 anomaly, got %d (%v)", len(got), got)
	}
	a := got[0]
	if a.Kind != AnomalyCostSpike {
		t.Errorf("kind: %v", a.Kind)
	}
	if !a.Time.Equal(time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("time: %v", a.Time)
	}
	if a.Ratio < 9 || a.Ratio > 11 {
		t.Errorf("ratio: %v (want ~10)", a.Ratio)
	}
	if a.Period != PeriodDay {
		t.Errorf("period: %v", a.Period)
	}
}

// A 1.5x bump must NOT cross the 2x threshold.
func TestAnomalies_BelowThreshold(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/a", t0.AddDate(0, 0, i)))
	}
	// 1.5x — under the 2x threshold.
	agg.Add(turn("claude-opus-4-7", 1_500_000, 0, false, "/p/a", t0.AddDate(0, 0, 8)))
	if got := agg.Anomalies(); len(got) != 0 {
		t.Errorf("expected no anomalies, got %v", got)
	}
}

// A zero-cost (or very small) baseline must not produce divide-by-zero
// flags. Eight idle days followed by a 1-turn day should stay quiet.
func TestAnomalies_ZeroBaselineSuppressed(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	// One turn on day 0 (so trend mode tracks it), then gap-filled zero
	// days until day 8. The 7 prior buckets to day 8 are all zero-cost.
	agg.Add(turn("claude-opus-4-7", 100, 0, false, "/p/a", t0))
	agg.Add(turn("claude-opus-4-7", 1_000_000, 0, false, "/p/a", t0.AddDate(0, 0, 8)))
	if got := agg.Anomalies(); len(got) != 0 {
		t.Errorf("zero-baseline window must not flag, got %v", got)
	}
}

// Eight cache-rich days (>=70% hit ratio) followed by a cache-cold day
// (~0% hit ratio) must flag a hit-ratio drop.
func TestAnomalies_HitRatioDropFlagged(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	// Baseline days: input=100, cache_read=900 → 90% hit ratio.
	for i := 0; i < 8; i++ {
		agg.Add(fullTurn("s1", "/p/x", "claude-opus-4-7", 100, 0, 0, 0, 900, t0.AddDate(0, 0, i)))
	}
	// Drop day: input=1000, cache_read=0 → 0% hit ratio.
	agg.Add(fullTurn("s1", "/p/x", "claude-opus-4-7", 1000, 0, 0, 0, 0, t0.AddDate(0, 0, 8)))

	got := agg.Anomalies()
	var hr *Anomaly
	for i := range got {
		if got[i].Kind == AnomalyHitRatioDrop {
			hr = &got[i]
			break
		}
	}
	if hr == nil {
		t.Fatalf("expected hit-ratio drop anomaly, got %v", got)
	}
	if hr.Value > 0.05 {
		t.Errorf("value: %v (want ~0)", hr.Value)
	}
	if hr.Baseline < 0.85 {
		t.Errorf("baseline: %v (want ~0.9)", hr.Baseline)
	}
	if hr.Ratio < 0.85 {
		t.Errorf("ratio (pp gap): %v", hr.Ratio)
	}
}

// A bucket with zero cacheable tokens must not be flagged as a hit-ratio
// drop — there's no traffic to have a "hit ratio" for. Cost spikes
// remain eligible since cost is well-defined.
func TestAnomalies_NoCacheableTokensSkippedForHitRatio(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices).WithPeriod(PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

	// 8 cache-rich days.
	for i := 0; i < 8; i++ {
		agg.Add(fullTurn("s1", "/p/x", "claude-opus-4-7", 100, 0, 0, 0, 900, t0.AddDate(0, 0, i)))
	}
	// Drop day with NO cacheable tokens — output-only, no input or cache_read.
	agg.Add(parse.Turn{
		SessionID: "s1", CWD: "/p/x", Model: "claude-opus-4-7",
		Timestamp: t0.AddDate(0, 0, 8),
		Usage:     parse.Usage{OutputTokens: 500},
	})
	for _, a := range agg.Anomalies() {
		if a.Kind == AnomalyHitRatioDrop {
			t.Errorf("output-only bucket must not flag hit-ratio drop, got %v", a)
		}
	}
}
