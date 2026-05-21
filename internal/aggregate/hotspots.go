package aggregate

import (
	"sort"
)

// HotspotKind identifies the dimension a hotspot was found on. Each kind
// has its own LLM-prompt template in the render package, because the kind
// of advice that helps differs wildly (a Bash command pattern needs
// different help than a single huge subagent invocation).
type HotspotKind string

const (
	HotspotBashPattern   HotspotKind = "bash_pattern"
	HotspotFileExt       HotspotKind = "file_ext"  // Read/Edit/Write/NotebookEdit by extension
	HotspotGrepGlob      HotspotKind = "grep_glob" // Grep/Glob patterns
	HotspotWebHost       HotspotKind = "web_host"
	HotspotProject       HotspotKind = "project"
	HotspotSubagentType  HotspotKind = "subagent_type"
	HotspotInvocation    HotspotKind = "invocation"     // a single subagent run
	HotspotSkill         HotspotKind = "skill"          // a Skill or SlashCommand
	HotspotCacheMiss     HotspotKind = "cache_miss"     // low cache hit rate at a project
	HotspotPromptPattern HotspotKind = "prompt_pattern" // an expensive habitual user prompt
)

// Hotspot is one ranked optimization target. Title is what the renderer
// shows; Context is the substitution data for the LLM-prompt template.
type Hotspot struct {
	Kind       HotspotKind
	Title      string // human-readable, e.g. "Bash: `bd show`"
	CostUSD    float64
	PctOfTotal float64

	// Context carries everything the prompt template needs, populated per kind.
	// Conventional keys (kind-dependent): "tool", "pattern", "calls", "turns",
	// "project", "dominant_model", "subagent_type", "description", "started",
	// "skill_key", "ext".
	Context map[string]any
}

