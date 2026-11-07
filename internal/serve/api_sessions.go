package serve

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/agentflow"
)

// storyMemoMax bounds how many sessions' stories stay built: enough for a
// few open sessions, small enough that a long session's steps never pile
// up. It also bounds how many filters keep their index rows.
const storyMemoMax = 8

// handleAPISessions serves /_claudit/api/sessions — the Sessions list:
// one light row per session in the filtered window, with its prompt-family
// key, and a cursor naming this answer. Built straight from the snapshot
// like /agents. A row's active flag expires with wall-clock time while the
// snapshot stands still, so the variant counts the rows no longer active
// at the request's now: the body and ETag change the moment any row's
// window runs out.
//
// ?since=<cursor> asks only for what changed since the answer that cursor
// named: {cursor, changed, removed}, changed holding every row rebuilt or
// whose active flag flipped since, removed the ids no longer listed. A
// cursor the server cannot answer from — another filter's, or older than
// the last indexHistoryMax builds — gets the full {cursor, sessions}.
func (s *Server) handleAPISessions(w http.ResponseWriter, r *http.Request) {
	s.serveAPISection(w, r, apiSectionSpec{
		section:     apiSectionSessions,
		takesCursor: true,
		variant: func(snap *Snapshot, q Query, now time.Time) string {
			return "inactive" + strconv.Itoa(s.sessionIndex(snap, q).Inactive(now, agentActiveWindow))
		},
		buildFromSnapshot: func(snap *Snapshot, q Query, now time.Time) (any, error) {
			idx := s.sessionIndex(snap, q)
			key := filterQueryKey(q)
			cur := sessionCursor{gen: snap.Generation, atMs: now.UnixMilli(), filter: filterHash(key)}.String()
			if d, ok := s.sessionDelta(snap, key, idx, q.cursor, now); ok {
				d.Cursor = cur
				return d, nil
			}
			return sessionIndexBody{Cursor: cur, Sessions: idx.At(now, agentActiveWindow).Sessions}, nil
		},
	})
}

// sessionIndexBody is the full /sessions answer.
type sessionIndexBody struct {
	Cursor   string                      `json:"cursor"`
	Sessions []agentflow.SessionIndexRow `json:"sessions"`
}

// sessionIndexDelta is the ?since answer: the rows to replace or add and
// the ids to drop, by session_id.
type sessionIndexDelta struct {
	Cursor  string                      `json:"cursor"`
	Changed []agentflow.SessionIndexRow `json:"changed"`
	Removed []string                    `json:"removed"`
}

// sessionDelta answers raw, a ?since cursor, against idx, the index of
// the filter key at snap; ok is false when raw names no build the history
// still holds, and the caller answers the full index. A row is changed
// when it was rebuilt after the cursor's build or its active flag at now
// differs from the one at the cursor's time, which every row the cursor's
// answer carried was read at.
func (s *Server) sessionDelta(snap *Snapshot, key string, idx agentflow.SessionIndex, raw string, now time.Time) (sessionIndexDelta, bool) {
	c, ok := parseSessionCursor(raw)
	if !ok || c.filter != filterHash(key) {
		return sessionIndexDelta{}, false
	}
	s.filterAggMu.Lock()
	built, gone, ok := s.indexHist[key].since(c.gen, snap.Generation)
	s.filterAggMu.Unlock()
	if !ok {
		return sessionIndexDelta{}, false
	}
	then := time.UnixMilli(c.atMs)
	d := sessionIndexDelta{Changed: []agentflow.SessionIndexRow{}, Removed: []string{}}
	listed := make(map[string]bool, len(idx.Sessions))
	for _, r := range idx.Sessions {
		listed[r.SessionID] = true
		row := r.At(now, agentActiveWindow)
		if built[r.SessionID] || r.At(then, agentActiveWindow).Active != row.Active {
			d.Changed = append(d.Changed, row)
		}
	}
	for id := range gone {
		if !listed[id] {
			d.Removed = append(d.Removed, id)
		}
	}
	sort.Strings(d.Removed)
	return d, true
}

// indexHistoryMax bounds how many builds of one filter's index a ?since
// cursor can name: about a minute of generations while logs write.
const indexHistoryMax = 32

