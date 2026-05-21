package render

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// TestStaticPayload_EqualsUnionOfAPISections is the Phase 4 contract
// test. The JSON the static report inlines via <script
// id="claudit-data"> must equal the union of the per-section
// /_claudit/api/* responses for the empty filter, modulo a small
// documented set of fields that are intentionally one-sided. The
// failure mode this test exists to catch is the Risk #2 the plan
// calls out: a column added to (say) the Cost tab in the API path
// but not the static path — or vice versa — that drifts unnoticed
// until a downstream consumer breaks.
//
// The fixture intentionally exercises every collection BuildPayload
// touches — multiple models, multiple projects, multiple sessions, a
// sidechain turn, tool uses, and period=day with several buckets so
// the trend maps populate. A thinner fixture (e.g. one turn) would
// leave most fields as null/empty and let a per-row drift slip past.
//
// The reassembly here flattens BuildTrends(model|project|...) into
// the legacy `trend_by_*` keys + a single shared `period`, matching
// the legacy BuildPayload shape so the equality is field-by-field
// rather than structural.
func TestStaticPayload_EqualsUnionOfAPISections(t *testing.T) {
	a := contractFixture(t)
	ctx := context.Background()

	inline, err := BuildPayload(ctx, a, HTMLOptions{})
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}
	legacy := decodeObject(t, inline)

	expected := unionOfAPISections(t, a)

	// Fields the static surface SSRs directly and therefore omits
	// from the inline JSON island. The /api/* surface still emits
	// them so the SPA can render client-side.
	apiOnly := map[string]string{
		"hotspots": "renderHotspotsHTML SSRs the hotspot cards in html.go",
		"sessions": "renderSessionsHTML SSRs the session-card markup; static report uses opts.SessionTimelines",
	}
	for k := range apiOnly {
		delete(expected, k)
	}

	// Fields the static surface needs but the API doesn't expose
	// (each section endpoint returns only its own slice of the data).
	staticOnly := map[string]string{
		"main":        "main-chain totals — fed to SSR'd headline tiles",
		"sidechain":   "sidechain totals — fed to SSR'd headline tiles",
		"prompt_keys": "derived from opts.SessionTimelines for cross-link availability",
	}

	// The forecast.as_of timestamp is wall-clock — both sides call
	// time.Now() and can differ by a tick across the two builds. Zero
	// it out so the rest of the forecast struct still compares.
	legacy = normalizeForecastAsOf(t, legacy)
	expected = normalizeForecastAsOf(t, expected)

	for _, k := range sortedKeys(expected) {
		want := expected[k]
		got, ok := legacy[k]
		if !ok {
			t.Errorf("inline payload missing API field %q", k)
			continue
		}
		if !jsonEqual(got, want) {
			t.Errorf("field %q drifts between static and API surfaces:\n inline: %s\n api:    %s",
				k, string(got), string(want))
		}
	}
	for _, k := range sortedKeys(legacy) {
		if _, ok := expected[k]; ok {
			continue
		}
		if _, ok := staticOnly[k]; ok {
			continue
		}
		t.Errorf("inline payload has %q not in API surface or static-only allowlist; either expose it via an /api/* endpoint or document it as static-only", k)
	}
}

