// Package codex parses Codex CLI rollouts (~/.codex/sessions/YYYY/MM/DD/
// rollout-<ts>-<uuid>.jsonl) into the provider-neutral parse surface.
//
// A rollout is one thread. Each line is {timestamp, type, payload}; the
// records modeled here are session_meta (thread identity), turn_context
// (the model in effect until the next one), token_usage_record (one per
// model response — one Turn), the response_item user and assistant messages,
// tool calls and their outputs, compacted (a context compaction, one
// CompactBoundary), and event_msg token_count (the per-response usage
// of rollouts written before token_usage_record existed). Every other record type is skipped.
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
	Info *struct {
		LastTokenUsage *rawUsage `json:"last_token_usage"`
	} `json:"info"`
}

// response gathers the tool calls and assistant text written before the
// usage reading that closes a model response.
type response struct {
	tools []parse.ToolUse
	text  string
}

// turn closes the gathered response into a Turn and starts the next one.
// prompt is the UUID of the user prompt the response answers.
func (rsp *response) turn(id, prompt string, ts time.Time, model, cwd string, u rawUsage, path string) parse.Turn {
	t := parse.Turn{
		Provider:     parse.ProviderCodex,
		MessageID:    id,
		UUID:         id,
		ParentUUID:   prompt,
		Timestamp:    ts,
		EndTimestamp: ts,
		CWD:          cwd,
		Model:        model,
		Usage:        convertUsage(u),
		ToolUses:     rsp.tools,
		Text:         rsp.text,
		SourceFile:   path,
	}
	*rsp = response{}
	return t
}

// legacyTurn is a Turn read from an event_msg token_count's last_token_usage.
// Its id needs the thread id, which session_meta may not have supplied yet,
// so the token_count's line number is kept until the file ends.
type legacyTurn struct {
	line int
	turn parse.Turn
}

// ParseFile streams one rollout. path is recorded on each Turn and names the
// thread: a subagent rollout's SessionID is the uuid its file name carries.
//
// Each user message carrying the person's own text becomes a UserMessage,
// and every Turn after it, up to the next one, takes its UUID as ParentUUID.
//
// Turns come from token_usage_record. A rollout written before that record
// existed prices from each token_count's last_token_usage instead; the two
// never mix within one file, since a newer rollout carries both for the
// same responses.
func ParseFile(r io.Reader, path string) (parse.Result, error) {
	var (
		res     parse.Result
		meta    rawSessionMeta
		model   string
		turnCWD string
		// cur gathers toward the next token_usage_record, old toward the
		// next token_count reading.
		cur, old   response
		legacy     []legacyTurn
		lastUsage  *rawUsage
		lineNumber int
		// prompt is the latest real user prompt's UUID, provisional until
		// stampThread names the thread.
		prompt string
	)
	sc := bufio.NewScanner(r)
	// session_meta carries the full base instructions and tool outputs can
	// run to megabytes, so lines routinely exceed bufio's 64 KB default.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		lineNumber++
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
		case "compacted":
			// The thread's context was summarized here: every tool output
			// before this record leaves the next response's prompt.
			res.CompactBoundaries = append(res.CompactBoundaries, parse.CompactBoundary{Timestamp: ts, SourceFile: path})
		case "token_usage_record":
			var tu rawTokenUsageRecord
			if err := json.Unmarshal(rec.Payload, &tu); err != nil || tu.Usage == nil {
				res.Malformed++
				continue
			}
			// A response's tool calls and messages are written before its
			// usage record.
			res.Turns = append(res.Turns, cur.turn(tu.ResponseID, prompt, ts, model, turnCWD, *tu.Usage, path))
		case "response_item":
			var kind rawKind
			if err := json.Unmarshal(rec.Payload, &kind); err != nil {
				res.Malformed++
				continue
			}
			if kind.Type == "message" {
				// A message whose content is not the modeled item list is an
				// unmodeled shape, skipped like any other.
				var m rawMessage
				if json.Unmarshal(rec.Payload, &m) != nil {
					continue
				}
				if text := m.userText(); text != "" {
					prompt = "u" + strconv.Itoa(lineNumber)
					res.UserMessages = append(res.UserMessages, parse.UserMessage{
						UUID:       prompt,
						Timestamp:  ts,
						CWD:        turnCWD,
						Text:       parse.PromptSnippet(text),
						SourceFile: path,
					})
					continue
				}
				msg := m.assistantText()
				cur.text = parse.JoinTurnText(cur.text, msg)
				old.text = parse.JoinTurnText(old.text, msg)
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
				use := toolUse(it)
				cur.tools = append(cur.tools, use)
				old.tools = append(old.tools, use)
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
			// Codex repeats a reading when only rate_limits change; a
			// repeat is the same response, counted once.
			if ev.Info != nil && ev.Info.LastTokenUsage != nil {
				if u := *ev.Info.LastTokenUsage; lastUsage == nil || u != *lastUsage {
					legacy = append(legacy, legacyTurn{lineNumber, old.turn("", prompt, ts, model, turnCWD, u, path)})
					lastUsage = &u
				}
			}
		}
	}
	if len(res.Turns) == 0 && len(legacy) > 0 {
		thread := threadID(path, meta.ID)
		for _, l := range legacy {
			l.turn.MessageID = thread + ":" + strconv.Itoa(l.line)
			l.turn.UUID = l.turn.MessageID
			res.Turns = append(res.Turns, l.turn)
		}
		cur = old
	}
	// A message after the last usage reading still belongs to that response.
	if cur.text != "" && len(res.Turns) > 0 {
		last := &res.Turns[len(res.Turns)-1]
		last.Text = parse.JoinTurnText(last.Text, cur.text)
	}
	stampThread(&res, meta, path)
	return res, sc.Err()
}

