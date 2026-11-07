package serve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	apiPathTokens    = "/_claudit/api/tokens"
	apiPathTools     = "/_claudit/api/tools"
	apiPathSubagents = "/_claudit/api/subagents"
	apiPathTrends    = "/_claudit/api/trends"
	apiPathAnomalies = "/_claudit/api/anomalies"
	apiPathTheme     = "/_claudit/api/theme"
	// apiPathAgentsFull reads one tool call's or turn's whole text back
	// from disk for the story's "show full".
	apiPathAgentsFull = "/_claudit/api/agents/full"
	// apiPathSessions is the Sessions list's index; apiPathSessionsStory
	// pages one session's story.
	apiPathSessions        = "/_claudit/api/sessions"
	apiPathSessionsStory   = "/_claudit/api/sessions/story"
	apiPathSessionsSummary = "/_claudit/api/sessions/summary"
)

// Section labels for the per-section render cache. Trends uses a
// composite "api-trends-<dim>" to keep per-dim payloads from churning
// each other.
const (
	apiSectionOverview  = "api-overview"
	apiSectionCost      = "api-cost"
	apiSectionTokens    = "api-tokens"
	apiSectionTools     = "api-tools"
	apiSectionSubagents = "api-subagents"
	apiSectionAnomalies = "api-anomalies"
	apiSectionTrends    = "api-trends"
	// apiSectionSessions and apiSectionSessionsStory label the Sessions
	// index and story pages; each story page is its own cache entry since
	// session/prompt/offset are part of the canonical query.
	apiSectionSessions        = "api-sessions"
	apiSectionSessionsStory   = "api-sessions-story"
	apiSectionSessionsSummary = "api-sessions-summary"
)

// sectionBuilder receives the shared aggregator and returns a section's
// payload. Returning `any` keeps each builder's static type intact at
// the call site while letting the shared handler marshal uniformly.
type sectionBuilder func(a *aggregate.Aggregator) (any, error)

// apiResponse describes everything the shared handler needs to know
// to serve one section. Built per-request by each handler before
// calling serveAPISection.
type apiSectionSpec struct {
	// section is the cache-key label and the trailing component of
	// the ETag. Must be stable for repeated equivalent requests.
	section string
	// build extracts and marshals the section's payload from the
	// shared aggregator.
	build sectionBuilder
	// buildFromSnapshot, when set, builds the payload straight from the
	// snapshot + query and bypasses sharedAggregateData entirely (no
	// Aggregator). Used by sections like /agents that
	// derive their own structure from raw turns. Takes precedence over build.
	// now is the request's clock reading, the one variant saw.
	buildFromSnapshot func(snap *Snapshot, q Query, now time.Time) (any, error)
	// variant, when set, names what else besides the generation and query
	// the body depends on: wall-clock state such as which sessions are
	// still active. It joins the ETag and the render-cache key, so a body
	// that went stale as time passed is rebuilt and fails revalidation
	// though the snapshot has not moved.
	variant func(snap *Snapshot, q Query, now time.Time) string
	// buildWithPrev, when set, also receives the aggregator over the
	// previous window of equal length (nil for an unbounded window).
	// Takes precedence over build.
	buildWithPrev func(a, prev *aggregate.Aggregator) (any, error)
	// takesCursor marks a section that reads a ?since cursor; any other
	// answers 400 to one.
	takesCursor bool
	// promptFree marks a payload that reads no per-prompt figure
	// (Aggregator.ByPrompt), so its Aggregators skip the PromptIndex.
	promptFree bool
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
	now := s.clock()
	q, err := parseQuery(r.URL.Query(), now)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if q.cursor != "" && !spec.takesCursor {
		http.Error(w, "?since: a cursor applies only to "+apiPathSessions, http.StatusBadRequest)
		return
	}
	s.applyDefaults(&q, now)

	snap := s.cache.Snapshot()
	section := spec.section
	if spec.variant != nil {
		section += "-" + spec.variant(snap, q, now)
	}
	etag := buildAPIEtag(snap.Generation, section, q.rawQuery)

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
	if body, ok := s.lookupCached(q, section, snap.Generation, wantGzip); ok {
		if err := writeCached(w, body, wantGzip); err != nil {
			s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write api response failed",
				slog.Any("err", err),
				slog.String("section", spec.section),
				slog.Bool("cached", true))
		}
		return
	}

	var payload any
	if spec.buildFromSnapshot != nil {
		// Snapshot-derived section (e.g. /agents): skip the Aggregator
		// pass entirely and build straight from raw turns.
		payload, err = spec.buildFromSnapshot(snap, q, now)
	} else {
		aggregateFor := s.sharedAggregateData
		if spec.promptFree {
			aggregateFor = s.promptFreeAggregateData
		}
		// The previous window builds beside the current one: they share
		// nothing but the snapshot.
		type built struct {
			agg *aggregate.Aggregator
			err error
		}
		var prevCh chan built
		if spec.buildWithPrev != nil {
			if pq, ok := previousWindow(q, now); ok {
				prevCh = make(chan built, 1)
				go func() {
					a, err := aggregateFor(r.Context(), snap, pq)
					prevCh <- built{a, err}
				}()
			}
		}
		agg, aerr := aggregateFor(r.Context(), snap, q)
		if aerr != nil {
			if isClientGone(aerr) {
				// Client disconnected mid-aggregation (reload/navigation).
				// Nothing to report to a socket that's already closed, and
				// it isn't our failure — don't log at ERROR or write a 500.
				return
			}
			s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: aggregate failed",
				slog.Any("err", aerr),
				slog.String("section", spec.section))
			http.Error(w, "aggregate failed", http.StatusInternalServerError)
			return
		}
		switch {
		case spec.buildWithPrev != nil:
			var prev *aggregate.Aggregator
			if prevCh != nil {
				b := <-prevCh
				if prev, aerr = b.agg, b.err; aerr != nil {
					if isClientGone(aerr) {
						return
					}
					s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: aggregate failed",
						slog.Any("err", aerr),
						slog.String("section", spec.section),
						slog.Bool("previous_window", true))
					http.Error(w, "aggregate failed", http.StatusInternalServerError)
					return
				}
			}
			payload, err = spec.buildWithPrev(agg, prev)
		default:
			payload, err = spec.build(agg)
		}
	}
	if err != nil {
		// Distinguish "user gave bad input" from "we screwed up":
		// the Trends builder rejects unknown ?dim with an error,
		// which deserves a 400, not a 500.
		if isUserInputError(err) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, errNotFound) {
			w.Header().Del("ETag")
			http.NotFound(w, r)
			return
		}
		if isClientGone(err) {
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
	s.storeCached(q, section, snap.Generation, plain, gz)

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
//
// rawQuery is the canonical query string (sorted k=v&k=v). Mixing it
// into the ETag is load-bearing: two requests at the same snapshot
// generation but with different filters (?last=7d vs ?scope=all)
// produce different bodies, so they MUST get different ETags or a
// browser revalidation would 304 the wrong cached body. We hash the
// query rather than embed it so a long filter doesn't blow the
// header size — 8 hex chars (32 bits) is plenty for cache-key
// disambiguation in a single-binary self-served app.
func buildAPIEtag(generation int64, section, rawQuery string) string {
	sum := sha256.Sum256([]byte(rawQuery))
	qh := hex.EncodeToString(sum[:])[:8]
	return fmt.Sprintf(`W/"gen-%d-%s-q%s"`, generation, section, qh)
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

// errNotFound is returned by section builders when the request names
// something the snapshot does not hold (a story's prompt index past the
// end). The handler answers 404.
var errNotFound = errors.New("not found")

func isUserInputError(err error) bool {
	_, ok := err.(*userInputError)
	return ok
}

// isClientGone reports whether err is the request context being
// canceled or timing out — i.e. the browser disconnected mid-request
// (reload/navigation). That is a client hang-up, not a server fault, so
// handlers swallow it: no ERROR log, no 500 written to a dead socket.
func isClientGone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// API handlers — one per route. Each is a thin wrapper that names
// its cache section and provides the right builder closure.

func (s *Server) handleAPISnapshot(w http.ResponseWriter, r *http.Request) {
	snap := s.cache.Snapshot()
	host := s.cache.HostInfo()
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
		buildWithPrev: func(a, prev *aggregate.Aggregator) (any, error) {
			return render.BuildOverview(a, prev), nil
		},
		promptFree: true,
	})
}

