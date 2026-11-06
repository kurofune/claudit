package agentflow

import (
	"sort"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/project"
)

// outcomeMaxRunes caps a session's outcome line.
const outcomeMaxRunes = 200

// SessionIndex is the /_claudit/api/sessions payload: one light row per
// session in the filtered window, newest last activity first.
type SessionIndex struct {
	Sessions []SessionIndexRow `json:"sessions"`
}

// SessionIndexRow is one session's list row. It carries no steps and no
// tool snippets; the story endpoint pages those.
type SessionIndexRow struct {
	SessionID string `json:"session_id"`
	// FamilyKey groups sessions asked the same thing (see FamilyKey);
	// Remainder is what it blanked; Title is the unblanked first line of
	// the prompt they come from. All three read the session's prompts
	// whatever the window, since a family is a property of the session.
	FamilyKey string `json:"family_key"`
	Remainder string `json:"remainder"`
	Title     string `json:"title"`
	// Outcome is the first line of the main agent's last message.
	Outcome  string `json:"outcome"`
	Project  string `json:"project"`
	Provider string `json:"provider"`
	// Model is the model that ran the most turns.
	Model      string  `json:"model"`
	CostUSD    float64 `json:"cost_usd"`
	DurationMs int64   `json:"duration_ms"`
	// Turns and Commands count every agent's turns and tool calls.
	Turns    int `json:"turns"`
	Commands int `json:"commands"`
	// Failed counts the errored tool calls whose exact input never later
	// succeeded in the session: a failure the session fixed is not failed.
	Failed int `json:"failed"`
	// FirstFailedTurn is the uuid of the earliest turn holding a call
	// Failed counts, "" when Failed is 0.
	FirstFailedTurn string `json:"first_failed_turn"`
	// FailingChecks is one entry per check kind (checkKind) whose last run
	// in the session errored, in the order of those runs: a check the
	// session ran again and got green is not failing.
	FailingChecks []FailingCheck `json:"failing_checks"`
	// FailedEdits counts the Edit, Write, MultiEdit and apply_patch calls
	// that errored and never later succeeded with the identical input: an
	// edit that never applied.
	FailedEdits int `json:"failed_edits"`
	// Compactions counts the context compaction boundaries in the
	// session's files.
	Compactions  int       `json:"compactions"`
	LastActivity time.Time `json:"last_activity"`
	// Active is LastActivity within IndexOptions.ActiveWindow of Now
	// (activeAt).
	Active    bool      `json:"active"`
	StartedAt time.Time `json:"started_at"`
}

// FailingCheck is a check whose last run in a session errored.
type FailingCheck struct {
	// Check is the check's label, as checkKind names it ("go test").
	Check string `json:"check"`
	// Command is the first line of that last run's command, cut to
	// SummaryInputRunes.
	Command string `json:"command"`
	// Turn is the uuid of the turn that ran it.
	Turn string `json:"turn"`
	// InputKey is that run's whole-input identity, as the story's
	// input_key carries it; "" when the call has none.
	InputKey string `json:"input_key"`
}

// IndexOptions tunes the index. Zero values are sensible defaults.
type IndexOptions struct {
	// Now and ActiveWindow decide Active; a zero window marks none active.
	Now          time.Time
	ActiveWindow time.Duration
	// Replays is the snapshot's aggregate.BuildReplaySet when the caller
	// already holds it; nil builds it.
	Replays *aggregate.ReplaySet
}

// BuildSessionIndex builds one row per session in the filtered window from
// the same grouping BuildSessionStory uses, without building steps.
func BuildSessionIndex(snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter, opts IndexOptions) SessionIndex {
	if snap == nil || len(snap.Turns) == 0 {
		return SessionIndex{Sessions: []SessionIndexRow{}}
	}
	idx, _, _ := ReuseSessionIndex(snap, prices, f, NewLayout(snap, opts.Replays), nil, opts)
	return idx
}

// IndexRows is the rows one index build made, each with the Layout key of
// its session, for a later build under the same filter and prices to reuse.
type IndexRows struct {
	rows map[string]indexRowEntry
}

type indexRowEntry struct {
	key string
	row SessionIndexRow
	// ok is false for a session the filter or replay dedup left no turn.
	ok bool
}

