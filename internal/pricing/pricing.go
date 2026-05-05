// Package pricing loads per-model token prices from a YAML file at
// ~/.config/claudit/prices.yaml, writing a sensible default the first
// time it's run. Unknown models cost $0 and are surfaced via a warning.
package pricing

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed default.yaml
var defaultYAML []byte

// ModelPrice is per-million-token rates in USD.
type ModelPrice struct {
	Input        float64 `yaml:"input_per_mtok"`
	Output       float64 `yaml:"output_per_mtok"`
	CacheRead    float64 `yaml:"cache_read_per_mtok"`
	CacheWrite5m float64 `yaml:"cache_write_5m_per_mtok"`
	CacheWrite1h float64 `yaml:"cache_write_1h_per_mtok"`
}

// Table holds all known model prices.
type Table struct {
	Models map[string]ModelPrice `yaml:"models"`
}

// Cost returns total USD for the given token counts. Unknown models
// return cost=0 and known=false so the caller can warn.
func (t *Table) Cost(model string, in, out, cacheCreate5m, cacheCreate1h, cacheRead int) (cost float64, known bool) {
	p, ok := t.Models[model]
	if !ok {
		return 0, false
	}
	const m = 1_000_000.0
	cost = float64(in)*p.Input/m +
		float64(out)*p.Output/m +
		float64(cacheCreate5m)*p.CacheWrite5m/m +
		float64(cacheCreate1h)*p.CacheWrite1h/m +
		float64(cacheRead)*p.CacheRead/m
	return cost, true
}

// DefaultPath is ~/.config/claudit/prices.yaml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claudit", "prices.yaml"), nil
}

// Load reads the table from path. If the file doesn't exist, it writes the
// embedded default into place, then loads it. Returns the table and the
// path it was actually read from (useful in startup logging).
func Load(path string) (*Table, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create prices dir: %w", err)
		}
		if err := os.WriteFile(path, defaultYAML, 0o644); err != nil {
			return nil, fmt.Errorf("write default prices: %w", err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read prices: %w", err)
	}
	var t Table
	if err := yaml.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse prices: %w", err)
	}
	if t.Models == nil {
		t.Models = map[string]ModelPrice{}
	}
	return &t, nil
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
