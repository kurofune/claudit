package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/kurofune/claudit/internal/parse"
)

// copyCodexFixture lays a committed Codex fixture into root at the
// YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl path a real rollout lives at.
func copyCodexFixture(t *testing.T, root, fixture, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "parse", "codex", "testdata", fixture))
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
	return dst
}

func TestServe_SnapshotIncludesCodexTurns(t *testing.T) {
	ccRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(ccRoot, "s.jsonl"), []byte(`{"type":"assistant","uuid":"a1","timestamp":"2026-10-25T00:00:00Z","sessionId":"cc1","cwd":"/p","message":{"id":"m1","model":"claude-opus-4-7","role":"assistant","content":[],"usage":{"input_tokens":1,"output_tokens":1}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codexRoot := t.TempDir()
	basic := copyCodexFixture(t, codexRoot, "basic.jsonl", filepath.Join("2026", "10", "24", "rollout-2026-10-24T17-52-01-01a1708b-04a3-75a1-a07c-92bbb16be66f.jsonl"))
	copyCodexFixture(t, codexRoot, "subagent.jsonl", filepath.Join("2026", "10", "31", "rollout-2026-10-31T06-12-26-01a19217-0c60-71a2-ab13-917397bf7ed1.jsonl"))

	cache := newServeCache(ccRoot, codexRoot)
	if _, err := cache.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	snap := cache.Snapshot()
	if snap.FileCount != 3 {
		t.Errorf("FileCount = %d, want 3 (one Claude Code, two Codex)", snap.FileCount)
	}
	byProvider := map[string]int{}
	for _, tr := range snap.Turns {
		byProvider[tr.Provider]++
		if tr.Provider == parse.ProviderCodex && tr.SourceFile == basic && tr.Model != "gpt-6-astra" {
			t.Errorf("codex turn model = %q", tr.Model)
		}
	}
	if byProvider[parse.ProviderCodex] != 3 || byProvider[parse.ProviderClaudeCode] != 1 {
		t.Errorf("turns by provider = %v, want codex 3 and claude-code 1", byProvider)
	}
	if len(snap.Links) != 1 {
		t.Errorf("Links = %+v, want the subagent's parent link", snap.Links)
	}
	if got := cache.HostInfo().Root; got != ccRoot+", "+codexRoot {
		t.Errorf("HostInfo.Root = %q, want both roots", got)
	}

	// An empty codex root leaves only the Claude Code Source.
	only := newServeCache(ccRoot, "")
	if _, err := only.Refresh(); err != nil {
		t.Fatal(err)
	}
	if only.Snapshot().FileCount != 1 {
		t.Errorf("FileCount without a codex root = %d, want 1", only.Snapshot().FileCount)
	}
}

func TestDefaultCodexRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	if got, want := defaultCodexRoot(), filepath.Join(home, ".codex", "sessions"); got != want {
		t.Errorf("defaultCodexRoot() = %q, want %q", got, want)
	}
	t.Setenv("CODEX_HOME", "/opt/codex")
	if got, want := defaultCodexRoot(), filepath.Join("/opt/codex", "sessions"); got != want {
		t.Errorf("defaultCodexRoot() with CODEX_HOME = %q, want %q", got, want)
	}
}

func TestCodexRootFlag_OverridesDefault(t *testing.T) {
	t.Setenv("CODEX_HOME", "/opt/codex")
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	root := codexRootFlag(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/opt/codex", "sessions"); *root != want {
		t.Errorf("default --codex-root = %q, want %q", *root, want)
	}
	fs = flag.NewFlagSet("serve", flag.ContinueOnError)
	root = codexRootFlag(fs)
	if err := fs.Parse([]string{"--codex-root", "/tmp/elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if *root != "/tmp/elsewhere" {
		t.Errorf("--codex-root = %q, want /tmp/elsewhere", *root)
	}
}
