// Package codex parses Codex CLI rollouts (~/.codex/sessions/YYYY/MM/DD/
// rollout-<ts>-<uuid>.jsonl) into the provider-neutral parse surface.
//
// A rollout is one thread. Each line is {timestamp, type, payload}; the
// records modeled here are session_meta (thread identity), turn_context
// (the model in effect until the next one), token_usage_record (one per
// model response — one Turn), the response_item tool calls and their
// outputs, and event_msg token_count (plan-usage readings). Every other
// record type is skipped.
package codex

import (
	"bufio"
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// rawKind is a response_item or event_msg payload's discriminator, decoded
// before the payload itself so an unmodeled kind is never held to a modeled
// kind's field types.
type rawKind struct {
	Type string `json:"type"`
}

type rawRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type rawSessionMeta struct {
	SessionID      string `json:"session_id"`
	ID             string `json:"id"`
	CWD            string `json:"cwd"`
	Originator     string `json:"originator"`
	ThreadSource   string `json:"thread_source"`
	ParentThreadID string `json:"parent_thread_id"`
}

type rawTurnContext struct {
	Model string `json:"model"`
	CWD   string `json:"cwd"`
}

type rawUsage struct {
	Input      int `json:"input_tokens"`
	Cached     int `json:"cached_input_tokens"`
	CacheWrite int `json:"cache_write_input_tokens"`
	Output     int `json:"output_tokens"`
	Reasoning  int `json:"reasoning_output_tokens"`
}

// rawTokenUsageRecord omits turn_token_usage and thread_token_usage: they
// are running totals, and summing them would count every response again.
type rawTokenUsageRecord struct {
	ResponseID string    `json:"response_id"`
	Usage      *rawUsage `json:"usage"`
}

type rawItem struct {
	Type      string          `json:"type"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Input     string          `json:"input"`
	Arguments string          `json:"arguments"`
	Output    json.RawMessage `json:"output"`
}

type rawEvent struct {
	RateLimits *struct {
		Primary *struct {
			UsedPercent   float64 `json:"used_percent"`
			WindowMinutes int     `json:"window_minutes"`
			ResetsAt      int64   `json:"resets_at"`
		} `json:"primary"`
		PlanType string `json:"plan_type"`
	} `json:"rate_limits"`
}

// ParseFile streams one rollout. path is recorded on each Turn and names the
// thread: a subagent rollout's SessionID is the uuid its file name carries.
func ParseFile(r io.Reader, path string) (parse.Result, error) {
	var (
		res     parse.Result
		meta    rawSessionMeta
		model   string
		turnCWD string
		pending []parse.ToolUse
	)
	sc := bufio.NewScanner(r)
	// session_meta carries the full base instructions and tool outputs can
	// run to megabytes, so lines routinely exceed bufio's 64 KB default.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			res.Malformed++
			continue
		}
		ts, _ := time.Parse(time.RFC3339, rec.Timestamp)
		switch rec.Type {
		case "session_meta":
			if err := json.Unmarshal(rec.Payload, &meta); err != nil {
				res.Malformed++
			}
		case "turn_context":
			var tc rawTurnContext
			if err := json.Unmarshal(rec.Payload, &tc); err != nil {
				res.Malformed++
				continue
			}
			model, turnCWD = tc.Model, tc.CWD
		case "token_usage_record":
			var tu rawTokenUsageRecord
			if err := json.Unmarshal(rec.Payload, &tu); err != nil || tu.Usage == nil {
				res.Malformed++
				continue
			}
			res.Turns = append(res.Turns, parse.Turn{
				Provider:     parse.ProviderCodex,
				MessageID:    tu.ResponseID,
				UUID:         tu.ResponseID,
				Timestamp:    ts,
				EndTimestamp: ts,
				CWD:          turnCWD,
				Model:        model,
				Usage:        convertUsage(*tu.Usage),
				ToolUses:     pending,
				SourceFile:   path,
			})
			// A response's tool calls are written before its usage record.
			pending = nil
		case "response_item":
			var kind rawKind
			if err := json.Unmarshal(rec.Payload, &kind); err != nil {
				res.Malformed++
				continue
			}
			call := kind.Type == "custom_tool_call" || kind.Type == "function_call"
			output := kind.Type == "custom_tool_call_output" || kind.Type == "function_call_output"
			if !call && !output {
				continue
			}
			var it rawItem
			if err := json.Unmarshal(rec.Payload, &it); err != nil {
				res.Malformed++
				continue
			}
			if call {
				pending = append(pending, toolUse(it))
				continue
			}
			text := outputText(it.Output)
			res.ToolResults = append(res.ToolResults, parse.NewToolResult(it.CallID, exitError(text), text, ts))
		case "event_msg":
			var kind rawKind
			if err := json.Unmarshal(rec.Payload, &kind); err != nil {
				res.Malformed++
				continue
			}
			if kind.Type != "token_count" {
				continue
			}
			var ev rawEvent
			if err := json.Unmarshal(rec.Payload, &ev); err != nil {
				res.Malformed++
				continue
			}
			if ev.RateLimits == nil || ev.RateLimits.Primary == nil {
				continue
			}
			p := ev.RateLimits.Primary
			res.RateLimitSamples = append(res.RateLimitSamples, parse.RateLimitSample{
				Provider:      parse.ProviderCodex,
				Timestamp:     ts,
				UsedPercent:   p.UsedPercent,
				WindowMinutes: p.WindowMinutes,
				ResetsAt:      time.Unix(p.ResetsAt, 0).UTC(),
				PlanType:      ev.RateLimits.PlanType,
			})
		}
	}
	stampThread(&res, meta, path)
	return res, sc.Err()
}

// stampThread applies the thread identity from session_meta to everything
// the file yielded, so record order within the file does not matter.
func stampThread(res *parse.Result, meta rawSessionMeta, path string) {
	sessionID := meta.SessionID
	subagent := meta.ThreadSource == "subagent"
	if subagent {
		sessionID = threadID(path, meta.ID)
		if meta.ParentThreadID != "" {
			res.ParentLinks = append(res.ParentLinks, parse.ParentLink{UUID: sessionID, ParentUUID: meta.ParentThreadID})
		}
	}
	for i := range res.Turns {
		t := &res.Turns[i]
		t.SessionID = sessionID
		t.Sidechain = subagent
		t.Entrypoint = meta.Originator
		if t.CWD == "" {
			t.CWD = meta.CWD
		}
	}
	for i := range res.RateLimitSamples {
		res.RateLimitSamples[i].SessionID = sessionID
	}
}

var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// threadID is the uuid ending a rollout's file name, or fallback when the
// name carries none.
func threadID(path, fallback string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if id := uuidPattern.FindString(name); id != "" {
		return id
	}
	return fallback
}

// convertUsage maps a Codex usage block onto parse.Usage. Codex counts cached
// tokens inside input_tokens and reasoning tokens inside output_tokens;
// parse.Usage keeps input and cache reads disjoint, so cached tokens come out
// of InputTokens, while reasoning stays inside OutputTokens and is also
// reported on its own.
func convertUsage(u rawUsage) parse.Usage {
	in := u.Input - u.Cached
	if in < 0 {
		in = 0
	}
	return parse.Usage{
		InputTokens:           in,
		CacheReadTokens:       u.Cached,
		CacheCreate5mTokens:   u.CacheWrite,
		OutputTokens:          u.Output,
		ReasoningOutputTokens: u.Reasoning,
	}
}

// toolUse maps a custom_tool_call or function_call. A shell call (the exec
// custom tool, or the exec_command function) keeps its command string as
// Input and buckets by its Bash pattern; any other call keeps its raw input.
func toolUse(it rawItem) parse.ToolUse {
	tu := parse.ToolUse{ID: it.CallID, Name: it.Name}
	var cmd, input string
	switch {
	case it.Type == "custom_tool_call" && it.Name == "exec":
		if cmds, ok := execCommands(it.Input); ok {
			cmd = strings.Join(cmds, "\n")
		}
		input = it.Input
	case it.Type == "custom_tool_call":
		input = it.Input
	case it.Name == "exec_command":
		var args struct {
			Cmd string `json:"cmd"`
		}
		if json.Unmarshal([]byte(it.Arguments), &args) == nil {
			cmd = args.Cmd
		}
		input = it.Arguments
	default:
		input = it.Arguments
	}
	if cmd != "" {
		tu.Input = parse.ToolInputSnippet(cmd)
		tu.Detail = parse.BashPattern(cmd)
		return tu
	}
	tu.Input = parse.ToolInputSnippet(input)
	return tu
}

// execCall matches the opening of a tools.exec_command({...}) call inside
// the JavaScript an exec custom tool runs, through the object's brace.
var execCall = regexp.MustCompile(`exec_command\(\s*\{`)

// execCommands returns the cmd string of every exec_command call in an exec
// tool's JavaScript source, wherever cmd sits among the object's properties,
// and reports whether every call's cmd resolved. A cmd that is anything but
// one complete static string literal (a variable, a concatenation, an
// interpolated template) cannot be resolved, so the source has no faithful
// command list and ok is false.
func execCommands(src string) (cmds []string, ok bool) {
	for _, loc := range execCall.FindAllStringIndex(src, -1) {
		s, ok := objectCmd(src[loc[1]:])
		if !ok {
			return nil, false
		}
		cmds = append(cmds, s)
	}
	return cmds, len(cmds) > 0
}

// objectCmd walks the top-level properties of the object literal whose body
// starts at s and returns its cmd property when that is one complete static
// string literal: the literal must end the property's value, and a template
// literal must not interpolate.
// Any shape it cannot follow (a spread, a computed key) yields no command.
func objectCmd(s string) (string, bool) {
	i := 0
	for {
		i = skipSpace(s, i)
		if i >= len(s) || s[i] == '}' {
			return "", false
		}
		var key string
		if c := s[i]; c == '"' || c == '\'' || c == '`' {
			k, n, ok := jsString(s[i:])
			if !ok {
				return "", false
			}
			key, i = k, i+n
		} else {
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			if j == i {
				return "", false
			}
			key, i = s[i:j], j
		}
		i = skipSpace(s, i)
		if i >= len(s) {
			return "", false
		}
		switch s[i] {
		case ',':
			i++
			continue
		case '}':
			return "", false
		case ':':
		default:
			return "", false
		}
		i = skipSpace(s, i+1)
		if key == "cmd" {
			v, n, ok := jsString(s[i:])
			if !ok || s[i] == '`' && interpolates(s[i:i+n]) {
				return "", false
			}
			if j := skipSpace(s, i+n); j >= len(s) || s[j] != ',' && s[j] != '}' {
				return "", false
			}
			return v, true
		}
		n, ok := skipValue(s[i:])
		if !ok {
			return "", false
		}
		i = skipSpace(s, i+n)
		if i >= len(s) || s[i] != ',' {
			return "", false
		}
		i++
	}
}

// skipValue returns the length of the expression at the start of s: up to
// the comma or closing brace that ends it at the top level, stepping over
// string literals and nested brackets.
func skipValue(s string) (int, bool) {
	depth := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '"', '\'', '`':
			_, n, ok := jsString(s[i:])
			if !ok {
				return 0, false
			}
			i += n
			continue
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth == 0 {
				return i, true
			}
			depth--
		case ',':
			if depth == 0 {
				return i, true
			}
		}
		i++
	}
	return 0, false
}