// stampThread applies the thread identity from session_meta to everything
// the file yielded, so record order within the file does not matter. A user
// prompt's provisional UUID (its line) gains the thread id, which makes it
// unique across rollouts.
func stampThread(res *parse.Result, meta rawSessionMeta, path string) {
	thread := threadID(path, meta.ID)
	sessionID := meta.SessionID
	subagent := meta.ThreadSource == "subagent"
	if subagent {
		sessionID = thread
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
		if t.ParentUUID != "" {
			t.ParentUUID = thread + ":" + t.ParentUUID
		}
	}
	for i := range res.CompactBoundaries {
		res.CompactBoundaries[i].SessionID = sessionID
	}
	for i := range res.UserMessages {
		m := &res.UserMessages[i]
		m.UUID = thread + ":" + m.UUID
		m.SessionID = sessionID
		if m.CWD == "" {
			m.CWD = meta.CWD
		}
	}
}

var uuidPattern = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// threadID is the uuid ending a rollout's file name, or fallback when the
// name carries none.
func threadID(path, fallback string) string {
	name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".zst"), ".jsonl")
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
// custom tool with a resolvable command, or the exec_command function) is
// named Bash, keeps its command string as Input and buckets by its Bash
// pattern; any other call keeps its raw name and input.
func toolUse(it rawItem) parse.ToolUse {
	input, cmd := toolInput(it)
	tu := parse.ToolUse{ID: it.CallID, Name: toolName(it, cmd), Input: parse.ToolInputSnippet(input)}
	if cmd != "" {
		tu.Detail = parse.BashPattern(cmd)
	}
	return tu
}

// toolName is Bash for a call that runs the shell command cmd, else the
// call's raw name.
func toolName(it rawItem, cmd string) string {
	if cmd != "" {
		return parse.BashToolName
	}
	return it.Name
}

// toolInput is a call's untruncated input as ToolUse.Input represents it —
// the shell command when the call runs one (cmd, also returned), else the
// call's raw input.
func toolInput(it rawItem) (input, cmd string) {
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
		return cmd, cmd
	}
	return input, ""
}

// FindToolUseDetail streams r (one rollout) and returns the untruncated input
// and output of the call whose call_id == callID, in the representation
// ParseFile snippets. found is false when no call in r carried that id;
// Output and Status stay empty when no output was recorded.
func FindToolUseDetail(r io.Reader, callID string) (parse.ToolUseDetail, bool) {
	out := parse.ToolUseDetail{ID: callID}
	found := false
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		var rec rawRecord
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Type != "response_item" {
			continue
		}
		var it rawItem
		if json.Unmarshal(rec.Payload, &it) != nil || it.CallID != callID {
			continue
		}
		switch it.Type {
		case "custom_tool_call", "function_call":
			var cmd string
			out.Input, cmd = toolInput(it)
			out.Name = toolName(it, cmd)
			found = true
		case "custom_tool_call_output", "function_call_output":
			out.Output = outputText(it.Output)
			out.Status = "ok"
			if exitError(out.Output) {
				out.Status = "error"
			}
		}
	}
	return out, found
}

// execCall matches the opening of a tools.exec_command({...}) call inside
// the JavaScript an exec custom tool runs, through the object's brace.
var execCall = regexp.MustCompile(`exec_command\(\s*\{`)

// execCommands returns the final cmd string of every exec_command call in an exec
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

// objectCmd walks every top-level property of the object literal whose body
// starts at s and returns the cmd value JavaScript would see: the last cmd
// property wins, as it does at runtime. That value resolves only when it is
// one complete static string literal (the literal must end the property's
// value, and a template literal must not interpolate) and nothing after it
// can override it. A spread or a computed key may set cmd, so either one
// leaves cmd unresolved until a later static cmd property overrides it. Any
// shape it cannot follow (a method, an accessor, an unclosed object) yields
// no command.
func objectCmd(s string) (string, bool) {
	cmd, resolved := "", false
	i := 0
	for {
		i = skipSpace(s, i)
		if i >= len(s) {
			return "", false
		}
		if s[i] == '}' {
			return cmd, resolved
		}
		if strings.HasPrefix(s[i:], "...") || s[i] == '[' {
			n, ok := skipValue(s[i:])
			if !ok {
				return "", false
			}
			cmd, resolved = "", false
			if i = skipSpace(s, i+n); i < len(s) && s[i] == ',' {
				i++
			}
			continue
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
		case ',', '}':
			if key == "cmd" {
				cmd, resolved = "", false
			}
			if s[i] == ',' {
				i++
			}
			continue
		case ':':
		default:
			return "", false
		}
		i = skipSpace(s, i+1)
		if key == "cmd" {
			cmd, resolved = "", false
			if v, n, ok := jsString(s[i:]); ok && !(s[i] == '`' && interpolates(s[i:i+n])) {
				if j := skipSpace(s, i+n); j < len(s) && (s[j] == ',' || s[j] == '}') {
					cmd, resolved = v, true
				}
			}
		}
		n, ok := skipValue(s[i:])
		if !ok {
			return "", false
		}
		i = skipSpace(s, i+n)
		if i < len(s) && s[i] == ',' {
			i++
		}
	}
}

