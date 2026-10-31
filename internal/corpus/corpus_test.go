package corpus

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/provider"
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

func TestCache_SnapshotCarriesToolResults(t *testing.T) {
	dir := t.TempDir()
	c := New(provider.NewClaudeCode(dir))

	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	// An assistant turn with a tool_use, then the user turn carrying its
	// tool_result. The snapshot must surface the result for the join.
	assistant := `{"type":"assistant","uuid":"a1","parentUuid":"","timestamp":"2026-05-01T10:00:00Z","sessionId":"s1","cwd":"/p","message":{"model":"claude-opus-4-7","role":"assistant","content":[{"type":"tool_use","id":"toolu_9","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":1,"output_tokens":1}}}`
	result := `{"type":"user","uuid":"u1","parentUuid":"a1","timestamp":"2026-05-01T10:00:01Z","sessionId":"s1","cwd":"/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_9","content":"file listing","is_error":false}]}}`
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), assistant, result)
	_ = t0

	if _, err := c.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	snap := c.Snapshot()
	if len(snap.ToolResults) != 1 {
		t.Fatalf("snapshot ToolResults = %d, want 1", len(snap.ToolResults))
	}
	if r := snap.ToolResults[0]; r.ToolUseID != "toolu_9" || r.Content != "file listing" {
		t.Errorf("ToolResult = %+v, want toolu_9/file listing", r)
	}
}

func TestCache_RefreshDetectsAddedFile(t *testing.T) {
	dir := t.TempDir()
	c := New(provider.NewClaudeCode(dir))

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

	c := New(provider.NewClaudeCode(dir))
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

	c := New(provider.NewClaudeCode(dir))
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

	c := New(provider.NewClaudeCode(dir))
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

	c := New(provider.NewClaudeCode(dir))
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := len(c.Snapshot().Turns); got != 1 {
		t.Errorf("nested file not picked up: turns=%d", got)
	}
}

func TestCache_HostInfo(t *testing.T) {
	c := New(provider.NewClaudeCode(t.TempDir()))
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
	c := New(provider.NewClaudeCode(dir))
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

	snap, err := LoadConcurrent([]provider.Source{provider.NewClaudeCode(dir)}, time.Time{})
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

	snap, err := LoadConcurrent([]provider.Source{provider.NewClaudeCode(dir)}, time.Time{})
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
	snap, err := LoadConcurrent([]provider.Source{provider.NewClaudeCode(dir)}, earliest)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(snap.Turns); got != 1 {
		t.Errorf("turns = %d, want 1 (old file should be mtime-skipped)", got)
	}
}

func TestSnapshotsCarryCompactBoundaries(t *testing.T) {
	dir := t.TempDir()
	compact := `{"type":"user","sessionId":"s1","uuid":"c1","parentUuid":"a1","timestamp":"2026-05-01T11:00:00Z","cwd":"/p","message":{"role":"user","content":"This session is being continued from a previous conversation."},"isCompactSummary":true}`
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)), compact)

	c := New(provider.NewClaudeCode(dir))
	if _, err := c.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := c.Snapshot().CompactBoundaries; len(got) != 1 || got[0].SessionID != "s1" {
		t.Errorf("Cache snapshot CompactBoundaries = %+v, want one for s1", got)
	}
	snap, err := LoadConcurrent([]provider.Source{provider.NewClaudeCode(dir)}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.CompactBoundaries; len(got) != 1 || got[0].SessionID != "s1" {
		t.Errorf("LoadConcurrent CompactBoundaries = %+v, want one for s1", got)
	}
}

// fakeSource is a second provider: it yields one file of its own and parses
// it into one turn tagged Provider "fake", without reading the bytes as
// Claude Code JSONL.
type fakeSource struct{ path string }

func (f fakeSource) Name() string { return "fake" }

func (f fakeSource) Walk(earliest time.Time, fn func(path string, mod time.Time, size int64)) error {
	info, err := os.Stat(f.path)
	if err != nil {
		return err
	}
	if earliest.IsZero() || !info.ModTime().Before(earliest) {
		fn(f.path, info.ModTime(), info.Size())
	}
	return nil
}

func (f fakeSource) ParseFile(r io.Reader, path string) (parse.Result, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return parse.Result{}, err
	}
	return parse.Result{Turns: []parse.Turn{{
		Provider:   "fake",
		SessionID:  strings.TrimSpace(string(b)),
		Model:      "fake-model",
		SourceFile: path,
	}}}, nil
}

