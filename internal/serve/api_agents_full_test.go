package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mkToolUseLine builds an assistant JSONL line carrying one Bash tool_use.
func mkToolUseLine(sessionID, uuid, cwd, toolID, command string, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"timestamp":%q,"sessionId":%q,"cwd":%q,"message":{"model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}],"usage":{"input_tokens":10,"output_tokens":2}}}`,
		uuid, ts.Format(time.RFC3339), sessionID, cwd, toolID, command,
	)
}

// mkToolResultLine builds a user JSONL line carrying the matching tool_result.
func mkToolResultLine(sessionID, uuid, cwd, toolID, content string, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"user","uuid":%q,"timestamp":%q,"sessionId":%q,"cwd":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":false,"content":%q}]}}`,
		uuid, ts.Format(time.RFC3339), sessionID, cwd, toolID, content,
	)
}

// toolFixtureServer seeds a session whose tool_use input/output exceed the
// 2000-rune snippet caps, so the full-load endpoint has something to expand.
func toolFixtureServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	bigCmd := "echo " + strings.Repeat("x", 5000)
	bigOut := strings.Repeat("y", 6000)
	writeJSONL(t, filepath.Join(dir, "s-tool.jsonl"),
		mkToolUseLine("s-tool", "a1", "/p/tool", "toolu_full", bigCmd, t0),
		mkToolResultLine("s-tool", "u2", "/p/tool", "toolu_full", bigOut, t0.Add(time.Second)),
	)
	return newTestServer(t, dir), bigCmd, bigOut
}

func TestAPIAgentsFull_ReturnsUntruncated(t *testing.T) {
	srv, bigCmd, bigOut := toolFixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session=s-tool&tool=toolu_full", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got struct {
		ID, Name, Input, Output, Status string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, w.Body.String())
	}
	if got.ID != "toolu_full" || got.Name != "Bash" {
		t.Errorf("ID/Name = %q/%q, want toolu_full/Bash", got.ID, got.Name)
	}
	if got.Input != bigCmd {
		t.Errorf("Input len = %d, want %d (untruncated)", len(got.Input), len(bigCmd))
	}
	if got.Output != bigOut {
		t.Errorf("Output len = %d, want %d (untruncated)", len(got.Output), len(bigOut))
	}
	if got.Status != "ok" {
		t.Errorf("Status = %q, want ok", got.Status)
	}
}

func TestAPIAgentsFull_MissingParams(t *testing.T) {
	srv, _, _ := toolFixtureServer(t)
	for _, target := range []string{
		"/_claudit/api/agents/full",
		"/_claudit/api/agents/full?session=s-tool",
		"/_claudit/api/agents/full?tool=toolu_full",
	} {
		w := doAPI(t, srv, http.MethodGet, target, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, w.Code)
		}
	}
}

// Exactly one of tool/turn must drive the lookup: both set (ambiguous) and
// neither set are 400s, matching the missing-param behavior.
func TestAPIAgentsFull_ToolAndTurnAreExclusive(t *testing.T) {
	srv, _, _ := turnFixtureServer(t)
	for _, target := range []string{
		"/_claudit/api/agents/full?session=s-think&tool=toolu_x&turn=turn-1",
		"/_claudit/api/agents/full?session=s-think",
	} {
		w := doAPI(t, srv, http.MethodGet, target, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, w.Code)
		}
	}
}

