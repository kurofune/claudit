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
	Name         string
	SkillName    string // when Name == "Skill"
	SlashCommand string // when Name == "SlashCommand"
	SubagentType string // when Name == "Agent"
	// Detail is a per-tool drill-down key, populated by detail.go's extractor:
	// "git status" for Bash, ".go" for Read/Edit/Write, "github.com" for
	// WebFetch, etc. Empty when no useful sub-key applies.
	Detail string
}

// Turn is one assistant message — the only event type that costs money.
type Turn struct {
	SessionID  string
	UUID       string
	ParentUUID string
	Sidechain  bool
	Timestamp  time.Time
	CWD        string
	Model      string
	Usage      Usage
	ToolUses   []ToolUse
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
	Message    json.RawMessage `json:"message"`
	IsMeta     bool            `json:"isMeta"`
}

type rawMessage struct {
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
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
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
		return Turn{
			SessionID:  raw.SessionID,
			UUID:       raw.UUID,
			ParentUUID: raw.ParentUUID,
			Sidechain:  raw.Sidechain,
			Timestamp:  ts,
			CWD:        raw.CWD,
			Model:      msg.Model,
			Usage:      convertUsage(msg.Usage),
			ToolUses:   extractToolUses(msg.Content),
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
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	return res, nil
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
		tu := ToolUse{Name: e.Name}
		if len(e.Input) > 0 && (e.Name == "Skill" || e.Name == "SlashCommand" || e.Name == "Agent") {
			var in rawSkillInput
			if err := json.Unmarshal(e.Input, &in); err == nil {
				tu.SkillName = in.Skill
				tu.SlashCommand = in.Command
				tu.SubagentType = in.SubagentType
			}
		}
		tu.Detail = extractDetail(e.Name, e.Input)
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
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return SubagentMeta{}, false
	}
	return SubagentMeta{AgentType: raw.AgentType, Description: raw.Description}, true
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