// interpolates reports whether the template literal lit holds an unescaped
// ${ substitution.
func interpolates(lit string) bool {
	for i := 1; i < len(lit)-1; i++ {
		switch {
		case lit[i] == '\\':
			i++
		case lit[i] == '$' && lit[i+1] == '{':
			return true
		}
	}
	return false
}

func skipSpace(s string, i int) int {
	for i < len(s) && strings.ContainsRune(" \t\r\n", rune(s[i])) {
		i++
	}
	return i
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// jsString decodes the JavaScript string literal at the start of s — single,
// double or backtick quoted — returning its value and the bytes it spans,
// and reports whether one was there.
func jsString(s string) (string, int, bool) {
	if s == "" || !strings.ContainsRune(`"'`+"`", rune(s[0])) {
		return "", 0, false
	}
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == q:
			return b.String(), i + 1, true
		case c != '\\':
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			return "", 0, false
		}
		switch e := s[i]; e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '0':
			b.WriteByte(0)
		case 'u', 'x':
			n := 4
			if e == 'x' {
				n = 2
			}
			if i+n >= len(s) {
				return "", 0, false
			}
			v, err := strconv.ParseUint(s[i+1:i+1+n], 16, 32)
			if err != nil {
				return "", 0, false
			}
			b.WriteRune(rune(v))
			i += n
		case '\n':
			// A line continuation contributes nothing.
		default:
			b.WriteByte(e)
		}
	}
	return "", 0, false
}