// ReuseSessionIndex is BuildSessionIndex over l, taking each session's row
// from prev, built under the same filter and prices, when the session's
// Layout key is unchanged and building only the rest. It returns the rows
// for the next build and the sessions whose rows it built.
func ReuseSessionIndex(snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter, l *Layout, prev *IndexRows, opts IndexOptions) (SessionIndex, *IndexRows, []string) {
	idx := SessionIndex{Sessions: []SessionIndexRow{}}
	next := &IndexRows{rows: make(map[string]indexRowEntry, len(l.files))}
	keys := make(map[string]string, len(l.files))
	var built []string
	for id := range l.files {
		key := l.Key(id)
		keys[id] = key
		if prev != nil {
			if e, ok := prev.rows[id]; ok && e.key == key {
				next.rows[id] = e
				continue
			}
		}
		built = append(built, id)
	}
	sort.Strings(built)
	if len(built) > 0 {
		c := collectIn(snap, prices, f, l, built)
		results := toolErrors(snap, l, c.sessions)
		prompts := sessionPrompts(snap, l, c.sessions)
		for _, id := range built {
			e := indexRowEntry{key: keys[id]}
			if s, ok := c.sessions[id]; ok {
				e.row, e.ok = indexRow(s, prompts[id], results, len(l.compactBoundaries(snap, id)), IndexOptions{}), true
			}
			next.rows[id] = e
		}
	}
	for _, e := range next.rows {
		if e.ok {
			idx.Sessions = append(idx.Sessions, e.row.At(opts.Now, opts.ActiveWindow))
		}
	}
	sort.Slice(idx.Sessions, func(i, j int) bool {
		a, b := idx.Sessions[i], idx.Sessions[j]
		if !a.LastActivity.Equal(b.LastActivity) {
			return a.LastActivity.After(b.LastActivity)
		}
		return a.SessionID < b.SessionID
	})
	return idx, next, built
}

func indexRow(s *sessionAccum, prompts []string, results map[string]bool, compactions int, opts IndexOptions) SessionIndexRow {
	fam := SessionFamily(prompts)
	row := SessionIndexRow{
		SessionID:   s.sessionID,
		Compactions: compactions,
		FamilyKey:   fam.Key,
		Remainder:   fam.Remainder,
		Title:       fam.Title,
		Project:     project.Default.Resolve(s.cwd).Name,
		Provider:    s.provider,
	}
	models := map[string]int{}
	var turns []*parse.Turn
	var ordinary []*nodeAccum
	for _, n := range s.nodes {
		if row.StartedAt.IsZero() || n.started.Before(row.StartedAt) {
			row.StartedAt = n.started
		}
		if n.ended.After(row.LastActivity) {
			row.LastActivity = n.ended
		}
		row.CostUSD += n.cost
		for _, r := range n.turns {
			turns = append(turns, r.t)
			if r.t.Model != "" && r.t.Model != "<synthetic>" {
				models[r.t.Model]++
			}
		}
		if !n.subagent {
			ordinary = append(ordinary, n)
		}
	}
	row.Turns = len(turns)
	row.DurationMs = row.LastActivity.Sub(row.StartedAt).Milliseconds()
	row.Model = mostUsed(models)
	row.Active = row.activeAt(opts.Now, opts.ActiveWindow)
	sortMainFirst(ordinary, s.sessionID)
	if len(ordinary) > 0 {
		row.Outcome = lastMessageLine(ordinary[0].turns)
	}
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].Timestamp.Before(turns[j].Timestamp) })
	c := countCommands(turns, results)
	row.Commands, row.Failed, row.FirstFailedTurn = c.commands, c.failed, c.firstFailed
	row.FailingChecks, row.FailedEdits = c.failingChecks, c.failedEdits
	return row
}

func (r SessionIndexRow) activeAt(now time.Time, window time.Duration) bool {
	return window > 0 && !r.LastActivity.Before(now.Add(-window))
}

// At is the row as it reads at now: Active recomputed for window.
func (r SessionIndexRow) At(now time.Time, window time.Duration) SessionIndexRow {
	r.Active = r.activeAt(now, window)
	return r
}

// At is the index as it reads at now: each row's Active recomputed for
// window, everything else unchanged. idx is not modified.
func (idx SessionIndex) At(now time.Time, window time.Duration) SessionIndex {
	rows := make([]SessionIndexRow, len(idx.Sessions))
	for i, r := range idx.Sessions {
		rows[i] = r.At(now, window)
	}
	return SessionIndex{Sessions: rows}
}

// Inactive counts the rows not active at now for window. A row only ever
// turns from active to inactive as now advances, so the count names the
// active set exactly: two instants with the same count read the same
// index through At.
func (idx SessionIndex) Inactive(now time.Time, window time.Duration) int {
	n := 0
	for _, r := range idx.Sessions {
		if !r.activeAt(now, window) {
			n++
		}
	}
	return n
}

// toolErrors maps each tool_use id of the sessions' calls with a result to
// whether it errored.
func toolErrors(snap *corpus.Snapshot, l *Layout, sessions map[string]*sessionAccum) map[string]bool {
	calls := map[string]bool{}
	ids := make([]string, 0, len(sessions))
	for id, s := range sessions {
		ids = append(ids, id)
		for _, n := range s.nodes {
			for _, r := range n.turns {
				for _, u := range r.t.ToolUses {
					if u.ID != "" {
						calls[u.ID] = true
					}
				}
			}
		}
	}
	sort.Strings(ids)
	out := make(map[string]bool, len(calls))
	for _, r := range l.toolResults(snap, ids, calls) {
		out[r.ToolUseID] = r.IsError
	}
	return out
}

