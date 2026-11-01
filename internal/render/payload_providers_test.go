package render

import (
	"math"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func providerTurn(provider, session, model string, in, out int, ts time.Time) parse.Turn {
	t := mkTurn(model, "/p/x", in, out, ts)
	t.Provider = provider
	t.SessionID = session
	return t
}

// TestBuildOverview_PerProviderTotals: one row per provider that has data,
// carrying its sessions, turns, dollars and share of total dollars.
func TestBuildOverview_PerProviderTotals(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	a := aggregate.New(prices)
	a.Add(providerTurn(parse.ProviderClaudeCode, "c1", "claude-opus-4-7", 1_000_000, 100_000, ts))
	a.Add(providerTurn(parse.ProviderClaudeCode, "c1", "claude-opus-4-7", 500_000, 50_000, ts.Add(time.Minute)))
	a.Add(providerTurn(parse.ProviderClaudeCode, "c2", "claude-opus-4-7", 200_000, 10_000, ts.Add(2*time.Minute)))
	a.Add(providerTurn(parse.ProviderCodex, "x1", "gpt-6-astra", 300_000, 20_000, ts.Add(3*time.Minute)))

	p := BuildOverview(a, nil)
	if len(p.Providers) != 2 {
		t.Fatalf("Providers = %+v, want two rows", p.Providers)
	}
	rows := map[string]ProviderTotal{}
	sum := 0.0
	costSum := 0.0
	for _, r := range p.Providers {
		rows[r.Provider] = r
		sum += r.Share
		costSum += r.CostUSD
	}
	cc, cx := rows["claude-code"], rows["codex"]
	if cc.Sessions != 2 || cc.Turns != 3 {
		t.Errorf("claude-code row = %+v, want 2 sessions / 3 turns", cc)
	}
	if cx.Sessions != 1 || cx.Turns != 1 {
		t.Errorf("codex row = %+v, want 1 session / 1 turn", cx)
	}
	if cc.CostUSD <= 0 || cx.CostUSD <= 0 {
		t.Fatalf("both providers must be priced: %+v", p.Providers)
	}
	if math.Abs(costSum-p.Totals.CostUSD) > 1e-9 {
		t.Errorf("provider dollars sum to %v, totals say %v", costSum, p.Totals.CostUSD)
	}
	if math.Abs(sum-100) > 1e-9 {
		t.Errorf("shares sum to %v, want 100", sum)
	}
	if want := 100 * cx.CostUSD / p.Totals.CostUSD; math.Abs(cx.Share-want) > 1e-9 {
		t.Errorf("codex share = %v, want %v", cx.Share, want)
	}
	if p.Providers[0].CostUSD < p.Providers[1].CostUSD {
		t.Errorf("rows not ordered by dollars: %+v", p.Providers)
	}

	only := aggregate.New(prices).WithFilter(aggregate.Filter{Provider: parse.ProviderCodex})
	only.Add(providerTurn(parse.ProviderClaudeCode, "c1", "claude-opus-4-7", 1_000_000, 100_000, ts))
	only.Add(providerTurn(parse.ProviderCodex, "x1", "gpt-6-astra", 300_000, 20_000, ts))
	po := BuildOverview(only, nil)
	if len(po.Providers) != 1 || po.Providers[0].Provider != "codex" || po.Providers[0].Share != 100 {
		t.Errorf("provider=codex Providers = %+v, want only codex at 100%%", po.Providers)
	}
}

// TestBuildOverview_PlanUsagePicksLatestSample: plan usage is the latest
// rate-limit sample inside the window; samples outside it, or from a
// provider the filter excludes, never count.
func TestBuildOverview_PlanUsagePicksLatestSample(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	resets := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	sample := func(ts time.Time, used float64) parse.RateLimitSample {
		return parse.RateLimitSample{
			SessionID: "x1", Provider: parse.ProviderCodex, Timestamp: ts,
			UsedPercent: used, WindowMinutes: 10080, ResetsAt: resets, PlanType: "pro",
		}
	}
	latest := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	samples := []parse.RateLimitSample{
		sample(time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC), 12),
		sample(latest, 31.5),
		sample(time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC), 4),
		sample(time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC), 99),  // after the window
		sample(time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC), 98), // before it
	}

	a := aggregate.New(prices).WithFilter(aggregate.Filter{Since: since, Until: until})
	a.AddRateLimitSamples(samples)
	pu := BuildOverview(a, nil).PlanUsage
	if pu == nil {
		t.Fatal("PlanUsage = nil, want the latest in-window sample")
	}
	want := PlanUsage{
		Provider: "codex", UsedPercent: 31.5, WindowMinutes: 10080,
		ResetsAt: resets, PlanType: "pro", SampledAt: latest,
	}
	if *pu != want {
		t.Errorf("PlanUsage = %+v, want %+v", *pu, want)
	}

	none := aggregate.New(prices).WithFilter(aggregate.Filter{Since: since, Until: until})
	none.AddRateLimitSamples(samples[3:])
	if pu := BuildOverview(none, nil).PlanUsage; pu != nil {
		t.Errorf("PlanUsage = %+v, want nil when no sample is in the window", *pu)
	}

	claude := aggregate.New(prices).WithFilter(aggregate.Filter{Provider: parse.ProviderClaudeCode})
	claude.AddRateLimitSamples(samples)
	if pu := BuildOverview(claude, nil).PlanUsage; pu != nil {
		t.Errorf("PlanUsage = %+v under provider=claude-code, want nil", *pu)
	}
}
