// Package parse streams Claude Code session JSONL files and extracts
// the per-turn data we need for cost aggregation. We intentionally keep
// the schema decoupled from the upstream Anthropic types — we only
// decode the fields claudit needs, and tolerate unknown ones.
package parse

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Usage is the per-turn token accounting we read off `message.usage`.
// We always take the outer fields; we do NOT also sum `iterations` —
// the outer fields are the rolled-up totals (see brief).
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheCreate5mTokens int
	CacheCreate1hTokens int
	CacheReadTokens     int
}

// ToolUse is one tool_use entry inside an assistant turn.
type ToolUse struct {
	// ID is the tool_use block's id (e.g. "toolu_01..."). It ties this call
	// to its tool_result in the following user turn so the aggregator can
	// join a call to its outcome. Empty for older sessions that omit it.
	ID           string
	Name         string
	SkillName    string // when Name == "Skill"
	SlashCommand string // when Name == "SlashCommand"
	SubagentType string // when Name == "Agent"
	// Detail is a per-tool drill-down key, populated by detail.go's extractor:
	// "git status" for Bash, ".go" for Read/Edit/Write, "github.com" for
	// WebFetch, etc. Empty when no useful sub-key applies.
	Detail string
	// Input is a bounded, human-readable snippet of the tool's input for
	// high-value tools — the full Bash command, the prompt handed to an
	// Agent/Task subagent, etc. (see extractToolInput). Empty for tools
	// whose Detail already says everything useful. Capped to bound payload.
	Input string
}

// ToolResult is one tool_result block from a user turn — the outcome of a
// prior assistant tool_use, linked by ToolUseID. We surface these (despite
// dropping the user turns that carry them) so the Agents view can show
// whether a tool call succeeded and a snippet of what it returned.
type ToolResult struct {
	ToolUseID string
	IsError   bool
	// Content is a bounded snippet of the result text. The wire `content`
	// is either a bare string or an array of {type:"text",text} blocks; both
	// collapse to plain text here. Capped to bound payload size.
	Content string
	// Timestamp is the carrying user line's `timestamp` — the wall-clock moment
	// the tool's result arrived. It's the only per-tool time we can recover (the
	// tool_use side stamps the whole assistant turn, not each call), so the join
	// uses it as each tool's end. Zero when the line lacks a parseable timestamp.
	Timestamp time.Time
}

// Turn is one assistant message — the only event type that costs money.
type Turn struct {
	SessionID string
	// MessageID is `message.id` — the wire id shared by every JSONL line of
	// one streamed assistant turn (Claude Code writes one line per content
	// block, all repeating this id and the same cumulative usage). It's the
	// key the coalescer groups on so a multi-block turn counts once. Empty
	// for older single-line-per-message transcripts.
	MessageID  string
	UUID       string
	ParentUUID string
	Sidechain  bool
	Timestamp  time.Time
	CWD        string
	Model      string
	Usage      Usage
	ToolUses   []ToolUse
	// Thinking is the joined text of the assistant message's `thinking`
	// blocks — the model's extended-thinking reasoning. Empty when none.
	Thinking string
	// Text is the joined text of the assistant message's `text` narration
	// blocks. Empty when none.
	Text string
	// Entrypoint is the session origin from the JSONL line: "cli" for an
	// interactive session, "sdk-cli" for a headless/SDK run. Constant
	// across a session; the aggregator lifts it to the session level.
	Entrypoint string
	// SourceFile is the JSONL path; lets aggregator look up subagent meta.
	SourceFile string
}

// UserMessage is one human-authored prompt (or a slash-command line) — i.e.
// a `type:"user"` line whose content is text rather than tool_result. We
// keep these separately from Turn so the aggregator can walk parentUuid
// chains back to the originating prompt and attribute downstream cost.
type UserMessage struct {
	SessionID  string
	UUID       string
	ParentUUID string
	Timestamp  time.Time
	CWD        string
	Text       string // full text — render layer truncates
	SourceFile string
}

