package agentflow

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

// Layout is a snapshot's sessions by the source files they were read from:
// which files each session's story and index row read, and the replay
// decisions over each file. A session's Key changes exactly when one of
// its files was re-parsed, joined or left it, or saw another file take or
// give up one of its replayed turns, so work built over a session can be
// reused across snapshots while its Key stands.
type Layout struct {
	// Replays is the snapshot's replay set.
	Replays aggregate.ReplaySet
	roots   map[string]string
	// files lists each session's files, sorted: those of its turns and of
	// its user messages.
	files map[string][]string
	// fileTurns indexes each file's turns in snap.Turns, in order.
	fileTurns map[string][]int
	// replayDigest sums a hash of each replayed turn per file.
	replayDigest map[string]uint64
	stamps       map[string]corpus.FileStamp
	generation   int64
	// snap is the snapshot the layout groups; chains indexes its parent
	// edges and prompts, built once on the first prompt chain that leaves
	// a session's own files.
	snap       *corpus.Snapshot
	chainsOnce sync.Once
	chains     *snapshotChains
}

// NewLayout groups snap's files by session; replays is the snapshot's
// replay set when the caller holds it, nil builds it.
func NewLayout(snap *corpus.Snapshot, replays *aggregate.ReplaySet) *Layout {
	l := &Layout{
		files:        map[string][]string{},
		fileTurns:    map[string][]int{},
		replayDigest: map[string]uint64{},
	}
	if snap == nil {
		return l
	}
	if replays == nil {
		built := aggregate.BuildReplaySet(snap.Turns)
		replays = &built
	}
	l.Replays, l.roots, l.stamps, l.generation, l.snap = *replays, threadRoots(snap), snap.Files, snap.Generation, snap

	seen := map[string]map[string]bool{}
	add := func(rawID, file string) {
		id := l.root(rawID)
		fs := seen[id]
		if fs == nil {
			fs = map[string]bool{}
			seen[id] = fs
		}
		if !fs[file] {
			fs[file] = true
			l.files[id] = append(l.files[id], file)
		}
	}
	for i := range snap.Turns {
		t := &snap.Turns[i]
		l.fileTurns[t.SourceFile] = append(l.fileTurns[t.SourceFile], i)
		add(t.SessionID, t.SourceFile)
		if l.Replays.IsReplay(*t) {
			h := fnv.New64a()
			h.Write([]byte(t.MessageID))
			h.Write([]byte{0})
			h.Write([]byte(t.UUID))
			l.replayDigest[t.SourceFile] += h.Sum64()
		}
	}
	for i := range snap.Users {
		m := &snap.Users[i]
		if _, ok := seen[l.root(m.SessionID)]; ok {
			add(m.SessionID, m.SourceFile)
		}
	}
	for _, fs := range l.files {
		sort.Strings(fs)
	}
	return l
}

func (l *Layout) root(id string) string {
	if r, ok := l.roots[id]; ok {
		return r
	}
	return id
}

// Has reports whether any turn of the snapshot belongs to sessionID, as
// HasSession does.
func (l *Layout) Has(sessionID string) bool {
	_, ok := l.files[sessionID]
	return ok && sessionID != ""
}

