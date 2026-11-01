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
	Failed       int       `json:"failed"`
	LastActivity time.Time `json:"last_activity"`
	// Active is LastActivity within IndexOptions.ActiveWindow of Now.
	Active    bool      `json:"active"`
	StartedAt time.Time `json:"started_at"`
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
// the same grouping BuildAgentGraph uses, without building steps.
func BuildSessionIndex(snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter, opts IndexOptions) SessionIndex {
	idx := SessionIndex{Sessions: []SessionIndexRow{}}
	if snap == nil || len(snap.Turns) == 0 {
		return idx
	}
	c := collectSessions(snap, prices, f, "", opts.Replays)
	results := toolErrors(snap)
	prompts := sessionPrompts(snap, c.sessions)

	for _, s := range c.sessions {
		idx.Sessions = append(idx.Sessions, indexRow(s, prompts[s.sessionID], results, opts))
	}
	sort.Slice(idx.Sessions, func(i, j int) bool {
		a, b := idx.Sessions[i], idx.Sessions[j]
		if !a.LastActivity.Equal(b.LastActivity) {
			return a.LastActivity.After(b.LastActivity)
		}
		return a.SessionID < b.SessionID
	})
	return idx
}

func indexRow(s *sessionAccum, prompts []string, results map[string]bool, opts IndexOptions) SessionIndexRow {
	fam := SessionFamily(prompts)
	row := SessionIndexRow{
		SessionID: s.sessionID,
		FamilyKey: fam.Key,
		Remainder: fam.Remainder,
		Title:     fam.Title,
		Project:   project.Default.Resolve(s.cwd).Name,
		Provider:  s.provider,
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
	row.Active = opts.ActiveWindow > 0 && !row.LastActivity.Before(opts.Now.Add(-opts.ActiveWindow))
	sortMainFirst(ordinary, s.sessionID)
	if len(ordinary) > 0 {
		row.Outcome = lastMessageLine(ordinary[0].turns)
	}
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].Timestamp.Before(turns[j].Timestamp) })
	row.Commands, row.Failed = countCommands(turns, results)
	return row
}

// toolErrors maps each tool_use id with a result to whether it errored.
func toolErrors(snap *corpus.Snapshot) map[string]bool {
	out := make(map[string]bool, len(snap.ToolResults))
	for _, r := range snap.ToolResults {
		if r.ToolUseID != "" {
			out[r.ToolUseID] = r.IsError
		}
	}
	return out
}

// countCommands counts a session's tool calls, and those that errored and
// never later succeeded with the identical (name, input). turns are in
// time order; errored maps a tool_use id with a result to whether it
// errored.
func countCommands(turns []*parse.Turn, errored map[string]bool) (commands, failed int) {
	type call struct{ name, input string }
	var pending map[call]int
	for _, t := range turns {
		for _, u := range t.ToolUses {
			commands++
			if u.ID == "" {
				continue
			}
			isErr, ok := errored[u.ID]
			if !ok {
				continue
			}
			k := call{u.Name, u.Input}
			if isErr {
				if pending == nil {
					pending = map[call]int{}
				}
				pending[k]++
			} else {
				delete(pending, k)
			}
		}
	}
	for _, n := range pending {
		failed += n
	}
	return commands, failed
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
func sessionPrompts(snap *corpus.Snapshot, sessions map[string]*sessionAccum) map[string][]string {
	type msg struct {
		ts   time.Time
		text string
	}
	byID := map[string][]msg{}
	for _, m := range snap.Users {
		if _, ok := sessions[m.SessionID]; !ok || parse.IsSubagentFile(m.SourceFile) {
			continue
		}
		byID[m.SessionID] = append(byID[m.SessionID], msg{m.Timestamp, m.Text})
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
