package aggregate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kurofune/claudit/internal/parse"
)

// writeSubagentFixture creates <dir>/subagents/agent-x.jsonl plus its
// sibling agent-x.meta.json and returns the jsonl path.
func writeSubagentFixture(t *testing.T, dir, agentType, description string) string {
	t.Helper()
	subDir := filepath.Join(dir, "subagents")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonlPath := filepath.Join(subDir, "agent-x.jsonl")
	if err := os.WriteFile(jsonlPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"agentType":%q,"description":%q}`, agentType, description)
	if err := os.WriteFile(filepath.Join(subDir, "agent-x.meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return jsonlPath
}

func TestMemoizedSubagentLookup_NonSubagentPathReturnsEmpty(t *testing.T) {
	lookup := NewMemoizedSubagentLookup()
	typ, desc := lookup(parse.Turn{SourceFile: "/some/project/session-abc.jsonl"})
	if typ != "" || desc != "" {
		t.Errorf("lookup(non-subagent) = (%q, %q), want (\"\", \"\")", typ, desc)
	}
}

func TestMemoizedSubagentLookup_MemoizesPerSourceFile(t *testing.T) {
	jsonlPath := writeSubagentFixture(t, t.TempDir(), "explorer", "Search the tree")
	lookup := NewMemoizedSubagentLookup()
	if typ, _ := lookup(parse.Turn{SourceFile: jsonlPath}); typ != "explorer" {
		t.Fatalf("first lookup type = %q, want %q", typ, "explorer")
	}
	// Remove the meta file: a memoized lookup must not touch the
	// filesystem again for the same source file.
	metaPath := filepath.Join(filepath.Dir(jsonlPath), "agent-x.meta.json")
	if err := os.Remove(metaPath); err != nil {
		t.Fatal(err)
	}
	typ, desc := lookup(parse.Turn{SourceFile: jsonlPath})
	if typ != "explorer" || desc != "Search the tree" {
		t.Errorf("second lookup = (%q, %q), want cached (%q, %q)", typ, desc, "explorer", "Search the tree")
	}
}

func TestMemoizedSubagentLookup_CachesNegativeResult(t *testing.T) {
	jsonlPath := writeSubagentFixture(t, t.TempDir(), "late", "Arrives after first lookup")
	metaPath := filepath.Join(filepath.Dir(jsonlPath), "agent-x.meta.json")
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metaPath); err != nil {
		t.Fatal(err)
	}
	lookup := NewMemoizedSubagentLookup()
	if typ, desc := lookup(parse.Turn{SourceFile: jsonlPath}); typ != "" || desc != "" {
		t.Fatalf("lookup with missing meta = (%q, %q), want (\"\", \"\")", typ, desc)
	}
	// Restore the meta file: the negative result must have been cached,
	// so the second call still returns empty without re-reading.
	if err := os.WriteFile(metaPath, metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if typ, desc := lookup(parse.Turn{SourceFile: jsonlPath}); typ != "" || desc != "" {
		t.Errorf("second lookup = (%q, %q), want cached negative (\"\", \"\")", typ, desc)
	}
}

func TestMemoizedSubagentLookup_ReadsMetaForSubagentFile(t *testing.T) {
	jsonlPath := writeSubagentFixture(t, t.TempDir(), "code-reviewer", "Review the diff")
	lookup := NewMemoizedSubagentLookup()
	typ, desc := lookup(parse.Turn{SourceFile: jsonlPath})
	if typ != "code-reviewer" || desc != "Review the diff" {
		t.Errorf("lookup(subagent) = (%q, %q), want (%q, %q)", typ, desc, "code-reviewer", "Review the diff")
	}
}

func TestMemoizedSubagentLookup_ConcurrentCalls(t *testing.T) {
	jsonlPath := writeSubagentFixture(t, t.TempDir(), "racer", "Concurrent access")
	lookup := NewMemoizedSubagentLookup()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				typ, desc := lookup(parse.Turn{SourceFile: jsonlPath})
				if typ != "racer" || desc != "Concurrent access" {
					t.Errorf("concurrent lookup = (%q, %q), want (%q, %q)", typ, desc, "racer", "Concurrent access")
					return
				}
			}
		}()
	}
	wg.Wait()
}
