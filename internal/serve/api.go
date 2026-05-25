package serve

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/render"
)

// API URL paths. Constants so the route registration in routes()
// and the per-section handler bodies can't drift.
const (
	apiPathSnapshot  = "/_claudit/api/snapshot"
	apiPathOverview  = "/_claudit/api/overview"
	apiPathCost      = "/_claudit/api/cost"
	apiPathCache     = "/_claudit/api/cache"
	apiPathTokens    = "/_claudit/api/tokens"
	apiPathTools     = "/_claudit/api/tools"
	apiPathSubagents = "/_claudit/api/subagents"
	apiPathSessions  = "/_claudit/api/sessions"
	apiPathTrends    = "/_claudit/api/trends"
	apiPathAnomalies = "/_claudit/api/anomalies"
	apiPathTheme     = "/_claudit/api/theme"

	// sessionTimelineSuffix lives on a /api/sessions/{id}/timeline path.
	// The dispatcher in handleAPISessionsTree splits the path and matches
	// on this suffix.
	sessionTimelineSuffix = "/timeline"
)

// Section labels for the per-section render cache. Trends uses a
// composite "api-trends-<dim>" to keep per-dim payloads from churning
// each other.
const (
	apiSectionOverview  = "api-overview"
	apiSectionCost      = "api-cost"
	apiSectionCache     = "api-cache"
	apiSectionTokens    = "api-tokens"
	apiSectionTools     = "api-tools"
	apiSectionSubagents = "api-subagents"
	apiSectionSessions  = "api-sessions"
	apiSectionAnomalies = "api-anomalies"
	apiSectionTrends    = "api-trends"
)

// sectionBuilder receives the aggregator and the lazily-built session
// timelines and returns a section's payload. Returning `any` keeps
// each builder's static type intact at the call site while letting
// the shared handler marshal uniformly.
//
// timelines are nil for handlers that don't need them — the shared
// handler avoids the timeline pass entirely when no builder asks for
// them, since timeline construction is the most expensive step in
// sharedAggregateData and the per-section caches already short-
// circuit repeat work.
type sectionBuilder func(a *aggregate.Aggregator, timelines []aggregate.SessionTimeline) (any, error)

// apiResponse describes everything the shared handler needs to know
// to serve one section. Built per-request by each handler before
// calling serveAPISection.
type apiSectionSpec struct {
	// section is the cache-key label and the trailing component of
	// the ETag. Must be stable for repeated equivalent requests.
	section string
	// build extracts and marshals the section's payload from the
	// shared aggregator (and timelines, when needed).
	build sectionBuilder
	// needsTimelines flips on the BuildSessionTimelines pass inside
	// sharedAggregateData. Skipping it for sections that don't use
	// timelines (Cost, Cache, Tools, Subagents, Anomalies, Trends,
	// Overview) shaves the multi-MB walk off the cold path.
	needsTimelines bool
}

// serveAPISection is the shared body of every /_claudit/api/*
// section endpoint. The dance:
//  1. parse + apply server defaults to the URL query
//  2. snapshot the cache and compute ETag = W/"gen-{generation}-{section}"
//  3. 304 on If-None-Match match (no body, ETag still set)
//  4. cache-lookup (canonical_query, section, generation); on hit, write
//  5. on miss, run sharedAggregateData (singleflight'd), build the
//     section, marshal, cache, gzip if asked, write
//
// Headers per the plan: Cache-Control: no-cache, must-revalidate so
// the browser revalidates with If-None-Match on every navigation but
// can short-circuit the heavy work whenever the snapshot hasn't moved.
func (s *Server) serveAPISection(w http.ResponseWriter, r *http.Request, spec apiSectionSpec) {
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
	s.applyDefaults(&q)

	snap := s.cache.Snapshot()
	etag := buildAPIEtag(snap.Generation, spec.section)

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

	wantGzip := acceptsGzip(r)
	if body, ok := s.lookupCached(q, spec.section, snap.Generation, wantGzip); ok {
		if err := writeCached(w, body, wantGzip); err != nil {
			s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write api response failed",
				slog.Any("err", err),
				slog.String("section", spec.section),
				slog.Bool("cached", true))
		}
		return
	}

	agg, timelines, err := s.sharedAggregateData(r.Context(), snap, q)
	if err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: aggregate failed",
			slog.Any("err", err),
			slog.String("section", spec.section))
		http.Error(w, "aggregate failed", http.StatusInternalServerError)
		return
	}
	if !spec.needsTimelines {
		timelines = nil
	}

	payload, err := spec.build(agg, timelines)
	if err != nil {
		// Distinguish "user gave bad input" from "we screwed up":
		// the Trends builder rejects unknown ?dim with an error,
		// which deserves a 400, not a 500.
		if isUserInputError(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: section build failed",
			slog.Any("err", err),
			slog.String("section", spec.section))
		http.Error(w, "build failed", http.StatusInternalServerError)
		return
	}

	plain, err := json.Marshal(payload)
	if err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: marshal failed",
			slog.Any("err", err),
			slog.String("section", spec.section))
		http.Error(w, "marshal failed", http.StatusInternalServerError)
		return
	}

	var gz []byte
	if wantGzip {
		gz = gzipBytes(plain)
	}
	s.storeCached(q, spec.section, snap.Generation, plain, gz)

	body := plain
	if wantGzip {
		body = gz
	}
	if err := writeCached(w, body, wantGzip); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write api response failed",
			slog.Any("err", err),
			slog.String("section", spec.section),
			slog.Bool("cached", false))
	}
}

