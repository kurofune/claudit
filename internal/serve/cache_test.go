package serve

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mkAssistantLine matches the schema used in internal/watch tests so
// these fixtures look like real session JSONL. One assistant line per
// call; uuid/parent unique enough that the chain walks behave.
func mkAssistantLine(uuid, parent string, ts time.Time) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":"s1","cwd":"/p","message":{"model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}`,
		uuid, parent, ts.Format(time.RFC3339))
}

// mkUserLine builds a user prompt line matching the schema parse expects
// (type:"user", message.role:"user", message.content as string). Used by
// data-endpoint tests that exercise the SessionTimelines → prompt_keys
// pipeline; mkAssistantLine alone produces orphan turns.
func mkUserLine(uuid, text string, ts time.Time) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"timestamp":%q,"sessionId":"s1","cwd":"/p","message":{"role":"user","content":%q}}`,
		uuid, ts.Format(time.RFC3339), text)
}

func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCache_RefreshDetectsAddedFile(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)

	// Empty root: refresh succeeds but does not bump generation
	// because the snapshot starts empty and stays empty.
	changed, err := c.Refresh()
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if changed {
		t.Errorf("first refresh of empty dir reported changed; expected false")
	}

	// Add a file and refresh — should bump generation and parse one turn.
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))

	changed, err = c.Refresh()
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if !changed {
		t.Errorf("refresh after add reported changed=false; expected true")
	}
	snap := c.Snapshot()
	if got, want := len(snap.Turns), 1; got != want {
		t.Errorf("snapshot turns = %d, want %d", got, want)
	}
	if snap.FileCount != 1 {
		t.Errorf("snapshot file count = %d, want 1", snap.FileCount)
	}
	if snap.Generation < 1 {
		t.Errorf("generation = %d, want >= 1", snap.Generation)
	}
}

func TestCache_RefreshSkipsUnchanged(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))

	c := NewCache(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	genA := c.Snapshot().Generation

	// No file changes: second refresh must not bump generation.
	changed, err := c.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("unchanged refresh reported changed=true; expected false")
	}
	if got := c.Snapshot().Generation; got != genA {
		t.Errorf("generation moved from %d to %d on unchanged refresh", genA, got)
	}
}

func TestCache_RefreshDetectsAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, path, mkAssistantLine("a1", "", t0))

	c := NewCache(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	gen0 := c.Snapshot().Generation
	turns0 := len(c.Snapshot().Turns)

	// Append a second turn. mtime resolution on some filesystems (HFS+,
	// older FAT) is coarse enough that an immediate rewrite can keep
	// the same mtime; size changes too, which is why the cache keys on
	// (mtime, size). To make the test robust everywhere, also force a
	// future mtime stamp.
	writeJSONL(t, path,
		mkAssistantLine("a1", "", t0),
		mkAssistantLine("a2", "a1", t0.Add(time.Second)),
	)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	changed, err := c.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Errorf("refresh after append reported changed=false")
	}
	snap := c.Snapshot()
	if snap.Generation <= gen0 {
		t.Errorf("generation did not advance: %d -> %d", gen0, snap.Generation)
	}
	if len(snap.Turns) != turns0+1 {
		t.Errorf("turns = %d, want %d", len(snap.Turns), turns0+1)
	}
}

func TestCache_RefreshDropsRemovedFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, a, mkAssistantLine("a1", "", t0))
	writeJSONL(t, b, mkAssistantLine("b1", "", t0))

	c := NewCache(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := len(c.Snapshot().Turns); got != 2 {
		t.Fatalf("setup snapshot turns = %d, want 2", got)
	}

	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	changed, err := c.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Errorf("refresh after remove reported changed=false")
	}
	snap := c.Snapshot()
	if len(snap.Turns) != 1 || snap.FileCount != 1 {
		t.Errorf("after remove: turns=%d files=%d, want 1/1", len(snap.Turns), snap.FileCount)
	}
}

func TestCache_RecursiveWalk(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "project-foo", "session-bar")
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(nested, "s.jsonl"), mkAssistantLine("a1", "", t0))

	c := NewCache(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := len(c.Snapshot().Turns); got != 1 {
		t.Errorf("nested file not picked up: turns=%d", got)
	}
}

func TestCache_HostInfo(t *testing.T) {
	c := NewCache(t.TempDir())
	hi := c.hostInfo()
	if hi.Root == "" || hi.GoOS == "" || hi.NumCPU < 1 {
		t.Errorf("hostInfo looks empty: %+v", hi)
	}
}
