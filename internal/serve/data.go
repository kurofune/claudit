package serve

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/render"
)

// dataPath is the URL the served HTML fetches asynchronously for its
// data island. Constant so the render-side preamble and the handler
// route stay in lockstep — change here, change in routes().
const dataPath = "/_claudit/data.json"

// buildTimelines runs the session-timeline pipeline the HTML report
// also uses. The data endpoint needs them so promptKeysFromTimelines
// can populate prompt_keys — without it, every "view session →"
// cross-link in the served page is silently disabled.
//
// Mirrors the timeline-building block in renderHTML; if you change
// the options here, change them there.
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

// handleData serves the JSON payload that the served-mode HTML fetches
// asynchronously. Mirrors handleReport's query parsing, defaults, and
// snapshot binding so a filter like ?project=foo narrows the JSON the
// same way it narrows the HTML.
//
// Caching: keyed on (canonical query, snapshot generation). Skipped on
// repeat requests within the same generation, which is the common case
// while the browser is awaiting the initial fetch right after page load.
func (s *Server) handleData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q, err := parseQuery(r.URL.Query(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Apply the same server defaults the HTML report does; the JSON
	// payload must match the chrome's expected scope.
	_ = s.applyDefaults(&q)

	snap := s.cache.Snapshot()
	wantGzip := acceptsGzip(r)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Accept-Encoding")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Render-cache lookup: same canonical query + same generation
	// → return the bytes computed last time. The JSON cache shares
	// the (q.rawQuery, gen) key shape with the HTML cache but lives
	// in its own LRU so HTML evictions don't churn it (and vice
	// versa).
	if body, ok := s.lookupCachedJSON(q, snap.Generation, wantGzip); ok {
		if err := writeCached(w, body, wantGzip); err != nil {
			s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write data response failed",
				slog.Any("err", err),
				slog.String("path", dataPath),
				slog.Bool("cached", true))
		}
		return
	}

	agg := s.buildAggregator(snap, q)
	timelines, err := s.buildTimelines(r.Context(), snap, q)
	if err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: build timelines failed",
			slog.Any("err", err),
			slog.String("path", dataPath))
		http.Error(w, "data build failed", http.StatusInternalServerError)
		return
	}
	plain, err := render.BuildPayload(r.Context(), agg, render.HTMLOptions{
		SessionTimelines: timelines,
	})
	if err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: build payload failed",
			slog.Any("err", err),
			slog.String("path", dataPath))
		http.Error(w, "data build failed", http.StatusInternalServerError)
		return
	}

	var gz []byte
	if wantGzip {
		gz = gzipBytes(plain)
	}
	s.storeCachedJSON(q, snap.Generation, plain, gz)

	body := plain
	if wantGzip {
		body = gz
	}
	if err := writeCached(w, body, wantGzip); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write data response failed",
			slog.Any("err", err),
			slog.String("path", dataPath),
			slog.Bool("cached", false))
	}
}