// commandCounts is what countCommands counts over a session's calls.
type commandCounts struct {
	commands, failed int
	firstFailed      string
	failingChecks    []FailingCheck
	failedEdits      int
}

// editTools are the tools that change a file; one that errored and never
// later succeeded is an edit that never applied.
var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "apply_patch": true}

// countCommands counts a session's tool calls, and those that errored and
// never later succeeded with the identical (name, whole input), and names
// the earliest turn holding one of those. Of those, it counts the edits
// (editTools). For each check kind a Bash call ran (checkKind), it keeps
// the last run, failing when that run errored; a Concurrent call runs no
// check, since its error is no single command's. turns are in time order;
// errored maps a tool_use id with a result to whether it errored.
func countCommands(turns []*parse.Turn, errored map[string]bool) commandCounts {
	type call struct {
		name, input string
		key         uint64
	}
	type open struct {
		n, first int
	}
	type run struct {
		errored bool
		at      int
		check   FailingCheck
	}
	var out commandCounts
	var pending map[call]open
	var checks map[string]run
	for i, t := range turns {
		for _, u := range t.ToolUses {
			out.commands++
			if u.ID == "" {
				continue
			}
			isErr, ok := errored[u.ID]
			if !ok {
				continue
			}
			if u.Name == parse.BashToolName && !u.Concurrent && !parse.ToolInputSnippetCut(u.Input) {
				if c := checkKind(u.Input); c.owns(isErr) {
					if checks == nil {
						checks = map[string]run{}
					}
					checks[c.kind] = run{isErr, i, FailingCheck{Check: c.kind, Command: firstLine(u.Input, SummaryInputRunes), Turn: t.UUID, InputKey: storyInputKey(u.InputKey)}}
				}
			}
			k := call{u.Name, u.Input, u.InputKey}
			if isErr {
				if pending == nil {
					pending = map[call]open{}
				}
				o, seen := pending[k]
				if !seen {
					o.first = i
				}
				o.n++
				pending[k] = o
			} else {
				delete(pending, k)
			}
		}
	}
	first := -1
	for k, o := range pending {
		out.failed += o.n
		if editTools[k.name] {
			out.failedEdits += o.n
		}
		if first < 0 || o.first < first {
			first = o.first
		}
	}
	if first >= 0 {
		out.firstFailed = turns[first].UUID
	}
	failing := make([]run, 0, len(checks))
	for _, r := range checks {
		if r.errored {
			failing = append(failing, r)
		}
	}
	sort.Slice(failing, func(i, j int) bool {
		if failing[i].at != failing[j].at {
			return failing[i].at < failing[j].at
		}
		return failing[i].check.Check < failing[j].check.Check
	})
	out.failingChecks = make([]FailingCheck, len(failing))
	for i, r := range failing {
		out.failingChecks[i] = r.check
	}
	return out
}

// lastMessageLine is the first non-empty line of the latest turn that
// narrated text, capped to outcomeMaxRunes.
func lastMessageLine(turns []turnRef) string {
	var last *parse.Turn
	for _, r := range turns {
		if strings.TrimSpace(r.t.Text) == "" {
			continue
		}
		if last == nil || !r.t.Timestamp.Before(last.Timestamp) {
			last = r.t
		}
	}
	if last == nil {
		return ""
	}
	for _, l := range strings.Split(last.Text, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			return truncateRunesPlain(strings.Join(f, " "), outcomeMaxRunes)
		}
	}
	return ""
}

func mostUsed(counts map[string]int) string {
	best, bestN := "", 0
	for m, n := range counts {
		if n > bestN || n == bestN && m < best {
			best, bestN = m, n
		}
	}
	return best
}

// sessionPrompts lists each collected session's own prompts in time order,
// whatever the window: user messages from the session's main files, not a
// sub-agent's file and not a folded sub-agent thread.
func sessionPrompts(snap *corpus.Snapshot, l *Layout, sessions map[string]*sessionAccum) map[string][]string {
	type msg struct {
		ts   time.Time
		text string
	}
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	byID := map[string][]msg{}
	for _, part := range l.users(snap, ids) {
		for _, m := range part {
			if _, ok := sessions[m.SessionID]; !ok || parse.IsSubagentFile(m.SourceFile) {
				continue
			}
			byID[m.SessionID] = append(byID[m.SessionID], msg{m.Timestamp, m.Text})
		}
	}
	out := make(map[string][]string, len(byID))
	for id, ms := range byID {
		sort.SliceStable(ms, func(i, j int) bool { return ms[i].ts.Before(ms[j].ts) })
		texts := make([]string, len(ms))
		for i, m := range ms {
			texts[i] = m.text
		}
		out[id] = texts
	}
	return out
}
