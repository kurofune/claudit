package main

import (
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
	defer r.Close()
	defer w.Close()

	// term.NewStyle on a pipe returns a colorless style — that's fine.
	// We're not asserting on output content, only on call latency.
	style := term.NewStyle(w)
	p := newScreenPainter(w, style)
	// Skip Close: with a stalled pipe the paint goroutine is parked
	// inside scr.Paint, so Close would block waiting for it. That's
	// the documented Close behavior — see the comment on Close. The
	// test process exits cleanly because the goroutine is daemon-like.
	_ = p

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