// ParentLink is one (child UUID → parent UUID) edge from any line type.
// Surface for chain walks that need to climb through non-content lines
// (system events, file-history-snapshots, agent-color markers) which
// sit between an assistant turn and the originating user message.
type ParentLink struct {
	UUID, ParentUUID string
}

// Result is what ParseFile returns.
type Result struct {
	Turns        []Turn
	UserMessages []UserMessage
	// ToolResults are the tool_result blocks from this file's user turns,
	// keyed for join by ToolUseID. Surfaced even though the carrying user
	// turns are filtered out of UserMessages.
	ToolResults []ToolResult
	// ParentLinks contains uuid → parentUuid edges from every line that
	// has both fields, including non-content message types. The chain
	// walk needs these to bridge over hooks and snapshots.
	ParentLinks []ParentLink
	Malformed   int // count of lines we couldn't decode
}

// rawLine is the wire format. Only the fields we care about.
type rawLine struct {
	Type       string          `json:"type"`
	SessionID  string          `json:"sessionId"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	Sidechain  bool            `json:"isSidechain"`
	Timestamp  string          `json:"timestamp"`
	CWD        string          `json:"cwd"`
	Entrypoint string          `json:"entrypoint"`
	Message    json.RawMessage `json:"message"`
	IsMeta     bool            `json:"isMeta"`
}

type rawMessage struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	Input        int            `json:"input_tokens"`
	Output       int            `json:"output_tokens"`
	CacheCreate  int            `json:"cache_creation_input_tokens"`
	CacheRead    int            `json:"cache_read_input_tokens"`
	CacheCreaSub *cacheCreation `json:"cache_creation"`
}

type cacheCreation struct {
	Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
}

type rawContentEntry struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
}

// rawToolResultEntry decodes a tool_result block from a user message.
// `content` is either a JSON string or an array of {type:"text",text}
// blocks, so it's held raw and flattened by toolResultText.
type rawToolResultEntry struct {
	Type      string          `json:"type"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// rawUserContentEntry decodes the `{"type":"text","text":"..."}` blocks
// found in user messages. Sharing rawContentEntry would conflate fields
// (Input is for tool_use blocks; Text here is for text blocks).
type rawUserContentEntry struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type rawSkillInput struct {
	Skill        string `json:"skill"`
	Command      string `json:"command"`
	SubagentType string `json:"subagent_type"`
}

// LineKind classifies what ParseLine recognized in a single JSONL line.
type LineKind int

const (
	LineUnknown LineKind = iota
	LineMalformed
	LineAssistant
	LineUserMessage
)

// ParseLine decodes one JSONL line. Returns the turn or user-message it
// produced (only one is non-zero per call) and the kind. path is recorded
// on each surface object so callers can later resolve subagent metadata.
//
// This exists so streaming consumers (like `claudit watch`) can reuse the
// same decoding logic ParseFile uses, without re-implementing JSON
// schema knowledge.
func ParseLine(line []byte, path string) (Turn, UserMessage, LineKind) {
	if len(line) == 0 {
		return Turn{}, UserMessage{}, LineUnknown
	}
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return Turn{}, UserMessage{}, LineMalformed
	}
	switch raw.Type {
	case "assistant":
		if len(raw.Message) == 0 {
			return Turn{}, UserMessage{}, LineUnknown
		}
		var msg rawMessage
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			return Turn{}, UserMessage{}, LineMalformed
		}
		if msg.Usage == nil {
			return Turn{}, UserMessage{}, LineUnknown
		}
		ts, _ := time.Parse(time.RFC3339, raw.Timestamp)
		thinking, text := extractAssistantText(msg.Content)
		return Turn{
			SessionID:  raw.SessionID,
			MessageID:  msg.ID,
			UUID:       raw.UUID,
			ParentUUID: raw.ParentUUID,
			Sidechain:  raw.Sidechain,
			Timestamp:  ts,
			CWD:        raw.CWD,
			Model:      msg.Model,
			Usage:      convertUsage(msg.Usage),
			ToolUses:   extractToolUses(msg.Content),
			Thinking:   thinking,
			Text:       text,
			Entrypoint: raw.Entrypoint,
			SourceFile: path,
		}, UserMessage{}, LineAssistant
	case "user":
		if raw.IsMeta || len(raw.Message) == 0 {
			return Turn{}, UserMessage{}, LineUnknown
		}
		var msg rawMessage
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			return Turn{}, UserMessage{}, LineMalformed
		}
		text, hasToolResult := extractUserText(msg.Content)
		if hasToolResult || text == "" {
			return Turn{}, UserMessage{}, LineUnknown
		}
		ts, _ := time.Parse(time.RFC3339, raw.Timestamp)
		return Turn{}, UserMessage{
			SessionID:  raw.SessionID,
			UUID:       raw.UUID,
			ParentUUID: raw.ParentUUID,
			Timestamp:  ts,
			CWD:        raw.CWD,
			Text:       text,
			SourceFile: path,
		}, LineUserMessage
	}
	return Turn{}, UserMessage{}, LineUnknown
}

