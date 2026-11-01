package serve

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/kurofune/claudit/internal/corpus"
	"github.com/kurofune/claudit/internal/provider"
)

// codexFixtureRoot lays the committed Codex fixtures into a temp sessions
// root at the YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl paths Codex writes.
func codexFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for fixture, rel := range map[string]string{
		"basic.jsonl":    filepath.Join("2026", "10", "24", "rollout-2026-10-24T17-52-01-01a1708b-04a3-75a1-a07c-92bbb16be66f.jsonl"),
		"tools.jsonl":    filepath.Join("2026", "10", "31", "rollout-2026-10-31T16-53-12-01a19461-aed4-78a0-bad1-efc7859dd26a.jsonl"),
		"subagent.jsonl": filepath.Join("2026", "10", "31", "rollout-2026-10-31T06-12-26-01a19217-0c60-71a2-ab13-917397bf7ed1.jsonl"),
		"guardian.jsonl": filepath.Join("2026", "10", "29", "rollout-2026-10-29T04-56-37-01a18784-eaa5-7850-b7db-776a88eb52ee.jsonl"),
	} {
		b, err := os.ReadFile(filepath.Join("..", "parse", "codex", "testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestAPICost_CodexModelRow(t *testing.T) {
	cache := corpus.New(provider.NewClaudeCode(t.TempDir()), provider.NewCodex(codexFixtureRoot(t)))
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("seed refresh: %v", err)
	}
	srv := NewServer(cache, Options{Prices: loadPricesForTest(t)})

	w := doAPI(t, srv, http.MethodGet, "/_claudit/api/cost?scope=all", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	var rows []struct {
		Model   string
		CostUSD float64
		Turns   int
	}
	if err := json.Unmarshal(body["by_model"], &rows); err != nil {
		t.Fatalf("by_model: %v", err)
	}
	got := map[string]float64{}
	for _, r := range rows {
		got[r.Model] = r.CostUSD
	}
	if c, ok := got["gpt-6-astra"]; !ok || c <= 0 {
		t.Fatalf("by_model gpt-6-astra cost = %v (present %v), want > 0; rows = %+v", c, ok, rows)
	}
	// basic.jsonl: (38472 in, 512 write, 99 out) + (9873 in, 38400 read, 146 out)
	// at $10 / $12.50 / $50 / $1 per MTok.
	want := (38472*10 + 512*12.5 + 99*50 + 9873*10 + 38400*1 + 146*50) / 1e6
	if c := got["gpt-6-astra"]; c < want-1e-9 || c > want+1e-9 {
		t.Errorf("gpt-6-astra cost = %v, want %v", c, want)
	}
	if got["codex-auto-review"] != 0 {
		t.Errorf("codex-auto-review cost = %v, want 0 (unpriced)", got["codex-auto-review"])
	}

	ov := readJSONResponse(t, doAPI(t, srv, http.MethodGet, "/_claudit/api/overview?scope=all", nil))
	var unknown []struct{ Provider, Model string }
	if err := json.Unmarshal(ov["unknown_models"], &unknown); err != nil {
		t.Fatalf("unknown_models: %v; raw %s", err, ov["unknown_models"])
	}
	if len(unknown) != 1 || unknown[0].Provider != "codex" || unknown[0].Model != "codex-auto-review" {
		t.Errorf("unknown_models = %s, want only codex/codex-auto-review", ov["unknown_models"])
	}
}
