package aggregate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// SessionTimeline is one drill-down record: a session in the corpus,
// expanded into the ordered list of user prompts and the assistant turns
// each one produced. The renderer pages this into the "Sessions" view of
// the HTML report.
type SessionTimeline struct {
	SessionID string    `json:"session_id"`
	CWD       string    `json:"cwd"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	CostUSD   float64   `json:"cost_usd"`
	Turns     int       `json:"turns"`
	// Entrypoint is the session origin lifted from its turns: "cli" for an
	// interactive session, "sdk-cli" for a headless/SDK run. Lets the
	// Sessions view split interactive from headless. Empty if unknown.
	Entrypoint string           `json:"entrypoint"`
	Prompts    []PromptTimeline `json:"prompts"`
}

// PromptTimeline is one user prompt within a session along with the
// downstream assistant turns it produced. Cost is the sum of TurnSummary
// costs — saved here too so the renderer doesn't have to re-sum at render
// time.
type PromptTimeline struct {
	UUID string `json:"uuid"`
	// Key is the same normalized bucket key used by PromptBucket and the
	// prompt-kind hotspots, computed from the RAW prompt text before any
	// redaction. The frontend uses it to cross-link a hotspot or
	// per-prompt row back to this session's drill-down. Empty for orphan
	// prompts.
	Key       string        `json:"key"`
	Text      string        `json:"text"`      // may be truncated, or "[redacted N chars]" when Redact is set
	Truncated bool          `json:"truncated"` // true when Text was shortened from the original
	Timestamp time.Time     `json:"timestamp"`
	CostUSD   float64       `json:"cost_usd"`
	Turns     []TurnSummary `json:"turns"`
}

// TurnSummary is one assistant turn rendered in the drill-down. Carries
// what an engineer needs to recognize the turn at a glance — what model
// answered, what it cost, which tools fired — without the full tool I/O
// (that's deferred to v2.0.x).
type TurnSummary struct {
	Timestamp time.Time `json:"timestamp"`
	Model     string    `json:"model"`
	CostUSD   float64   `json:"cost_usd"`
	Tokens    Tokens    `json:"tokens"`
	// Tools is distinct tool invocations in first-occurrence order. A pair
	// of (Name, Detail) is treated as distinct — so "Bash · git status"
	// and "Bash · go test" both appear, but "Read · .go" repeated five
	// times in the same turn collapses to one entry.
	Tools []ToolInvocation `json:"tools"`
	// DurationMs is the wall-clock gap to the next turn within the same
	// prompt, in milliseconds. Surfaces "this turn took 11s" hotspots that
	// pure cost doesn't expose. Zero for the last turn of a prompt (no next)
	// or when the next turn arrived in the same millisecond.
	DurationMs int64 `json:"duration_ms"`
	Sidechain  bool  `json:"sidechain"`
}

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
	// older session that didn't record one). Output is a bounded, redaction-
	// aware snippet of what the tool returned. Both empty when unjoined.
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

// SessionTimelinesOptions tunes BuildSessionTimelines. All zero values are
// sensible: no cap, no truncation, no redaction. The caller (typically
// cmd/claudit) plumbs CLI flag values into this struct.
type SessionTimelinesOptions struct {
	// TopN caps the returned slice to the N most-recently-active sessions.
	// 0 means no cap — return every session that passed the filter, which
	// is what the served Sessions view uses (the time-range filter is the
	// only bound; the UI paginates client-side). The static HTML report
	// still sets a positive cap to bound the self-contained file's size.
	TopN int

	// Redact replaces every prompt's Text with "[redacted N chars]" so a
	// generated report can be shared without leaking conversation
	// contents. Costs, tokens, tool names, and timestamps are still
	// emitted — only the prompt body is hidden.
	Redact bool

	// MaxPromptChars truncates each prompt's Text to this many characters
	// (after redaction). 0 disables truncation. PromptTimeline.Truncated
	// records whether the body was shortened so the renderer can show a
	// "(truncated)" marker.
	MaxPromptChars int
}

// BuildSessionTimelines walks the corpus and produces per-session
// timelines suitable for the HTML report's drill-down view. The filter
// mirrors Aggregator.WithFilter so the drill-down respects --since/--until/
// --project just like the rolled-up sections do.
//
// turns and msgs come from the same parse pass as everything else;
// parentLinks supplies extra parent edges so the chain walker can climb
// through non-content lines (system events, file-history snapshots).
//
// The returned slice is sorted by last activity (EndedAt) descending —
// most recent session first — and capped to opts.TopN if set. Synthetic /
// zero-cost turns are still included; they contribute to the turn list and,
// via their timestamps, to a session's StartedAt/EndedAt span.
func BuildSessionTimelines(
	ctx context.Context,
	turns []parse.Turn,
	msgs []parse.UserMessage,
	parentLinks []parse.ParentLink,
	prices *pricing.Table,
	filter Filter,
	opts SessionTimelinesOptions,
) ([]SessionTimeline, error) {
	// Replay set is built from the same turns this call rolls up. The
	// single-session entrypoint (BuildSessionTimeline) instead builds it from
	// the full corpus before narrowing and calls buildSessionTimelines
	// directly — replays are cross-session, so a narrowed slice can't detect
	// them on its own.
	return buildSessionTimelines(ctx, turns, msgs, parentLinks, prices, filter, opts, BuildReplaySet(turns))
}

func buildSessionTimelines(
	ctx context.Context,
	turns []parse.Turn,
	msgs []parse.UserMessage,
	parentLinks []parse.ParentLink,
	prices *pricing.Table,
	filter Filter,
	opts SessionTimelinesOptions,
	replays ReplaySet,
) ([]SessionTimeline, error) {
	if len(turns) == 0 {
		return nil, nil
	}

	// Chain walker: any turn's parentUuid → its originating user prompt UUID.
	// Shared with the Agents Conversation lens (agentflow.BuildAgentGraph) so
	// the two drill-downs attribute turns to prompts identically. Kept local
	// to this call (not reused from PromptIndex) so the drill-down stays
	// opt-in — callers needn't construct an index.
	resolver := NewPromptResolver(turns, msgs, parentLinks)
	resolveUserUUID := resolver.Resolve

	// sessionAccum holds in-progress per-session state. We use a stable
	// secondary key (orphan "" bucket) so turns whose chain doesn't reach
	// a recognized prompt still get a slot.
	type promptAccum struct {
		UUID      string
		Timestamp time.Time
		CostUSD   float64
		Turns     []TurnSummary
	}
	type sessionAccum struct {
		SessionID  string
		CWD        string
		Entrypoint string
		StartedAt  time.Time
		EndedAt    time.Time
		CostUSD    float64
		Turns      int
		// Map prompt UUID → accumulator. "" key holds orphan turns.
		Prompts map[string]*promptAccum
	}

	sessions := map[string]*sessionAccum{}

	// Cancellation: a disconnected HTTP client triggers this. Check at
	// entry and every 1024 turns — frequent enough to short-circuit a
	// large corpus quickly, cheap enough not to dominate the hot loop.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for i, t := range turns {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if !matchesFilter(t, filter) {
			continue
		}
		// Skip replayed copies from a resumed/forked session: their cost and
		// tokens were counted on the canonical occurrence, so counting them
		// here too would inflate this session's spend above the headline.
		if replays.IsReplay(t) {
			continue
		}
		cost, _ := prices.Cost(t.Model,
			t.Usage.InputTokens, t.Usage.OutputTokens,
			t.Usage.CacheCreate5mTokens, t.Usage.CacheCreate1hTokens,
			t.Usage.CacheReadTokens)

		s, ok := sessions[t.SessionID]
		if !ok {
			s = &sessionAccum{
				SessionID: t.SessionID,
				CWD:       t.CWD,
				StartedAt: t.Timestamp,
				EndedAt:   t.Timestamp,
				Prompts:   map[string]*promptAccum{},
			}
			sessions[t.SessionID] = s
		}
		if s.CWD == "" && t.CWD != "" {
			s.CWD = t.CWD
		}
		// Entrypoint is a session invariant — capture the first non-empty one.
		if s.Entrypoint == "" && t.Entrypoint != "" {
			s.Entrypoint = t.Entrypoint
		}
		if t.Timestamp.Before(s.StartedAt) {
			s.StartedAt = t.Timestamp
		}
		if t.Timestamp.After(s.EndedAt) {
			s.EndedAt = t.Timestamp
		}
		s.CostUSD += cost
		s.Turns++

		userUUID := resolveUserUUID(t.ParentUUID)
		pa, ok := s.Prompts[userUUID]
		if !ok {
			pa = &promptAccum{
				UUID:      userUUID,
				Timestamp: resolver.Timestamp(userUUID), // zero for orphan ""
			}
			s.Prompts[userUUID] = pa
		}
		pa.CostUSD += cost

		var tokens Tokens
		tokens.addUsage(t.Usage)
		pa.Turns = append(pa.Turns, TurnSummary{
			Timestamp: t.Timestamp,
			Model:     t.Model,
			CostUSD:   cost,
			Tokens:    tokens,
			// The Sessions view doesn't render per-tool sub-spans (it shows
			// turn-level DurationMs), and it joins no tool_results here, so pass
			// a zero turnTS to leave StartedAt/EndedAt unset — only the agentflow
			// Timeline path needs per-tool timing.
			Tools:     distinctToolInvocations(t.ToolUses, nil, opts.Redact, time.Time{}),
			Sidechain: t.Sidechain,
		})
	}

	// Materialize, sort, and cap.
	out := make([]SessionTimeline, 0, len(sessions))
	for _, s := range sessions {
		st := SessionTimeline{
			SessionID:  s.SessionID,
			CWD:        s.CWD,
			StartedAt:  s.StartedAt,
			EndedAt:    s.EndedAt,
			CostUSD:    s.CostUSD,
			Turns:      s.Turns,
			Entrypoint: s.Entrypoint,
		}
		for _, pa := range s.Prompts {
			raw := resolver.Text(pa.UUID)
			text, truncated := preparePromptText(raw, opts)
			// Key is computed from the raw text — never from the
			// possibly-redacted display text — so cross-links from
			// hotspots/by-prompt rows still match even with --redact.
			// Orphan prompts (no resolved user UUID) get an empty key
			// because there's nothing meaningful to link from.
			var key string
			if pa.UUID != "" {
				key = normalizePromptKey(raw)
			}
			// Order turns within a prompt chronologically — JSONL is
			// usually already in order but defensive sort is cheap.
			sort.Slice(pa.Turns, func(i, j int) bool {
				return pa.Turns[i].Timestamp.Before(pa.Turns[j].Timestamp)
			})
			// Inter-turn duration: gap from this turn's timestamp to the
			// next within the same prompt. Last turn has no "next" — leave
			// zero. The frontend hides zero values.
			for i := 0; i < len(pa.Turns)-1; i++ {
				d := pa.Turns[i+1].Timestamp.Sub(pa.Turns[i].Timestamp)
				if d > 0 {
					pa.Turns[i].DurationMs = d.Milliseconds()
				}
			}
			// If the prompt itself has no recorded timestamp (orphan), use
			// the first turn's so it still sorts coherently.
			ts := pa.Timestamp
			if ts.IsZero() && len(pa.Turns) > 0 {
				ts = pa.Turns[0].Timestamp
			}
			st.Prompts = append(st.Prompts, PromptTimeline{
				UUID:      pa.UUID,
				Key:       key,
				Text:      text,
				Truncated: truncated,
				Timestamp: ts,
				CostUSD:   pa.CostUSD,
				Turns:     pa.Turns,
			})
		}
		// Prompts within a session ordered by first occurrence.
		sort.Slice(st.Prompts, func(i, j int) bool {
			return st.Prompts[i].Timestamp.Before(st.Prompts[j].Timestamp)
		})
		out = append(out, st)
	}

	sort.Slice(out, func(i, j int) bool {
		// Most-recent-activity first: rank by last turn timestamp (EndedAt)
		// descending so the freshest sessions lead the drill-down. The view
		// pages this 10-at-a-time, bounded only by the filter window.
		if !out[i].EndedAt.Equal(out[j].EndedAt) {
			return out[i].EndedAt.After(out[j].EndedAt)
		}
		// Stable tiebreak on SessionID so the output is deterministic.
		return out[i].SessionID < out[j].SessionID
	})
	if opts.TopN > 0 && len(out) > opts.TopN {
		out = out[:opts.TopN]
	}
	return out, nil
}

// BuildSessionTimeline is the single-session entry point used by
// /_claudit/api/sessions/{id}/timeline. Filters turns down to the
// requested SessionID up front so the chain-walker only does work
// for one session — turning the multi-session O(N) into O(turns in
// session), the perf win that makes per-session-on-click lazy
// loading affordable.
//
// Returns nil (no error) when no turns belong to the requested
// session in the (filtered) corpus — the handler can surface that
// as 404.
func BuildSessionTimeline(
	ctx context.Context,
	sessionID string,
	turns []parse.Turn,
	msgs []parse.UserMessage,
	parentLinks []parse.ParentLink,
	prices *pricing.Table,
	filter Filter,
	opts SessionTimelinesOptions,
) (*SessionTimeline, error) {
	if sessionID == "" {
		return nil, nil
	}
	// Build the replay set from the FULL corpus before narrowing. Replays
	// are cross-session (a fork lives in a different sessionId/file), so a
	// slice narrowed to one session can't tell its replayed turns from
	// originals — the canonical copy is in another session entirely.
	replays := BuildReplaySet(turns)
	// Narrow the turn slice to just this session before the heavy
	// timeline walk runs. The user-messages slice can stay as-is —
	// resolveUserUUID is cached and irrelevant entries cost nothing
	// past the initial map allocation.
	filtered := make([]parse.Turn, 0, 64)
	for _, t := range turns {
		if t.SessionID == sessionID {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	// TopN is meaningless for a single-session view — force off so a
	// caller that forwards the same options struct can't accidentally
	// cap it to zero rows.
	opts.TopN = 0
	tls, err := buildSessionTimelines(ctx, filtered, msgs, parentLinks, prices, filter, opts, replays)
	if err != nil {
		return nil, err
	}
	if len(tls) == 0 {
		return nil, nil
	}
	// At most one entry — filtered turns share a sessionID.
	return &tls[0], nil
}

// MatchesFilter is the exported entry to the same since/until/project test
// the rolled-up sections use, so other packages (e.g. agentflow) filter turns
// identically instead of keeping their own divergent copy.
func MatchesFilter(t parse.Turn, f Filter) bool {
	return matchesFilter(t, f)
}

// matchesFilter is the same logic as Aggregator.match — duplicated here
// because BuildSessionTimelines doesn't hold an Aggregator reference (it
// runs as a standalone pass over the same corpus). If the two diverge,
// drill-down will silently disagree with the rolled-up sections, so keep
// them in lockstep.
func matchesFilter(t parse.Turn, f Filter) bool {
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

// redactMarker returns the parity redaction placeholder used for both prompt
// bodies and tool inputs: a "[redacted N chars]" marker echoing the raw length.
func redactMarker(s string) string {
	return fmt.Sprintf("[redacted %d chars]", len(s))
}

// RedactMarker is the exported wrapper around redactMarker so other packages
// (e.g. agentflow) redact reasoning/narration text with identical parity
// rather than reimplementing the marker format.
func RedactMarker(s string) string {
	return redactMarker(s)
}

// preparePromptText applies redact + truncation. Order matters: redact
// first (so the [redacted N chars] count reflects the real length, not the
// truncated length).
func preparePromptText(raw string, opts SessionTimelinesOptions) (string, bool) {
	if opts.Redact {
		return redactMarker(raw), false
	}
	if opts.MaxPromptChars > 0 && len(raw) > opts.MaxPromptChars {
		return raw[:opts.MaxPromptChars], true
	}
	return raw, false
}

// DistinctToolInvocations is the exported entry point to the same dedup +
// detail-selection logic the session drill-down uses, so other packages
// (e.g. agentflow) surface tool calls identically instead of drifting.
func DistinctToolInvocations(uses []parse.ToolUse, results map[string]parse.ToolResult, redact bool, turnTS time.Time) []ToolInvocation {
	return distinctToolInvocations(uses, results, redact, turnTS)
}

// distinctToolInvocations returns (Name, Detail) pairs in first-occurrence
// order, deduped by (Name, Detail). A turn that runs `git status` three
// times collapses to one pill, but `git status` then `go test` keeps both
// — same tool, different work. Detail comes from the per-tool field that
// best identifies the call (SubagentType for Agent, SkillName for Skill,
// SlashCommand for SlashCommand, ToolUse.Detail for everything else).
func distinctToolInvocations(uses []parse.ToolUse, results map[string]parse.ToolResult, redact bool, turnTS time.Time) []ToolInvocation {
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
		// Dedup on the REAL input so two distinct commands of the same
		// length stay distinct even under --redact.
		k := key{u.Name, d, u.Input}
		if idx, ok := seen[k]; ok {
			out[idx].Count++
			continue
		}
		input := u.Input
		// Redact the input snippet (full Bash command, subagent prompt, etc.)
		// to a length-echoing marker so shared/static reports don't leak it.
		// Detail is left alone — it's a coarse, low-cardinality bucket key,
		// not a content leak. Empty input stays empty (nothing to leak, and
		// "[redacted 0 chars]" would just add noise).
		if redact && input != "" {
			input = redactMarker(input)
		}
		inv := ToolInvocation{ID: u.ID, Name: u.Name, Kind: ToolKind(u.Name), Detail: d, Input: input, StartedAt: startedAt, Count: 1}
		// Join the outcome from the matching tool_result (by tool_use id).
		// Status is content-free so it survives redaction; Output is masked
		// the same way Input is.
		if res, ok := results[u.ID]; ok && u.ID != "" {
			if res.IsError {
				inv.Status = "error"
			} else {
				inv.Status = "ok"
			}
			inv.Output = res.Content
			if redact && inv.Output != "" {
				inv.Output = redactMarker(inv.Output)
			}
			// Size measures are pure counts (no content), so they ride through
			// untouched by redaction.
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