// ParseFile streams r line-by-line. path is recorded on each Turn so the
// aggregator can later resolve subagent metadata via the sibling .meta.json.
func ParseFile(r io.Reader, path string) (Result, error) {
	var res Result
	sc := bufio.NewScanner(r)
	// Some session lines are very large (>1 MB) — bump the buffer.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		t, u, kind := ParseLine(line, path)
		switch kind {
		case LineMalformed:
			res.Malformed++
		case LineAssistant:
			res.Turns = append(res.Turns, t)
		case LineUserMessage:
			res.UserMessages = append(res.UserMessages, u)
		}
		// Always extract the parent link if present, even for line types
		// we otherwise ignore. This bridges system / snapshot rows that
		// would otherwise break the prompt-attribution chain.
		if uuid, parentUUID := peekParentLink(line); uuid != "" && parentUUID != "" {
			res.ParentLinks = append(res.ParentLinks, ParentLink{UUID: uuid, ParentUUID: parentUUID})
		}
		// Tool results ride in user turns that ParseLine filters out, so
		// pull them off the raw line separately (keyed by tool_use_id).
		res.ToolResults = append(res.ToolResults, peekToolResults(line)...)
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	// Coalesce the per-line assistant turns: every JSONL line of one streamed
	// message repeats the same message.id and the same cumulative usage, so
	// counting per-line inflates cost. Collapse each message to one Turn.
	res.Turns = coalesceTurns(res.Turns)
	return res, nil
}

// coalesceTurns groups assistant turns by MessageID and emits one merged Turn
// per group, preserving first-appearance order. A turn with an empty MessageID
// (older single-line-per-message transcripts, or malformed lines that lost the
// id) stands alone — so the result is a no-op for the legacy format. Grouping
// is by id, not merely consecutive, so an interleaved id still collapses to one.
func coalesceTurns(turns []Turn) []Turn {
	if len(turns) == 0 {
		return turns
	}
	out := make([]Turn, 0, len(turns))
	index := make(map[string]int, len(turns)) // MessageID -> position in out
	for _, t := range turns {
		if t.MessageID == "" {
			out = append(out, t)
			continue
		}
		if pos, ok := index[t.MessageID]; ok {
			out[pos] = mergeTurn(out[pos], t)
			continue
		}
		index[t.MessageID] = len(out)
		out = append(out, t)
	}
	return out
}

