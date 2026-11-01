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
	if _, ok := nodes["codex-auto-review"]; ok {
		t.Error("codex-auto-review has an entry, but OpenAI's pricing page lists no rate for it")
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
		if p.Rate == (Rate{}) {
			t.Errorf("codex/%s has all-zero rates", name)
		}
	}
}

// The bundled Codex rates are OpenAI's Standard-tier card as published on
// the pricing page on 2026-10-31.
func TestDefault_CodexRates(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]Rate{
		"gpt-6-astra": {Input: 10, Output: 50, CacheRead: 1, CacheWrite5m: 12.5, CacheWrite1h: 25},
		"gpt-6-sol":   {Input: 2, Output: 10, CacheRead: 0.2, CacheWrite5m: 2.5, CacheWrite1h: 5},
		"gpt-6.1-sol": {Input: 2, Output: 10, CacheRead: 0.1, CacheWrite5m: 2.5, CacheWrite1h: 5},
		"gpt-5.5":     {Input: 5, Output: 30, CacheRead: 0.5},
	}
	for model, want := range cases {
		got, ok := tab.EffectiveRate("codex", model, time.Time{}, 0, "")
		if !ok || got != want {
			t.Errorf("codex/%s = %+v (known %v), want %+v", model, got, ok, want)
		}
	}
	// 1M input + 1M cache read + 1M output on gpt-6-astra.
	cost, known := tab.CostAt("codex", "gpt-6-astra", time.Time{}, 1_000_000, 1_000_000, 0, 0, 1_000_000, "")
	if !known || math.Abs(cost-61) > 1e-9 {
		t.Errorf("codex/gpt-6-astra cost = %v, %v; want 61, true", cost, known)
	}
	if _, known := tab.CostAt("codex", "codex-auto-review", time.Time{}, 1, 1, 0, 0, 0, ""); known {
		t.Error("codex/codex-auto-review is priced; it must stay unknown")
	}
}
