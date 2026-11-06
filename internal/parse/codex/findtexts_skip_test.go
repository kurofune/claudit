package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/parse"
)

// everyText asks FindTexts for every turn, prompt and tool input a rollout
// holds.
func everyText(t *testing.T, raw []byte, path string) parse.TextRequest {
	t.Helper()
	caps := fullText
	caps.skipUnread = false
	res, err := parseFile(bytes.NewReader(raw), path, caps)
	if err != nil {
		t.Fatal(err)
	}
	want := parse.TextRequest{Turns: map[string]bool{}, Users: map[string]bool{}, Tools: map[string]bool{}}
	for _, tr := range res.Turns {
		want.Turns[tr.UUID] = true
		for _, u := range tr.ToolUses {
			want.Tools[u.ID] = true
		}
	}
	for _, m := range res.UserMessages {
		want.Users[m.UUID] = true
	}
	return want
}

// findTextsUnskipped is FindTexts decoding every line.
func findTextsUnskipped(raw []byte, path string, want parse.TextRequest) parse.FullTexts {
	caps := fullText
	caps.skipUnread, caps.skipCalls = false, false
	res, _ := parseFile(bytes.NewReader(raw), path, caps)
	return textsOf(res, want)
}

// TestFindTexts_SkippingUnreadLinesChangesNothing: FindTexts, which skips
// the lines no text comes from (tool outputs, reasoning, events other than
// token counts, compactions), answers what decoding every line answers, over
// every testdata rollout, one whose lines carry an ordinal and reordered
// keys, and, with CLAUDIT_CODEX_ROLLOUT set, a real rollout.
func TestFindTexts_SkippingUnreadLinesChangesNothing(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("testdata", "*.jsonl"))
	if real := os.Getenv("CLAUDIT_CODEX_ROLLOUT"); real != "" {
		paths = append(paths, real)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		variants := map[string][]byte{"as written": raw, "ordinal and payload first": reshape(raw)}
		for name, b := range variants {
			all := everyText(t, b, p)
			noTools := all
			noTools.Tools = map[string]bool{}
			if len(all.Tools) == 0 && strings.Contains(p, "tools") {
				t.Errorf("%s asks for no tool input; the fixture lost its calls", p)
			}
			for asked, want := range map[string]parse.TextRequest{"everything": all, "no tool inputs": noTools} {
				got := FindTexts(bytes.NewReader(b), p, want)
				ref := findTextsUnskipped(b, p, want)
				if !reflect.DeepEqual(got, ref) {
					t.Errorf("%s (%s, %s): skipping changed FindTexts:\n got %+v\nwant %+v", p, name, asked, got, ref)
				}
			}
		}
	}
}

// reshape rewrites each record as {"timestamp","ordinal","type","payload"}
// on even lines and {"payload","type","timestamp"} on odd ones.
func reshape(raw []byte) []byte {
	var out bytes.Buffer
	for i, line := range bytes.Split(raw, []byte("\n")) {
		var rec map[string]json.RawMessage
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &rec) != nil {
			out.Write(line)
			out.WriteByte('\n')
			continue
		}
		keys := []string{"timestamp", "type", "payload"}
		if i%2 == 0 {
			rec["ordinal"] = json.RawMessage(strconv.Itoa(i))
			keys = []string{"timestamp", "ordinal", "type", "payload"}
		} else {
			keys = []string{"payload", "type", "timestamp"}
		}
		out.WriteByte('{')
		for j, k := range keys {
			if j > 0 {
				out.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			out.Write(kb)
			out.WriteByte(':')
			v := rec[k]
			if v == nil {
				v = json.RawMessage("null")
			}
			out.Write(v)
		}
		out.WriteString("}\n")
	}
	return out.Bytes()
}

// TestSkipUnread names the lines FindTexts may skip, and keeps every line
// it cannot place.
func TestSkipUnread(t *testing.T) {
	for line, want := range map[string]bool{
		`{"timestamp":"t","ordinal":3,"type":"response_item","payload":{"type":"function_call_output","call_id":"c"}}`: true,
		`{"timestamp":"t","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c"}}`:          true,
		`{"timestamp":"t","type":"response_item","payload":{"type":"reasoning","summary":[]}}`:                         true,
		`{"timestamp":"t","type":"event_msg","payload":{"type":"exec_command_output_delta"}}`:                          true,
		`{"timestamp":"t","type":"compacted","payload":{"message":"x"}}`:                                               true,
		`{"timestamp":"t","type":"inter_agent_communication_metadata","payload":{}}`:                                   true,
		`{"timestamp":"t","type":"event_msg","payload":{"type":"token_count","info":null}}`:                            false,
		`{"timestamp":"t","type":"response_item","payload":{"type":"message","role":"user"}}`:                          false,
		`{"timestamp":"t","type":"response_item","payload":{"type":"function_call","call_id":"c"}}`:                    false,
		`{"timestamp":"t","type":"session_meta","payload":{"id":"x"}}`:                                                 false,
		`{"timestamp":"t","type":"turn_context","payload":{"model":"m"}}`:                                              false,
		`{"timestamp":"t","type":"token_usage_record","payload":{"response_id":"r"}}`:                                  false,
		`{"payload":{"type":"function_call_output"},"type":"response_item"}`:                                           false,
		`{"timestamp":"t","type":"response_item","payload":{"call_id":"c","type":"function_call_output"}}`:             false,
		`{"timestamp":{"nested":1},"type":"compacted"}`:                                                                false,
		`not json`: false,
	} {
		if got := skipUnread([]byte(line), true); got != want {
			t.Errorf("skipUnread(%s) = %v, want %v", line, got, want)
		}
		call := strings.Contains(line, `"payload":{"type":"function_call",`)
		if got := skipUnread([]byte(line), false); got != (want || call) {
			t.Errorf("skipUnread(%s) without calls = %v, want %v", line, got, want || call)
		}
	}
}