// An unknown session/tool 404s rather than leaking a 500 or an empty 200.
func TestAPIAgentsFull_NotFound(t *testing.T) {
	srv, _, _ := toolFixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session=s-tool&tool=nope", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

// An unknown turn uuid 404s rather than leaking a 500 or an empty 200.
func TestAPIAgentsFull_TurnNotFound(t *testing.T) {
	srv, _, _ := turnFixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session=s-think&turn=nope", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestAPIAgentsFull_MethodNotAllowed(t *testing.T) {
	srv, _, _ := toolFixtureServer(t)
	w := doAPI(t, srv, http.MethodPost, "/_claudit/api/agents/full?session=s-tool&tool=toolu_full", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
	if !strings.Contains(w.Header().Get("Allow"), "GET") {
		t.Errorf("Allow = %q, want GET listed", w.Header().Get("Allow"))
	}
}

// mkThinkingLine builds an assistant JSONL line carrying thinking + text
// blocks (no tool_use) with an explicit message.id.
func mkThinkingLine(sessionID, uuid, msgID, cwd, thinking, text string, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"timestamp":%q,"sessionId":%q,"cwd":%q,"message":{"id":%q,"model":"claude-opus-4-7","role":"assistant","content":[{"type":"thinking","thinking":%q},{"type":"text","text":%q}],"usage":{"input_tokens":10,"output_tokens":2}}}`,
		uuid, ts.Format(time.RFC3339), sessionID, cwd, msgID, thinking, text,
	)
}

// turnFixtureServer seeds a session whose turn's thinking/text exceed the
// 2000-rune snippet cap, so the full-load endpoint has something to expand.
func turnFixtureServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	bigThink := strings.Repeat("t", 5000)
	bigText := strings.Repeat("n", 6000)
	writeJSONL(t, filepath.Join(dir, "s-think.jsonl"),
		mkThinkingLine("s-think", "turn-1", "msg_1", "/p/think", bigThink, bigText, t0),
	)
	return newTestServer(t, dir), bigThink, bigText
}

// The /agents payload ships a capped snippet; /agents/full?turn= reads the
// untruncated thinking/text back from disk.
func TestAPIAgentsFull_TurnReturnsUntruncated(t *testing.T) {
	srv, bigThink, bigText := turnFixtureServer(t)

	// The list payload carries only the bounded snippet, keyed by the step uuid.
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("agents status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var graph struct {
		Sessions []struct {
			Main struct {
				Steps []struct {
					UUID     string `json:"uuid"`
					Thinking string `json:"thinking"`
					Text     string `json:"text"`
				} `json:"steps"`
			} `json:"main"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &graph); err != nil {
		t.Fatalf("unmarshal agents: %v; body=%s", err, w.Body.String())
	}
	if len(graph.Sessions) != 1 || len(graph.Sessions[0].Main.Steps) != 1 {
		t.Fatalf("want 1 session with 1 step, got %+v", graph)
	}
	step := graph.Sessions[0].Main.Steps[0]
	if step.UUID != "turn-1" {
		t.Errorf("step uuid = %q, want turn-1", step.UUID)
	}
	if n := len([]rune(step.Thinking)); n > 2001 || !strings.HasSuffix(step.Thinking, "…") {
		t.Errorf("payload Thinking runes = %d (ellipsis=%v), want capped snippet", n, strings.HasSuffix(step.Thinking, "…"))
	}
	if n := len([]rune(step.Text)); n > 2001 || !strings.HasSuffix(step.Text, "…") {
		t.Errorf("payload Text runes = %d (ellipsis=%v), want capped snippet", n, strings.HasSuffix(step.Text, "…"))
	}

	// The full endpoint expands it.
	w = doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session=s-think&turn=turn-1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("full status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct{ Thinking, Text string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal full: %v; body=%s", err, w.Body.String())
	}
	if got.Thinking != bigThink {
		t.Errorf("full Thinking len = %d, want %d (untruncated)", len(got.Thinking), len(bigThink))
	}
	if got.Text != bigText {
		t.Errorf("full Text len = %d, want %d (untruncated)", len(got.Text), len(bigText))
	}
}

// Redaction masks the turn's thinking/text with length-echoing markers.
func TestAPIAgentsFull_TurnRedact(t *testing.T) {
	srv, _, _ := turnFixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session=s-think&turn=turn-1&redact=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct{ Thinking, Text string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(got.Thinking, "[redacted") || !strings.HasPrefix(got.Text, "[redacted") {
		t.Errorf("Thinking/Text not redacted: %q / %q", got.Thinking, got.Text)
	}
}

// Redaction masks input/output content while preserving the structural fields.
func TestAPIAgentsFull_Redact(t *testing.T) {
	srv, _, _ := toolFixtureServer(t)
	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/agents/full?session=s-tool&tool=toolu_full&redact=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct{ Input, Output, Name string }
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "Bash" {
		t.Errorf("Name = %q, want Bash (structure survives redaction)", got.Name)
	}
	if !strings.HasPrefix(got.Input, "[redacted") || !strings.HasPrefix(got.Output, "[redacted") {
		t.Errorf("Input/Output not redacted: %q / %q", got.Input, got.Output)
	}
}
