// Package pricing loads per-model token prices. It starts from a bundled
// default table embedded in the binary and optionally overlays a user file
// at ~/.config/claudit/prices.yaml — per-model replacement, with bundled
// entries the user didn't touch left intact. Unknown models cost $0 and
// are surfaced via a warning.
package pricing

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

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

// ModelPrice is a model's current rate plus, optionally, the rates that
// applied before it. The rate fields sit at the top level so the original
// flat form keeps parsing unchanged; `rates` is purely additive.
//
// Periods may be listed in any order — the lookup picks the narrowest one
// covering the timestamp, so neither oldest-first nor newest-first is
// privileged.
type ModelPrice struct {
	Rate  `yaml:",inline"`
	Rates []RatePeriod `yaml:"rates"`
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

// Table holds all known model prices.
type Table struct {
	Models map[string]ModelPrice `yaml:"models"`
}

// CostAt returns total USD for the given token counts, priced at the rate in
// effect for ts. Unknown models return cost=0 and known=false so the caller
// can warn. A zero ts prices at the model's current rate — see RateAt.
func (t *Table) CostAt(model string, ts time.Time, in, out, cacheCreate5m, cacheCreate1h, cacheRead int) (cost float64, known bool) {
	p, ok := t.Models[model]
	if !ok {
		return 0, false
	}
	r := p.RateAt(ts)
	const m = 1_000_000.0
	cost = float64(in)*r.Input/m +
		float64(out)*r.Output/m +
		float64(cacheCreate5m)*r.CacheWrite5m/m +
		float64(cacheCreate1h)*r.CacheWrite1h/m +
		float64(cacheRead)*r.CacheRead/m
	return cost, true
}

// Cost prices token counts at each model's current rate. It is CostAt with a
// zero timestamp; prefer CostAt when a turn timestamp is in hand, so that
// historical turns price at the rate that was actually in effect.
func (t *Table) Cost(model string, in, out, cacheCreate5m, cacheCreate1h, cacheRead int) (cost float64, known bool) {
	return t.CostAt(model, time.Time{}, in, out, cacheCreate5m, cacheCreate1h, cacheRead)
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
// optionally overlaid by a user file at path. Overlay is per-model
// replacement: a model defined in the user file fully replaces the bundled
// entry for that name; bundled entries the user didn't mention are kept;
// user entries for new model names are added. If path does not exist, the
// bundled defaults are returned and no file is created. Malformed or
// unreadable user files return an error.
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
	var user Table
	if err := yaml.Unmarshal(b, &user); err != nil {
		return nil, fmt.Errorf("parse prices: %w", err)
	}
	for name, price := range user.Models {
		bundled.Models[name] = price
	}
	return bundled, nil
}

// LoadDefault returns the embedded default table without touching disk —
// useful for tests.
func LoadDefault() (*Table, error) {
	var t Table
	if err := yaml.Unmarshal(defaultYAML, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
