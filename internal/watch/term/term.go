// Package term renders a multi-line live region at the bottom of the
// terminal for `claudit watch`. It owns a fixed area: each Render call
// clears the area it last drew and repaints from the supplied Frame.
//
// The implementation is a small hand-rolled ANSI helper, not a full
// curses/TUI. We don't track cursor position across application writes
// other than our own; callers should funnel out-of-band messages
// through Println instead of writing to the same stream directly.
package term

import (
	"io"
	"os"
	"strings"
)

// Frame is the structured payload one Render paints. Header lines sit
// above Body lines; both are rendered verbatim. Callouts are short,
// one-line transient messages (e.g. "spike: turn 17 cost $1.20 — 6.2x
// median") printed *above* the persistent region and scrolled into the
// terminal's history — Render does not re-clear them on the next paint.
type Frame struct {
	Header   []string
	Body     []string
	Callouts []string
}

// Renderer paints Frames into an io.Writer. Safe for the single-writer
// pattern used by `claudit watch`; do not call Render concurrently.
type Renderer struct {
	ew    errWriter
	tty   bool
	drawn int // lines currently occupied by the live region
}

// New returns a Renderer that paints to w. If w is os.Stdout/Stderr
// and points at a TTY, the renderer uses ANSI cursor controls for a
// multi-line live region. Otherwise it falls back to single-line
// behavior — one Println per Frame collapsing Header+Body into one
// trailing line — so piped output stays readable.
func New(w io.Writer) *Renderer {
	r := &Renderer{ew: errWriter{w: w}}
	if f, ok := w.(*os.File); ok {
		fi, err := f.Stat()
		if err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
			r.tty = true
		}
	}
	return r
}

// IsTTY reports whether the writer is a terminal. Callers can use this
// to decide whether to enable multi-line UI elements at all.
func (r *Renderer) IsTTY() bool { return r.tty }

// Err returns the first write error encountered by any Render / Println /
// Clear call, or nil. Inspecting this is optional — a broken terminal
// stream has nothing meaningful to recover to, but callers that want
// to drop the live region on error can check this between paints.
func (r *Renderer) Err() error { return r.ew.err }

// Render paints frame. On a TTY: clears the previously-drawn region
// (drawn lines worth of cursor-up + clear-line), prints any new
// Callouts (which scroll naturally), then prints Header+Body and
// remembers the new region size.
//
// Off-TTY: prints each Callout on its own line, then prints a single
// compact status line built by joining Header+Body with " · ".
func (r *Renderer) Render(frame Frame) {
	if !r.tty {
		for _, c := range frame.Callouts {
			r.ew.Println(c)
		}
		// Off-TTY status: skip if nothing to show. Combining Header+Body
		// keeps a piped log scannable instead of one entry per sub-line.
		combined := append([]string{}, frame.Header...)
		combined = append(combined, frame.Body...)
		if len(combined) > 0 {
			r.ew.Println(strings.Join(combined, " · "))
		}
		return
	}
	r.clearRegion()
	for _, c := range frame.Callouts {
		// \033[2K clears any leftover characters on the line in case the
		// callout is shorter than whatever previously occupied this row.
		r.ew.Printf("\r\033[2K%s\n", c)
	}
	lines := append([]string{}, frame.Header...)
	lines = append(lines, frame.Body...)
	for i, line := range lines {
		if i == len(lines)-1 {
			// Last line: no trailing newline. Keeps the cursor on this row
			// so a subsequent Render can move back up without skipping past it.
			r.ew.Printf("\r\033[2K%s", line)
		} else {
			r.ew.Printf("\r\033[2K%s\n", line)
		}
	}
	r.drawn = len(lines)
}

// Println prints a one-shot line above the live region (the line
// scrolls into terminal history). Use this for notices that should
// outlive a Render cycle. After printing, the region is re-marked
// empty — the next Render fully repaints.
func (r *Renderer) Println(msg string) {
	if !r.tty {
		r.ew.Println(msg)
		return
	}
	r.clearRegion()
	r.ew.Printf("\r\033[2K%s\n", msg)
}

// Clear wipes the live region and resets internal state. Use before
// the program prints its final summary block so the rolling status
// doesn't sit underneath it.
func (r *Renderer) Clear() {
	if !r.tty {
		return
	}
	r.clearRegion()
}

func (r *Renderer) clearRegion() {
	if r.drawn == 0 {
		return
	}
	// We're sitting on the last line of the region (Render leaves the
	// cursor there). Move up (drawn-1) lines, clearing each as we go,
	// then clear the line we land on.
	for i := 0; i < r.drawn-1; i++ {
		r.ew.Print("\r\033[2K\033[1A")
	}
	r.ew.Print("\r\033[2K")
	r.drawn = 0
}