// indexHistory is one filter's last index rows and its last builds, oldest
// first, each step relative to the one before it.
type indexHistory struct {
	rows  *agentflow.IndexRows
	steps []indexStep
}

// indexStep is one index build: its generation, the sessions whose rows it
// built and those whose rows it dropped against the build before.
type indexStep struct {
	gen     int64
	built   []string
	removed []string
}

// since folds the steps after the build at from up to the one at to: the
// sessions rebuilt and those dropped along the way. ok is false when h
// holds no build at from or its last build is not at to.
func (h *indexHistory) since(from, to int64) (built, removed map[string]bool, ok bool) {
	if h == nil || len(h.steps) == 0 || h.steps[len(h.steps)-1].gen != to {
		return nil, nil, false
	}
	i := len(h.steps) - 1
	for i >= 0 && h.steps[i].gen != from {
		i--
	}
	if i < 0 {
		return nil, nil, false
	}
	built, removed = map[string]bool{}, map[string]bool{}
	for _, st := range h.steps[i+1:] {
		for _, id := range st.built {
			built[id] = true
		}
		for _, id := range st.removed {
			removed[id] = true
		}
	}
	return built, removed, true
}

// recordIndexBuild files the build of key's index at gen, made from prev,
// in its history. A build landing after one at gen or later is dropped;
// one made from rows other than the history's last starts the history
// over, since its step would not be relative to the last one. Called with
// filterAggMu held.
func (s *Server) recordIndexBuild(key string, gen int64, prev, rows *agentflow.IndexRows, built []string) {
	h := s.indexHist[key]
	switch {
	case h != nil && len(h.steps) > 0 && h.steps[len(h.steps)-1].gen >= gen:
		return
	case h != nil && prev != nil && h.rows == prev:
		steps := append(h.steps, indexStep{gen: gen, built: built, removed: rows.Gone(prev)})
		if len(steps) > indexHistoryMax {
			steps = append([]indexStep(nil), steps[len(steps)-indexHistoryMax:]...)
		}
		h.rows, h.steps = rows, steps
	default:
		if h == nil && len(s.indexHist) >= storyMemoMax {
			s.indexHist = map[string]*indexHistory{}
		}
		s.indexHist[key] = &indexHistory{rows: rows, steps: []indexStep{{gen: gen}}}
	}
}

// sessionIndex returns the session index for q's filter, memoized per
// generation; its Active flags are stale, so read it through At. A new
// generation rebuilds only the rows of the sessions whose files changed
// (agentflow.ReuseSessionIndex) and reuses the rest, and records which in
// the filter's history for ?since.
func (s *Server) sessionIndex(snap *Snapshot, q Query) agentflow.SessionIndex {
	key := filterQueryKey(q)
	s.filterAggMu.Lock()
	if s.indexMemoGen == snap.Generation {
		if idx, ok := s.indexMemo[key]; ok {
			s.filterAggMu.Unlock()
			return idx
		}
	}
	var prev *agentflow.IndexRows
	if h := s.indexHist[key]; h != nil {
		prev = h.rows
	}
	s.filterAggMu.Unlock()

	idx, rows, built := agentflow.ReuseSessionIndex(snap, s.opts.Prices, q.Filter, s.layout(snap), prev, agentflow.IndexOptions{})
	s.filterAggMu.Lock()
	defer s.filterAggMu.Unlock()
	for _, id := range built {
		s.indexRowBuilds[id]++
	}
	if s.indexMemoGen != snap.Generation || len(s.indexMemo) >= storyMemoMax {
		s.indexMemo = map[string]agentflow.SessionIndex{}
		s.indexMemoGen = snap.Generation
	}
	s.indexMemo[key] = idx
	s.recordIndexBuild(key, snap.Generation, prev, rows, built)
	return idx
}

// sessionCursor names one /sessions answer: the generation it was built
// at, its clock reading in Unix milliseconds and filterHash of its filter.
// It travels as c<gen>-<ms>-<hash>, which no ?since date reads as.
type sessionCursor struct {
	gen    int64
	atMs   int64
	filter string
}

func (c sessionCursor) String() string {
	return fmt.Sprintf("c%d-%d-%s", c.gen, c.atMs, c.filter)
}

var sessionCursorRE = regexp.MustCompile(`^c(\d{1,18})-(\d{1,18})-([0-9a-f]{8})$`)