func (s *Server) handleAPICost(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionCost,
		build: func(a *aggregate.Aggregator) (any, error) {
			return render.BuildCost(a), nil
		},
	})
}

func (s *Server) handleAPITokens(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionTokens,
		build: func(a *aggregate.Aggregator) (any, error) {
			return render.BuildTokens(a), nil
		},
	})
}

func (s *Server) handleAPITools(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionTools,
		build: func(a *aggregate.Aggregator) (any, error) {
			return render.BuildTools(a), nil
		},
	})
}

func (s *Server) handleAPISubagents(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionSubagents,
		build: func(a *aggregate.Aggregator) (any, error) {
			return render.BuildSubagents(a), nil
		},
	})
}

func (s *Server) handleAPIAnomalies(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionAnomalies,
		build: func(a *aggregate.Aggregator) (any, error) {
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
		build: func(a *aggregate.Aggregator) (any, error) {
			p, err := render.BuildTrends(a, dim)
			if err != nil {
				return nil, &userInputError{err: err}
			}
			return p, nil
		},
	})
}

// previousWindow returns q narrowed to the window of equal length just
// before it: for [since, until) that is [since − (until − since), since).
// An open until means now. False when q has no since — an unbounded window
// has no equal-length predecessor. The previous query carries no trend
// period: only its hotspot costs are read.
func previousWindow(q Query, now time.Time) (Query, bool) {
	since, until := q.Filter.Since, q.Filter.Until
	if since.IsZero() {
		return Query{}, false
	}
	if until.IsZero() {
		until = now
	}
	if !until.After(since) {
		return Query{}, false
	}
	pq := q
	pq.Filter.Since = since.Add(-until.Sub(since))
	pq.Filter.Until = since
	pq.Period = aggregate.PeriodNone
	pq.TrendFillStart, pq.TrendFillEnd = time.Time{}, time.Time{}
	pq.rawQuery = fmt.Sprintf("%s|prev:%d:%d", q.rawQuery, pq.Filter.Since.UnixNano(), pq.Filter.Until.UnixNano())
	return pq, true
}
