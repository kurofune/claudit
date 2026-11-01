package pricing

import (
	"math"
	"regexp"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// codexPricingSource is the page every bundled Codex rate is copied from.
var codexPricingSource = regexp.MustCompile(`https://(?:openai\.com/api/pricing/|platform\.openai\.com/docs/pricing)`)

// codexFreeModels are the Codex models OpenAI states are free, so their
// all-zero rates are deliberate.
var codexFreeModels = map[string]bool{"codex-auto-review": true}

var isoDate = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)

// codexModelNodes returns the key and value nodes of providers.codex.models
// in the bundled YAML, so comments can be read alongside the rates.
func codexModelNodes(t *testing.T) map[string][2]*yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(defaultYAML, &doc); err != nil {
		t.Fatal(err)
	}
	child := func(n *yaml.Node, key string) *yaml.Node {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	n := doc.Content[0]
	for _, k := range []string{"providers", "codex", "models"} {
		if n = child(n, k); n == nil {
			t.Fatalf("default.yaml has no providers.codex.models (missing %q)", k)
		}
	}
	out := map[string][2]*yaml.Node{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out[n.Content[i].Value] = [2]*yaml.Node{n.Content[i], n.Content[i+1]}
	}
	return out
}

func TestDefault_CodexEntriesCiteSource(t *testing.T) {
	nodes := codexModelNodes(t)
	for _, m := range []string{"gpt-6-astra", "gpt-6-sol", "gpt-6.1-sol", "gpt-5.5"} {
		if _, ok := nodes[m]; !ok {
			t.Errorf("providers.codex.models lacks %s, which OpenAI's pricing page lists", m)
		}
	}
	if _, ok := nodes["codex-auto-review"]; !ok {
		t.Error("providers.codex.models lacks codex-auto-review, which OpenAI's Codex FAQ says is free")
	}
	for name, kv := range nodes {
		comment := kv[0].HeadComment + " " + kv[0].LineComment + " " + kv[1].HeadComment + " " + kv[1].LineComment
		if !codexPricingSource.MatchString(comment) {
			t.Errorf("codex/%s comment %q does not cite OpenAI's pricing page URL", name, comment)
		}
		if !isoDate.MatchString(comment) {
			t.Errorf("codex/%s comment %q carries no YYYY-MM-DD fetch date", name, comment)
		}
	}

	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if len(tab.Providers["codex"].Models) != len(nodes) {
		t.Fatalf("loaded %d codex models, YAML holds %d", len(tab.Providers["codex"].Models), len(nodes))
	}
	for name, p := range tab.Providers["codex"].Models {
		if p.Rate == (Rate{}) && !codexFreeModels[name] {
			t.Errorf("codex/%s has all-zero rates", name)
		}
		if p.Rate != (Rate{}) && codexFreeModels[name] {
			t.Errorf("codex/%s is free per OpenAI's Codex FAQ but carries rates %+v", name, p.Rate)
		}
	}
}

// The bundled Codex rates are OpenAI's Standard-tier card as published on
// the pricing page on 2026-10-31: short-context rates up to 272K input
// tokens, long-context rates for a prompt over 272K.
func TestDefault_CodexRates(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ short, long Rate }{
		"gpt-6-astra": {
			Rate{Input: 10, Output: 50, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 25},
			Rate{Input: 20, Output: 75, CacheRead: 2, CacheWrite5m: 25, CacheWrite1h: 50},
		},
		"gpt-6-sol": {
			Rate{Input: 2, Output: 10, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 5},
			Rate{Input: 4, Output: 15, CacheRead: 0.4, CacheWrite5m: 5, CacheWrite1h: 10},
		},
		"gpt-6.1-sol": {
			Rate{Input: 2, Output: 10, CacheRead: 0.1, CacheWrite5m: 2.5, CacheWrite1h: 5},
			Rate{Input: 4, Output: 15, CacheRead: 0.2, CacheWrite5m: 5, CacheWrite1h: 10},
		},
		"gpt-5.5": {
			Rate{Input: 5, Output: 30, CacheRead: 0.5},
			Rate{Input: 10, Output: 45, CacheRead: 1},
		},
	}
	for model, want := range cases {
		for _, prompt := range []int{0, 272_000} {
			if got, ok := tab.EffectiveRate("codex", model, time.Time{}, prompt, ""); !ok || got != want.short {
				t.Errorf("codex/%s at %d prompt tokens = %+v (known %v), want short context %+v", model, prompt, got, ok, want.short)
			}
		}
		for _, prompt := range []int{272_001, 475_000} {
			if got, ok := tab.EffectiveRate("codex", model, time.Time{}, prompt, ""); !ok || got != want.long {
				t.Errorf("codex/%s at %d prompt tokens = %+v (known %v), want long context %+v", model, prompt, got, ok, want.long)
			}
		}
	}
	// A 300,000-token uncached gpt-6.1-sol request with 1,000 output tokens
	// bills at long-context rates: 0.3 × $4 + 0.001 × $15.
	cost, known := tab.CostAt("codex", "gpt-6.1-sol", time.Time{}, 300_000, 1_000, 0, 0, 0, "")
	if !known || math.Abs(cost-1.215) > 1e-9 {
		t.Errorf("codex/gpt-6.1-sol 300K uncached cost = %v, %v; want 1.215, true", cost, known)
	}
	// 200K input + 50K cache read + 10K output on gpt-6-astra stays short:
	// 0.2 × $10 + 0.05 × $1 + 0.01 × $50.
	cost, known = tab.CostAt("codex", "gpt-6-astra", time.Time{}, 200_000, 10_000, 0, 0, 50_000, "")
	if !known || math.Abs(cost-2.55) > 1e-9 {
		t.Errorf("codex/gpt-6-astra short cost = %v, %v; want 2.55, true", cost, known)
	}
	// 1M input + 1M cache read + 1M output on gpt-6-astra is long context:
	// $20 + $2 + $75.
	cost, known = tab.CostAt("codex", "gpt-6-astra", time.Time{}, 1_000_000, 1_000_000, 0, 0, 1_000_000, "")
	if !known || math.Abs(cost-97) > 1e-9 {
		t.Errorf("codex/gpt-6-astra long cost = %v, %v; want 97, true", cost, known)
	}
	if cost, known := tab.CostAt("codex", "codex-auto-review", time.Time{}, 1000, 1000, 1000, 1000, 1000, ""); cost != 0 || !known {
		t.Errorf("codex/codex-auto-review cost = %v, %v; want 0, true (auto-review is free)", cost, known)
	}
}
