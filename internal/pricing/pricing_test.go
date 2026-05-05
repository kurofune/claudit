package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDefault_KnownModels(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{
		"claude-opus-4-7",
		"claude-opus-4-7[1m]",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
	} {
		if _, ok := tab.Models[m]; !ok {
			t.Errorf("default missing %q", m)
		}
	}
}

func TestCost(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// 1M input on opus-4-7 at $5 = $5.00
	cost, known := tab.Cost("claude-opus-4-7", 1_000_000, 0, 0, 0, 0)
	if !known || math.Abs(cost-5.00) > 0.001 {
		t.Errorf("opus input: cost=%v known=%v", cost, known)
	}
	// 1M output on opus = $25
	cost, _ = tab.Cost("claude-opus-4-7", 0, 1_000_000, 0, 0, 0)
	if math.Abs(cost-25.0) > 0.001 {
		t.Errorf("opus output: %v", cost)
	}
	// Cache read pricing matters most — 10M cache reads on opus = $5
	cost, _ = tab.Cost("claude-opus-4-7", 0, 0, 0, 0, 10_000_000)
	if math.Abs(cost-5.0) > 0.001 {
		t.Errorf("opus cache read: %v", cost)
	}
	// Mixed
	cost, _ = tab.Cost("claude-haiku-4-5-20251001", 1_000_000, 1_000_000, 0, 0, 0)
	if math.Abs(cost-(1.0+5.0)) > 0.001 {
		t.Errorf("haiku mix: %v", cost)
	}
}

func TestCost_Unknown(t *testing.T) {
	tab, _ := LoadDefault()
	cost, known := tab.Cost("not-a-real-model", 1_000_000, 0, 0, 0, 0)
	if known {
		t.Errorf("expected unknown")
	}
	if cost != 0 {
		t.Errorf("expected 0, got %v", cost)
	}
}

func TestLoad_WritesDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tab.Models["claude-opus-4-7"]; !ok {
		t.Errorf("expected default written")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should exist: %v", err)
	}
}
