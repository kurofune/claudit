package aggregate

import (
	"sort"

	"github.com/kurofune/claudit/v2/internal/parse"
	"github.com/kurofune/claudit/v2/internal/pricing"
)

// ProviderBucket is one agent tool's share of the counted turns.
type ProviderBucket struct {
	Provider string
	Sessions int
	Turns    int
	CostUSD  float64
}

type providerAccum struct {
	turns    int
	cost     float64
	sessions map[string]struct{}
}

func (a *Aggregator) addProvider(t parse.Turn, cost float64) {
	name := pricing.PriceProvider(t.Provider)
	p := a.byProvider[name]
	if p == nil {
		p = &providerAccum{sessions: map[string]struct{}{}}
		a.byProvider[name] = p
	}
	p.turns++
	p.cost += cost
	if t.SessionID != "" {
		p.sessions[t.SessionID] = struct{}{}
	}
}

// ByProvider returns one row per provider with counted turns, most dollars
// first, ties broken by name.
func (a *Aggregator) ByProvider() []ProviderBucket {
	out := make([]ProviderBucket, 0, len(a.byProvider))
	for name, p := range a.byProvider {
		out = append(out, ProviderBucket{Provider: name, Sessions: len(p.sessions), Turns: p.turns, CostUSD: p.cost})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		return out[i].Provider < out[j].Provider
	})
	return out
}
