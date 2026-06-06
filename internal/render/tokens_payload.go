package render

import (
	"sort"

	"github.com/kurofune/claudit/internal/aggregate"
)

// TokenCategory is one row of the token-composition breakdown — a
// single category's absolute count and its share of the grand total.
type TokenCategory struct {
	Label  string  `json:"label"`
	Tokens int64   `json:"tokens"`
	Pct    float64 `json:"pct"` // share of grand total, 0..100
}

// TokenModelRow is one row of the by-model token table.
type TokenModelRow struct {
	Model      string  `json:"model"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheWrite int64   `json:"cache_write"` // cache_create_5m + cache_create_1h
	CacheRead  int64   `json:"cache_read"`
	Total      int64   `json:"total"`
	Pct        float64 `json:"pct"` // share of grand total tokens, 0..100
}

// TokenBreakdownRow is one row of a by-X token table (project, skill, or
// prompt). It carries the same 4-category split as TokenModelRow but with
// a generic Label so the four Tokens-tab tables render through one code
// path. Sample is the full label text for tooltip use (prompts truncate
// the visible Label); it is empty for project/skill rows.
type TokenBreakdownRow struct {
	Label      string  `json:"label"`
	Sample     string  `json:"sample,omitempty"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheWrite int64   `json:"cache_write"`
	CacheRead  int64   `json:"cache_read"`
	Total      int64   `json:"total"`
	Pct        float64 `json:"pct"`
}

// TokensPayload backs /_claudit/api/tokens — the token-composition
// story: grand total, the category breakdown, the per-period token
// trend (reused from TrendTotals), and the by-model / by-project /
// by-skill / by-prompt breakdown tables (each a token-centric cut of
// the matching Cost-tab table).
type TokensPayload struct {
	Total       int64                  `json:"total"`
	Composition []TokenCategory        `json:"composition"`
	Trend       []aggregate.TrendPoint `json:"trend"`
	ByModel     []TokenModelRow        `json:"by_model"`
	ByProject   []TokenBreakdownRow    `json:"by_project"`
	BySkill     []TokenBreakdownRow    `json:"by_skill"`
	ByPrompt    []TokenBreakdownRow    `json:"by_prompt"`
	// Period is the bucket granularity of Trend — read by the SPA to
	// label the trend axis ("hour" → HH:MM) instead of hardcoding day.
	Period aggregate.Period `json:"period"`
}

// tokenComposition splits a token tuple into the canonical 4-category
// breakdown (Input / Output / Cache write / Cache read), each with its
// share of grand (0..100, or 0 when grand is zero). The category order
// is fixed so callers can zip two compositions positionally — relied on
// by BuildTokenDiff to pair A against B. Cache write folds the 5m and 1h
// cache-create buckets into one figure.
func tokenComposition(tot aggregate.Tokens, grand int64) []TokenCategory {
	pct := func(n int64) float64 {
		if grand == 0 {
			return 0
		}
		return 100 * float64(n) / float64(grand)
	}
	cw := tot.CacheCreate5mTokens + tot.CacheCreate1hTokens
	return []TokenCategory{
		{Label: "Input", Tokens: tot.InputTokens, Pct: pct(tot.InputTokens)},
		{Label: "Output", Tokens: tot.OutputTokens, Pct: pct(tot.OutputTokens)},
		{Label: "Cache write", Tokens: cw, Pct: pct(cw)},
		{Label: "Cache read", Tokens: tot.CacheReadTokens, Pct: pct(tot.CacheReadTokens)},
	}
}

// BuildTokens rolls the aggregator into the Tokens-tab payload.
func BuildTokens(a *aggregate.Aggregator) TokensPayload {
	tot := a.Totals().Tokens
	grand := tot.Total()
	pct := func(n int64) float64 {
		if grand == 0 {
			return 0
		}
		return 100 * float64(n) / float64(grand)
	}

	composition := tokenComposition(tot, grand)

	buckets := a.ByModel()
	byModel := make([]TokenModelRow, 0, len(buckets))
	for _, b := range buckets {
		total := b.Total()
		byModel = append(byModel, TokenModelRow{
			Model:      b.Model,
			Input:      b.InputTokens,
			Output:     b.OutputTokens,
			CacheWrite: b.CacheCreate5mTokens + b.CacheCreate1hTokens,
			CacheRead:  b.CacheReadTokens,
			Total:      total,
			Pct:        pct(total),
		})
	}

	return TokensPayload{
		Total:       grand,
		Composition: composition,
		Trend:       a.TrendTotals(),
		ByModel:     byModel,
		ByProject:   projectTokenRows(a, grand),
		BySkill:     skillTokenRows(a, grand),
		ByPrompt:    promptTokenRows(a, grand),
		Period:      a.Period(),
	}
}

// breakdownRow turns a token tuple + identifying label into one
// TokenBreakdownRow, computing the cache-write fold and the share of the
// grand total. sample carries the untruncated label text (only prompts
// use it; project/skill pass "").
func breakdownRow(label, sample string, tok aggregate.Tokens, grand int64) TokenBreakdownRow {
	total := tok.Total()
	pct := 0.0
	if grand > 0 {
		pct = 100 * float64(total) / float64(grand)
	}
	return TokenBreakdownRow{
		Label:      label,
		Sample:     sample,
		Input:      tok.InputTokens,
		Output:     tok.OutputTokens,
		CacheWrite: tok.CacheCreate5mTokens + tok.CacheCreate1hTokens,
		CacheRead:  tok.CacheReadTokens,
		Total:      total,
		Pct:        pct,
	}
}

// sortByTotalDesc orders breakdown rows by total tokens descending — the
// Tokens tab ranks by volume, not by cost (which is how the aggregator
// returns its buckets).
func sortByTotalDesc(rows []TokenBreakdownRow) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Total > rows[j].Total })
}

func projectTokenRows(a *aggregate.Aggregator, grand int64) []TokenBreakdownRow {
	buckets := a.ByProject()
	rows := make([]TokenBreakdownRow, 0, len(buckets))
	for _, b := range buckets {
		rows = append(rows, breakdownRow(b.Project, "", b.Tokens, grand))
	}
	sortByTotalDesc(rows)
	return rows
}

func skillTokenRows(a *aggregate.Aggregator, grand int64) []TokenBreakdownRow {
	buckets := a.BySkill()
	rows := make([]TokenBreakdownRow, 0, len(buckets))
	for _, b := range buckets {
		rows = append(rows, breakdownRow(b.Key, "", b.Tokens, grand))
	}
	sortByTotalDesc(rows)
	return rows
}

func promptTokenRows(a *aggregate.Aggregator, grand int64) []TokenBreakdownRow {
	buckets := a.ByPrompt()
	rows := make([]TokenBreakdownRow, 0, len(buckets))
	for _, b := range buckets {
		// Label gets the sample (the SPA truncates it); Sample keeps the
		// full text for the row tooltip. Fall back to Key for orphans.
		label := b.Sample
		if label == "" {
			label = b.Key
		}
		rows = append(rows, breakdownRow(label, b.Sample, b.Tokens, grand))
	}
	sortByTotalDesc(rows)
	return rows
}
