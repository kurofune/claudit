package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

func mkAssistantLine(uuid, parent string, ts time.Time) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":"s1","cwd":"/p","message":{"model":"claude-opus-4-7","role":"assistant","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":100,"output_tokens":200,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}}}`,
		uuid, parent, ts.Format(time.RFC3339))
}

func mkUserLine(uuid, parent, text string, ts time.Time) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":%q,"timestamp":%q,"sessionId":"s1","cwd":"/p","message":{"role":"user","content":%q}}`,
		uuid, parent, ts.Format(time.RFC3339), text)
}

// blockingCollector wraps the event slice with a mutex so the test
// goroutine and the tail goroutine can both touch it.
type blockingCollector struct {
	mu     sync.Mutex
	events []Event
}

func (c *blockingCollector) Add(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *blockingCollector) Snapshot() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

// waitForCount polls c until it has n events or timeout elapses.
func waitForCount(t *testing.T, c *blockingCollector, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(c.Snapshot()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waited %s for %d events; have %d", timeout, n, len(c.Snapshot()))
}

func TestTail_ReadsExistingAndAppended(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live.jsonl")

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	initial := mkUserLine("u1", "", "do thing", t0) + "\n" +
		mkAssistantLine("a1", "u1", t0.Add(time.Second)) + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &blockingCollector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Tail(ctx, path, TailOptions{Interval: 25 * time.Millisecond, FromBeginning: true},
			c.Add, nil)
	}()

	waitForCount(t, c, 2, 2*time.Second)
	got := c.Snapshot()
	if got[0].Kind != parse.LineUserMessage || got[0].User.UUID != "u1" {
		t.Errorf("event 0: %+v", got[0])
	}
	if got[1].Kind != parse.LineAssistant || got[1].Turn.UUID != "a1" {
		t.Errorf("event 1: %+v", got[1])
	}

	// Append two more lines and confirm they surface within a couple ticks.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	appended := mkAssistantLine("a2", "a1", t0.Add(2*time.Second)) + "\n" +
		mkAssistantLine("a3", "a2", t0.Add(3*time.Second)) + "\n"
	if _, err := f.WriteString(appended); err != nil {
		t.Fatal(err)
	}
	f.Close()

	waitForCount(t, c, 4, 2*time.Second)

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Tail err: %v", err)
	}
	final := c.Snapshot()
	if len(final) != 4 {
		t.Errorf("final count: %d", len(final))
	}
	if final[3].Turn.UUID != "a3" {
		t.Errorf("last event: %+v", final[3])
	}
}

func TestTail_PartialLineBuffersAcrossReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	c := &blockingCollector{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Tail(ctx, path, TailOptions{Interval: 20 * time.Millisecond, FromBeginning: true},
			c.Add, nil)
	}()

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	full := mkAssistantLine("a1", "u1", t0)
	half := full[:len(full)/2]

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(half); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	// Give the watcher a poll or two while the line is half-written.
	time.Sleep(80 * time.Millisecond)
	if got := len(c.Snapshot()); got != 0 {
		t.Errorf("partial line shouldn't emit yet, got %d", got)
	}
	if _, err := f.WriteString(full[len(full)/2:] + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	waitForCount(t, c, 1, 2*time.Second)
	cancel()
	<-done
	got := c.Snapshot()
	if len(got) != 1 || got[0].Turn.UUID != "a1" {
		t.Errorf("partial line not reassembled: %+v", got)
	}
}

func TestTail_RotationReopens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "live.jsonl")
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(path, []byte(mkAssistantLine("a1", "u1", t0)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &blockingCollector{}
	notices := 0
	var nmu sync.Mutex
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Tail(ctx, path, TailOptions{Interval: 25 * time.Millisecond, FromBeginning: true},
			c.Add, func(n Notice) {
				nmu.Lock()
				if n.Kind == NoticeRotated || n.Kind == NoticeTruncated {
					notices++
				}
				nmu.Unlock()
			})
	}()
	waitForCount(t, c, 1, 2*time.Second)

	// Replace the file (rotation: new inode).
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rotated := mkAssistantLine("a2", "u2", t0.Add(time.Second)) + "\n"
	if err := os.WriteFile(path, []byte(rotated), 0o644); err != nil {
		t.Fatal(err)
	}

	waitForCount(t, c, 2, 2*time.Second)
	cancel()
	<-done

	final := c.Snapshot()
	uuids := []string{}
	for _, e := range final {
		if e.Kind == parse.LineAssistant {
			uuids = append(uuids, e.Turn.UUID)
		}
	}
	wantHas := func(u string) bool {
		for _, x := range uuids {
			if x == u {
				return true
			}
		}
		return false
	}
	if !wantHas("a1") || !wantHas("a2") {
		t.Errorf("expected both pre- and post-rotation turns, got %v", uuids)
	}
	nmu.Lock()
	if notices == 0 {
		t.Errorf("expected at least one rotation notice")
	}
	nmu.Unlock()
}

func TestTail_WaitsForFileToAppear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "later.jsonl")

	c := &blockingCollector{}
	gotWaiting := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Tail(ctx, path, TailOptions{Interval: 25 * time.Millisecond, FromBeginning: true},
			c.Add, func(n Notice) {
				if n.Kind == NoticeWaiting {
					select {
					case gotWaiting <- struct{}{}:
					default:
					}
				}
			})
	}()

	select {
	case <-gotWaiting:
	case <-time.After(2 * time.Second):
		t.Fatal("never got waiting notice")
	}

	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(path, []byte(mkAssistantLine("a1", "u1", t0)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, c, 1, 2*time.Second)

	cancel()
	<-done
}

func TestMostRecentJSONL_PicksLatest(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "a.jsonl")
	mid := filepath.Join(dir, "b.jsonl")
	new := filepath.Join(dir, "sub", "c.jsonl")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	for _, p := range []string{old, mid, new} {
		os.WriteFile(p, nil, 0o644)
	}
	now := time.Now()
	os.Chtimes(old, now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	os.Chtimes(mid, now.Add(-time.Hour), now.Add(-time.Hour))
	os.Chtimes(new, now, now)

	got, err := MostRecentJSONL(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != new {
		t.Errorf("got %s, want %s", got, new)
	}
}

func TestFindBySessionID_PrefixMatch(t *testing.T) {
	dir := t.TempDir()
	// Real session.
	os.WriteFile(filepath.Join(dir, "207f394c-cf10-4341-8c0c-f17617b5ae36.jsonl"), nil, 0o644)
	// Subagent under same session — must be ignored.
	subDir := filepath.Join(dir, "207f394c-cf10-4341-8c0c-f17617b5ae36", "subagents")
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(subDir, "agent-abc.jsonl"), nil, 0o644)

	got, err := FindBySessionID(dir, "207f394c")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "207f394c-cf10-4341-8c0c-f17617b5ae36.jsonl") {
		t.Errorf("got %s", got)
	}

	if _, err := FindBySessionID(dir, "00000000"); err == nil {
		t.Errorf("expected error for unknown id")
	}
}
