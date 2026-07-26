package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

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

func TestDefault_Fable5AndMythos5(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// Fable 5 and Mythos 5 launched at $10 input / $50 output, with cache
	// rates following the standard ratios off the $10 input rate.
	for _, m := range []string{
		"claude-fable-5",
		"claude-fable-5[1m]",
		"claude-mythos-5",
		"claude-mythos-5[1m]",
	} {
		p, ok := tab.Models[m]
		if !ok {
			t.Errorf("default missing %q", m)
			continue
		}
		if p.Input != 10.00 || p.Output != 50.00 {
			t.Errorf("%s base rates wrong: %+v", m, p)
		}
		if p.CacheRead != 1.00 || p.CacheWrite5m != 12.50 || p.CacheWrite1h != 20.00 {
			t.Errorf("%s cache rates wrong: %+v", m, p)
		}
	}
}

func TestDefault_Opus5(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// Opus 5 is a drop-in at the Opus 4.8 rate card: $5 input / $25 output,
	// cache rates on the standard ratios off the $5 input rate.
	for _, m := range []string{
		"claude-opus-5",
		"claude-opus-5[1m]",
	} {
		p, ok := tab.Models[m]
		if !ok {
			t.Errorf("default missing %q", m)
			continue
		}
		if p.Input != 5.00 || p.Output != 25.00 {
			t.Errorf("%s base rates wrong: %+v", m, p)
		}
		if p.CacheRead != 0.50 || p.CacheWrite5m != 6.25 || p.CacheWrite1h != 10.00 {
			t.Errorf("%s cache rates wrong: %+v", m, p)
		}
	}
}

// Every model the bundled table claims gets 1M context at standard rates
// should carry a "[1m]"-suffixed entry priced identically to its base id.
func TestDefault_OneMillionContextVariantsMatchBase(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{
		"claude-fable-5",
		"claude-mythos-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
	} {
		bp, ok := tab.Models[base]
		if !ok {
			t.Errorf("default missing base model %q", base)
			continue
		}
		vp, ok := tab.Models[base+"[1m]"]
		if !ok {
			t.Errorf("default missing 1M variant %q", base+"[1m]")
			continue
		}
		if bp.Rate != vp.Rate {
			t.Errorf("%s[1m] rates differ from base: %+v vs %+v", base, vp.Rate, bp.Rate)
		}
		// Rate history has to match too, or the variant drifts from its base
		// on historical turns even while today's rates agree.
		if len(bp.Rates) != len(vp.Rates) {
			t.Errorf("%s[1m] has %d rate periods, base has %d", base, len(vp.Rates), len(bp.Rates))
			continue
		}
		for i := range bp.Rates {
			if !bp.Rates[i].Until.Equal(vp.Rates[i].Until) || bp.Rates[i].Rate != vp.Rates[i].Rate {
				t.Errorf("%s[1m] rate period %d differs from base: %+v vs %+v", base, i, vp.Rates[i], bp.Rates[i])
			}
		}
	}
}

func TestDefault_Sonnet5(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// Sonnet 5's current rate is the standard $3 input / $15 output, with
	// cache rates on the standard ratios off the $3 input rate. The
	// introductory $2 / $10 window that ran through 2026-08-31 lives in the
	// rate history — see TestDefault_Sonnet5HasIntroPeriod.
	for _, m := range []string{
		"claude-sonnet-5",
		"claude-sonnet-5[1m]",
	} {
		p, ok := tab.Models[m]
		if !ok {
			t.Errorf("default missing %q", m)
			continue
		}
		if p.Input != 3.00 || p.Output != 15.00 {
			t.Errorf("%s base rates wrong: %+v", m, p.Rate)
		}
		if p.CacheRead != 0.30 || p.CacheWrite5m != 3.75 || p.CacheWrite1h != 6.00 {
			t.Errorf("%s cache rates wrong: %+v", m, p.Rate)
		}
	}
}

// ── Date-effective rates ──────────────────────────────────────────────

func TestRateAt_NoHistory_FlatRateAtAnyTime(t *testing.T) {
	p := ModelPrice{Rate: Rate{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite5m: 6.25, CacheWrite1h: 10}}
	for _, ts := range []time.Time{
		{},
		mustTime(t, "2020-01-01T00:00:00Z"),
		mustTime(t, "2099-12-31T23:59:59Z"),
	} {
		if got := p.RateAt(ts); got != p.Rate {
			t.Errorf("RateAt(%v) = %+v, want flat %+v", ts, got, p.Rate)
		}
	}
}

