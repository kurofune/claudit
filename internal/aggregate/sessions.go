package aggregate

import (
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// ToolInvocation is one distinct tool call surfaced on a turn row. Detail
// is the same per-tool sub-key the rolled-up drill-down uses (Bash command,
// Read extension, Agent subagent type, Skill name, etc.) — empty when the
// tool has nothing useful to qualify it.
type ToolInvocation struct {
	// ID is the tool_use id of the (first, when deduped) call this row
	// represents. Lets the drawer fetch the untruncated input/output back
	// from disk on demand. Omitted for older sessions that lack tool_use ids.
	ID     string `json:"id,omitempty"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
	// Kind is the normalized tool category — "agent", "exec", "read", "edit",
	// "web", "skill", "command", "mcp", "todo", "other" — derived from Name by
	// ToolKind. Lets the frontend filter and color by category without matching
	// raw tool names. Distinct from AgentNode.Kind ("main"/"subagent").
	Kind string `json:"kind"`
	// Input is the bounded input snippet from parse.ToolUse.Input — the full
	// Bash command, the subagent prompt, etc. Empty for tools where Detail
	// already says everything. Distinct inputs are NOT collapsed (the dedup
	// key includes Input), so two different Bash commands in one turn both show.
	Input string `json:"input"`
	// Status is the tool's outcome, joined from the matching tool_result:
	// "ok", "error", or "" when no result was captured (still running, or an
	// older session that didn't record one). Output is a bounded snippet of
	// what the tool returned. Both empty when unjoined.
	Status string `json:"status,omitempty"`
	Output string `json:"output,omitempty"`
	// Count is how many raw invocations collapsed into this row — a turn that
	// ran the same (Name, Detail, Input) five times reports Count:5 on the one
	// surviving row instead of hiding the cardinality. Always ≥1. Lets the UI
	// say "ran Read 400×" that the dedup would otherwise erase.
	Count int `json:"count"`
	// OutputBytes/OutputLines are the joined result's FULL size (pre-truncation)
	// from the matching tool_result — how much the tool actually returned, not
	// the bounded Output snippet. Rows is a structured record count (changed
	// lines for Edit/Write, match/result rows for Grep/WebSearch). All zero when
	// no result joined or the tool exposes no such measure.
	OutputBytes int `json:"output_bytes,omitempty"`
	OutputLines int `json:"output_lines,omitempty"`
	Rows        int `json:"rows,omitempty"`
	// Spawned is the rolled-up cost of the sub-agent this Agent call launched —
	// nil for non-Agent calls and Agent calls whose sub-agent isn't in the
	// snapshot. It surfaces one decision's full blast radius (the sub-agent's
	// own cost/tokens/errors/duration) inline on the spawning call.
	Spawned *SpawnRollup `json:"spawned,omitempty"`
	// StartedAt is when the call was emitted — the assistant turn's timestamp.
	// Every tool in a turn shares it (the wire only stamps the turn, not each
	// call). EndedAt is the matched tool_result's timestamp. Together they give
	// per-tool wall-clock the Timeline draws as a sub-span. Pointers so a missing
	// time serializes to null (omitted) rather than a year-1 zero the frontend
	// would misread; both nil for older sessions lacking ids/timestamps, which
	// makes the frontend fall back to turn-level segments.
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// SpawnRollup is the cumulative cost of a single sub-agent, attributed to the
// exact Agent tool_use that spawned it. It's attribution, not double-counting:
// the figures here are the sub-agent's own totals, also counted once at the
// session level.
type SpawnRollup struct {
	// AgentRef identifies the spawned sub-agent — the parent tool_use id, which
	// is also the join key (child.ParentToolUseID == this) the UI uses to jump
	// from the Agent call to its sub-agent.
	AgentRef   string  `json:"agent_ref"`
	CostUSD    float64 `json:"cost_usd"`
	Tokens     Tokens  `json:"tokens"`
	DurationMs int64   `json:"duration_ms"`
	ErrorCount int     `json:"error_count"`
}

// MatchesFilter is the since/until/project test the rolled-up sections use
// (Aggregator.match), exported so other packages (e.g. agentflow) filter turns
// identically. It runs without an Aggregator reference, so it duplicates that
// logic; if the two diverge the Agents view silently disagrees with the
// rolled-up sections, so keep them in lockstep.
func MatchesFilter(t parse.Turn, f Filter) bool {
	if !f.Since.IsZero() && t.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !t.Timestamp.Before(f.Until) {
		return false
	}
	if f.ProjectSubstring != "" {
		if !strings.Contains(strings.ToLower(t.CWD), strings.ToLower(f.ProjectSubstring)) {
			return false
		}
	}
	return true
}

// DistinctToolInvocations returns (Name, Detail) pairs in first-occurrence
// order, deduped by (Name, Detail). A turn that runs `git status` three
// times collapses to one pill, but `git status` then `go test` keeps both
// — same tool, different work. Detail comes from the per-tool field that
// best identifies the call (SubagentType for Agent, SkillName for Skill,
// SlashCommand for SlashCommand, ToolUse.Detail for everything else).
func DistinctToolInvocations(uses []parse.ToolUse, results map[string]parse.ToolResult, turnTS time.Time) []ToolInvocation {
	if len(uses) == 0 {
		return nil
	}
	var startedAt *time.Time
	if !turnTS.IsZero() {
		ts := turnTS
		startedAt = &ts
	}
	type key struct{ name, detail, input string }
	// Map each dedup key to its row index in out, so a repeated call bumps the
	// surviving row's Count instead of being silently dropped.
	seen := make(map[key]int, len(uses))
	out := make([]ToolInvocation, 0, len(uses))
	for _, u := range uses {
		d := toolDetailFor(u)
		k := key{u.Name, d, u.Input}
		if idx, ok := seen[k]; ok {
			out[idx].Count++
			continue
		}
		inv := ToolInvocation{ID: u.ID, Name: u.Name, Kind: ToolKind(u.Name), Detail: d, Input: u.Input, StartedAt: startedAt, Count: 1}
		// Join the outcome from the matching tool_result (by tool_use id).
		if res, ok := results[u.ID]; ok && u.ID != "" {
			if res.IsError {
				inv.Status = "error"
			} else {
				inv.Status = "ok"
			}
			inv.Output = res.Content
			inv.OutputBytes = res.OutputBytes
			inv.OutputLines = res.OutputLines
			inv.Rows = res.Rows
			// The result line's timestamp is this tool's wall-clock end. Guard
			// the zero time (older transcripts without line timestamps) so a
			// missing end stays nil rather than a year-1 instant.
			if !res.Timestamp.IsZero() {
				end := res.Timestamp
				inv.EndedAt = &end
			}
		}
		seen[k] = len(out)
		out = append(out, inv)
	}
	return out
}

// toolDetailFor picks the most identifying sub-key for a tool call. The
// special tools (Agent/Skill/SlashCommand) have their own dedicated fields
// because Input parsing in parse.go fills those out separately; everything
// else falls back to Detail, populated by detail.go's extractor.
func toolDetailFor(u parse.ToolUse) string {
	switch u.Name {
	case "Agent":
		return u.SubagentType
	case "Skill":
		return u.SkillName
	case "SlashCommand":
		return u.SlashCommand
	}
	return u.Detail
}
