package serve

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCache_SubscribeGeneration_ReceivesPublishedGeneration verifies the
// fan-out wiring: a subscriber registered before a Refresh actually
// changes the corpus must observe the new generation number on its
// channel. The SSE handler depends on this primitive — without it, a
// connected browser never learns a new turn was indexed.
func TestCache_SubscribeGeneration_ReceivesPublishedGeneration(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)

	ch, unsub := c.SubscribeGeneration()
	defer unsub()

	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))

	changed, err := c.Refresh()
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !changed {
		t.Fatalf("refresh did not flag changed=true")
	}

	select {
	case gen := <-ch:
		if gen != c.Snapshot().Generation {
			t.Errorf("subscriber received gen=%d, snapshot gen=%d", gen, c.Snapshot().Generation)
		}
	case <-time.After(time.Second):
		t.Fatalf("subscriber did not receive generation within 1s")
	}
}

// TestCache_SubscribeGeneration_UnsubscribeStopsDelivery confirms the
// returned cancel func actually deregisters the subscriber: after
// calling it, subsequent publishes must not block on or leak into the
// abandoned channel.
func TestCache_SubscribeGeneration_UnsubscribeStopsDelivery(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)

	ch, unsub := c.SubscribeGeneration()
	unsub()

	// After unsubscribe the channel should be closed; reading must
	// return the zero value with ok=false.
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("post-unsubscribe receive ok=true, want ok=false (closed channel)")
		}
	case <-time.After(time.Second):
		t.Fatalf("post-unsubscribe channel did not close within 1s")
	}

	// A subsequent publish must not panic or block — exercises the
	// "subscriber set no longer holds this channel" path.
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	if _, err := c.Refresh(); err != nil {
		t.Fatalf("refresh after unsubscribe: %v", err)
	}
}

// TestCache_SubscribeGeneration_SlowConsumerNeverBlocksPublisher is the
// pressure-release contract: a subscriber that never reads must not
// stall publishSnapshot. We push more notifications than the channel
// buffer can hold and assert Refresh returns promptly each time. Drops
// are acceptable; deadlocks are not.
func TestCache_SubscribeGeneration_SlowConsumerNeverBlocksPublisher(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir)

	_, unsub := c.SubscribeGeneration()
	defer unsub()

	path := filepath.Join(dir, "s.jsonl")
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, path, mkAssistantLine("a1", "", t0))

	for i := 0; i < 32; i++ {
		// Force a fresh mtime so the (mtime,size) cache key flips
		// every iteration — otherwise Refresh short-circuits and the
		// fan-out path never runs.
		future := time.Now().Add(time.Duration(i+1) * 10 * time.Millisecond)
		if err := os.Chtimes(path, future, future); err != nil {
			t.Fatal(err)
		}

		done := make(chan struct{})
		go func() {
			_, _ = c.Refresh()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("publishSnapshot blocked on slow subscriber at iteration %d", i)
		}
	}
}

// TestEvents_StreamsInitialAndUpdate exercises the end-to-end SSE path:
// connect, receive the initial-generation event so the client syncs to
// the current state, then trigger a refresh and receive the next event.
// This is the contract the SPA's auto-reload nudge depends on.
func TestEvents_StreamsInitialAndUpdate(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeJSONL(t, filepath.Join(dir, "s.jsonl"), mkAssistantLine("a1", "", t0))
	srv := newTestServer(t, dir)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	br := bufio.NewReader(resp.Body)
	gen0 := srv.cache.Snapshot().Generation
	if err := waitForGenerationEvent(br, gen0, 2*time.Second); err != nil {
		t.Fatalf("initial event: %v", err)
	}

	// Trigger a new generation: append a turn, bump mtime, refresh.
	path := filepath.Join(dir, "s.jsonl")
	writeJSONL(t, path,
		mkAssistantLine("a1", "", t0),
		mkAssistantLine("a2", "a1", t0.Add(time.Second)),
	)
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.cache.Refresh(); err != nil {
		t.Fatalf("trigger refresh: %v", err)
	}

	if err := waitForGenerationEvent(br, srv.cache.Snapshot().Generation, 2*time.Second); err != nil {
		t.Fatalf("post-refresh event: %v", err)
	}
}

// TestEvents_MethodNotAllowed verifies the handler refuses non-GET
// verbs with 405 + Allow header — mirrors handleReport's contract and
// keeps the surface predictable for clients.
func TestEvents_MethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, t.TempDir())
	r := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader("body"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /events = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); !strings.Contains(allow, "GET") {
		t.Errorf("Allow = %q, want GET", allow)
	}
}

