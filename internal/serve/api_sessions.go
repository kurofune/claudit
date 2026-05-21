package serve

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/render"
)

// handleAPISessions serves /_claudit/api/sessions — the lightweight
// session-list endpoint. Returns per-session totals only; the full
// per-prompt timeline lands on /_claudit/api/sessions/{id}/timeline
// so the SPA can lazy-fetch one session at a time on user click.
//
// Reuses the shared section-handler machinery for ETag, caching, and
// gzip. The build callback projects the (already-built) timelines
// down to summaries — the timeline pass is the expensive step and
// the singleflight inside sharedAggregateData ensures the cost is
// shared with any concurrent /api/sessions/{id}/timeline request.
func (s *Server) handleAPISessions(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionSessions,
		build: func(_ *aggregate.Aggregator, timelines []aggregate.SessionTimeline) (any, error) {
			return render.BuildSessions(timelines), nil
		},
		needsTimelines: true,
	})
}

// handleAPISessionsTree dispatches /_claudit/api/sessions/{id}/...
// requests. The only supported subpath today is .../timeline; every
// other shape 404s. Lifted into its own handler because
// http.ServeMux doesn't pattern-match path segments — the route
// table forwards anything under /api/sessions/ here.
func (s *Server) handleAPISessionsTree(w http.ResponseWriter, r *http.Request) {
	// Strip the /api/sessions/ prefix to get "<id>/<rest>".
	rest := strings.TrimPrefix(r.URL.Path, apiPathSessions+"/")
	if rest == "" || rest == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	idx := strings.Index(rest, "/")
	if idx < 0 {
		// /api/sessions/{id} (no subpath): not a defined endpoint yet.
		// Could surface a single-session summary, but the SPA only
		// needs the list endpoint plus the timeline endpoint, so 404
		// rather than invent a third shape.
		http.NotFound(w, r)
		return
	}
	id := rest[:idx]
	sub := rest[idx:]
	switch sub {
	case sessionTimelineSuffix:
		s.handleAPISessionTimeline(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

// handleAPISessionTimeline serves /_claudit/api/sessions/{id}/timeline.
// Returns one session's full PromptTimeline + TurnSummary tree.
//
// ETag derives from the max mtime of the source files that
// contributed turns to this session — per the plan, finer-grain
// invalidation than the snapshot generation. A turn appended to a
// different session leaves this session's ETag unchanged and the
// browser's cached copy stays valid.
//
// Filter parameters (project/since/until/redact) still apply: the
// SPA passes the page's current filter when fetching, and a session
// with no turns matching the filter returns 404 — matching what the
// static report would have rendered.
func (s *Server) handleAPISessionTimeline(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}

	q, err := parseQuery(r.URL.Query(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = s.applyDefaults(&q)

	snap := s.cache.Snapshot()

	// ETag from the session's contributing files. Falls back to the
	// snapshot generation if no source files can be located — same
	// staleness behavior as the other endpoints.
	etag := sessionTimelineEtag(snap, sessionID)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("ETag", etag)

	if match := r.Header.Get("If-None-Match"); match != "" && ifNoneMatchHit(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Build directly (no shared cache): per-session timelines are
	// keyed by sessionID, which the (canonical_query, section,
	// generation) cache doesn't include. Folding sessionID into the
	// section label is the right shape, and we do it here so each
	// session has its own cache slot rather than churning a single
	// "api-session-timeline" slot across N sessions.
	section := apiSectionSessionTimelinePrefix + sessionID
	wantGzip := acceptsGzip(r)
	if body, ok := s.lookupCached(q, section, snap.Generation, wantGzip); ok {
		if err := writeCached(w, body, wantGzip); err != nil {
			s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write timeline response failed",
				slog.Any("err", err),
				slog.String("session", sessionID),
				slog.Bool("cached", true))
		}
		return
	}

	tl, err := aggregate.BuildSessionTimeline(
		r.Context(), sessionID,
		snap.Turns, snap.Users, snap.Links,
		s.opts.Prices, q.Filter,
		aggregate.SessionTimelinesOptions{
			Redact:         q.Redact,
			MaxPromptChars: 2000,
		},
	)
	if err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: build session timeline failed",
			slog.Any("err", err),
			slog.String("session", sessionID))
		http.Error(w, "build failed", http.StatusInternalServerError)
		return
	}
	if tl == nil {
		http.NotFound(w, r)
		return
	}

	plain, err := json.Marshal(tl)
	if err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: marshal timeline failed",
			slog.Any("err", err),
			slog.String("session", sessionID))
		http.Error(w, "marshal failed", http.StatusInternalServerError)
		return
	}

	var gz []byte
	if wantGzip {
		gz = gzipBytes(plain)
	}
	s.storeCached(q, section, snap.Generation, plain, gz)

	body := plain
	if wantGzip {
		body = gz
	}
	if err := writeCached(w, body, wantGzip); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write timeline response failed",
			slog.Any("err", err),
			slog.String("session", sessionID),
			slog.Bool("cached", false))
	}
}

// apiSectionSessionTimelinePrefix is the section-key prefix used for
// the per-session timeline cache. The actual section becomes
// "api-session-timeline-{id}" so each session has its own slot.
const apiSectionSessionTimelinePrefix = "api-session-timeline-"

// sessionTimelineEtag computes a weak ETag for the session's
// timeline based on the max mtime of the source files that
// contributed turns to this session. Falls back to the snapshot
// generation when no source files match — same staleness model as
// the other endpoints, no false cache hits.
//
// We use mtime-nanos so files modified within the same second don't
// collide. A weak ETag (W/...) signals semantic equivalence — the
// gzipped and plain variants share an ETag.
func sessionTimelineEtag(snap *Snapshot, sessionID string) string {
	var maxNanos int64
	files := map[string]struct{}{}
	for _, t := range snap.Turns {
		if t.SessionID != sessionID || t.SourceFile == "" {
			continue
		}
		files[t.SourceFile] = struct{}{}
	}
	for f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		// UnixNano is monotonic within a single host, which is
		// exactly what we need for ETag freshness.
		if n := info.ModTime().UnixNano(); n > maxNanos {
			maxNanos = n
		}
	}
	if maxNanos == 0 {
		// No contributing files found (e.g. session was deleted but a
		// browser cached the URL). Fall back to generation so the
		// ETag still advances with the snapshot.
		return fmt.Sprintf(`W/"sess-%s-gen-%d"`, sanitizeSessionIDForETag(sessionID), snap.Generation)
	}
	return fmt.Sprintf(`W/"sess-%s-mt-%d"`, sanitizeSessionIDForETag(sessionID), maxNanos)
}

// sanitizeSessionIDForETag strips characters that aren't safe inside
// an HTTP header value. Session IDs are UUIDs in practice — only
// hex + dashes — but the function makes the no-bad-bytes invariant
// explicit so a future ID scheme can't smuggle CR/LF/quote into the
// ETag header.
func sanitizeSessionIDForETag(id string) string {
	// Trim to the basename of the path to defend against bogus input
	// (the dispatcher already extracted the segment, but defense in
	// depth is cheap).
	id = path.Base(id)
	var b strings.Builder
	b.Grow(len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= '0' && c <= '9',
			c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c == '-', c == '_':
			b.WriteByte(c)
		}
	}
	return b.String()
}