// Hotspots returns the top-N actionable cost contributors, ranked by cost
// descending. We mix dimensions deliberately so the user sees a project,
// a Bash pattern, a subagent type, etc. all in one ranked view.
//
// Aggregate-only rows (the by-tool parent buckets, by-model rows, the
// main vs sidechain split) are deliberately excluded — they're not
// directly actionable, since cost there is just a roll-up of the more
// specific hotspots we already include.
func (a *Aggregator) Hotspots(top int) []Hotspot {
	if top <= 0 {
		return nil
	}
	total := a.totals.CostUSD
	mainCost := a.side.Main.CostUSD + a.side.Sidechain.CostUSD
	_ = mainCost // total already includes both; kept for clarity

	var cands []Hotspot

	// (Tool, pattern) hotspots from the per-tool drill-down.
	// Skip Skill/SlashCommand/Agent — those have their own dimensions.
	skipTool := map[string]bool{"Skill": true, "SlashCommand": true, "Agent": true}
	for tool, rows := range a.byToolDetail {
		if skipTool[tool] {
			continue
		}
		dominant := dominantModelOverall(a) // we don't track per-bucket model; use overall
		for _, r := range rows {
			kind, title := classifyDetail(tool, r.Detail)
			cands = append(cands, Hotspot{
				Kind:    kind,
				Title:   title,
				CostUSD: r.CostUSD,
				Context: map[string]any{
					"tool":           tool,
					"pattern":        r.Detail,
					"calls":          r.Count,
					"turns":          r.TurnCount,
					"output_tokens":  r.OutputTokens,
					"dominant_model": dominant,
				},
			})
		}
	}

	// Project-level rows are deliberately excluded from hotspots — they're
	// diagnostic ("$3k went here"), not directly actionable. The full
	// by-project section in the report still surfaces them. The HotspotProject
	// kind and its prompt template are kept for callers that want to opt in.

	// Subagent-type hotspots.
	for _, sb := range a.BySubagent() {
		// "sidechain — unknown subagent" is an artifact, not actionable.
		if sb.Type == "sidechain — unknown subagent" {
			continue
		}
		cands = append(cands, Hotspot{
			Kind:    HotspotSubagentType,
			Title:   "Subagent type: " + sb.Type,
			CostUSD: sb.CostUSD,
			Context: map[string]any{
				"subagent_type": sb.Type,
				"turns":         sb.Turns,
				"output_tokens": sb.OutputTokens,
				"cache_read":    sb.CacheReadTokens,
			},
		})
	}

	// Top single subagent invocations — only the very biggest are worth
	// pulling into the headline list as "this one run cost $X."
	invs := a.AgentInvocations("")
	for i, inv := range invs {
		if i >= 5 {
			break
		}
		desc := inv.Description
		if desc == "" {
			desc = "(no description)"
		}
		cands = append(cands, Hotspot{
			Kind:    HotspotInvocation,
			Title:   "Invocation: " + desc,
			CostUSD: inv.CostUSD,
			Context: map[string]any{
				"subagent_type": inv.SubagentType,
				"description":   inv.Description,
				"turns":         inv.Turns,
				"project":       inv.Project,
				"started":       inv.First.UTC().Format("2006-01-02 15:04 UTC"),
				"source_file":   inv.SourceFile,
			},
		})
	}

	// Cache-miss hotspots: projects w/ poor hit ratio AND non-trivial
	// miss volume. Ranked by "wasteable cost" — the row's spend weighted
	// by (1 − hit_ratio), an estimate of dollars that went to fresh
	// upload/cache-create instead of cheap cache reads.
	const cacheHotspotMinMiss = int64(1_000_000)
	const cacheHotspotMaxRatio = 0.85
	for i, r := range a.CacheByProject() {
		if i >= 5 {
			break
		}
		if r.Miss < cacheHotspotMinMiss || r.HitRatio >= cacheHotspotMaxRatio {
			continue
		}
		wasteable := r.CostUSD * (1 - r.HitRatio)
		cands = append(cands, Hotspot{
			Kind:    HotspotCacheMiss,
			Title:   "Cache miss: " + r.Key,
			CostUSD: wasteable,
			Context: map[string]any{
				"key":             r.Key,
				"hit_ratio_pct":   100 * r.HitRatio,
				"miss_tokens":     r.Miss,
				"cache_read":      r.CacheReadTokens,
				"input_tokens":    r.InputTokens,
				"cache_create_5m": r.CacheCreate5mTokens,
				"cache_create_1h": r.CacheCreate1hTokens,
				"turns":           r.Turns,
				"row_cost":        r.CostUSD,
			},
		})
	}

	// Top expensive prompts. Habitual user asks are often the highest-
	// leverage thing to restructure — usually invisible because they're
	// hidden inside the agent's intent rather than tool output.
	prompts := a.ByPrompt()
	for i, p := range prompts {
		if i >= 5 {
			break
		}
		// Skip the orphan bucket — there's nothing for the LLM to
		// reformulate, and it would drown out real prompts.
		if p.Key == noPromptKey {
			continue
		}
		title := p.Sample
		if len(title) > 80 {
			title = title[:80] + "…"
		}
		cands = append(cands, Hotspot{
			Kind:    HotspotPromptPattern,
			Title:   "Prompt: " + title,
			CostUSD: p.CostUSD,
			Context: map[string]any{
				"sample":      p.Sample,
				"key":         p.Key,
				"invocations": p.Invocations,
				"sessions":    p.Sessions,
				"turns":       p.TurnCount,
			},
		})
	}

	// Skills + slash commands.
	for _, s := range a.BySkill() {
		cands = append(cands, Hotspot{
			Kind:    HotspotSkill,
			Title:   s.Key,
			CostUSD: s.CostUSD,
			Context: map[string]any{
				"skill_key":     s.Key,
				"calls":         s.Count,
				"turns":         s.TurnCount,
				"output_tokens": s.OutputTokens,
			},
		})
	}

	// Sort descending by cost; populate PctOfTotal; cap.
	sort.Slice(cands, func(i, j int) bool { return cands[i].CostUSD > cands[j].CostUSD })
	if len(cands) > top {
		cands = cands[:top]
	}
	for i := range cands {
		if total > 0 {
			cands[i].PctOfTotal = 100 * cands[i].CostUSD / total
		}
	}
	return cands
}

// classifyDetail picks a Hotspot kind from (tool, detail) so the renderer
// can pick the right prompt template. Falls back to HotspotBashPattern
// for unknown tools.
func classifyDetail(tool, detail string) (HotspotKind, string) {
	switch tool {
	case "Bash", "Monitor":
		return HotspotBashPattern, "Bash pattern: `" + detail + "`"
	case "Read", "Edit", "Write", "NotebookEdit":
		return HotspotFileExt, tool + " of `" + detail + "` files"
	case "Grep", "Glob":
		return HotspotGrepGlob, tool + ": `" + detail + "`"
	case "WebFetch":
		return HotspotWebHost, "WebFetch: " + detail
	}
	return HotspotBashPattern, tool + ": `" + detail + "`"
}

// dominantModelOverall returns the single-most-expensive model, used as
// "context" for tool-pattern hotspots since we don't track per-bucket
// model. Best-effort — the prompt should be useful even if this is
// slightly off for a specific bucket.
func dominantModelOverall(a *Aggregator) string {
	var best string
	var bestCost float64
	for _, m := range a.byModel {
		if m.CostUSD > bestCost {
			bestCost = m.CostUSD
			best = m.Model
		}
	}
	return best
}