// mergeTurn folds a continuation line b into the in-progress turn a (same
// message.id). Identity/timing come from a (the first line): UUID, ParentUUID,
// Timestamp, SessionID, etc. are left untouched. Content accumulates in order —
// thinking and text blocks join with newlines, tool_uses concatenate. Usage is
// taken as the per-field max: the lines repeat one identical cumulative total,
// so max counts it once while tolerating a malformed line that lost a field.
func mergeTurn(a, b Turn) Turn {
	a.Thinking = joinBlocks(a.Thinking, b.Thinking)
	a.Text = joinBlocks(a.Text, b.Text)
	a.ToolUses = append(a.ToolUses, b.ToolUses...)
	a.Usage.InputTokens = maxInt(a.Usage.InputTokens, b.Usage.InputTokens)
	a.Usage.OutputTokens = maxInt(a.Usage.OutputTokens, b.Usage.OutputTokens)
	a.Usage.CacheCreate5mTokens = maxInt(a.Usage.CacheCreate5mTokens, b.Usage.CacheCreate5mTokens)
	a.Usage.CacheCreate1hTokens = maxInt(a.Usage.CacheCreate1hTokens, b.Usage.CacheCreate1hTokens)
	a.Usage.CacheReadTokens = maxInt(a.Usage.CacheReadTokens, b.Usage.CacheReadTokens)
	return a
}

// joinBlocks concatenates two block strings with a newline, skipping the
// separator when either side is empty so a turn with no thinking (or no text)
// doesn't gain a leading/trailing blank line.
func joinBlocks(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n" + b
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Coalescer is the streaming counterpart of coalesceTurns: it merges the
// content-block lines of one assistant message into a single Turn for consumers
// that see one parsed line at a time (e.g. `claudit watch`). Feed each assistant
// Turn through Push in arrival order; call Flush when a non-assistant line or
// end-of-stream signals the in-progress message is done. The zero value is ready
// to use.
//
// Unlike the batch coalesceTurns, this merges only the consecutive run sharing a
// message.id — a stream can't look ahead to regroup interleaved ids — which
// matches how Claude Code writes a message's blocks contiguously.
type Coalescer struct {
	cur    *Turn
	hasCur bool
	curID  string
}

// Push feeds the next assistant Turn. When t continues the in-progress message
// (same non-empty MessageID), it is merged and Push returns (zero, false). When
// t starts a new message, the previous in-progress message is completed and
// returned (turn, true) and t becomes the new in-progress message. A turn with
// an empty MessageID never merges: it flushes any pending message and is itself
// held as a standalone in-progress turn (so legacy lines stay one-per-line).
func (c *Coalescer) Push(t Turn) (Turn, bool) {
	if c.hasCur && t.MessageID != "" && t.MessageID == c.curID {
		merged := mergeTurn(*c.cur, t)
		c.cur = &merged
		return Turn{}, false
	}
	done, ok := c.Flush()
	tc := t
	c.cur = &tc
	c.hasCur = true
	c.curID = t.MessageID
	return done, ok
}

// Flush completes and returns the in-progress message, if any, and clears it.
// Returns (zero, false) when nothing is pending. Call at end-of-stream (or on a
// non-assistant line) so the final message isn't left uncounted.
func (c *Coalescer) Flush() (Turn, bool) {
	if !c.hasCur {
		return Turn{}, false
	}
	out := *c.cur
	c.cur = nil
	c.hasCur = false
	c.curID = ""
	return out, true
}

func convertUsage(u *rawUsage) Usage {
	out := Usage{
		InputTokens:     u.Input,
		OutputTokens:    u.Output,
		CacheReadTokens: u.CacheRead,
	}
	if u.CacheCreaSub != nil {
		out.CacheCreate5mTokens = u.CacheCreaSub.Ephemeral5m
		out.CacheCreate1hTokens = u.CacheCreaSub.Ephemeral1h
	} else {
		// Older sessions only have the flat cache_creation_input_tokens.
		// Bucket the whole thing as 5m (the default tier) so we don't lose it.
		out.CacheCreate5mTokens = u.CacheCreate
	}
	return out
}

// peekParentLink decodes only the uuid and parentUuid fields off a line.
// Cheaper than the full rawLine decode and tolerates any line shape; we
// only use it to build the parent-link index for chain walking.
func peekParentLink(line []byte) (uuid, parentUUID string) {
	if len(line) == 0 {
		return "", ""
	}
	var raw struct {
		UUID       string `json:"uuid"`
		ParentUUID string `json:"parentUuid"`
	}
	if err := json.Unmarshal(line, &raw); err != nil {
		return "", ""
	}
	return raw.UUID, raw.ParentUUID
}

// extractUserText pulls the human-readable text out of a user message's
// content. Returns hasToolResult=true if any block is a tool_result —
// callers skip those entirely because the spec attributes cost only to
// non-tool-result user messages (i.e. real prompts and slash commands).
//
// Content may be a JSON string (older sessions) or an array of typed
// blocks (newer). For arrays, text blocks are joined with newlines.
func extractUserText(content json.RawMessage) (text string, hasToolResult bool) {
	if len(content) == 0 {
		return "", false
	}
	if content[0] == '"' {
		var s string
		if err := json.Unmarshal(content, &s); err == nil {
			return s, false
		}
		return "", false
	}
	var entries []rawUserContentEntry
	if err := json.Unmarshal(content, &entries); err != nil {
		return "", false
	}
	var b strings.Builder
	for _, e := range entries {
		if e.Type == "tool_result" {
			return "", true
		}
		if e.Type == "text" && e.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(e.Text)
		}
	}
	return b.String(), false
}

