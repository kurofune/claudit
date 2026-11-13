// Package pricing loads token prices per (provider, model). It starts from a
// bundled default table embedded in the binary and optionally overlays a user
// file at ~/.config/claudit/prices.yaml — per-(provider, model) replacement,
// with bundled entries the user didn't touch left intact. Unknown pairs cost
// $0 and are surfaced via a warning.
package pricing

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kurofune/claudit/v2/internal/parse"
	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultYAML []byte

// Rate is a set of per-million-token rates in USD.
type Rate struct {
	Input        float64 `yaml:"input_per_mtok"`
	Output       float64 `yaml:"output_per_mtok"`
	CacheRead    float64 `yaml:"cache_read_per_mtok"`
	CacheWrite5m float64 `yaml:"cache_write_5m_per_mtok"`
	CacheWrite1h float64 `yaml:"cache_write_1h_per_mtok"`
}

// RatePeriod is a Rate that applied through Until, inclusive: it covers every
// timestamp up to and including 23:59:59.999… UTC on that date. Written in
// YAML as a plain date alongside the rate fields:
//
//	rates:
//	  - until: 2026-08-31
//	    input_per_mtok: 2.00
//	    output_per_mtok: 10.00
type RatePeriod struct {
	Until time.Time `yaml:"until"`
	Rate  `yaml:",inline"`
}

// LongPromptRate is the Rate a request pays on every token, output included,
// when its prompt (input plus cache reads and writes) exceeds ThresholdTokens.
type LongPromptRate struct {
	ThresholdTokens int `yaml:"threshold_tokens"`
	Rate            `yaml:",inline"`
}

// ModelPrice is a model's current rate plus, optionally, the rates that
// applied before it. The rate fields sit at the top level so the original
// flat form keeps parsing unchanged. Rates holds standard-rate history;
// Fast is an optional current fast-mode rate, with no rate history;
// LongPrompt is an optional current prompt-length tier for standard turns.
//
// Periods may be listed in any order — the lookup picks the narrowest one
// covering the timestamp, so neither oldest-first nor newest-first is
// privileged.
type ModelPrice struct {
	Rate       `yaml:",inline"`
	Rates      []RatePeriod    `yaml:"rates"`
	Fast       *Rate           `yaml:"fast"`
	LongPrompt *LongPromptRate `yaml:"long_prompt"`
}

// RateAt returns the rate in effect for ts. It picks the period with the
// earliest Until that still covers ts; if no period does — ts is after every
// listed period, or none are listed — the current (top-level) rate applies.
//
// A zero ts falls back to the current rate. Some older transcripts carry no
// parseable timestamp, and pricing those at today's rate is both the closest
// approximation available and the same answer the table gave before rate
// history existed.
func (p ModelPrice) RateAt(ts time.Time) Rate {
	if ts.IsZero() {
		return p.Rate
	}
	best := p.Rate
	var bestUntil time.Time
	for _, rp := range p.Rates {
		// Until is an inclusive date: the period ends at the start of the
		// following UTC day.
		end := rp.Until.UTC().AddDate(0, 0, 1)
		if !ts.UTC().Before(end) {
			continue // ts is past this period
		}
		if bestUntil.IsZero() || rp.Until.Before(bestUntil) {
			best, bestUntil = rp.Rate, rp.Until
		}
	}
	return best
}

// ClaudeCode is the provider a bare top-level models: map prices.
const ClaudeCode = parse.ProviderClaudeCode

// ProviderPrices is one provider's model price list.
type ProviderPrices struct {
	Models map[string]ModelPrice `yaml:"models"`
}

// Table holds every known price, keyed by provider and then model.
//
// Models is the bare top-level models: map, the form every prices file took
// before providers: existed; it prices provider ClaudeCode. Load and
// LoadDefault fold it into Providers[ClaudeCode] and leave Models as that same
// map, so a table built either way answers the same lookups.
type Table struct {
	Models    map[string]ModelPrice     `yaml:"models"`
	Providers map[string]ProviderPrices `yaml:"providers"`
}

// PriceProvider is the provider whose price list prices a turn of provider.
// A turn that names no provider predates the provider seam, when every log
// was Claude Code's.
func PriceProvider(provider string) string {
	if provider == "" {
		return ClaudeCode
	}
	return provider
}

func (t *Table) lookup(provider, model string) (ModelPrice, bool) {
	provider = PriceProvider(provider)
	if p, ok := t.Providers[provider].Models[model]; ok {
		return p, true
	}
	if provider == ClaudeCode {
		p, ok := t.Models[model]
		return p, ok
	}
	return ModelPrice{}, false
}

