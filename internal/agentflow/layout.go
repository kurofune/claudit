package agentflow

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

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
	l.Replays, l.roots, l.stamps, l.generation = *replays, threadRoots(snap), snap.Files, snap.Generation

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
	if len(calls) > 0 {
		var results []parse.ToolResult
		for i := range snap.ToolResults {
			if calls[snap.ToolResults[i].ToolUseID] {
				results = append(results, snap.ToolResults[i])
			}
		}
		agg.AddToolResults(results)
	}
	files := map[string]bool{}
	for _, f := range l.files[sessionID] {
		files[f] = true
	}
	var bounds []parse.CompactBoundary
	for _, b := range snap.CompactBoundaries {
		if files[b.SourceFile] {
			bounds = append(bounds, b)
		}
	}
	agg.AddCompactBoundaries(bounds)
	return agg.ContextCostByTurnWhere(l.InSession(sessionID))
}
