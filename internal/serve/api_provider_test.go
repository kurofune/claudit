package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/provider"
)

// mixedProviderServer serves the Claude Code fixture corpus beside the
// committed Codex rollouts.
func mixedProviderServer(t *testing.T) *Server {
	t.Helper()
	cache := corpus.New(provider.NewClaudeCode(fixtureCorpus(t)), provider.NewCodex(codexFixtureRoot(t)))
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
	// Fixture facts: two Claude Code sessions (s-alpha, s-beta) with three
	// turns; Codex tools.jsonl calls exec, Claude Code fixtures call no tool.
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
				if strings.Contains(p, "alpha") || strings.Contains(p, "beta") {
					hasClaudeProject = true
				}
			}
			if hasCodexProject != want["codex"] || hasClaudeProject != want["claude-code"] {
				t.Errorf("by_project = %v, want codex=%v claude-code=%v", projects, want["codex"], want["claude-code"])
			}

			var byPrompt []struct{ Sample string }
			decodeField(t, cost, "by_prompt", &byPrompt)
			for _, r := range byPrompt {
				claudePrompt := r.Sample == "fix the login bug" || r.Sample == "now write a test" || r.Sample == "refactor the cache"
				if claudePrompt && !want["claude-code"] {
					t.Errorf("by_prompt carries Claude Code prompt %q under provider=%s", r.Sample, pk)
				}
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

			sub := getSection(t, srv, "/_claudit/api/subagents", pk)
			var main struct{ Turns int }
			decodeField(t, sub, "main", &main)
			if main.Turns == 0 {
				t.Errorf("subagents main turns = 0 under provider=%s", pk)
			}

			tools := getSection(t, srv, "/_claudit/api/tools", pk)
			var byTool []struct{ Tool string }
			decodeField(t, tools, "by_tool", &byTool)
			if (len(byTool) > 0) != want["codex"] {
				t.Errorf("by_tool = %+v, want rows only when Codex is in scope", byTool)
			}

			ov := getSection(t, srv, "/_claudit/api/overview", pk)
			var tot struct{ Sessions, Turns int }
			decodeField(t, ov, "totals", &tot)
			switch pk {
			case "claude-code":
				if tot.Sessions != 2 || tot.Turns != 3 {
					t.Errorf("claude-code totals = %+v, want 2 sessions / 3 turns", tot)
				}
			case "codex":
				if tot.Sessions == 0 || tot.Sessions != all.Sessions-2 || tot.Turns != all.Turns-3 {
					t.Errorf("codex totals = %+v, want all (%+v) minus the Claude Code 2 / 3", tot, all)
				}
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
