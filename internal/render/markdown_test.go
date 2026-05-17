package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/pricing"
)

// A 10x cost spike on the 9th bucket must render an Anomalies section
// with the kind, baseline, and ratio visible in the bullet.
func TestMarkdown_AnomaliesSectionEmitted(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := aggregate.New(prices).WithPeriod(aggregate.PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		agg.Add(mkTurn("claude-opus-4-7", "/p/a", 1_000_000, 0, t0.AddDate(0, 0, i)))
	}
	agg.Add(mkTurn("claude-opus-4-7", "/p/a", 10_000_000, 0, t0.AddDate(0, 0, 8)))

	var buf bytes.Buffer
	if err := Markdown(&buf, agg); err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "## Anomalies") {
		t.Fatalf("missing Anomalies section. got:\n%s", got)
	}
	if !strings.Contains(got, "2026-04-09") {
		t.Errorf("expected spike date 2026-04-09 in section, got:\n%s", got)
	}
	if !strings.Contains(got, "cost spike") {
		t.Errorf("expected 'cost spike' label, got:\n%s", got)
	}
	// 10M-input vs 1M-input baseline = ~10x.
	if !strings.Contains(got, "10.0×") {
		t.Errorf("expected '10.0×' ratio, got:\n%s", got)
	}
}

// A quiet report (no flagged buckets) must not emit the Anomalies
// section at all — silence keeps the markdown report scannable.
func TestMarkdown_NoAnomaliesSectionWhenQuiet(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := aggregate.New(prices).WithPeriod(aggregate.PeriodDay)
	t0 := time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		agg.Add(mkTurn("claude-opus-4-7", "/p/a", 1_000_000, 0, t0.AddDate(0, 0, i)))
	}
	var buf bytes.Buffer
	if err := Markdown(&buf, agg); err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	if strings.Contains(buf.String(), "## Anomalies") {
		t.Errorf("did not expect Anomalies section in a steady-state report")
	}
}