// isSessionCursor reports whether a ?since value is a cursor, not a date.
func isSessionCursor(raw string) bool { return sessionCursorRE.MatchString(raw) }

func parseSessionCursor(raw string) (sessionCursor, bool) {
	m := sessionCursorRE.FindStringSubmatch(raw)
	if m == nil {
		return sessionCursor{}, false
	}
	gen, err1 := strconv.ParseInt(m[1], 10, 64)
	at, err2 := strconv.ParseInt(m[2], 10, 64)
	if err1 != nil || err2 != nil {
		return sessionCursor{}, false
	}
	return sessionCursor{gen: gen, atMs: at, filter: m[3]}, true
}

// filterHash is a short digest of a filterQueryKey, so a cursor from one
// filter never answers another.
func filterHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// indexRowBuildCount is how many times session's index row was built.
// Test-only accessor.
func (s *Server) indexRowBuildCount(session string) int {
	s.filterAggMu.Lock()
	defer s.filterAggMu.Unlock()
	return s.indexRowBuilds[session]
}

// layout returns the snapshot's agentflow.Layout, built once per
// generation: which files each session reads, the key every per-session
// memo is checked against.
func (s *Server) layout(snap *Snapshot) *agentflow.Layout {
	s.layoutMu.Lock()
	defer s.layoutMu.Unlock()
	if s.layoutMemo != nil && s.layoutGen == snap.Generation {
		return s.layoutMemo
	}
	s.layoutMemo, s.layoutGen = agentflow.NewLayout(snap, nil), snap.Generation
	return s.layoutMemo
}

// sessionQuery validates the ?session and filter params a per-session
// endpoint takes, answering 400 or 404 itself; ok is false when it did.
func (s *Server) sessionQuery(w http.ResponseWriter, r *http.Request) (session string, ok bool) {
	v := r.URL.Query()
	session = strings.TrimSpace(v.Get("session"))
	if session == "" {
		http.Error(w, "?session is required", http.StatusBadRequest)
		return "", false
	}
	if _, err := parseQuery(v, time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	if !s.layout(s.cache.Snapshot()).Has(session) {
		http.NotFound(w, r)
		return "", false
	}
	return session, true
}

// handleAPISessionsSummary serves /_claudit/api/sessions/summary?session=<id>
// — what the session header computes over: the session's index row under
// the filter plus every step of it and its sub-agents with their numbers
// and calls but none of their text (agentflow.SessionSummary). An unknown
// session answers 404. Built from the memoized story, so it costs the
// session's own files only.
func (s *Server) handleAPISessionsSummary(w http.ResponseWriter, r *http.Request) {
	session, ok := s.sessionQuery(w, r)
	if !ok {
		return
	}
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionSessionsSummary,
		variant: func(snap *Snapshot, q Query, now time.Time) string {
			if st, ok := s.sessionStory(snap, q, session); ok {
				if row, ok := st.Row(); ok && row.At(now, agentActiveWindow).Active {
					return "active"
				}
			}
			return "inactive"
		},
		buildFromSnapshot: func(snap *Snapshot, q Query, now time.Time) (any, error) {
			st, ok := s.sessionStory(snap, q, session)
			if !ok {
				return nil, errNotFound
			}
			sum := st.Summary()
			sum.SessionIndexRow = sum.At(now, agentActiveWindow)
			return sum, nil
		},
	})
}

