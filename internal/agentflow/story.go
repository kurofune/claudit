package agentflow

import (
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/parse/codex"
	"github.com/kurofune/claudit/internal/pricing"
)

// StoryPageTurns is how many main-agent turns one story page carries.
const StoryPageTurns = 200

// StoryOptions tunes the story. Zero values are sensible defaults.
type StoryOptions struct {
	// SnippetRunes caps each tool call's input and output to this many
	// runes, ending a cut one with "…"; 0 keeps the parse-time snippet
	// whole.
	SnippetRunes int
	// Replays is the snapshot's aggregate.BuildReplaySet when the caller
	// already holds it; nil builds it.
	Replays *aggregate.ReplaySet
	// PromptIndex is the snapshot's aggregate.BuildPromptIndex when the
	// caller already holds it, so the story reads each turn's prompt from it
	// instead of walking the whole snapshot's parent links; nil walks them.
	PromptIndex *aggregate.PromptIndex
}

// SessionStory is one session's whole story: its prompts, the main agent's
// steps under them, its sub-agents and what the window or replay dedup hid.
// Page cuts it into the /_claudit/api/sessions/story payload.
type SessionStory struct {
	SessionID string
	Prompts   []StoryPrompt
	Hidden    StoryHidden
	steps     []StoryStep
	// promptStart[i] is the index in steps of prompt i's first turn.
	promptStart []int
	children    []StoryChild
	// turnLog, promptLog and toolLog name the log each turn, prompt and
	// tool call was read from, so FullText can read its whole text back.
	turnLog, promptLog, toolLog map[string]LogSource
	// sourceFiles is every session log the story's turns came from.
	sourceFiles []string
}

// LogSource is a session log and the provider that wrote it.
type LogSource struct {
	File, Provider string
}

