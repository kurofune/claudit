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
