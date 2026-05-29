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

func TestLoad_EmptyUserFile_ReturnsBundledDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	if err := os.WriteFile(path, []byte("models: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := Load(path)
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
			t.Errorf("bundled default missing after empty overlay: %q", m)
		}
	}
}

func TestLoad_UserFileOverridesOneModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	userYAML := `models:
  claude-opus-4-7:
    input_per_mtok: 99
    output_per_mtok: 199
    cache_read_per_mtok: 0
    cache_write_5m_per_mtok: 0
    cache_write_1h_per_mtok: 0
`
	if err := os.WriteFile(path, []byte(userYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	opus, ok := tab.Models["claude-opus-4-7"]
	if !ok {
		t.Fatalf("claude-opus-4-7 missing")
	}
	if opus.Input != 99 || opus.Output != 199 {
		t.Errorf("opus override not applied: %+v", opus)
	}
	if opus.CacheRead != 0 || opus.CacheWrite5m != 0 || opus.CacheWrite1h != 0 {
		t.Errorf("opus override should fully replace, got cache fields: %+v", opus)
	}
	sonnet, ok := tab.Models["claude-sonnet-4-6"]
	if !ok {
		t.Fatalf("claude-sonnet-4-6 should still be present from bundled defaults")
	}
	// Bundled sonnet-4-6 rates: input 3, output 15.
	if sonnet.Input != 3.00 || sonnet.Output != 15.00 {
		t.Errorf("bundled sonnet rates clobbered: %+v", sonnet)
	}
}

func TestLoad_UserFileAddsNewModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	userYAML := `models:
  my-private-model:
    input_per_mtok: 7
    output_per_mtok: 11
    cache_read_per_mtok: 0.7
    cache_write_5m_per_mtok: 8.75
    cache_write_1h_per_mtok: 14
`
	if err := os.WriteFile(path, []byte(userYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	priv, ok := tab.Models["my-private-model"]
	if !ok {
		t.Fatalf("my-private-model not added")
	}
	if priv.Input != 7 || priv.Output != 11 {
		t.Errorf("my-private-model rates wrong: %+v", priv)
	}
	// Bundled models stay priced per the bundle.
	opus, ok := tab.Models["claude-opus-4-7"]
	if !ok {
		t.Fatalf("bundled claude-opus-4-7 missing")
	}
	if opus.Input != 5.00 || opus.Output != 25.00 {
		t.Errorf("bundled opus rates clobbered: %+v", opus)
	}
	haiku, ok := tab.Models["claude-haiku-4-5-20251001"]
	if !ok {
		t.Fatalf("bundled claude-haiku-4-5-20251001 missing")
	}
	if haiku.Input != 1.00 || haiku.Output != 5.00 {
		t.Errorf("bundled haiku rates clobbered: %+v", haiku)
	}
}

func TestLoad_MalformedUserFile_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	if err := os.WriteFile(path, []byte("not: : yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Errorf("expected error for malformed YAML, got nil")
	}
}

func TestLoad_MissingFile_ReturnsBundledDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"claude-opus-4-7", "claude-haiku-4-5-20251001"} {
		if _, ok := tab.Models[m]; !ok {
			t.Errorf("bundled default missing %q", m)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected path to not exist, stat err = %v", err)
	}
}