// handleAPISessionsStory serves /_claudit/api/sessions/story — one page of
// a session's story: ?session=<id> (required), ?prompt=<i> (default 0) and
// ?offset=<n> turns into that prompt (default 0), agentflow.StoryPageTurns
// turns per page. ?turn=<uuid> instead of ?prompt and ?offset answers the
// page that carries that turn. An unknown session, prompt, offset or turn
// answers 404. ?whole=1 instead answers the whole session as one page —
// every turn of every prompt and every sub-agent, text left as snippets —
// with the session's title, project, provider, model and time span from
// its index row: what the session header computes over.
//
// Every step carries context_cost_usd (agentflow.SessionContextCost) and
// every page the session's source_files.
//
// The story is built from the session's own files alone and memoized on
// their stamps (sessionStory), so a session's later pages, and its pages
// after another session wrote, rebuild nothing.
func (s *Server) handleAPISessionsStory(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
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
	turn := strings.TrimSpace(v.Get("turn"))
	if turn != "" && (v.Has("prompt") || v.Has("offset")) {
		http.Error(w, "?turn excludes ?prompt and ?offset", http.StatusBadRequest)
		return
	}
	whole := v.Get("whole") == "1"
	if whole && (turn != "" || v.Has("prompt") || v.Has("offset")) {
		http.Error(w, "?whole excludes ?turn, ?prompt and ?offset", http.StatusBadRequest)
		return
	}
	session, ok := s.sessionQuery(w, r)
	if !ok {
		return
	}
	s.serveAPISection(w, r, apiSectionSpec{
		section: apiSectionSessionsStory,
		buildFromSnapshot: func(snap *Snapshot, q Query, _ time.Time) (any, error) {
			st, ok := s.sessionStory(snap, q, session)
			if !ok {
				return nil, errNotFound
			}
			if whole {
				return wholeSession(st), nil
			}
			prompt, offset := prompt, offset
			if turn != "" {
				if prompt, offset, ok = st.Locate(turn, agentflow.StoryPageTurns); !ok {
					return nil, errNotFound
				}
			}
			page, ok := st.Page(prompt, offset, agentflow.StoryPageTurns)
			if !ok {
				return nil, errNotFound
			}
			return st.FullText(page, readLogText), nil
		},
	})
}

// sessionWhole is the ?whole=1 story payload: the whole session as one
// page plus what its index row says of it.
type sessionWhole struct {
	agentflow.StoryPage
	Title     string    `json:"title"`
	Project   string    `json:"project"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// wholeSession is the story as one page joined to its index row; a
// session the filter left no row keeps those fields empty.
func wholeSession(st *agentflow.SessionStory) sessionWhole {
	out := sessionWhole{StoryPage: st.Whole()}
	if row, ok := st.Row(); ok {
		out.Title, out.Project, out.Provider, out.Model = row.Title, row.Project, row.Provider, row.Model
		out.StartedAt, out.EndedAt = row.StartedAt, row.LastActivity
	}
	return out
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

// storyMemoEntry is one built story and when it was last served.
type storyMemoEntry struct {
	story *agentflow.SessionStory
	used  int64
}

// sessionStory returns the session's whole story under q's filter,
// memoized on the filter, the session and the stamps of its own files
// (agentflow.Layout.Key), so another session's write leaves it built. ok
// is false when the snapshot holds no turn of the session.
func (s *Server) sessionStory(snap *Snapshot, q Query, session string) (*agentflow.SessionStory, bool) {
	l := s.layout(snap)
	if !l.Has(session) {
		return nil, false
	}
	key := filterQueryKey(q) + "|" + l.Key(session)
	s.filterAggMu.Lock()
	s.storyMemoTick++
	if e, ok := s.storyMemo[key]; ok {
		e.used = s.storyMemoTick
		s.filterAggMu.Unlock()
		return e.story, true
	}
	s.filterAggMu.Unlock()

	v, _, _ := s.storySF.Do(key, func() (any, error) {
		s.storyBuildN.Add(1)
		st, _ := agentflow.BuildSessionStory(snap, s.opts.Prices, q.Filter, session, agentflow.StoryOptions{
			SnippetRunes: agentSnippetRunes,
			Layout:       l,
		})
		return st, nil
	})
	st, _ := v.(*agentflow.SessionStory)
	if st == nil {
		return nil, false
	}
	s.filterAggMu.Lock()
	defer s.filterAggMu.Unlock()
	if s.storyMemo == nil {
		s.storyMemo = map[string]*storyMemoEntry{}
	}
	if _, ok := s.storyMemo[key]; !ok && len(s.storyMemo) >= storyMemoMax {
		var oldest string
		for k, e := range s.storyMemo {
			if oldest == "" || e.used < s.storyMemo[oldest].used {
				oldest = k
			}
		}
		delete(s.storyMemo, oldest)
	}
	s.storyMemo[key] = &storyMemoEntry{story: st, used: s.storyMemoTick}
	return st, true
}

// storyBuildCount is how many stories were built. Test-only accessor.
func (s *Server) storyBuildCount() int64 { return s.storyBuildN.Load() }