// StoryPrompt is one user prompt and how many main-agent turns it drove.
// An empty UUID marks turns with no resolvable prompt.
type StoryPrompt struct {
	UUID      string    `json:"uuid"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	Turns     int       `json:"turns"`
}

// StoryPageInfo places a page: the prompt it belongs to and its turn range
// within that prompt.
type StoryPageInfo struct {
	PromptIndex int  `json:"prompt_index"`
	TurnOffset  int  `json:"turn_offset"`
	TurnLimit   int  `json:"turn_limit"`
	HasMore     bool `json:"has_more"`
}

// StoryHidden discloses the session's turns the story leaves out: replays
// of another session's turns, and turns outside the request's filter.
type StoryHidden struct {
	Replays       int `json:"replays"`
	OutsideFilter int `json:"outside_filter"`
}

// StoryTool is a tool call with the exit code Codex writes above a
// command's output split off: ExitCode is nil when the output carries no
// such envelope, and Output then is unchanged.
type StoryTool struct {
	aggregate.ToolInvocation
	ExitCode *int `json:"exit_code"`
	// InputHash is the call's whole-input identity (parse.ToolUse.InputKey)
	// in hex, "" when it had no input: two calls ran the same input exactly
	// when their names and InputHash match, however Input is capped.
	InputHash string `json:"input_key"`
	// InputFull is the call's whole input when Input is a cut snippet, ""
	// when Input is already whole.
	InputFull string `json:"input_full,omitempty"`
	// inputOnDisk marks an InputFull that only the session log holds: the
	// parse-time snippet was cut too.
	inputOnDisk bool
}

// StoryStep is an AgentStep whose tools carry their exit code. UUID is the
// turn uuid /_claudit/api/agents/full?turn= takes.
type StoryStep struct {
	AgentStep
	Tools []StoryTool `json:"tools"`
	// ContextCostUSD is the write cost of the tool results this turn
	// carried into context (aggregate.ContextCostByTurn): what the turn
	// added. Re-reads stay on the session total.
	ContextCostUSD float64 `json:"context_cost_usd"`
}

// StoryChild is one sub-agent (or further rollout sharing the session id,
// kind "main") with its own steps. ParentToolUseID is the spawning call's
// id, "" when the sub-agent's meta does not name it.
type StoryChild struct {
	Kind            string      `json:"kind"`
	AgentType       string      `json:"agent_type"`
	Description     string      `json:"description"`
	ParentToolUseID string      `json:"parent_tool_use_id"`
	StartedAt       time.Time   `json:"started_at"`
	EndedAt         time.Time   `json:"ended_at"`
	CostUSD         float64     `json:"cost_usd"`
	ErrorCount      int         `json:"error_count"`
	Steps           []StoryStep `json:"steps"`
}

// StoryPage is the /_claudit/api/sessions/story payload: one prompt's
// turns, at most a page of them, and the sub-agents started in that span.
type StoryPage struct {
	SessionID string        `json:"session_id"`
	Prompts   []StoryPrompt `json:"prompts"`
	Page      StoryPageInfo `json:"page"`
	Steps     []StoryStep   `json:"steps"`
	Children  []StoryChild  `json:"children"`
	Hidden    StoryHidden   `json:"hidden"`
	// SourceFiles is every session log the story's turns came from,
	// sorted.
	SourceFiles []string `json:"source_files"`
}

// BuildSessionStory assembles one session's story from the snapshot's
// sessions as collectSessions groups them. ok is false when the snapshot holds no turn of the
// session at all; a session whose turns the filter all excluded is found,
// with no prompts.
func BuildSessionStory(snap *corpus.Snapshot, prices *pricing.Table, f aggregate.Filter, sessionID string, opts StoryOptions) (*SessionStory, bool) {
	if snap == nil || sessionID == "" {
		return nil, false
	}
	c := collectSessions(snap, prices, f, sessionID, opts.Replays)
	st := &SessionStory{
		SessionID:   sessionID,
		Prompts:     []StoryPrompt{},
		Hidden:      StoryHidden{Replays: c.replays, OutsideFilter: c.outsideFilter},
		steps:       []StoryStep{},
		children:    []StoryChild{},
		turnLog:     map[string]LogSource{},
		promptLog:   map[string]LogSource{},
		toolLog:     map[string]LogSource{},
		sourceFiles: []string{},
	}
	s, ok := c.sessions[sessionID]
	if !ok {
		return st, c.replays+c.outsideFilter > 0
	}

	as := assembleSession(s, sessionToolResults(snap, s), storyPrompts(snap, s, opts.PromptIndex))
	for _, n := range s.nodes {
		for _, r := range n.turns {
			src := LogSource{File: r.t.SourceFile, Provider: r.t.Provider}
			if r.t.UUID != "" {
				st.turnLog[r.t.UUID] = src
			}
			for _, u := range r.t.ToolUses {
				if u.ID != "" {
					st.toolLog[u.ID] = src
				}
			}
		}
	}
	files := map[string]bool{}
	for _, src := range st.turnLog {
		if src.File != "" && !files[src.File] {
			files[src.File] = true
			st.sourceFiles = append(st.sourceFiles, src.File)
		}
	}
	sort.Strings(st.sourceFiles)
	codexOutput := s.provider == parse.ProviderCodex

	if as.Main != nil {
		st.steps = storySteps(as.Main.Steps, codexOutput, opts.SnippetRunes)
	}
	for i, m := range as.Prompts {
		end := len(st.steps)
		if i+1 < len(as.Prompts) {
			end = as.Prompts[i+1].FirstStepIndex
		}
		st.Prompts = append(st.Prompts, StoryPrompt{UUID: m.UUID, Text: m.Text, Timestamp: m.Timestamp, Turns: end - m.FirstStepIndex})
		st.promptStart = append(st.promptStart, m.FirstStepIndex)
		if m.UUID != "" {
			st.promptLog[m.UUID] = LogSource{Provider: s.provider}
		}
	}
	for _, m := range snap.Users {
		if src, ok := st.promptLog[m.UUID]; ok && src.File == "" {
			src.File = m.SourceFile
			st.promptLog[m.UUID] = src
		}
	}
	for _, ch := range as.Children {
		st.children = append(st.children, StoryChild{
			Kind:            ch.Kind,
			AgentType:       ch.AgentType,
			Description:     ch.Description,
			ParentToolUseID: ch.ParentToolUseID,
			StartedAt:       ch.StartedAt,
			EndedAt:         ch.EndedAt,
			CostUSD:         ch.CostUSD,
			ErrorCount:      ch.ErrorCount,
			Steps:           storySteps(ch.Steps, codexOutput, opts.SnippetRunes),
		})
	}
	return st, true
}

// HasSession reports whether any turn of the snapshot belongs to the
// session under the grouping BuildSessionIndex and BuildSessionStory use:
// a Codex sub-agent thread counts toward its root session, so a root whose
// own transcript is gone is still found through its children.
func HasSession(snap *corpus.Snapshot, sessionID string) bool {
	if snap == nil || sessionID == "" {
		return false
	}
	in := InSession(snap, sessionID)
	for i := range snap.Turns {
		if in(snap.Turns[i].SessionID) {
			return true
		}
	}
	return false
}

// InSession is the test HasSession applies to each turn: whether a turn
// whose SessionID is id belongs to sessionID, its own or a Codex sub-agent
// thread descending from it.
func InSession(snap *corpus.Snapshot, sessionID string) func(id string) bool {
	roots := threadRoots(snap)
	return func(id string) bool {
		if root, ok := roots[id]; ok {
			id = root
		}
		return id != "" && id == sessionID
	}
}

// Page cuts the story at prompt index prompt, turns [offset, offset+limit)
// of it. Its children are the sub-agents started from the page's first
// turn up to the turn after its last; the session's first page also takes
// those started before any turn, and its last those started after. ok is
// false for a prompt index or offset past the end. A story with no prompts
// has one empty page.
func (st *SessionStory) Page(prompt, offset, limit int) (StoryPage, bool) {
	page := StoryPage{
		SessionID:   st.SessionID,
		Prompts:     st.Prompts,
		Page:        StoryPageInfo{PromptIndex: prompt, TurnOffset: offset, TurnLimit: limit},
		Steps:       []StoryStep{},
		Children:    []StoryChild{},
		Hidden:      st.Hidden,
		SourceFiles: st.sourceFiles,
	}
	if limit <= 0 || prompt < 0 || offset < 0 {
		return StoryPage{}, false
	}
	if len(st.Prompts) == 0 {
		if prompt != 0 || offset != 0 {
			return StoryPage{}, false
		}
		page.Children = st.children
		return page, true
	}
	if prompt >= len(st.Prompts) {
		return StoryPage{}, false
	}
	turns := st.Prompts[prompt].Turns
	if offset > 0 && offset >= turns {
		return StoryPage{}, false
	}
	from := st.promptStart[prompt] + offset
	to := st.promptStart[prompt] + min(offset+limit, turns)
	page.Steps = st.steps[from:to]
	page.Page.HasMore = offset+limit < turns

	for _, ch := range st.children {
		if st.childOnPage(ch, from, to) {
			page.Children = append(page.Children, ch)
		}
	}
	return page, true
}

// childOnPage reports whether the page holding main-agent steps [from, to)
// carries ch: the sub-agent started in that span.
func (st *SessionStory) childOnPage(ch StoryChild, from, to int) bool {
	afterStart := from == 0 || !ch.StartedAt.Before(st.steps[from].Timestamp)
	beforeEnd := to >= len(st.steps) || ch.StartedAt.Before(st.steps[to].Timestamp)
	return afterStart && beforeEnd
}

// Whole is the session's whole story as one page: every main-agent turn
// and every sub-agent, Page placing it at prompt 0 offset 0 with every turn
// in it. Its text stays the parse-time snippets.
func (st *SessionStory) Whole() StoryPage {
	return StoryPage{
		SessionID:   st.SessionID,
		Prompts:     st.Prompts,
		Page:        StoryPageInfo{TurnLimit: len(st.steps)},
		Steps:       st.steps,
		Children:    st.children,
		Hidden:      st.Hidden,
		SourceFiles: st.sourceFiles,
	}
}

// WithContextCost returns page with each step's ContextCostUSD read from
// byTurn by turn uuid, its main and sub-agent steps copied so the story
// they came from is left unchanged.
func (page StoryPage) WithContextCost(byTurn map[string]float64) StoryPage {
	withCost := func(steps []StoryStep) []StoryStep {
		out := make([]StoryStep, len(steps))
		for i, s := range steps {
			s.ContextCostUSD = byTurn[s.UUID]
			out[i] = s
		}
		return out
	}
	page.Steps = withCost(page.Steps)
	children := make([]StoryChild, len(page.Children))
	for i, ch := range page.Children {
		ch.Steps = withCost(ch.Steps)
		children[i] = ch
	}
	page.Children = children
	return page
}

// Locate finds the page that carries turn uuid: its prompt index and the
// page offset into that prompt, a multiple of limit. A sub-agent's turn is
// on the page its sub-agent sits on, the one it started in. ok is false
// when no turn of the story has that uuid.
func (st *SessionStory) Locate(uuid string, limit int) (prompt, offset int, ok bool) {
	if uuid == "" || limit <= 0 {
		return 0, 0, false
	}
	for i := range st.steps {
		if st.steps[i].UUID != uuid {
			continue
		}
		for p := len(st.promptStart) - 1; p >= 0; p-- {
			if i >= st.promptStart[p] {
				return p, (i - st.promptStart[p]) / limit * limit, true
			}
		}
		return 0, 0, false
	}
	for _, ch := range st.children {
		if !slices.ContainsFunc(ch.Steps, func(s StoryStep) bool { return s.UUID == uuid }) {
			continue
		}
		if len(st.Prompts) == 0 {
			return 0, 0, true
		}
		for p, pr := range st.Prompts {
			for off := 0; off < pr.Turns; off += limit {
				from := st.promptStart[p] + off
				to := st.promptStart[p] + min(off+limit, pr.Turns)
				if st.childOnPage(ch, from, to) {
					return p, off, true
				}
			}
		}
		return 0, 0, false
	}
	return 0, 0, false
}

// storyPrompts is the promptLookup for one session's story: the snapshot's
// PromptIndex when given, else a PromptResolver over the session's turns.
func storyPrompts(snap *corpus.Snapshot, s *sessionAccum, idx *aggregate.PromptIndex) promptLookup {
	if idx == nil {
		var turns []parse.Turn
		for _, n := range s.nodes {
			for _, r := range n.turns {
				turns = append(turns, *r.t)
			}
		}
		return resolverLookup{aggregate.NewPromptResolver(turns, snap.Users, snap.Links)}
	}
	l := indexLookup{idx: idx, users: map[string]parse.UserMessage{}}
	want := map[string]bool{}
	for _, n := range s.nodes {
		for _, r := range n.turns {
			if u := idx.Lookup(r.t.UUID).UserUUID; u != "" {
				want[u] = true
			}
		}
	}
	for _, m := range snap.Users {
		if want[m.UUID] {
			l.users[m.UUID] = m
		}
	}
	return l
}

// indexLookup is a promptLookup over the snapshot's PromptIndex, with the
// session's prompts by uuid for their text and time.
type indexLookup struct {
	idx   *aggregate.PromptIndex
	users map[string]parse.UserMessage
}

func (l indexLookup) promptOf(step *AgentStep) string { return l.idx.Lookup(step.UUID).UserUUID }
func (l indexLookup) text(uuid string) string         { return l.users[uuid].Text }
func (l indexLookup) timestamp(uuid string) time.Time { return l.users[uuid].Timestamp }

// sessionToolResults indexes the tool results of one session's calls.
func sessionToolResults(snap *corpus.Snapshot, s *sessionAccum) map[string]parse.ToolResult {
	ids := map[string]bool{}
	for _, n := range s.nodes {
		for _, r := range n.turns {
			for _, u := range r.t.ToolUses {
				if u.ID != "" {
					ids[u.ID] = true
				}
			}
		}
	}
	out := make(map[string]parse.ToolResult, len(ids))
	for _, r := range snap.ToolResults {
		if ids[r.ToolUseID] {
			out[r.ToolUseID] = r
		}
	}
	return out
}

// storySteps converts an agent's steps, splitting each Codex tool output's
// exit-code envelope off before capping the snippets.
func storySteps(steps []AgentStep, codexOutput bool, snippetRunes int) []StoryStep {
	out := make([]StoryStep, len(steps))
	for i, s := range steps {
		tools := make([]StoryTool, len(s.Tools))
		for j, inv := range s.Tools {
			tool := StoryTool{ToolInvocation: inv}
			if codexOutput {
				if code, body, ok := codex.ExitEnvelope(inv.Output); ok {
					tool.ExitCode = &code
					tool.Output = body
				}
			}
			if snippetRunes > 0 {
				whole := tool.Input
				tool.Input = truncateRunes(tool.Input, snippetRunes)
				tool.Output = truncateRunes(tool.Output, snippetRunes)
				if tool.Input != whole {
					tool.InputFull = whole
					tool.inputOnDisk = parse.ToolInputSnippetCut(whole)
				}
			}
			if inv.InputKey != 0 {
				tool.InputHash = strconv.FormatUint(inv.InputKey, 16)
			}
			tools[j] = tool
		}
		s.Tools = nil
		out[i] = StoryStep{AgentStep: s, Tools: tools}
	}
	return out
}

// TextReader reads the whole text a TextRequest names back from one session
// log.
type TextReader func(src LogSource, want parse.TextRequest) parse.FullTexts

// FullText gives page the whole text the parse-time snippets cut: every
// prompt's text, every agent message's text, its sub-agents' messages and
// each cut tool input, read back through read, one pass per log. A Codex
// prompt reads back even when uncut, since its corpus text drops the
// injected wrapper items and trims what the person typed. What read cannot
// find keeps its snippet. The story itself is left unchanged.
func (st *SessionStory) FullText(page StoryPage, read TextReader) StoryPage {
	reqs := map[LogSource]*parse.TextRequest{}
	want := func(src LogSource, ok bool) *parse.TextRequest {
		if !ok || src.File == "" {
			return nil
		}
		r := reqs[src]
		if r == nil {
			r = &parse.TextRequest{Turns: map[string]bool{}, Users: map[string]bool{}, Tools: map[string]bool{}}
			reqs[src] = r
		}
		return r
	}
	for _, p := range page.Prompts {
		src := st.promptLog[p.UUID]
		if parse.TextSnippetCut(p.Text) || src.Provider == parse.ProviderCodex {
			if r := want(src, src != LogSource{}); r != nil {
				r.Users[p.UUID] = true
			}
		}
	}
	askSteps := func(steps []StoryStep) {
		for _, s := range steps {
			if parse.TextSnippetCut(s.Text) {
				src, ok := st.turnLog[s.UUID]
				if r := want(src, ok); r != nil {
					r.Turns[s.UUID] = true
				}
			}
			for _, t := range s.Tools {
				if t.inputOnDisk {
					src, ok := st.toolLog[t.ID]
					if r := want(src, ok); r != nil {
						r.Tools[t.ID] = true
					}
				}
			}
		}
	}
	askSteps(page.Steps)
	for _, ch := range page.Children {
		askSteps(ch.Steps)
	}
	if len(reqs) == 0 {
		return page
	}
	got := parse.NewFullTexts()
	for src, r := range reqs {
		texts := read(src, *r)
		for k, v := range texts.Turns {
			got.Turns[k] = v
		}
		for k, v := range texts.Users {
			got.Users[k] = v
		}
		for k, v := range texts.Inputs {
			got.Inputs[k] = v
		}
	}
	prompts := make([]StoryPrompt, len(page.Prompts))
	for i, p := range page.Prompts {
		if text, ok := got.Users[p.UUID]; ok && text != "" {
			p.Text = text
		}
		prompts[i] = p
	}
	page.Prompts = prompts
	page.Steps = fullSteps(page.Steps, got)
	children := make([]StoryChild, len(page.Children))
	for i, ch := range page.Children {
		ch.Steps = fullSteps(ch.Steps, got)
		children[i] = ch
	}
	page.Children = children
	return page
}

// fullSteps copies steps with the whole text got holds in place of their
// cut text and tool inputs.
func fullSteps(steps []StoryStep, got parse.FullTexts) []StoryStep {
	out := make([]StoryStep, len(steps))
	for i, s := range steps {
		if d, ok := got.Turns[s.UUID]; ok && d.Text != "" {
			s.Text = d.Text
		}
		tools := make([]StoryTool, len(s.Tools))
		for j, t := range s.Tools {
			if in, ok := got.Inputs[t.ID]; ok && in != "" && t.inputOnDisk {
				t.InputFull = in
			}
			tools[j] = t
		}
		s.Tools = tools
		out[i] = s
	}
	return out
}