// The zero timestamp is the documented fallback: some older transcripts have
// no parseable timestamp, and those turns price at the current rate.
func TestRateAt_ZeroTimestamp_UsesCurrentRate(t *testing.T) {
	p := ModelPrice{
		Rate:  Rate{Input: 3, Output: 15},
		Rates: []RatePeriod{{Until: mustTime(t, "2026-08-31T00:00:00Z"), Rate: Rate{Input: 2, Output: 10}}},
	}
	got := p.RateAt(time.Time{})
	if got.Input != 3 || got.Output != 15 {
		t.Errorf("zero timestamp should use current rate, got %+v", got)
	}
}

// "until" is inclusive through the end of that UTC day.
func TestRateAt_UntilIsInclusiveThroughEndOfDay(t *testing.T) {
	p := ModelPrice{
		Rate:  Rate{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite5m: 3.75, CacheWrite1h: 6.00},
		Rates: []RatePeriod{{Until: mustTime(t, "2026-08-31T00:00:00Z"), Rate: Rate{Input: 2, Output: 10, CacheRead: 0.20, CacheWrite5m: 2.50, CacheWrite1h: 4.00}}},
	}
	cases := []struct {
		ts        string
		wantInput float64
	}{
		{"2026-07-26T12:00:00Z", 2}, // well inside the intro period
		{"2026-08-31T00:00:00Z", 2}, // first instant of the final day
		{"2026-08-31T23:59:59Z", 2}, // last instant of the final day
		{"2026-09-01T00:00:00Z", 3}, // first instant after the cliff
		{"2027-01-01T00:00:00Z", 3},
	}
	for _, c := range cases {
		got := p.RateAt(mustTime(t, c.ts))
		if got.Input != c.wantInput {
			t.Errorf("RateAt(%s).Input = %v, want %v", c.ts, got.Input, c.wantInput)
		}
	}
}

// Rate periods may be listed in any order; the lookup sorts nothing and must
// still pick the narrowest period covering the timestamp.
func TestRateAt_OrderIndependent(t *testing.T) {
	oldest := RatePeriod{Until: mustTime(t, "2025-01-31T00:00:00Z"), Rate: Rate{Input: 1}}
	middle := RatePeriod{Until: mustTime(t, "2026-08-31T00:00:00Z"), Rate: Rate{Input: 2}}
	current := Rate{Input: 3}

	oldestFirst := ModelPrice{Rate: current, Rates: []RatePeriod{oldest, middle}}
	newestFirst := ModelPrice{Rate: current, Rates: []RatePeriod{middle, oldest}}

	cases := []struct {
		ts   string
		want float64
	}{
		{"2024-06-01T00:00:00Z", 1},
		{"2025-01-31T23:59:59Z", 1},
		{"2025-02-01T00:00:00Z", 2},
		{"2026-08-31T23:59:59Z", 2},
		{"2026-09-01T00:00:00Z", 3},
	}
	for _, c := range cases {
		a := oldestFirst.RateAt(mustTime(t, c.ts))
		b := newestFirst.RateAt(mustTime(t, c.ts))
		if a.Input != c.want || b.Input != c.want {
			t.Errorf("at %s: oldest-first=%v newest-first=%v, want %v", c.ts, a.Input, b.Input, c.want)
		}
	}
}

func TestCostAt_Sonnet5AcrossTheCliff(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	// 1M input + 1M output under intro pricing ($2 / $10) = $12.
	cost, known := tab.CostAt("claude-sonnet-5", mustTime(t, "2026-07-26T00:00:00Z"), 1_000_000, 1_000_000, 0, 0, 0)
	if !known || math.Abs(cost-12.0) > 0.001 {
		t.Errorf("intro-period sonnet-5: cost=%v known=%v, want 12", cost, known)
	}
	// Same turn on 2026-09-01 under standard pricing ($3 / $15) = $18.
	cost, known = tab.CostAt("claude-sonnet-5", mustTime(t, "2026-09-01T00:00:00Z"), 1_000_000, 1_000_000, 0, 0, 0)
	if !known || math.Abs(cost-18.0) > 0.001 {
		t.Errorf("post-cliff sonnet-5: cost=%v known=%v, want 18", cost, known)
	}
	// Cache rates move with the base rate: 10M cache reads = $2 then $3.
	cost, _ = tab.CostAt("claude-sonnet-5", mustTime(t, "2026-07-26T00:00:00Z"), 0, 0, 0, 0, 10_000_000)
	if math.Abs(cost-2.0) > 0.001 {
		t.Errorf("intro-period sonnet-5 cache read: %v, want 2", cost)
	}
	cost, _ = tab.CostAt("claude-sonnet-5", mustTime(t, "2026-09-01T00:00:00Z"), 0, 0, 0, 0, 10_000_000)
	if math.Abs(cost-3.0) > 0.001 {
		t.Errorf("post-cliff sonnet-5 cache read: %v, want 3", cost)
	}
}

