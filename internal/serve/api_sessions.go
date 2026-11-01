package serve

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/agentflow"
	"github.com/kurofune/claudit/internal/aggregate"
)

// storyMemoMax bounds how many sessions' stories stay built at one
// generation: enough for a few open sessions, small enough that a long
// session's steps never pile up.
const storyMemoMax = 8

// handleAPISessions serves /_claudit/api/sessions — the Sessions list:
// one light row per session in the filtered window, with its prompt-family
// key. Built straight from the snapshot like /agents. A row's active flag
// expires with wall-clock time while the snapshot stands still, so the
// variant counts the rows no longer active at the request's now: the body
// and ETag change the moment any row's window runs out.
func (s *Server) handleAPISessions(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionSessions,
		variant: func(snap *Snapshot, q Query, now time.Time) string {
			return "inactive" + strconv.Itoa(s.sessionIndex(snap, q).Inactive(now, agentActiveWindow))
		},
		buildFromSnapshot: func(snap *Snapshot, q Query, now time.Time) (any, error) {
			return s.sessionIndex(snap, q).At(now, agentActiveWindow), nil
		},
	})
}

// sessionIndex returns the session index for q's filter, memoized per
// generation; its Active flags are stale, so read it through At.
func (s *Server) sessionIndex(snap *Snapshot, q Query) agentflow.SessionIndex {
	key := filterQueryKey(q)
	s.filterAggMu.Lock()
	if s.indexMemoGen == snap.Generation {
		if idx, ok := s.indexMemo[key]; ok {
			s.filterAggMu.Unlock()
			return idx
		}
	}
	s.filterAggMu.Unlock()

	// No promptData here: its PromptIndex is the costly half, and the
	// index needs only the replay set, which it builds cheaply.
	idx := agentflow.BuildSessionIndex(snap, s.opts.Prices, q.Filter, agentflow.IndexOptions{})
	s.filterAggMu.Lock()
	if s.indexMemoGen != snap.Generation || len(s.indexMemo) >= storyMemoMax {
		s.indexMemo = map[string]agentflow.SessionIndex{}
		s.indexMemoGen = snap.Generation
	}
	s.indexMemo[key] = idx
	s.filterAggMu.Unlock()
	return idx
}

// handleAPISessionsStory serves /_claudit/api/sessions/story — one page of
// a session's story: ?session=<id> (required), ?prompt=<i> (default 0) and
// ?offset=<n> turns into that prompt (default 0), agentflow.StoryPageTurns
// turns per page. An unknown session, prompt or offset answers 404.
//
// The story is served on the shared-aggregator path so per-turn figures
// from the Aggregator can join its steps by turn uuid; filterAggregate keys
// that Aggregator on the filter alone, and sessionStory memoizes the built
// story, so a session's later pages rebuild neither.
func (s *Server) handleAPISessionsStory(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	session := strings.TrimSpace(v.Get("session"))
	if session == "" {
		http.Error(w, "?session is required", http.StatusBadRequest)
		return
	}
	prompt, err := nonNegativeParam(v.Get("prompt"))
	if err != nil {
		http.Error(w, "?prompt: "+err.Error(), http.StatusBadRequest)
		return
	}
	offset, err := nonNegativeParam(v.Get("offset"))
	if err != nil {
		http.Error(w, "?offset: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := parseQuery(v, time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !agentflow.HasSession(s.cache.Snapshot(), session) {
		http.NotFound(w, r)
		return
	}
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionSessionsStory,
		buildFiltered: func(snap *Snapshot, q Query, _ *aggregate.Aggregator) (any, error) {
			st, ok := s.sessionStory(snap, q, session)
			if !ok {
				return nil, errNotFound
			}
			page, ok := st.Page(prompt, offset, agentflow.StoryPageTurns)
			if !ok {
				return nil, errNotFound
			}
			return page, nil
		},
	})
}

// nonNegativeParam parses an optional non-negative integer query value;
// "" is 0.
func nonNegativeParam(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("must be a non-negative integer, got %q", raw)
	}
	return n, nil
}

// filterQueryKey is the canonical form of what an Aggregator reads from a
// filter-only query: the resolved window, project and provider.
func filterQueryKey(q Query) string {
	return fmt.Sprintf("filter|since=%d|until=%d|project=%s|provider=%s",
		unixNanoOrZero(q.Filter.Since), unixNanoOrZero(q.Filter.Until), q.Filter.ProjectSubstring, q.Filter.Provider)
}

func unixNanoOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// filterAggMemo is the last filter-only Aggregator built.
type filterAggMemo struct {
	generation int64
	key        string
	agg        *aggregate.Aggregator
}

// filterAggregate returns the Aggregator for q's filter alone — no trend
// period, no paging params — memoized for the last (generation, filter),
// so sequential story pages share one build that sharedAggregateData's
// singleflight alone would repeat.
func (s *Server) filterAggregate(ctx context.Context, snap *Snapshot, q Query) (*aggregate.Aggregator, error) {
	key := filterQueryKey(q)
	s.filterAggMu.Lock()
	if m := s.filterAgg; m != nil && m.generation == snap.Generation && m.key == key {
		s.filterAggMu.Unlock()
		return m.agg, nil
	}
	s.filterAggMu.Unlock()

	fq := Query{Filter: q.Filter, Hotspots: q.Hotspots, SessionsTop: q.SessionsTop, rawQuery: key}
	agg, err := s.sharedAggregateData(ctx, snap, fq)
	if err != nil {
		return nil, err
	}
	s.filterAggMu.Lock()
	s.filterAgg = &filterAggMemo{generation: snap.Generation, key: key, agg: agg}
	s.filterAggMu.Unlock()
	return agg, nil
}

// sessionStory returns the session's whole story under q's filter,
// memoized per generation. ok is false when the snapshot holds no turn of
// the session.
func (s *Server) sessionStory(snap *Snapshot, q Query, session string) (*agentflow.SessionStory, bool) {
	key := filterQueryKey(q) + "|session=" + session
	s.filterAggMu.Lock()
	if s.storyMemoGen == snap.Generation {
		if st, ok := s.storyMemo[key]; ok {
			s.filterAggMu.Unlock()
			return st, true
		}
	}
	s.filterAggMu.Unlock()

	idx, replays := s.promptData(snap)
	st, ok := agentflow.BuildSessionStory(snap, s.opts.Prices, q.Filter, session, agentflow.StoryOptions{
		SnippetRunes: agentSnippetRunes,
		Replays:      &replays,
		PromptIndex:  idx,
	})
	if !ok {
		return nil, false
	}
	s.filterAggMu.Lock()
	if s.storyMemoGen != snap.Generation || len(s.storyMemo) >= storyMemoMax {
		s.storyMemo = map[string]*agentflow.SessionStory{}
		s.storyMemoGen = snap.Generation
	}
	s.storyMemo[key] = st
	s.filterAggMu.Unlock()
	return st, true
}
