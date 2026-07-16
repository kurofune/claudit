package main

import (
	"io"
	"os"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/watch/term"
)

// TestScreenPainterRenderDoesNotBlockOnSlowWriter pins the invariant
// behind the fix for the watch-freeze bug: Render and Alert must return
// quickly even when the underlying writer is parked. The bug was that
// Render painted synchronously on the event-loop goroutine, so a TTY
// whose pty wasn't draining (Ghostty in a fully-obscured window,
// post-sleep macOS, ...) would block the writer indefinitely, which
// jammed the bounded channels from the Tail goroutines, which stopped
// session polling. Fix is to paint on a dedicated goroutine; Render
// and Alert only flip a dirty flag and a cap-1 wake channel.
//
// The test simulates a stalled pty with an os.Pipe whose reader is
// never read. The pipe buffer (16-64 KiB depending on platform) fills
// after a few paints, parking the paint goroutine inside scr.Paint —
// exactly the production stall. Render / Alert must keep returning.
func TestScreenPainterRenderDoesNotBlockOnSlowWriter(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	// term.NewStyle on a pipe returns a colorless style — that's fine.
	// We're not asserting on output content, only on call latency.
	style := term.NewStyle(w)
	p := newScreenPainter(w, style)

	// Teardown, in order: the painter's background goroutines must stop
	// touching w before w is closed. On Windows the resize watcher polls
	// TerminalSize(w) (reading w.Fd()) on a ticker, so closing w out from
	// under it is a data race (caught by -race in CI). p.Close() closes
	// stopCh to stop that watcher, but it also waits for the paint
	// goroutine — which is parked inside scr.Paint on the deliberately
	// stalled pipe — so drain the reader first to unpark it, then close
	// the pipe. Draining only starts here in teardown, so the pipe stays
	// stalled for the assertions above.
	defer func() {
		drained := make(chan struct{})
		go func() { _, _ = io.Copy(io.Discard, r); close(drained) }()
		p.Close()
		if err := w.Close(); err != nil {
			t.Errorf("close w: %v", err)
		}
		<-drained
		if err := r.Close(); err != nil {
			t.Errorf("close r: %v", err)
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			p.Render(Frame{
				Live: LivePanelData{
					Header: "test",
					Rows:   []string{"row a", "row b"},
				},
			})
			p.Alert("alert")
		}
	}()

	select {
	case <-done:
		// good — Render/Alert returned 200 times despite the pipe
		// never being drained.
	case <-time.After(2 * time.Second):
		t.Fatal("Render/Alert blocked on a stalled writer; paint should be off the event-loop goroutine")
	}
}