// TestEvents_DrainsOnServerShutdown is the Phase-2 risk-mitigation
// assertion called out in the plan: cancel the server's context with an
// active SSE client and the server must return within shutdownTimeout.
// Without an explicit drain signal, http.Server.Shutdown would block
// for the full WriteTimeout window because the connection is "in
// flight" — the handler is sitting on a select that never wakes.
func TestEvents_DrainsOnServerShutdown(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir)
	srv.shutdownTimeout = 500 * time.Millisecond

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	// Connect an SSE client and read at least one byte so we know the
	// handler is parked in its select loop before we trigger shutdown.
	reqCtx, reqCancel := context.WithCancel(context.Background())
	defer reqCancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://"+ln.Addr().String()+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	br := bufio.NewReader(resp.Body)
	if err := waitForGenerationEvent(br, srv.cache.Snapshot().Generation, 2*time.Second); err != nil {
		t.Fatalf("initial event: %v", err)
	}

	// Trigger shutdown. The handler must wake and return so
	// http.Server.Shutdown completes within shutdownTimeout.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("serve did not return within 2s of context cancel; SSE handler likely not draining")
	}
}

// TestEvents_NoGoroutineLeak runs 100 connect/disconnect cycles and
// asserts the goroutine count does not creep. A subtle bug in the
// subscribe/unsubscribe wiring (e.g., never closing the channel, never
// removing from the map) would have each cycle leak one or more
// goroutines; the test would fail by orders of magnitude rather than by
// a flaky one-or-two.
func TestEvents_NoGoroutineLeak(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, dir)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Warm: one connect/disconnect to let any one-shot init goroutines
	// stabilize so the baseline doesn't include first-use overhead.
	doOneConnect(t, ts.URL)

	// Settle: give pending close paths a tick.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	baseline := runtime.NumGoroutine()

	const cycles = 100
	for i := 0; i < cycles; i++ {
		doOneConnect(t, ts.URL)
	}

	// Allow up to a second for the server-side handler goroutines to
	// finish unwinding after the last client disconnects.
	deadline := time.Now().Add(2 * time.Second)
	var after int
	for {
		runtime.GC()
		after = runtime.NumGoroutine()
		if after-baseline <= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after-baseline > 5 {
		t.Errorf("goroutine leak: baseline=%d after %d cycles=%d (delta %d)", baseline, cycles, after, after-baseline)
	}
}

// waitForGenerationEvent reads SSE frames from br until it sees a
// `data: {"generation":N}` payload with N == want, or the deadline
// elapses. Skips keepalive comments and other framing.
func waitForGenerationEvent(br *bufio.Reader, want int64, timeout time.Duration) error {
	type read struct {
		line string
		err  error
	}
	deadline := time.Now().Add(timeout)
	target := fmt.Sprintf("data: {\"generation\":%d}", want)
	for time.Now().Before(deadline) {
		ch := make(chan read, 1)
		go func() {
			s, err := br.ReadString('\n')
			ch <- read{line: s, err: err}
		}()
		select {
		case r := <-ch:
			if r.err != nil && r.err != io.EOF {
				return fmt.Errorf("read: %w", r.err)
			}
			line := strings.TrimRight(r.line, "\n")
			if line == target {
				return nil
			}
			if r.err == io.EOF {
				return fmt.Errorf("eof before seeing %q", target)
			}
		case <-time.After(time.Until(deadline)):
			return fmt.Errorf("timeout waiting for %q", target)
		}
	}
	return fmt.Errorf("timeout waiting for %q", target)
}

// doOneConnect opens an SSE connection, reads at least the first byte
// to confirm the handler is in its select loop, then cancels the
// request and waits for the response body to close. Used by the
// goroutine-leak test to drive a deterministic connect-then-disconnect
// per cycle.
func doOneConnect(t *testing.T, base string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	// Read one byte so the handler has definitely entered its loop;
	// otherwise we might cancel before the goroutine is even started.
	buf := make([]byte, 1)
	if _, err := resp.Body.Read(buf); err != nil && err != io.EOF {
		_ = resp.Body.Close()
		cancel()
		t.Fatalf("read one byte: %v", err)
	}
	cancel()
	// Drain to release the connection back to the pool, then close.
	_, _ = io.Copy(io.Discard, resp.Body)
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
}