// skipValue returns the length of the expression at the start of s: up to
// the comma or closing brace that ends it at the top level, stepping over
// string and template literals, comments and nested brackets. A slash that
// opens no comment starts a regular expression or a division, which it cannot
// tell apart without parsing, so the expression cannot be followed.
func skipValue(s string) (int, bool) {
	depth := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case '"', '\'':
			_, n, ok := jsString(s[i:])
			if !ok {
				return 0, false
			}
			i += n
			continue
		case '`':
			n, ok := skipTemplate(s[i:])
			if !ok {
				return 0, false
			}
			i += n
			continue
		case '/':
			n, ok := skipComment(s[i:])
			if !ok || n == 0 {
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

// skipTemplate returns the length of the template literal at the start of s,
// following each ${...} substitution as an expression so a brace, quote or
// backtick inside one does not end the literal early.
func skipTemplate(s string) (int, bool) {
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] == '\\':
			i++
		case s[i] == '`':
			return i + 1, true
		case s[i] == '$' && i+1 < len(s) && s[i+1] == '{':
			n, ok := skipValue(s[i+2:])
			if !ok || s[i+2+n] != '}' {
				return 0, false
			}
			i += 2 + n
		}
	}
	return 0, false
}

// skipComment returns the length of the comment at the start of s, 0 when
// none starts there, and false when a block comment never closes.
func skipComment(s string) (int, bool) {
	switch {
	case strings.HasPrefix(s, "//"):
		for i := 2; i < len(s); i++ {
			if s[i] == '\n' || s[i] == '\r' || strings.HasPrefix(s[i:], "\u2028") || strings.HasPrefix(s[i:], "\u2029") {
				return i, true
			}
		}
		return len(s), true
	case strings.HasPrefix(s, "/*"):
		end := strings.Index(s[2:], "*/")
		if end < 0 {
			return 0, false
		}
		return end + 4, true
	}
	return 0, true
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

// skipSpace returns the index of the first byte at or after i that is
// neither whitespace nor inside a comment; len(s) when a block comment never
// closes, so the caller sees the source end there.
func skipSpace(s string, i int) int {
	for i < len(s) {
		if strings.ContainsRune(" \t\r\n", rune(s[i])) {
			i++
			continue
		}
		n, ok := skipComment(s[i:])
		if !ok {
			return len(s)
		}
		if n == 0 {
			return i
		}
		i += n
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

type rawMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// injectedPrefixes open the input_text items Codex writes into a user
// message on the person's behalf: the AGENTS.md instructions, the
// environment context, a goal reminder and the guardian's tool list.
var injectedPrefixes = []string{
	"# AGENTS.md instructions for",
	"<environment_context>",
	"<codex_internal_context",
	"<guardian_tool_descriptions>",
}

// userText is a user message's own input_text items, joined; "" for a
// message from any other role or one holding only injected items.
func (m rawMessage) userText() string {
	if m.Role != "user" {
		return ""
	}
	var parts []string
	for _, c := range m.Content {
		text := strings.TrimSpace(c.Text)
		if c.Type != "input_text" || text == "" || injected(text) {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}

func injected(text string) bool {
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// assistantText is an assistant message's output_text, joined; "" for a
// message from any other role.
func (m rawMessage) assistantText() string {
	if m.Role != "assistant" {
		return ""
	}
	out := ""
	for _, c := range m.Content {
		if c.Type == "output_text" && c.Text != "" {
			out = parse.JoinTurnText(out, c.Text)
		}
	}
	return out
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

// ExitEnvelope splits the header Codex writes above a command's output:
// an "Exit code: N" (or "Process exited with code N") line, perhaps with
// others such as "Wall time:", then an "Output:" line. It returns N and the
// text after the "Output:" line; ok is false when text carries no such
// envelope.
func ExitEnvelope(text string) (code int, body string, ok bool) {
	head, body, found := strings.Cut(text, "\nOutput:\n")
	if !found {
		head, found = strings.CutSuffix(text, "\nOutput:")
		if !found {
			return 0, "", false
		}
	}
	m := exitHeader.FindStringSubmatch(head)
	if m == nil {
		return 0, "", false
	}
	code, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}
	return code, body, true
}

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
