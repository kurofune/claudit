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
	SessionID string           `json:"session_id"`
	CWD       string           `json:"cwd"`
	StartedAt time.Time        `json:"started_at"`
	EndedAt   time.Time        `json:"ended_at"`
	CostUSD   float64          `json:"cost_usd"`
	Turns     int              `json:"turns"`
	Prompts   []PromptTimeline `json:"prompts"`
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
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// SessionTimelinesOptions tunes BuildSessionTimelines. All zero values are
// sensible: no cap, no truncation, no redaction. The caller (typically
// cmd/claudit) plumbs CLI flag values into this struct.
type SessionTimelinesOptions struct {
	// TopN caps the returned slice to the top-N sessions by cost. 0 means
	// no cap — return every session that passed the filter. The HTML
	// renderer defaults to 50 because the per-session prompt+turn payload
	// can grow large for active users.
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
// The returned slice is sorted by total cost descending and capped to
// opts.TopN if set. Synthetic / zero-cost turns are still included — they
// contribute to the turn list but not to ranking.
func BuildSessionTimelines(
	ctx context.Context,
	turns []parse.Turn,
	msgs []parse.UserMessage,
	parentLinks []parse.ParentLink,
	prices *pricing.Table,
	filter Filter,
	opts SessionTimelinesOptions,
) ([]SessionTimeline, error) {
	if len(turns) == 0 {
		return nil, nil
	}

	// Build the same parent + userText maps BuildPromptIndex uses. Kept
	// local rather than reused from PromptIndex so this function doesn't
	// require callers to construct one — the drill-down is opt-in.
	parent := make(map[string]string, len(turns)+len(msgs)+len(parentLinks))
	userText := make(map[string]string, len(msgs))
	userTS := make(map[string]time.Time, len(msgs))
	for _, l := range parentLinks {
		if _, exists := parent[l.UUID]; !exists {
			parent[l.UUID] = l.ParentUUID
		}
	}
	for _, t := range turns {
		parent[t.UUID] = t.ParentUUID
	}
	for _, m := range msgs {
		parent[m.UUID] = m.ParentUUID
		userText[m.UUID] = m.Text
		userTS[m.UUID] = m.Timestamp
	}

	// Iterative chain walk → originating user UUID. Same algorithm as
	// BuildPromptIndex but we keep the UUID itself rather than the
	// normalized bucket key.
	cache := make(map[string]string, len(turns))
	resolveUserUUID := func(start string) string {
		if start == "" {
			return ""
		}
		if v, ok := cache[start]; ok {
			return v
		}
		chain := []string{start}
		seen := map[string]struct{}{start: {}}
		var found string
		cur := start
		for {
			if _, ok := userText[cur]; ok {
				found = cur
				break
			}
			p, ok := parent[cur]
			if !ok || p == "" {
				break
			}
			if v, ok := cache[p]; ok {
				found = v
				break
			}
			if _, loop := seen[p]; loop {
				break
			}
			seen[p] = struct{}{}
			chain = append(chain, p)
			cur = p
		}
		for _, u := range chain {
			cache[u] = found
		}
		return found
	}

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
		SessionID string
		CWD       string
		StartedAt time.Time
		EndedAt   time.Time
		CostUSD   float64
		Turns     int
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
				Timestamp: userTS[userUUID], // zero for orphan ""
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
			Tools:     distinctToolInvocations(t.ToolUses),
			Sidechain: t.Sidechain,
		})
	}

	// Materialize, sort, and cap.
	out := make([]SessionTimeline, 0, len(sessions))
	for _, s := range sessions {
		st := SessionTimeline{
			SessionID: s.SessionID,
			CWD:       s.CWD,
			StartedAt: s.StartedAt,
			EndedAt:   s.EndedAt,
			CostUSD:   s.CostUSD,
			Turns:     s.Turns,
		}
		for _, pa := range s.Prompts {
			raw := userText[pa.UUID]
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
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
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
	tls, err := BuildSessionTimelines(ctx, filtered, msgs, parentLinks, prices, filter, opts)
	if err != nil {
		return nil, err
	}
	if len(tls) == 0 {
		return nil, nil
	}
	// At most one entry — filtered turns share a sessionID.
	return &tls[0], nil
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

// preparePromptText applies redact + truncation. Order matters: redact
// first (so the [redacted N chars] count reflects the real length, not the
// truncated length).
func preparePromptText(raw string, opts SessionTimelinesOptions) (string, bool) {
	if opts.Redact {
		return fmt.Sprintf("[redacted %d chars]", len(raw)), false
	}
	if opts.MaxPromptChars > 0 && len(raw) > opts.MaxPromptChars {
		return raw[:opts.MaxPromptChars], true
	}
	return raw, false
}

// distinctToolInvocations returns (Name, Detail) pairs in first-occurrence
// order, deduped by (Name, Detail). A turn that runs `git status` three
// times collapses to one pill, but `git status` then `go test` keeps both
// — same tool, different work. Detail comes from the per-tool field that
// best identifies the call (SubagentType for Agent, SkillName for Skill,
// SlashCommand for SlashCommand, ToolUse.Detail for everything else).
func distinctToolInvocations(uses []parse.ToolUse) []ToolInvocation {
	if len(uses) == 0 {
		return nil
	}
	type key struct{ name, detail string }
	seen := make(map[key]struct{}, len(uses))
	out := make([]ToolInvocation, 0, len(uses))
	for _, u := range uses {
		d := toolDetailFor(u)
		k := key{u.Name, d}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, ToolInvocation{Name: u.Name, Detail: d})
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