// outputText flattens a tool output — a bare string, or an array of
// {type: input_text, text} items — to plain text. Any other shape is kept
// as its JSON.
func outputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	case '[':
		var items []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &items) == nil {
			parts := make([]string, 0, len(items))
			for _, it := range items {
				if it.Text != "" {
					parts = append(parts, it.Text)
				}
			}
			return strings.Join(parts, "\n")
		}
	}
	return string(raw)
}

var (
	exitHeader = regexp.MustCompile(`(?m)^(?:Exit code: |Process exited with code )(-?\d+)\s*$`)
	exitField  = regexp.MustCompile(`"exit_code"\s*:\s*(-?\d+)`)
)

// exitError reports whether a tool output records a failure. Codex writes
// the outcome three ways: an "Exit code: N" (or "Process exited with code
// N") header above the "Output:" line; for the exec tool, a JSON result
// carrying "exit_code" per command it ran, any non-zero one failing the
// call; and a "Script failed" header when the exec script itself threw.
func exitError(text string) bool {
	head := text
	if i := strings.Index(head, "\nOutput:\n"); i >= 0 {
		head = head[:i]
	}
	if m := exitHeader.FindStringSubmatch(head); m != nil {
		return m[1] != "0"
	}
	if strings.HasPrefix(text, "Script failed") {
		return true
	}
	for _, m := range exitField.FindAllStringSubmatch(text, -1) {
		if m[1] != "0" {
			return true
		}
	}
	return false
}
