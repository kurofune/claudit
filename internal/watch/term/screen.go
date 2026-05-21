package term

import (
	"os"
	"strings"
	"sync"
)

// ANSI control sequences used by Screen. Kept in one block so anyone
// scanning for terminal-control concerns finds them together.
const (
	altEnter   = "\033[?1049h" // switch to alternate screen buffer
	altLeave   = "\033[?1049l" // switch back to main buffer
	curHide    = "\033[?25l"   // hide cursor
	curShow    = "\033[?25h"   // show cursor
	curHome    = "\033[H"      // move cursor to row 1 column 1
	clearAll   = "\033[2J"     // erase entire screen
	clearBelow = "\033[J"      // erase cursor → end of screen (used after home for redraws)
)

// ScreenFrame is the structured payload Screen.Paint consumes. The
// caller composes a list of Panels (totals, live, alerts, etc.); the
// renderer wraps each one with a border, pads them to terminal width,
// and writes the entire frame at once.
type ScreenFrame struct {
	Panels []Panel
}

// Screen owns the alt-screen-buffer mode of `claudit watch`. On
// construction it enters the alt buffer, hides the cursor, and
// captures terminal size. Close() restores everything.
//
// Screen is NOT safe for concurrent Paint() calls. The watch loop is
// already single-threaded around its state, so callers don't need
// internal locking — but if you ever fan rendering out to multiple
// goroutines, wrap calls in a mutex.
type Screen struct {
	ew      errWriter
	f       *os.File
	style   Style
	closeMu sync.Mutex
	closed  bool

	cols, rows int
}

// NewScreen enters alt-screen mode on f, hides the cursor, and
// captures the current terminal size. f must be a TTY — callers
// should branch on TTY detection before constructing a Screen.
func NewScreen(f *os.File) *Screen {
	s := &Screen{
		ew:    errWriter{w: f},
		f:     f,
		style: NewStyle(f),
	}
	s.cols, s.rows = TerminalSize(f)
	s.ew.WriteString(altEnter + curHide + clearAll + curHome)
	return s
}

// Close restores the main screen buffer and shows the cursor. Safe to
// call more than once (subsequent calls are no-ops).
func (s *Screen) Close() {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return
	}
	s.ew.WriteString(curShow + altLeave)
	s.closed = true
}

// Err returns the first write error encountered by any Paint / Println /
// Close call, or nil. Optional inspection point for callers.
func (s *Screen) Err() error { return s.ew.err }

// Style returns the colorizer Screen was constructed with — callers
// pass this through to their panel-builder helpers so a single
// consistent NO_COLOR / TTY decision is shared across the UI.
func (s *Screen) Style() Style { return s.style }

// Size returns the cached terminal width / height. Refresh() rereads
// the dimensions from the kernel; that's the right call to make on
// SIGWINCH.
func (s *Screen) Size() (cols, rows int) { return s.cols, s.rows }

// Refresh re-queries the terminal size and returns true when the
// dimensions changed since the previous query. Unix paths (SIGWINCH-
// driven) can ignore the return value; the Windows polling path uses
// it to skip needless repaints when nothing changed.
func (s *Screen) Refresh() bool {
	c, r := TerminalSize(s.f)
	if c == s.cols && r == s.rows {
		return false
	}
	s.cols, s.rows = c, r
	return true
}

// Paint clears the screen and draws every Panel in frame. Panels
// stack top-to-bottom; if the composed frame is taller than the
// terminal, trailing rows are dropped (no scrolling).
//
// The whole frame is composed in memory first and written in a single
// io.WriteString call so the terminal doesn't show partial paints.
func (s *Screen) Paint(frame ScreenFrame) {
	var b strings.Builder
	b.Grow(s.cols * s.rows)
	b.WriteString(curHome)
	b.WriteString(clearBelow)

	written := 0
	for i, p := range frame.Panels {
		if i > 0 && written < s.rows {
			// Blank line between panels.
			b.WriteString(strings.Repeat(" ", s.cols))
			b.WriteString("\n")
			written++
		}
		for _, line := range RenderPanel(p, s.cols, s.style) {
			if written >= s.rows {
				break
			}
			b.WriteString(line)
			b.WriteString("\n")
			written++
		}
		if written >= s.rows {
			break
		}
	}
	// Trailing blank lines to ensure the cursor doesn't sit on the
	// last drawn line (which can leave artifacts if the next paint is
	// shorter). Erase-to-end-of-screen at the very start already
	// handles this for the redraw case, but we still want the cursor
	// parked somewhere sensible.
	s.ew.WriteString(b.String())
}

// Println writes a one-shot line to the alt screen. Rarely useful in
// alt-screen mode (the line will be overwritten by the next Paint)
// but kept for symmetry with the non-TTY Renderer.
func (s *Screen) Println(msg string) {
	s.ew.Println(msg)
}