// extractAssistantText pulls the assistant message's reasoning and narration
// out of its content array as two distinct strings: thinking joins the
// `thinking` blocks (extended-thinking reasoning), text joins the `text`
// narration blocks. Multiple blocks of each kind join with newlines.
// tool_use and other block types are ignored.
//
// Content may be a bare JSON string (older sessions) which carries no typed
// blocks, so we return "","" for that case the way other extractors do.
func extractAssistantText(content json.RawMessage) (thinking, text string) {
	if len(content) == 0 || content[0] == '"' {
		return "", ""
	}
	var entries []rawContentEntry
	if err := json.Unmarshal(content, &entries); err != nil {
		return "", ""
	}
	var tb, xb strings.Builder
	for _, e := range entries {
		switch e.Type {
		case "thinking":
			if e.Thinking != "" {
				if tb.Len() > 0 {
					tb.WriteByte('\n')
				}
				tb.WriteString(e.Thinking)
			}
		case "text":
			if e.Text != "" {
				if xb.Len() > 0 {
					xb.WriteByte('\n')
				}
				xb.WriteString(e.Text)
			}
		}
	}
	return tb.String(), xb.String()
}

// toolResultMaxChars bounds the per-result snippet we retain — enough to
// see an error message or short stdout without ballooning the payload.
const toolResultMaxChars = 2000

// peekToolResults decodes a raw line and returns any tool_result blocks in
// its user-message content. Separate from ParseLine (which drops the
// carrying user turn) so streaming consumers keep their existing contract
// while the corpus path still captures outcomes. Returns nil for any line
// that isn't a user message with tool_result blocks.
func peekToolResults(line []byte) []ToolResult {
	if len(line) == 0 {
		return nil
	}
	var raw rawLine
	if err := json.Unmarshal(line, &raw); err != nil || raw.Type != "user" || len(raw.Message) == 0 {
		return nil
	}
	var msg rawMessage
	if err := json.Unmarshal(raw.Message, &msg); err != nil || len(msg.Content) == 0 || msg.Content[0] != '[' {
		return nil
	}
	var entries []rawToolResultEntry
	if err := json.Unmarshal(msg.Content, &entries); err != nil {
		return nil
	}
	ts, _ := time.Parse(time.RFC3339, raw.Timestamp)
	var out []ToolResult
	for _, e := range entries {
		if e.Type != "tool_result" {
			continue
		}
		out = append(out, ToolResult{
			ToolUseID: e.ToolUseID,
			IsError:   e.IsError,
			Content:   truncateRunes(toolResultText(e.Content), toolResultMaxChars),
			Timestamp: ts,
		})
	}
	return out
}