// EffectiveRate returns the rate that prices a turn of provider's model at
// ts whose prompt (input plus cache reads and writes) is prompt tokens, at
// speed. Only speed "fast" selects the optional Fast rate; every other speed,
// or a missing Fast rate, uses standard date-effective pricing, unless the
// prompt exceeds the model's LongPrompt threshold — then the tier's rate
// prices the whole turn. Fast wins over LongPrompt. A zero ts selects the
// model's current rate — see ModelPrice.RateAt. An unknown (provider, model)
// pair returns the zero Rate and false.
func (t *Table) EffectiveRate(provider, model string, ts time.Time, prompt int, speed string) (Rate, bool) {
	p, ok := t.lookup(provider, model)
	if !ok {
		return Rate{}, false
	}
	switch {
	case speed == "fast" && p.Fast != nil:
		return *p.Fast, true
	case p.LongPrompt != nil && prompt > p.LongPrompt.ThresholdTokens:
		return p.LongPrompt.Rate, true
	}
	return p.RateAt(ts), true
}

// CostAt returns total USD for the given token counts, priced at the
// EffectiveRate for the turn. An unknown (provider, model) pair returns
// cost=0 and known=false so the caller can warn.
func (t *Table) CostAt(provider, model string, ts time.Time, in, out, cacheCreate5m, cacheCreate1h, cacheRead int, speed string) (cost float64, known bool) {
	r, ok := t.EffectiveRate(provider, model, ts, in+cacheCreate5m+cacheCreate1h+cacheRead, speed)
	if !ok {
		return 0, false
	}
	const m = 1_000_000.0
	cost = float64(in)*r.Input/m +
		float64(out)*r.Output/m +
		float64(cacheCreate5m)*r.CacheWrite5m/m +
		float64(cacheCreate1h)*r.CacheWrite1h/m +
		float64(cacheRead)*r.CacheRead/m
	return cost, true
}

// Cost prices token counts at a Claude Code model's current standard rate.
// It is CostAt with provider ClaudeCode and a zero timestamp; prefer CostAt
// when a turn is in hand, so that historical turns price at the rate that was
// actually in effect.
func (t *Table) Cost(model string, in, out, cacheCreate5m, cacheCreate1h, cacheRead int) (cost float64, known bool) {
	return t.CostAt(ClaudeCode, model, time.Time{}, in, out, cacheCreate5m, cacheCreate1h, cacheRead, "")
}

// DefaultPath is ~/.config/claudit/prices.yaml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claudit", "prices.yaml"), nil
}

// Load returns a pricing Table that starts from the bundled defaults and is
// optionally overlaid by a user file at path. Overlay is per-(provider,
// model) replacement: a model defined in the user file fully replaces the
// bundled entry for that provider and name; bundled entries the user didn't
// mention are kept; user entries for new names or providers are added. A
// bare top-level models: map in the user file overlays provider ClaudeCode.
// If path does not exist, the bundled defaults are returned and no file is
// created. Malformed or unreadable user files return an error.
func Load(path string) (*Table, error) {
	bundled, err := LoadDefault()
	if err != nil {
		return nil, fmt.Errorf("parse bundled prices: %w", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return bundled, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prices: %w", err)
	}
	user, err := parseTable(b)
	if err != nil {
		return nil, fmt.Errorf("parse prices: %w", err)
	}
	bundled.overlay(user)
	return bundled, nil
}

// LoadDefault returns the embedded default table without touching disk —
// useful for tests.
func LoadDefault() (*Table, error) {
	return parseTable(defaultYAML)
}

// parseTable decodes a prices file and folds its bare models: map into
// Providers[ClaudeCode]. Where both name the same Claude Code model, the
// providers: entry wins.
func parseTable(b []byte) (*Table, error) {
	var t Table
	if err := yaml.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	if t.Providers == nil {
		t.Providers = map[string]ProviderPrices{}
	}
	cc := t.models(ClaudeCode)
	for name, price := range t.Models {
		if _, ok := cc[name]; !ok {
			cc[name] = price
		}
	}
	t.Models = cc
	return &t, nil
}

// models returns provider's model map, creating it when absent.
func (t *Table) models(provider string) map[string]ModelPrice {
	pp := t.Providers[provider]
	if pp.Models == nil {
		pp.Models = map[string]ModelPrice{}
		t.Providers[provider] = pp
	}
	return pp.Models
}

// overlay replaces or adds every (provider, model) user prices.
func (t *Table) overlay(user *Table) {
	for provider, pp := range user.Providers {
		dst := t.models(provider)
		for name, price := range pp.Models {
			dst[name] = price
		}
	}
}
