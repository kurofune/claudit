package serve

import (
	"net/http"
	"time"

	"github.com/kurofune/claudit/internal/agentflow"
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
			// by applyDefaults); without it a busy corpus would ship every
			// session in the window. A non-positive cap means unlimited.
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