// toolResultText flattens a tool_result `content` — a bare JSON string or
// an array of {type:"text",text} blocks — to plain text. Joins multiple
// text blocks with newlines; ignores non-text blocks (e.g. images).
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
		return ""
	}
	var entries []rawUserContentEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		if e.Type == "text" && e.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func extractToolUses(content json.RawMessage) []ToolUse {
	if len(content) == 0 {
		return nil
	}
	var entries []rawContentEntry
	if err := json.Unmarshal(content, &entries); err != nil {
		return nil
	}
	var out []ToolUse
	for _, e := range entries {
		if e.Type != "tool_use" {
			continue
		}
		tu := ToolUse{ID: e.ID, Name: e.Name}
		if len(e.Input) > 0 && (e.Name == "Skill" || e.Name == "SlashCommand" || e.Name == "Agent") {
			var in rawSkillInput
			if err := json.Unmarshal(e.Input, &in); err == nil {
				tu.SkillName = in.Skill
				tu.SlashCommand = in.Command
				tu.SubagentType = in.SubagentType
			}
		}
		tu.Detail = extractDetail(e.Name, e.Input)
		tu.Input = extractToolInput(e.Name, e.Input)
		out = append(out, tu)
	}
	return out
}

// IsSubagentFile reports whether path is one of the
// `<encoded-cwd>/<sessionId>/subagents/agent-*.jsonl` files.
func IsSubagentFile(path string) bool {
	dir, file := filepath.Split(path)
	if !strings.HasPrefix(file, "agent-") || !strings.HasSuffix(file, ".jsonl") {
		return false
	}
	return filepath.Base(filepath.Clean(dir)) == "subagents"
}

// SubagentMeta is the content of a sibling agent-<id>.meta.json file —
// Claude Code writes one alongside every subagent jsonl, naming the
// subagent type and the description from the launching Agent tool_use.
type SubagentMeta struct {
	AgentType   string
	Description string
	// ToolUseID is the id of the Agent tool_use in the parent session that
	// launched this sub-agent — the exact reverse link from child to the
	// spawning call. Empty for older metas that predate the field.
	ToolUseID string
}

// ReadSubagentMeta loads the sibling .meta.json next to jsonlPath. Returns
// the meta and true if found and parseable, zero value + false otherwise.
func ReadSubagentMeta(jsonlPath string) (SubagentMeta, bool) {
	metaPath := strings.TrimSuffix(jsonlPath, ".jsonl") + ".meta.json"
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return SubagentMeta{}, false
	}
	var raw struct {
		AgentType   string `json:"agentType"`
		Description string `json:"description"`
		ToolUseID   string `json:"toolUseId"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return SubagentMeta{}, false
	}
	return SubagentMeta{
		AgentType:   raw.AgentType,
		Description: raw.Description,
		ToolUseID:   raw.ToolUseID,
	}, true
}

// SubagentTypeFor returns the agentType from the sibling agent-*.meta.json,
// or "" if it doesn't exist or can't be parsed. Thin wrapper around
// ReadSubagentMeta for callers that only need the type.
func SubagentTypeFor(jsonlPath string) string {
	m, _ := ReadSubagentMeta(jsonlPath)
	return m.AgentType
}

// DecodeProjectDir converts an encoded directory name (leading dash + dashes
// for slashes) back into the absolute project path.
//
// The encoding is lossy — a real `-` in the path becomes `--` in the
// directory name, which we round-trip by collapsing `--` to `-`.
func DecodeProjectDir(name string) string {
	if !strings.HasPrefix(name, "-") {
		return name
	}
	// Replace -- with a sentinel, swap remaining - for /, then restore -.
	const sentinel = "\x00"
	s := strings.ReplaceAll(name, "--", sentinel)
	s = strings.ReplaceAll(s, "-", "/")
	s = strings.ReplaceAll(s, sentinel, "-")
	return s
}
