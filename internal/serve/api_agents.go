package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/kurofune/claudit/internal/agentflow"
	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
)

// agentActiveWindow is how recently an agent must have acted (relative to
// wall-clock now) to be reported "running". Sized comfortably above the
// serve poll interval so a live agent doesn't flicker to "done" between polls.
const agentActiveWindow = 60 * time.Second

// handleAPIAgents serves /_claudit/api/agents — the agent-observability tab's
// data. Reconstructs the per-session agent tree (main + sub-agents) and each
// agent's tool-call timeline straight from the snapshot. Reuses the shared
// section machinery (ETag, cache, gzip, 304) via buildFromSnapshot, which
// bypasses the Aggregator/timeline pass the other sections need.
func (s *Server) handleAPIAgents(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionAgents,
		buildFromSnapshot: func(snap *Snapshot, q Query) (any, error) {
			// TopN honors the same --sessions cap the Sessions tab uses (set
			// by applyDefaults). It defaults to 0 (no cap — every session in
			// the window, both views recency-sorted); a positive --sessions
			// bounds both to the N most-recently-active sessions. A negative
			// value (unset sentinel) is normalized to 0.
			topN := q.SessionsTop
			if topN < 0 {
				topN = 0
			}
			return agentflow.BuildAgentGraph(snap, s.opts.Prices, q.Filter, agentflow.Options{
				Now:          time.Now(),
				ActiveWindow: agentActiveWindow,
				Redact:       q.Redact,
				TopN:         topN,
			})
		},
	})
}

// handleAPIAgentsFull serves /_claudit/api/agents/full?session=&tool= — the
// drawer's "show full" action. Returns the UNTRUNCATED input/output for one
// tool_use, read back from the session JSONL on disk (the default /agents
// payload caps both to 2000-rune snippets to stay lean).
//
// Serve-mode only: the static HTML report inlines no disk and never calls
// this — its drawer falls back to the snippet. The source file is resolved
// from the trusted snapshot (matched by session + tool_use id), never from a
// user-supplied path, so there is no path-traversal surface.
func (s *Server) handleAPIAgentsFull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	v := r.URL.Query()
	session := v.Get("session")
	toolID := v.Get("tool")
	if session == "" || toolID == "" {
		http.Error(w, "missing session or tool", http.StatusBadRequest)
		return
	}
	// Parse the query for the redact flag (and to 400 on a malformed filter,
	// matching the sibling endpoints), even though session/tool drive the lookup.
	q, err := parseQuery(v, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	snap := s.cache.Snapshot()
	if snap == nil {
		http.NotFound(w, r)
		return
	}

	// Resolve the on-disk source file from the snapshot by matching the
	// tool_use id within the named session. Trusted input only.
	sourceFile := ""
	for i := range snap.Turns {
		t := &snap.Turns[i]
		if t.SessionID != session {
			continue
		}
		for _, u := range t.ToolUses {
			if u.ID == toolID {
				sourceFile = t.SourceFile
				break
			}
		}
		if sourceFile != "" {
			break
		}
	}
	if sourceFile == "" {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(sourceFile)
	if err != nil {
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	detail, ok := parse.FindToolUseDetail(f, toolID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if q.Redact {
		if detail.Input != "" {
			detail.Input = aggregate.RedactMarker(detail.Input)
		}
		if detail.Output != "" {
			detail.Output = aggregate.RedactMarker(detail.Output)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := json.NewEncoder(w).Encode(detail); err != nil {
		s.reqLogger(r.Context()).Error("serve: encode agents/full failed", "err", err)
	}
}
