package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/provider"
)

// mkSkillLine builds an assistant JSONL line carrying one Skill tool_use.
func mkSkillLine(sessionID, uuid, parentUUID, cwd, skill string, ts time.Time) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":%q,"cwd":%q,"message":{"model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"toolu_skill","name":"Skill","input":{"skill":%q}}],"usage":{"input_tokens":1000,"output_tokens":100}}}`,
		uuid, parentUUID, ts.Format(time.RFC3339), sessionID, cwd, skill,
	)
}

// mixedProviderServer serves the Claude Code fixture corpus, plus s-gamma
// (one prompt, one Skill call), beside the committed Codex rollouts.
func mixedProviderServer(t *testing.T) *Server {
	t.Helper()
	claude := fixtureCorpus(t)
	t0 := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(claude, "s-gamma.jsonl"),
		mkUserLineWithSession("s-gamma", "u4", "/p/gamma", "deploy it", t0),
		mkSkillLine("s-gamma", "a4", "u4", "/p/gamma", "deploy", t0.Add(time.Second)),
	)
	cache := corpus.New(provider.NewClaudeCode(claude), provider.NewCodex(codexFixtureRoot(t)))
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	return NewServer(cache, Options{Prices: loadPricesForTest(t)})
}

// providerOfModel names the fixture provider each model comes from.
func providerOfModel(t *testing.T, model string) string {
	t.Helper()
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "claude-code"
	case strings.HasPrefix(model, "gpt-"), model == "codex-auto-review":
		return "codex"
	}
	t.Fatalf("model %q belongs to no fixture provider", model)
	return ""
}

func getSection(t *testing.T, srv *Server, path, providerKey string) map[string]json.RawMessage {
	t.Helper()
	target := path + "?scope=all"
	if providerKey != "" {
		target += "&provider=" + providerKey
	}
	w := doAPI(t, srv, http.MethodGet, target, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d; body=%s", target, w.Code, w.Body.String())
	}
	return readJSONResponse(t, w)
}

func decodeField(t *testing.T, body map[string]json.RawMessage, key string, v any) {
	t.Helper()
	if err := json.Unmarshal(body[key], v); err != nil {
		t.Fatalf("decode %s: %v; raw %s", key, err, body[key])
	}
}

// providersOf maps each model to its provider and returns the set seen.
func providersOf(t *testing.T, models []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, m := range models {
		out[providerOfModel(t, m)] = true
	}
	return out
}

func wantProviders(providerKey string) map[string]bool {
	switch providerKey {
	case "claude-code":
		return map[string]bool{"claude-code": true}
	case "codex":
		return map[string]bool{"codex": true}
	}
	return map[string]bool{"claude-code": true, "codex": true}
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// TestAPICost_ProviderFilter: ?provider narrows Overview totals, every
// Spend tab and the Tools table to that provider's sessions.
func TestAPICost_ProviderFilter(t *testing.T) {
	srv := mixedProviderServer(t)
	// Fixture facts: three Claude Code sessions (s-alpha, s-beta, s-gamma)
	// with four turns; Codex tools.jsonl calls exec, Claude Code calls only Skill.
	allOV := getSection(t, srv, "/_claudit/api/overview", "")
	var all struct{ Sessions, Turns int }
	decodeField(t, allOV, "totals", &all)

	for _, pk := range []string{"", "claude-code", "codex"} {
		t.Run("provider="+pk, func(t *testing.T) {
			want := wantProviders(pk)

			cost := getSection(t, srv, "/_claudit/api/cost", pk)
			var byModel []struct{ Model string }
			decodeField(t, cost, "by_model", &byModel)
			models := make([]string, 0, len(byModel))
			for _, r := range byModel {
				models = append(models, r.Model)
			}
			if got := providersOf(t, models); !sameSet(got, want) {
				t.Errorf("by_model providers = %v (models %v), want %v", got, models, want)
			}

			var byProject []struct{ Project string }
			decodeField(t, cost, "by_project", &byProject)
			projects := map[string]bool{}
			for _, r := range byProject {
				projects[r.Project] = true
			}
			hasCodexProject := false
			for p := range projects {
				if strings.Contains(p, "proj") {
					hasCodexProject = true
				}
			}
			hasClaudeProject := false
			for p := range projects {
				if strings.Contains(p, "alpha") || strings.Contains(p, "beta") || strings.Contains(p, "gamma") {
					hasClaudeProject = true
				}
			}
			if hasCodexProject != want["codex"] || hasClaudeProject != want["claude-code"] {
				t.Errorf("by_project = %v, want codex=%v claude-code=%v", projects, want["codex"], want["claude-code"])
			}

			tok := getSection(t, srv, "/_claudit/api/tokens", pk)
			var tokModels []struct{ Model string }
			decodeField(t, tok, "by_model", &tokModels)
			tm := make([]string, 0, len(tokModels))
			for _, r := range tokModels {
				tm = append(tm, r.Model)
			}
			if got := providersOf(t, tm); !sameSet(got, want) {
				t.Errorf("tokens by_model providers = %v, want %v", got, want)
			}

			tools := getSection(t, srv, "/_claudit/api/tools", pk)
			var byTool []struct{ Tool string }
			decodeField(t, tools, "by_tool", &byTool)
			hasSkill, hasCodexTool := false, false
			for _, r := range byTool {
				if r.Tool == "Skill" {
					hasSkill = true
				} else {
					hasCodexTool = true
				}
			}
			if hasSkill != want["claude-code"] || hasCodexTool != want["codex"] {
				t.Errorf("by_tool = %+v, want Skill only with Claude Code and other tools only with Codex in scope", byTool)
			}

			ov := getSection(t, srv, "/_claudit/api/overview", pk)
			var tot struct{ Sessions, Turns int }
			decodeField(t, ov, "totals", &tot)
			switch pk {
			case "claude-code":
				if tot.Sessions != 3 || tot.Turns != 4 {
					t.Errorf("claude-code totals = %+v, want 3 sessions / 4 turns", tot)
				}
			case "codex":
				if tot.Sessions == 0 || tot.Sessions != all.Sessions-3 || tot.Turns != all.Turns-4 {
					t.Errorf("codex totals = %+v, want all (%+v) minus the Claude Code 3 / 4", tot, all)
				}
			}
		})
	}
}

// TestAPISpend_ProviderIsolation: the Subagents, Skills and Prompts tabs
// each carry exactly the rows of the providers in scope. Claude Code owns
// the four typed prompts, the deploy skill and four main turns; Codex owns
// basic.jsonl's real prompt, the "(no prompt)" row across its three sessions
// without one (tools, guardian, and the subagent whose parent is absent),
// five main turns and the one sidechain turn. An unfiltered section fails
// every single-provider case.
func TestAPISpend_ProviderIsolation(t *testing.T) {
	srv := mixedProviderServer(t)
	claudePrompts := []string{"deploy it", "fix the login bug", "now write a test", "refactor the cache"}
	codexPrompts := []string{"(no prompt)", "read the readme and summarize it. (replaced)"}
	allPrompts := append(append([]string{}, claudePrompts...), codexPrompts...)
	sort.Strings(allPrompts)
	cases := []struct {
		provider        string
		mainTurns       int
		sidechainTurns  int
		subagentTypes   []string
		skills          []string
		prompts         []string
		noPromptSession int
	}{
		{"", 9, 1, []string{"sidechain — unknown subagent"}, []string{"skill:deploy"}, allPrompts, 3},
		{"claude-code", 4, 0, []string{}, []string{"skill:deploy"}, claudePrompts, 0},
		{"codex", 5, 1, []string{"sidechain — unknown subagent"}, []string{}, codexPrompts, 3},
	}
	for _, c := range cases {
		t.Run("provider="+c.provider, func(t *testing.T) {
			sub := getSection(t, srv, "/_claudit/api/subagents", c.provider)
			var main, side struct{ Turns int }
			decodeField(t, sub, "main", &main)
			decodeField(t, sub, "sidechain", &side)
			if main.Turns != c.mainTurns || side.Turns != c.sidechainTurns {
				t.Errorf("subagents main/sidechain turns = %d/%d, want %d/%d", main.Turns, side.Turns, c.mainTurns, c.sidechainTurns)
			}
			var bySub []struct{ Type string }
			decodeField(t, sub, "by_subagent", &bySub)
			types := []string{}
			for _, r := range bySub {
				types = append(types, r.Type)
			}
			if !reflect.DeepEqual(types, c.subagentTypes) {
				t.Errorf("by_subagent = %v, want %v", types, c.subagentTypes)
			}

			cost := getSection(t, srv, "/_claudit/api/cost", c.provider)
			var bySkill []struct {
				Key   string
				Count int
			}
			decodeField(t, cost, "by_skill", &bySkill)
			skills := []string{}
			for _, r := range bySkill {
				skills = append(skills, r.Key)
				if r.Count != 1 {
					t.Errorf("by_skill %s count = %d, want 1", r.Key, r.Count)
				}
			}
			if !reflect.DeepEqual(skills, c.skills) {
				t.Errorf("by_skill = %v, want %v", skills, c.skills)
			}

			var byPrompt []struct {
				Key      string
				Sessions int
			}
			decodeField(t, cost, "by_prompt", &byPrompt)
			prompts := []string{}
			noPromptSessions := 0
			for _, r := range byPrompt {
				prompts = append(prompts, r.Key)
				if r.Key == "(no prompt)" {
					noPromptSessions = r.Sessions
				}
			}
			sort.Strings(prompts)
			if !reflect.DeepEqual(prompts, c.prompts) {
				t.Errorf("by_prompt keys = %v, want %v", prompts, c.prompts)
			}
			if noPromptSessions != c.noPromptSession {
				t.Errorf("(no prompt) sessions = %d, want %d", noPromptSessions, c.noPromptSession)
			}
		})
	}
}

// TestAPIAgents_ProviderFilter: ?provider narrows the Agents feed to that
// provider's sessions, and each session names its provider.
func TestAPIAgents_ProviderFilter(t *testing.T) {
	srv := mixedProviderServer(t)
	for _, pk := range []string{"", "claude-code", "codex"} {
		t.Run("provider="+pk, func(t *testing.T) {
			body := getSection(t, srv, "/_claudit/api/agents", pk)
			var sessions []struct {
				SessionID string `json:"session_id"`
				Provider  string `json:"provider"`
			}
			decodeField(t, body, "sessions", &sessions)
			got := map[string]bool{}
			for _, s := range sessions {
				got[s.Provider] = true
			}
			if want := wantProviders(pk); !sameSet(got, want) {
				t.Errorf("session providers = %v, want %v; sessions %+v", got, want, sessions)
			}
		})
	}
}