func TestCache_SecondSourceTurnsReachSnapshot(t *testing.T) {
	ccDir := t.TempDir()
	writeJSONL(t, filepath.Join(ccDir, "s.jsonl"), mkAssistantLine("a1", "", time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)))
	fakePath := filepath.Join(t.TempDir(), "rollout.log")
	if err := os.WriteFile(fakePath, []byte("fake-session\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(provider.NewClaudeCode(ccDir), fakeSource{path: fakePath})
	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	snap := c.Snapshot()
	if snap.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2 (one per source)", snap.FileCount)
	}
	providers := map[string]parse.Turn{}
	for _, tn := range snap.Turns {
		providers[tn.Provider] = tn
	}
	fake, ok := providers["fake"]
	if !ok {
		t.Fatalf("no fake turn in snapshot: %+v", snap.Turns)
	}
	if fake.SessionID != "fake-session" || fake.SourceFile != fakePath {
		t.Errorf("fake turn = %+v, want session fake-session from %s", fake, fakePath)
	}
	if _, ok := providers[parse.ProviderClaudeCode]; !ok {
		t.Errorf("claude-code turn missing beside the fake one: %+v", snap.Turns)
	}

	// An unchanged second source is not re-parsed into a new generation.
	gen := snap.Generation
	if changed, err := c.Refresh(); err != nil || changed {
		t.Errorf("second Refresh changed=%v err=%v, want no change", changed, err)
	}
	if c.Snapshot().Generation != gen {
		t.Error("generation advanced with nothing changed")
	}

	// LoadConcurrent reads both sources too.
	one, err := LoadConcurrent([]provider.Source{provider.NewClaudeCode(ccDir), fakeSource{path: fakePath}}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if one.FileCount != 2 || len(one.Turns) != 2 {
		t.Errorf("LoadConcurrent FileCount=%d turns=%d, want 2 and 2", one.FileCount, len(one.Turns))
	}
}

// flakySource yields whichever of its paths exist on disk, in order, until
// it reaches failAt files, then returns err. A negative failAt never fails.
type flakySource struct {
	paths  []string
	failAt int
	err    error
}

func (f *flakySource) Name() string { return "flaky" }

func (f *flakySource) Walk(_ time.Time, fn func(path string, mod time.Time, size int64)) error {
	for n, p := range f.paths {
		if n == f.failAt {
			return f.err
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		fn(p, info.ModTime(), info.Size())
	}
	if f.failAt >= len(f.paths) {
		return f.err
	}
	return nil
}

func (f *flakySource) ParseFile(r io.Reader, path string) (parse.Result, error) {
	return fakeSource{}.ParseFile(r, path)
}

func TestCache_FailedWalkKeepsCachedFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	b := filepath.Join(dir, "b.log")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte(filepath.Base(p)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ccDir := t.TempDir()
	writeJSONL(t, filepath.Join(ccDir, "s.jsonl"), mkAssistantLine("a1", "", time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)))
	src := &flakySource{paths: []string{a, b}, failAt: -1}
	c := New(provider.NewClaudeCode(ccDir), src)

	want := func(step string, turns, files int) {
		t.Helper()
		snap := c.Snapshot()
		if len(snap.Turns) != turns || snap.FileCount != files {
			t.Errorf("%s: turns=%d files=%d, want %d/%d", step, len(snap.Turns), snap.FileCount, turns, files)
		}
	}

	if _, err := c.Refresh(); err != nil {
		t.Fatal(err)
	}
	want("initial load", 3, 3)
	gen := c.Snapshot().Generation

	src.failAt, src.err = 0, errors.New("root unreadable")
	if changed, err := c.Refresh(); err == nil || changed {
		t.Errorf("walk failing before any file: changed=%v err=%v, want unchanged with an error", changed, err)
	}
	want("walk failing before any file", 3, 3)
	if c.Snapshot().Generation != gen {
		t.Error("failed walk published a new generation")
	}

	src.failAt = 1
	if changed, err := c.Refresh(); err == nil || changed {
		t.Errorf("partial walk: changed=%v err=%v, want unchanged with an error", changed, err)
	}
	want("partial walk", 3, 3)

	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	src.failAt = 0
	if _, err := c.Refresh(); err == nil {
		t.Error("failed walk after a deletion returned no error")
	}
	want("failed walk after a deletion", 3, 3)

	src.failAt, src.err = -1, nil
	if changed, err := c.Refresh(); err != nil || !changed {
		t.Errorf("recovered walk: changed=%v err=%v, want changed with no error", changed, err)
	}
	want("recovered walk drops the deleted file", 2, 2)

	src.paths = nil
	if changed, err := c.Refresh(); err != nil || !changed {
		t.Errorf("successful empty walk: changed=%v err=%v, want changed with no error", changed, err)
	}
	want("successful empty walk drops the source's files", 1, 1)
}