// Sessions lists every session id with a turn, sorted.
func (l *Layout) Sessions() []string {
	out := make([]string, 0, len(l.files))
	for id := range l.files {
		if id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Files lists the session's source files, sorted.
func (l *Layout) Files(sessionID string) []string { return l.files[sessionID] }

// InSession is InSession over the layout's thread roots.
func (l *Layout) InSession(sessionID string) func(id string) bool {
	return func(id string) bool {
		id = l.root(id)
		return id != "" && id == sessionID
	}
}

// Key names what the session's story and index row are built from: each of
// its files with its stamp and replay digest. A file the snapshot carries
// no stamp for pins the key to the snapshot's generation. "" for a session
// with no turn.
func (l *Layout) Key(sessionID string) string {
	files, ok := l.files[sessionID]
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString(sessionID)
	for _, f := range files {
		st, ok := l.stamps[f]
		if !ok {
			b.WriteString("\x00gen=")
			b.WriteString(strconv.FormatInt(l.generation, 10))
		}
		b.WriteByte(0)
		b.WriteString(f)
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(st.Size, 10))
		b.WriteByte(0)
		b.WriteString(strconv.FormatInt(st.ModTime.UnixNano(), 10))
		b.WriteByte(0)
		b.WriteString(strconv.FormatUint(l.replayDigest[f], 16))
	}
	return b.String()
}

// eachTurn calls fn with every turn of the files of the given
// sessions, each file once, in snapshot order within a file.
func (l *Layout) eachTurn(snap *corpus.Snapshot, ids []string, fn func(t *parse.Turn)) {
	done := map[string]bool{}
	for _, id := range ids {
		for _, f := range l.files[id] {
			if done[f] {
				continue
			}
			done[f] = true
			for _, i := range l.fileTurns[f] {
				fn(&snap.Turns[i])
			}
		}
	}
}

// filesOf lists the distinct files of the given sessions.
func (l *Layout) filesOf(ids []string) []string {
	if len(ids) == 1 {
		return l.files[ids[0]]
	}
	var out []string
	done := map[string]bool{}
	for _, id := range ids {
		for _, f := range l.files[id] {
			if !done[f] {
				done[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// fileParts is each of files' records in all, placed by the snapshot's
// spans; all of it when the snapshot carries no spans, so a reader that
// filters its parts reads the same records either way.
func fileParts[T any](snap *corpus.Snapshot, files []string, all []T, span func(corpus.FileSpans) corpus.Span) [][]T {
	if snap.Spans == nil {
		return [][]T{all}
	}
	out := make([][]T, 0, len(files))
	for _, f := range files {
		if sp, ok := snap.Spans[f]; ok {
			if r := span(sp); r.End > r.Start {
				out = append(out, all[r.Start:r.End])
			}
		}
	}
	return out
}

// users is the user messages of the sessions' files.
func (l *Layout) users(snap *corpus.Snapshot, ids []string) [][]parse.UserMessage {
	return fileParts(snap, l.filesOf(ids), snap.Users, func(s corpus.FileSpans) corpus.Span { return s.Users })
}

// links is the parent links of the sessions' files.
func (l *Layout) links(snap *corpus.Snapshot, ids []string) [][]parse.ParentLink {
	return fileParts(snap, l.filesOf(ids), snap.Links, func(s corpus.FileSpans) corpus.Span { return s.Links })
}

// toolResults is the tool results of the sessions' files whose call ids
// are in calls. A call's result is written to the log that made the call,
// so these are every result of the sessions' calls.
func (l *Layout) toolResults(snap *corpus.Snapshot, ids []string, calls map[string]bool) []parse.ToolResult {
	if len(calls) == 0 {
		return nil
	}
	var out []parse.ToolResult
	for _, part := range fileParts(snap, l.filesOf(ids), snap.ToolResults, func(s corpus.FileSpans) corpus.Span { return s.ToolResults }) {
		for i := range part {
			if calls[part[i].ToolUseID] {
				out = append(out, part[i])
			}
		}
	}
	return out
}

// compactBoundaries is the compaction points of the session's files.
func (l *Layout) compactBoundaries(snap *corpus.Snapshot, sessionID string) []parse.CompactBoundary {
	files := l.files[sessionID]
	in := make(map[string]bool, len(files))
	for _, f := range files {
		in[f] = true
	}
	var out []parse.CompactBoundary
	for _, part := range fileParts(snap, files, snap.CompactBoundaries, func(s corpus.FileSpans) corpus.Span { return s.CompactBoundaries }) {
		for _, b := range part {
			if in[b.SourceFile] {
				out = append(out, b)
			}
		}
	}
	return out
}

// snapshotChains is the snapshot-wide lookups a session's records fall
// back on: every parent edge and prompt by uuid, as BuildPromptIndex reads
// them (links first, then turns, then prompts). Built once per Layout,
// only when a session's prompt chain reaches past its own files.
type snapshotChains struct {
	parent map[string]string
	prompt map[string]int
	users  []parse.UserMessage
}

func (c *snapshotChains) Parent(uuid string) (string, bool) {
	p, ok := c.parent[uuid]
	return p, ok
}

func (c *snapshotChains) Prompt(uuid string) (parse.UserMessage, bool) {
	i, ok := c.prompt[uuid]
	if !ok {
		return parse.UserMessage{}, false
	}
	return c.users[i], true
}

// snapshotChains builds the snapshot-wide lookups on first use.
func (l *Layout) snapshotChains() *snapshotChains {
	l.chainsOnce.Do(func() {
		snap := l.snap
		c := &snapshotChains{parent: map[string]string{}, prompt: map[string]int{}}
		if snap != nil {
			c.users = snap.Users
			for _, k := range snap.Links {
				if _, ok := c.parent[k.UUID]; !ok {
					c.parent[k.UUID] = k.ParentUUID
				}
			}
			for i := range snap.Turns {
				c.parent[snap.Turns[i].UUID] = snap.Turns[i].ParentUUID
			}
			for i, m := range snap.Users {
				c.parent[m.UUID] = m.ParentUUID
				c.prompt[m.UUID] = i
			}
		}
		l.chains = c
	})
	return l.chains
}

// chainsBuilt reports whether the snapshot-wide lookups were built.
// Test-only.
func (l *Layout) chainsBuilt() bool { return l.chains != nil }

// SessionContextCost is aggregate.ContextCostByTurnWhere for one session
// and its Codex sub-agent threads under f, computed from the session's own
// files: the attribution walk reads only a file's own counted turns, the
// results of their calls and its compaction points, so an Aggregator fed
// those files yields the whole corpus's figures. l is NewLayout(snap) when
// the caller holds it; nil builds it.
func SessionContextCost(snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter, sessionID string, l *Layout) map[string]float64 {
	if l == nil {
		l = NewLayout(snap, nil)
	}
	agg := aggregate.New(prices).WithFilter(f).WithReplaySet(l.Replays)
	calls := map[string]bool{}
	l.eachTurn(snap, []string{sessionID}, func(t *parse.Turn) {
		for _, u := range t.ToolUses {
			if u.ID != "" {
				calls[u.ID] = true
			}
		}
		agg.Add(*t)
	})
	if results := l.toolResults(snap, []string{sessionID}, calls); len(results) > 0 {
		agg.AddToolResults(results)
	}
	agg.AddCompactBoundaries(l.compactBoundaries(snap, sessionID))
	return agg.ContextCostByTurnWhere(l.InSession(sessionID))
}
