package serve

import (
	"context"

	"github.com/kurofune/claudit/internal/aggregate"
)

// buildTimelines runs the per-session timeline pipeline used by the
// sessions API list and the per-session timeline endpoint. q.SessionsTop
// caps the number of sessions returned; <=0 short-circuits to nil so the
// expensive pass is skipped entirely for API endpoints that don't need
// timelines (every API section but /sessions and /sessions/{id}/timeline).
func (s *Server) buildTimelines(ctx context.Context, snap *Snapshot, q Query) ([]aggregate.SessionTimeline, error) {
	if q.SessionsTop <= 0 {
		return nil, nil
	}
	return aggregate.BuildSessionTimelines(
		ctx, snap.Turns, snap.Users, snap.Links, s.opts.Prices, q.Filter,
		aggregate.SessionTimelinesOptions{
			TopN:           q.SessionsTop,
			Redact:         q.Redact,
			MaxPromptChars: 2000,
		},
	)
}
