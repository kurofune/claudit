package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mkAssistantLine matches the schema used elsewhere so these fixtures
// look like real session JSONL. One assistant line per call.
func mkAssistantLine(uuid, parent string, ts time.Time) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":"s1","cwd":"/p","message":{"model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}`,
		uuid, parent, ts.Format(time.RFC3339))
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
	c := New(dir)

	changed, err := c.Refresh()
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if changed {
		t.Errorf("first refresh of empty dir reported changed; expected false")
	}

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

	c := New(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	genA := c.Snapshot().Generation

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

	c := New(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	gen0 := c.Snapshot().Generation
	turns0 := len(c.Snapshot().Turns)

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

	c := New(dir)
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

	c := New(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := len(c.Snapshot().Turns); got != 1 {
		t.Errorf("nested file not picked up: turns=%d", got)
	}
}

func TestCache_HostInfo(t *testing.T) {
	c := New(t.TempDir())
	hi := c.HostInfo()
	if hi.Root == "" || hi.GoOS == "" || hi.NumCPU < 1 {
		t.Errorf("hostInfo looks empty: %+v", hi)
	}
}

// --- new behavior: concurrent cold-load over many files -------------

func TestCache_ConcurrentColdLoadCountsAllTurns(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	const files = 50
	for i := 0; i < files; i++ {
		p := filepath.Join(dir, fmt.Sprintf("proj-%d", i), "s.jsonl")
		writeJSONL(t, p,
			mkAssistantLine(fmt.Sprintf("a%d", i), "", t0),
			mkAssistantLine(fmt.Sprintf("b%d", i), "", t0.Add(time.Minute)),
		)
	}
	c := New(dir)
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	snap := c.Snapshot()
	if got, want := len(snap.Turns), files*2; got != want {
		t.Errorf("turns = %d, want %d (concurrent parse dropped data?)", got, want)
	}
	if snap.FileCount != files {
		t.Errorf("file count = %d, want %d", snap.FileCount, files)
	}
}

// --- new behavior: one-shot LoadConcurrent with mtime pre-filter ----

func TestLoadConcurrent_AllFiles(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "a.jsonl"), mkAssistantLine("a1", "", t0))
	writeJSONL(t, filepath.Join(dir, "b.jsonl"), mkAssistantLine("b1", "", t0))

	snap, err := LoadConcurrent(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(snap.Turns); got != 2 {
		t.Errorf("turns = %d, want 2", got)
	}
}

func TestLoadConcurrent_SurfacesFileErrors(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	ok := filepath.Join(dir, "ok.jsonl")
	bad := filepath.Join(dir, "bad.jsonl")
	writeJSONL(t, ok, mkAssistantLine("a1", "", t0))
	writeJSONL(t, bad, mkAssistantLine("b1", "", t0))
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o644) })

	// Permission bits aren't enforced everywhere: Windows os.Chmod can't
	// clear the read bit, and root bypasses them. If the file is still
	// readable, this test's premise (a per-file open error) can't be
	// reproduced — skip rather than report a false failure.
	if f, err := os.Open(bad); err == nil {
		_ = f.Close()
		t.Skip("file permissions not enforced here (Windows or root); cannot simulate an unreadable file")
	}

	snap, err := LoadConcurrent(dir, time.Time{})
	if err != nil {
		t.Fatalf("walk should not fail on a per-file open error: %v", err)
	}
	if got := len(snap.Turns); got != 1 {
		t.Errorf("turns = %d, want 1 (readable file should still load)", got)
	}
	if len(snap.FileErrors) != 1 {
		t.Errorf("FileErrors = %d, want 1 (unreadable file should be surfaced)", len(snap.FileErrors))
	}
}

func TestLoadConcurrent_MtimePreFilterSkipsOldFiles(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	oldF := filepath.Join(dir, "old.jsonl")
	newF := filepath.Join(dir, "new.jsonl")
	writeJSONL(t, oldF, mkAssistantLine("o1", "", t0))
	writeJSONL(t, newF, mkAssistantLine("n1", "", t0))

	// Stamp old file well in the past, new file now.
	past := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(oldF, past, past); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(newF, now, now); err != nil {
		t.Fatal(err)
	}

	// earliest = 30 days ago → old file (mtime -60d) must be skipped.
	earliest := time.Now().AddDate(0, 0, -30)
	snap, err := LoadConcurrent(dir, earliest)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(snap.Turns); got != 1 {
		t.Errorf("turns = %d, want 1 (old file should be mtime-skipped)", got)
	}
}