func TestCostAt_UnknownModel(t *testing.T) {
	tab, _ := LoadDefault()
	cost, known := tab.CostAt("not-a-real-model", mustTime(t, "2026-07-26T00:00:00Z"), 1_000_000, 0, 0, 0, 0)
	if known || cost != 0 {
		t.Errorf("expected unknown/0, got cost=%v known=%v", cost, known)
	}
}

// Cost is the current-rate wrapper over CostAt.
func TestCost_MatchesCostAtCurrentRate(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := tab.CostAt("claude-sonnet-5", time.Time{}, 1_000_000, 1_000_000, 0, 0, 0)
	got, known := tab.Cost("claude-sonnet-5", 1_000_000, 1_000_000, 0, 0, 0)
	if !known || math.Abs(got-want) > 0.000001 {
		t.Errorf("Cost=%v, CostAt(zero)=%v", got, want)
	}
	// And the current rate is the post-cliff standard rate, not the intro one.
	if math.Abs(got-18.0) > 0.001 {
		t.Errorf("current sonnet-5 rate should be $3/$15 => 18, got %v", got)
	}
}

// Sonnet 5's intro window is encoded as history, so a July turn still prices
// at $2 / $10 after the flat fields were bumped to the standard rate.
func TestDefault_Sonnet5HasIntroPeriod(t *testing.T) {
	tab, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"claude-sonnet-5", "claude-sonnet-5[1m]"} {
		p, ok := tab.Models[m]
		if !ok {
			t.Errorf("default missing %q", m)
			continue
		}
		if len(p.Rates) != 1 {
			t.Errorf("%s: want 1 historical rate period, got %d", m, len(p.Rates))
			continue
		}
		intro := p.Rates[0]
		if !intro.Until.Equal(mustTime(t, "2026-08-31T00:00:00Z")) {
			t.Errorf("%s: intro period ends %v, want 2026-08-31", m, intro.Until)
		}
		if intro.Input != 2.00 || intro.Output != 10.00 {
			t.Errorf("%s intro base rates wrong: %+v", m, intro.Rate)
		}
		if intro.CacheRead != 0.20 || intro.CacheWrite5m != 2.50 || intro.CacheWrite1h != 4.00 {
			t.Errorf("%s intro cache rates wrong: %+v", m, intro.Rate)
		}
	}
}

// A user overlay written in the old flat form must keep working unchanged.
func TestLoad_FlatUserFileOverridesModelWithHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	userYAML := `models:
  claude-sonnet-5:
    input_per_mtok: 1
    output_per_mtok: 2
    cache_read_per_mtok: 0.1
    cache_write_5m_per_mtok: 1.25
    cache_write_1h_per_mtok: 2
`
	if err := os.WriteFile(path, []byte(userYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := tab.Models["claude-sonnet-5"]
	if !ok {
		t.Fatal("claude-sonnet-5 missing")
	}
	if p.Input != 1 || p.Output != 2 {
		t.Errorf("flat override not applied: %+v", p.Rate)
	}
	// Per-model replacement is total: the bundled intro-rate history goes too,
	// so the user's flat rate applies at every timestamp.
	if len(p.Rates) != 0 {
		t.Errorf("bundled rate history should be replaced, got %+v", p.Rates)
	}
	got := p.RateAt(mustTime(t, "2026-07-26T00:00:00Z"))
	if got.Input != 1 {
		t.Errorf("historical lookup after flat override = %v, want 1", got.Input)
	}
}

func TestLoad_UserFileWithRateHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prices.yaml")
	userYAML := `models:
  my-private-model:
    input_per_mtok: 20
    output_per_mtok: 100
    rates:
      - until: 2026-03-31
        input_per_mtok: 10
        output_per_mtok: 50
`
	if err := os.WriteFile(path, []byte(userYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cost, known := tab.CostAt("my-private-model", mustTime(t, "2026-03-15T00:00:00Z"), 1_000_000, 0, 0, 0, 0)
	if !known || math.Abs(cost-10.0) > 0.001 {
		t.Errorf("in-period: cost=%v known=%v, want 10", cost, known)
	}
	cost, _ = tab.CostAt("my-private-model", mustTime(t, "2026-04-01T00:00:00Z"), 1_000_000, 0, 0, 0, 0)
	if math.Abs(cost-20.0) > 0.001 {
		t.Errorf("post-period: cost=%v, want 20", cost)
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