// unionOfAPISections rebuilds the legacy BuildPayload shape from the
// per-section builders. The trends endpoint returns {period, dim,
// series} per dim — we flatten those into the legacy trend_by_{dim}
// keys plus a single shared period, since BuildPayload's shape
// predates the per-dim split.
func unionOfAPISections(t *testing.T, a *aggregate.Aggregator) map[string]json.RawMessage {
	t.Helper()
	out := map[string]json.RawMessage{}
	fold := func(label string, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", label, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal %s: %v", label, err)
		}
		for k, raw := range m {
			if existing, ok := out[k]; ok && !jsonEqual(existing, raw) {
				t.Fatalf("API surface emits conflicting values for %q across sections — first %s, then %s",
					k, string(existing), string(raw))
			}
			out[k] = raw
		}
	}
	fold("overview", BuildOverview(a))
	fold("cost", BuildCost(a))
	fold("cache", BuildCache(a))
	fold("tools", BuildTools(a))
	fold("subagents", BuildSubagents(a))
	fold("anomalies", BuildAnomalies(a))
	fold("sessions", BuildSessions(nil))

	for _, dim := range []string{"model", "project", "session", "tool", "subagent"} {
		p, err := BuildTrends(a, dim)
		if err != nil {
			t.Fatalf("BuildTrends(%q): %v", dim, err)
		}
		bP, err := json.Marshal(p.Period)
		if err != nil {
			t.Fatalf("marshal trends period: %v", err)
		}
		if existing, ok := out["period"]; ok && !jsonEqual(existing, bP) {
			t.Fatalf("trends period differs across dims (%s vs %s) — Aggregator.Period() is supposed to be stable", string(existing), string(bP))
		}
		out["period"] = bP
		bS, err := json.Marshal(p.Series)
		if err != nil {
			t.Fatalf("marshal trends series %s: %v", dim, err)
		}
		out["trend_by_"+dim] = bS
	}
	return out
}

func decodeObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode JSON object: %v; raw: %s", err, string(raw))
	}
	return m
}

// normalizeForecastAsOf zeros every forecast field whose value is
// derived from a wall-clock time.Now() call. Both BuildPayload and
// BuildOverview reach for time.Now() internally, and the two calls
// land microseconds apart in this test — enough to perturb
// days_elapsed (a fractional-day count) and everything downstream
// (daily_rate_usd, projected_month_end_usd). The time-invariant
// fields (month_start, days_in_month, mtd_cost_usd, ...) still
// participate in the equality check, so structural drift in the
// forecast struct is still caught.
func normalizeForecastAsOf(t *testing.T, m map[string]json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	raw, ok := m["forecast"]
	if !ok {
		return m
	}
	var f map[string]any
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode forecast: %v", err)
	}
	for _, k := range []string{"as_of", "days_elapsed", "daily_rate_usd", "projected_month_end_usd"} {
		if _, ok := f[k]; ok {
			f[k] = nil
		}
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("re-marshal forecast: %v", err)
	}
	m["forecast"] = b
	return m
}

func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// contractFixture builds the richer aggregator the contract test
// runs against — period=day, two models, two projects, two sessions,
// a sidechain turn, and Bash tool uses. Lives here (rather than
// reusing htmlSetup) because the contract test specifically wants
// each collection field to carry at least one row so per-row drift
// inside a struct surfaces as a JSON diff rather than getting
// flattened to null-on-both-sides.
func contractFixture(t *testing.T) *aggregate.Aggregator {
	t.Helper()
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	a := aggregate.New(prices).WithPeriod(aggregate.PeriodDay)
	day := func(n int) time.Time {
		return time.Date(2026, 5, n, 12, 0, 0, 0, time.UTC)
	}
	tu := func(model, cwd, session string, in, out int, ts time.Time, tools []parse.ToolUse, sidechain bool) parse.Turn {
		return parse.Turn{
			Model:     model,
			CWD:       cwd,
			SessionID: session,
			Sidechain: sidechain,
			Usage:     parse.Usage{InputTokens: in, OutputTokens: out},
			Timestamp: ts,
			ToolUses:  tools,
		}
	}
	bash := []parse.ToolUse{{Name: "Bash", Detail: "git status"}}
	read := []parse.ToolUse{{Name: "Read", Detail: ".go"}}

	a.Add(tu("claude-opus-4-7", "/p/x", "sess-a", 1_000_000, 200_000, day(1), bash, false))
	a.Add(tu("claude-opus-4-7", "/p/x", "sess-a", 500_000, 100_000, day(2), read, false))
	a.Add(tu("claude-sonnet-4-6", "/p/y", "sess-b", 800_000, 150_000, day(2), bash, false))
	a.Add(tu("claude-sonnet-4-6", "/p/y", "sess-b", 200_000, 50_000, day(3), nil, true))
	return a
}
