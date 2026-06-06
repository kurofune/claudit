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
	StartedAt time.Time   `json:"started_at"`
	EndedAt   time.Time   `json:"ended_at"`
	CostUSD   float64     `json:"cost_usd"`
	Main      *AgentNode  `json:"main"`
	Children  []AgentNode `json:"children"`
}

// AgentNode is one agent — the main session agent, or a sub-agent. Kind is
// "main" or "subagent". (Fields grow as tests drive them.)
type AgentNode struct {
	Kind string `json:"kind"`
	// AgentType and Description come from the sub-agent's sibling .meta.json
	// (e.g. "Explore", "find callers of Foo"). Empty for the main agent.
	AgentType   string           `json:"agent_type"`
	Description string           `json:"description"`
	StartedAt   time.Time        `json:"started_at"`
	EndedAt     time.Time        `json:"ended_at"`
	CostUSD     float64          `json:"cost_usd"`
	Tokens      aggregate.Tokens `json:"tokens"`
	// Status is "running" if the agent's last step is within opts.ActiveWindow
	// of the newest turn in the snapshot, else "done". CurrentTool is the last
	// tool the agent invoked, surfaced only while running.
	Status      string      `json:"status"`
	CurrentTool string      `json:"current_tool"`
	Steps       []AgentStep `json:"steps"`
}

// AgentStep is one assistant turn within an agent's timeline.
type AgentStep struct {
	Timestamp time.Time `json:"timestamp"`
	Model     string    `json:"model"`
	CostUSD   float64   `json:"cost_usd"`
	// DurationMs is the wall-clock gap to the next step within the same agent,
	// in milliseconds. Zero for the last step (no next).
	DurationMs int64                      `json:"duration_ms"`
	Tools      []aggregate.ToolInvocation `json:"tools"`
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
	// TopN caps the result to the N costliest sessions (the meatiest agent
	// trees), mirroring the Sessions tab's --sessions cap so a busy corpus
	// doesn't ship thousands of sessions. 0 means unlimited. The surviving
	// sessions are still returned in chronological order.
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
		n.steps = append(n.steps, AgentStep{
			Timestamp: t.Timestamp,
			Model:     t.Model,
			CostUSD:   cost,
			Tools:     aggregate.DistinctToolInvocations(t.ToolUses, opts.Redact),
		})
	}

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
		out = append(out, as)
	}
	// Cap to the N costliest sessions before the display sort, so a busy
	// corpus (thousands of sessions in the window) ships only the meatiest
	// trees — mirrors the Sessions tab's --sessions cap. The survivors are
	// still presented chronologically below.
	if opts.TopN > 0 && len(out) > opts.TopN {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].CostUSD != out[j].CostUSD {
				return out[i].CostUSD > out[j].CostUSD
			}
			return out[i].SessionID < out[j].SessionID
		})
		out = out[:opts.TopN]
	}
	// Deterministic session order: earliest first, tiebreak on SessionID.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
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
	node := AgentNode{
		Kind:      "main",
		StartedAt: n.started,
		EndedAt:   n.ended,
		CostUSD:   n.cost,
		Tokens:    n.tokens,
		Status:    "done",
		Steps:     n.steps,
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
		}
	}
	return node
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
