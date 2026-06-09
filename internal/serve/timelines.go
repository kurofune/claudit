package serve

import (
	"context"

	"github.com/kurofune/claudit/internal/aggregate"
)

// buildTimelines runs the per-session timeline pipeline used by the
// sessions API list and the per-session timeline endpoint. q.SessionsTop
// caps the number of sessions returned; 0 means "no cap" — return every
// session in the time window (the default, since the view paginates
// client-side). A negative value never reaches here in normal flow
// (applyDefaults resolves the -1 unset sentinel first) but is treated
// defensively as "skip the pass entirely".
func (s *Server) buildTimelines(ctx context.Context, snap *Snapshot, q Query) ([]aggregate.SessionTimeline, error) {
	if q.SessionsTop < 0 {
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