// buildAPIEtag returns the weak ETag for a section's response. The
// "W/" prefix signals semantic equivalence — the gzipped and plain
// variants are the same payload, so they share an ETag.
func buildAPIEtag(generation int64, section string) string {
	return fmt.Sprintf(`W/"gen-%d-%s"`, generation, section)
}

// ifNoneMatchHit reports whether the client's If-None-Match header
// matches the server's current ETag. RFC 7232 allows a comma-
// separated list, and the leading "W/" weak marker is significant
// for the comparison (we use weak ETags so any prefix variant should
// match). Doing the parse here rather than delegating to net/http
// keeps the predicate independent of the writer state.
func ifNoneMatchHit(header, want string) bool {
	for _, raw := range strings.Split(header, ",") {
		tok := strings.TrimSpace(raw)
		if tok == "*" {
			return true
		}
		// Strip "W/" so weak markers don't block a match. RFC 7232
		// §2.3.2 calls this "weak comparison" — appropriate when
		// the entity tag is itself weak.
		tok = strings.TrimPrefix(tok, "W/")
		w := strings.TrimPrefix(want, "W/")
		if tok == w {
			return true
		}
	}
	return false
}

// userInputError is returned by section builders when the caller's
// request was syntactically valid but semantically wrong (e.g. an
// unknown ?dim). The handler unwraps it to surface a 400 instead of
// a 500.
type userInputError struct{ err error }

func (e *userInputError) Error() string { return e.err.Error() }
func (e *userInputError) Unwrap() error { return e.err }

func isUserInputError(err error) bool {
	_, ok := err.(*userInputError)
	return ok
}

// API handlers — one per route. Each is a thin wrapper that names
// its cache section and provides the right builder closure. The
// repetition is intentional: a table-driven dispatcher would hide
// the per-section toggle (needsTimelines) and require a switch
// somewhere else to keep the closure types straight.

func (s *Server) handleAPISnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.cache.Snapshot()
	host := s.cache.hostInfo()
	body := struct {
		Generation  int64    `json:"generation"`
		LastUpdated string   `json:"last_updated"`
		FileCount   int      `json:"file_count"`
		TurnCount   int      `json:"turn_count"`
		Malformed   int      `json:"malformed"`
		Host        hostInfo `json:"host"`
	}{
		Generation:  snap.Generation,
		LastUpdated: snap.LastUpdate.UTC().Format(time.RFC3339),
		FileCount:   snap.FileCount,
		TurnCount:   len(snap.Turns),
		Malformed:   snap.Malformed,
		Host:        host,
	}
	w.Header().Set("Content-Type", "application/json")
	// Per plan: snapshot is no-store. It reflects a live counter the
	// SPA polls (or that SSE pushes) and is the wrong thing to cache.
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := writeJSON(w, body); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write snapshot response failed",
			slog.Any("err", err),
			slog.String("path", apiPathSnapshot))
	}
}

func (s *Server) handleAPIOverview(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionOverview,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			return render.BuildOverview(a), nil
		},
	})
}

func (s *Server) handleAPICost(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionCost,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			return render.BuildCost(a), nil
		},
	})
}

func (s *Server) handleAPICache(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionCache,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			return render.BuildCache(a), nil
		},
	})
}

func (s *Server) handleAPITokens(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionTokens,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			return render.BuildTokens(a), nil
		},
	})
}

func (s *Server) handleAPITools(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionTools,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			return render.BuildTools(a), nil
		},
	})
}

func (s *Server) handleAPISubagents(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionSubagents,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			return render.BuildSubagents(a), nil
		},
	})
}

func (s *Server) handleAPIAnomalies(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionAnomalies,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			return render.BuildAnomalies(a), nil
		},
	})
}

func (s *Server) handleAPITrends(w http.ResponseWriter, r *http.Request) {
	// dim is part of the cache section so per-dim payloads don't
	// churn each other. Pre-validating here lets a malformed ?dim
	// short-circuit before any aggregation runs.
	dim := strings.TrimSpace(r.URL.Query().Get("dim"))
	if dim == "" {
		http.Error(w, "?dim is required (one of model|project|session|tool|subagent)", http.StatusBadRequest)
		return
	}
	section := apiSectionTrends + "-" + dim
	s.serveAPISection(w, r, apiSectionSpec{
		section: section,
		build: func(a *aggregate.Aggregator, _ []aggregate.SessionTimeline) (any, error) {
			p, err := render.BuildTrends(a, dim)
			if err != nil {
				return nil, &userInputError{err: err}
			}
			return p, nil
		},
	})
}
