// Package agentflow reconstructs the agent tree (a main session plus the
// sub-agents it spawned) and each agent's tool-call timeline from an
// already-parsed corpus snapshot. It powers the "Agents" observability tab
// in `claudit serve`. The JSON shape emitted here is the stable contract the
// frontend renders against — keep it decoupled from presentation.
package agentflow

import (
	"sort"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// AgentGraph is the top-level payload: every session in the (filtered)
// corpus, each expanded into its agent tree.
type AgentGraph struct {
	Sessions []AgentSession `json:"sessions"`
}

// AgentSession is one session's agent tree: the main agent plus the
// sub-agents it spawned. (Fields grow as tests drive them.)
type AgentSession struct {
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
	// StartedAt/EndedAt span every agent in the session; CostUSD is the sum
	// across the main agent and all its sub-agents.
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	CostUSD   float64   `json:"cost_usd"`
	// ErrorCount is the sum of every agent's ErrorCount in this session —
	// the main agent plus all its sub-agents.
	ErrorCount int         `json:"error_count"`
	Main       *AgentNode  `json:"main"`
	Children   []AgentNode `json:"children"`
	// Prompts segments the main agent's timeline by originating user prompt,
	// in order, so the Conversation lens can interleave "what was asked" with
	// the turns it produced. Each marker points at the first Main.Steps index
	// belonging to that prompt.
	Prompts []PromptMarker `json:"prompts"`
}

// PromptMarker is one user prompt anchored to where its turns begin in the
// main agent's step timeline. Text is redaction-aware (a "[redacted N chars]"
// marker when Options.Redact is set); UUID/Timestamp come from the originating
// parse.UserMessage. An empty UUID marks a run of steps with no resolvable
// originating prompt (orphan turns) — the lens renders no bubble for it.
type PromptMarker struct {
	UUID      string    `json:"uuid"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	// FirstStepIndex is the index into Main.Steps of this prompt's first turn.
	FirstStepIndex int `json:"first_step_index"`
}

// AgentNode is one agent — the main session agent, or a sub-agent. Kind is
// "main" or "subagent". (Fields grow as tests drive them.)
type AgentNode struct {
	Kind string `json:"kind"`
	// AgentType and Description come from the sub-agent's sibling .meta.json
	// (e.g. "Explore", "find callers of Foo"). Empty for the main agent.
	AgentType   string `json:"agent_type"`
	Description string `json:"description"`
	// ParentToolUseID is the id of the Agent tool_use that spawned this
	// sub-agent, read from the sibling .meta.json (toolUseId). It's the exact
	// reverse link to the spawning call, so the tree can nest a sub-agent under
	// the precise step that launched it. Empty for the main agent and for
	// sub-agents whose meta predates the field.
	ParentToolUseID string           `json:"parent_tool_use_id,omitempty"`
	StartedAt       time.Time        `json:"started_at"`
	EndedAt         time.Time        `json:"ended_at"`
	CostUSD         float64          `json:"cost_usd"`
	Tokens          aggregate.Tokens `json:"tokens"`
	// Status is "running" if the agent's last step is within opts.ActiveWindow
	// of the newest turn in the snapshot, else "done". CurrentTool is the last
	// tool the agent invoked, surfaced only while running.
	Status      string `json:"status"`
	CurrentTool string `json:"current_tool"`
	// ErrorCount is the number of this agent's tool calls whose Status is
	// "error" — a heat signal for "where did it go wrong" across the trace.
	ErrorCount int         `json:"error_count"`
	Steps      []AgentStep `json:"steps"`
}

// AgentStep is one assistant turn within an agent's timeline.
type AgentStep struct {
	Timestamp time.Time `json:"timestamp"`
	Model     string    `json:"model"`
	CostUSD   float64   `json:"cost_usd"`
	// Tokens is this turn's usage (counted once for a coalesced message), so the
	// drawer can show per-turn input/output/cache without re-deriving it.
	Tokens aggregate.Tokens `json:"tokens"`
	// DurationMs is the wall-clock gap to the next step within the same agent,
	// in milliseconds. Zero for the last step (no next).
	DurationMs int64                      `json:"duration_ms"`
	Tools      []aggregate.ToolInvocation `json:"tools"`
	// Thinking is the turn's extended-thinking reasoning; Text is its
	// narration. Redacted to a length marker when Options.Redact is set.
	// Omitted from JSON when empty so tool-only turns stay lean.
	Thinking string `json:"thinking,omitempty"`
	Text     string `json:"text,omitempty"`
	// parentUUID is the turn's parentUuid, kept unexported (not serialized) so
	// prompt segmentation can resolve each main step back to its originating
	// user prompt after steps are sorted. Travels with the struct through sort.
	parentUUID string
}

// Options tunes graph construction. Zero values are sensible defaults.
type Options struct {
	// Now is the reference instant for the liveness check (the caller passes
	// wall-clock time). Using real "now" rather than the snapshot's newest turn
	// is load-bearing: an idle or historical corpus must report every agent
	// "done", not leave its last agent stuck "running" forever.
	Now time.Time
	// ActiveWindow marks an agent "running" when its last step falls within
	// this duration of Now. Zero disables liveness — every agent is "done".
	ActiveWindow time.Duration
	// Redact replaces each tool invocation's Input snippet with a
	// length-echoing "[redacted N chars]" marker so a shared report doesn't
	// leak command text or sub-agent prompts. Mirrors aggregate's --redact.
	Redact bool
	// TopN caps the result to the N most-recently-active sessions, mirroring
	// the Sessions tab's --sessions cap so a busy corpus doesn't ship
	// thousands of sessions. Recency (not cost) so a running session is never
	// capped out. 0 means unlimited. Survivors are returned newest-first.
	TopN int
}

// nodeAccum is the in-progress per-agent state during graph construction —
// one per SourceFile. Finalized into an AgentNode by finalizeNode.
type nodeAccum struct {
	sourceFile string
	subagent   bool
	started    time.Time
	ended      time.Time
	cost       float64
	tokens     aggregate.Tokens
	steps      []AgentStep
}

// BuildAgentGraph groups a snapshot's turns into per-session agent trees.
// All turns of a session — main and sub-agents — share the same SessionID
// (a sub-agent's JSONL carries its parent's sessionId); the individual agents
// are distinguished by SourceFile. parse.IsSubagentFile classifies each file.
func BuildAgentGraph(snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter, opts Options) (AgentGraph, error) {
	if snap == nil || len(snap.Turns) == 0 {
		return AgentGraph{}, nil
	}

	type sessionAccum struct {
		sessionID string
		cwd       string
		// nodes keyed by SourceFile — one agent per file.
		nodes map[string]*nodeAccum
	}
	sessions := map[string]*sessionAccum{}

	// Index tool results by tool_use id so each step's tool calls can be
	// joined to their outcome (status + output). Built once over the whole
	// snapshot; ids are unique per session file so cross-file collision is
	// not a concern in practice.
	results := make(map[string]parse.ToolResult, len(snap.ToolResults))
	for _, r := range snap.ToolResults {
		if r.ToolUseID != "" {
			results[r.ToolUseID] = r
		}
	}

	for _, t := range snap.Turns {
		if !aggregate.MatchesFilter(t, f) {
			continue
		}
		s, ok := sessions[t.SessionID]
		if !ok {
			s = &sessionAccum{sessionID: t.SessionID, nodes: map[string]*nodeAccum{}}
			sessions[t.SessionID] = s
		}
		if s.cwd == "" && t.CWD != "" {
			s.cwd = t.CWD
		}
		n, ok := s.nodes[t.SourceFile]
		if !ok {
			n = &nodeAccum{
				sourceFile: t.SourceFile,
				subagent:   parse.IsSubagentFile(t.SourceFile),
				started:    t.Timestamp,
				ended:      t.Timestamp,
			}
			s.nodes[t.SourceFile] = n
		}
		if t.Timestamp.Before(n.started) {
			n.started = t.Timestamp
		}
		if t.Timestamp.After(n.ended) {
			n.ended = t.Timestamp
		}
		cost, _ := prices.Cost(t.Model,
			t.Usage.InputTokens, t.Usage.OutputTokens,
			t.Usage.CacheCreate5mTokens, t.Usage.CacheCreate1hTokens,
			t.Usage.CacheReadTokens)
		n.cost += cost
		addUsage(&n.tokens, t.Usage)
		thinking, text := t.Thinking, t.Text
		if opts.Redact {
			if thinking != "" {
				thinking = aggregate.RedactMarker(thinking)
			}
			if text != "" {
				text = aggregate.RedactMarker(text)
			}
		}
		var stepTokens aggregate.Tokens
		addUsage(&stepTokens, t.Usage)
		n.steps = append(n.steps, AgentStep{
			Timestamp:  t.Timestamp,
			Model:      t.Model,
			CostUSD:    cost,
			Tokens:     stepTokens,
			Tools:      aggregate.DistinctToolInvocations(t.ToolUses, results, opts.Redact),
			Thinking:   thinking,
			Text:       text,
			parentUUID: t.ParentUUID,
		})
	}

	// Resolver for prompt segmentation (the Conversation lens): maps each main
	// step's parentUuid back to the user prompt that originated it. Built once
	// over the whole snapshot; parentUuids are globally unique so cross-session
	// lookups don't collide.
	resolver := aggregate.NewPromptResolver(snap.Turns, snap.Users, snap.Links)

	out := make([]AgentSession, 0, len(sessions))
	for _, s := range sessions {
		as := AgentSession{SessionID: s.sessionID, CWD: s.cwd}
		for _, n := range s.nodes {
			// Roll node spans/cost up to the session.
			if as.StartedAt.IsZero() || n.started.Before(as.StartedAt) {
				as.StartedAt = n.started
			}
			if n.ended.After(as.EndedAt) {
				as.EndedAt = n.ended
			}
			as.CostUSD += n.cost
			node := finalizeNode(n, opts)
			as.ErrorCount += node.ErrorCount
			if node.Kind == "subagent" {
				as.Children = append(as.Children, node)
			} else {
				main := node
				as.Main = &main
			}
		}
		// Children ordered by start time so the swimlane reads left-to-right
		// in spawn order; map iteration above is non-deterministic.
		sort.SliceStable(as.Children, func(i, j int) bool {
			return as.Children[i].StartedAt.Before(as.Children[j].StartedAt)
		})
		attachSpawnRollups(&as)
		as.Prompts = segmentPrompts(as.Main, resolver, opts.Redact)
		out = append(out, as)
	}
	// Cap to the N most-recently-active sessions before the display sort, so
	// a busy corpus (thousands of sessions in the window) ships only what
	// you're likely watching. Recency, not cost: a currently-running session
	// is by definition recent, so — unlike the old cost cap — it's never
	// dropped just because it hasn't spent much yet. EndedAt is the session's
	// last turn, the best "did something lately" signal.
	if opts.TopN > 0 && len(out) > opts.TopN {
		sort.SliceStable(out, func(i, j int) bool {
			if !out[i].EndedAt.Equal(out[j].EndedAt) {
				return out[i].EndedAt.After(out[j].EndedAt)
			}
			return out[i].SessionID < out[j].SessionID
		})
		out = out[:opts.TopN]
	}
	// Deterministic session order: most recently active first, so a live (or
	// the freshest) session leads the view. Tiebreak on SessionID.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].EndedAt.Equal(out[j].EndedAt) {
			return out[i].EndedAt.After(out[j].EndedAt)
		}
		return out[i].SessionID < out[j].SessionID
	})
	return AgentGraph{Sessions: out}, nil
}

// finalizeNode turns an accumulator into a presentable AgentNode: it orders
// the steps chronologically, computes inter-step durations, derives liveness
// against the snapshot's newest turn, and resolves sub-agent meta from disk.
func finalizeNode(n *nodeAccum, opts Options) AgentNode {
	// Order steps chronologically (JSONL is usually already ordered, but a
	// defensive sort is cheap) before computing inter-step gaps.
	sort.SliceStable(n.steps, func(i, j int) bool {
		return n.steps[i].Timestamp.Before(n.steps[j].Timestamp)
	})
	for i := 0; i < len(n.steps)-1; i++ {
		if d := n.steps[i+1].Timestamp.Sub(n.steps[i].Timestamp); d > 0 {
			n.steps[i].DurationMs = d.Milliseconds()
		}
	}
	errorCount := 0
	for _, step := range n.steps {
		for _, tool := range step.Tools {
			if tool.Status == "error" {
				errorCount++
			}
		}
	}
	node := AgentNode{
		Kind:       "main",
		StartedAt:  n.started,
		EndedAt:    n.ended,
		CostUSD:    n.cost,
		Tokens:     n.tokens,
		Status:     "done",
		ErrorCount: errorCount,
		Steps:      n.steps,
	}
	// Liveness: an agent whose last step is within ActiveWindow of Now is
	// still "running"; surface its most recent tool as the current activity.
	if opts.ActiveWindow > 0 && !n.ended.Before(opts.Now.Add(-opts.ActiveWindow)) {
		node.Status = "running"
		node.CurrentTool = lastToolName(n.steps)
	}
	if n.subagent {
		node.Kind = "subagent"
		if meta, ok := parse.ReadSubagentMeta(n.sourceFile); ok {
			node.AgentType = meta.AgentType
			node.Description = meta.Description
			node.ParentToolUseID = meta.ToolUseID
		}
	}
	return node
}

// attachSpawnRollups writes a SpawnRollup onto every Agent tool_use whose id
// matches a sub-agent's ParentToolUseID — the exact reverse link from the
// child's .meta.json. The rollup carries the child's own cumulative totals, so
// each spawning call shows the blast radius of that one decision. It's
// attribution, not double-counting: the same cost is still summed once at the
// session level (AgentSession.CostUSD).
func attachSpawnRollups(as *AgentSession) {
	// Index children by the spawning tool_use id. A sub-agent can itself spawn
	// sub-agents, so any agent's call — main or child — may be a parent.
	childByParentID := make(map[string]*AgentNode, len(as.Children))
	for i := range as.Children {
		if id := as.Children[i].ParentToolUseID; id != "" {
			childByParentID[id] = &as.Children[i]
		}
	}
	if len(childByParentID) == 0 {
		return
	}
	attach := func(n *AgentNode) {
		if n == nil {
			return
		}
		for si := range n.Steps {
			for ti := range n.Steps[si].Tools {
				inv := &n.Steps[si].Tools[ti]
				child, ok := childByParentID[inv.ID]
				if !ok {
					continue
				}
				inv.Spawned = &aggregate.SpawnRollup{
					AgentRef:   inv.ID,
					CostUSD:    child.CostUSD,
					Tokens:     child.Tokens,
					DurationMs: child.EndedAt.Sub(child.StartedAt).Milliseconds(),
					ErrorCount: child.ErrorCount,
				}
			}
		}
	}
	attach(as.Main)
	for i := range as.Children {
		attach(&as.Children[i])
	}
}

// segmentPrompts walks the main agent's (sorted) step timeline and emits one
// PromptMarker per contiguous run of steps sharing an originating user prompt.
// A new marker starts whenever the resolved prompt UUID changes from the
// previous step, so every step belongs to exactly one marker and the markers
// read in conversation order. Text is redacted when redact is set; the empty
// "" UUID (orphan steps with no resolvable prompt) yields a text-less marker.
func segmentPrompts(main *AgentNode, r *aggregate.PromptResolver, redact bool) []PromptMarker {
	if main == nil || len(main.Steps) == 0 {
		return nil
	}
	var markers []PromptMarker
	const sentinel = "\x00" // matches no real UUID, not even ""
	prev := sentinel
	for i := range main.Steps {
		uuid := r.Resolve(main.Steps[i].parentUUID)
		if uuid == prev {
			continue
		}
		prev = uuid
		text := r.Text(uuid)
		if redact && text != "" {
			text = aggregate.RedactMarker(text)
		}
		markers = append(markers, PromptMarker{
			UUID:           uuid,
			Text:           text,
			Timestamp:      r.Timestamp(uuid),
			FirstStepIndex: i,
		})
	}
	return markers
}

// lastToolName returns the name of the last tool invoked in an agent's most
// recent step, or "" if the latest step ran no tools (a text-only turn).
func lastToolName(steps []AgentStep) string {
	for i := len(steps) - 1; i >= 0; i-- {
		if tools := steps[i].Tools; len(tools) > 0 {
			return tools[len(tools)-1].Name
		}
	}
	return ""
}

// addUsage accumulates a turn's usage into a Tokens tuple. Local mirror of
// aggregate's unexported addUsage — same fields, exported here so agentflow
// can reuse the aggregate.Tokens type without aggregate exposing internals.
func addUsage(a *aggregate.Tokens, u parse.Usage) {
	a.InputTokens += int64(u.InputTokens)
	a.OutputTokens += int64(u.OutputTokens)
	a.CacheCreate5mTokens += int64(u.CacheCreate5mTokens)
	a.CacheCreate1hTokens += int64(u.CacheCreate1hTokens)
	a.CacheReadTokens += int64(u.CacheReadTokens)
}
