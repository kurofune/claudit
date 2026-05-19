package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kurofune/claudit/internal/watch/term"
)

// painter is the surface watchState and multiHub use to draw to the
// terminal. Two implementations:
//
//   - screenPainter: full-screen alt-buffer mode for TTYs. Frames are
//     stacked panels rendered with rounded-corner borders.
//   - streamPainter: line-oriented fallback for piped output. Status
//     frames render as a single dot-separated line; alerts print on
//     their own line so they survive grep / less.
//
// Both implementations are single-writer — the caller (watchState or
// multiHub) is already single-threaded around its event loop.
type painter interface {
	// Render paints the current frame.
	Render(frame Frame)
	// Alert records a one-off alert (SPIKE / BUDGET / notice). In
	// screen mode this appends to the in-frame ring buffer and triggers
	// a repaint; in stream mode it prints to stdout.
	Alert(msg string)
	// Close tears down any terminal-mode state (alt buffer, hidden
	// cursor). Safe to call multiple times.
	Close()
	// Style is the colorizer the painter has configured. Callers pass
	// this through to their line builders so the NO_COLOR / TTY
	// decision is made in one place.
	Style() term.Style
}

// Frame is the structured payload a painter renders. Built fresh on
// every event by watchState / multiHub.
type Frame struct {
	// Rolling: today / week / month totals. May be empty when rolling
	// is disabled, in which case the totals panel is omitted.
	Rolling RollingPanelData
	HasRolling bool
	// Live: cross-session totals header + per-session rows. Live.Rows
	// may be empty before the first event arrives.
	Live LivePanelData
}

// RollingPanelData is just the four numbers; the painter formats them.
type RollingPanelData struct {
	Hour, Today, Week, Month float64
}

// LivePanelData carries the live-session view: a one-line header
// (combined cost + session count) and a list of pre-rendered, colored
// body rows.
type LivePanelData struct {
	Header string
	Rows   []string
}

// newPainter returns the right painter for the writer + environment.
// Stdout is a TTY: screenPainter. Otherwise: streamPainter.
func newPainter(out *os.File) painter {
	style := term.NewStyle(out)
	if style.Enabled() && isTTY(out) {
		return newScreenPainter(out, style)
	}
	return newStreamPainter(out, style)
}

func isTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// --- screen painter -------------------------------------------------

// alertCapacity is how many recent alerts the alerts panel keeps.
// Five is enough to scan a recent burst without making the panel
// dominate the screen.
const alertCapacity = 5

type screenPainter struct {
	scr     *term.Screen
	style   term.Style
	alerts  []alertEntry
	last    Frame
	hasLast bool
	mu      sync.Mutex
	stopCh  chan struct{}
	closed  bool
}

type alertEntry struct {
	at  time.Time
	msg string // pre-colored
}

func newScreenPainter(out *os.File, style term.Style) *screenPainter {
	p := &screenPainter{
		scr:    term.NewScreen(out),
		style:  style,
		stopCh: make(chan struct{}),
	}
	p.startResizeHandler()
	return p
}

// handleResize re-reads the terminal size from the kernel and repaints
// the last frame at the new dimensions. Called by the SIGWINCH
// listener on Unix; the Windows polling loop calls pollResize instead.
func (p *screenPainter) handleResize() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.scr.Refresh()
	if p.hasLast {
		p.paint()
	}
}

// pollResize is the Windows-side variant. Only repaints when Refresh
// reports an actual dimension change — repainting unconditionally
// every poll tick would churn the screen for no benefit.
func (p *screenPainter) pollResize() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.scr.Refresh() && p.hasLast {
		p.paint()
	}
}

func (p *screenPainter) Style() term.Style { return p.style }

func (p *screenPainter) Render(frame Frame) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last = frame
	p.hasLast = true
	p.paint()
}

func (p *screenPainter) Alert(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alerts = append(p.alerts, alertEntry{at: time.Now(), msg: msg})
	if len(p.alerts) > alertCapacity {
		p.alerts = p.alerts[len(p.alerts)-alertCapacity:]
	}
	if p.hasLast {
		p.paint()
	}
}

func (p *screenPainter) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	close(p.stopCh)
	p.scr.Close()
}

// paint assembles the three-panel ScreenFrame and sends it. Caller
// must hold p.mu.
func (p *screenPainter) paint() {
	panels := []term.Panel{}
	if p.last.HasRolling {
		panels = append(panels, p.totalsPanel(p.last.Rolling))
	}
	panels = append(panels, p.livePanel(p.last.Live))
	panels = append(panels, p.alertsPanel())
	p.scr.Paint(term.ScreenFrame{Panels: panels})
}

func (p *screenPainter) totalsPanel(d RollingPanelData) term.Panel {
	return term.Panel{
		Title: p.style.Magenta("TOTALS"),
		Body: []string{
			rollingPanelLine(p.style, d.Hour, d.Today, d.Week, d.Month),
		},
		Pad: true,
	}
}

func (p *screenPainter) livePanel(d LivePanelData) term.Panel {
	panel := term.Panel{
		Title:     p.style.Green("LIVE"),
		TitleHint: p.style.Dim(d.Header),
		Body:      d.Rows,
		Pad:       true, // content-heavy panel; totals + alerts stay flush
	}
	if len(panel.Body) == 0 {
		panel.Empty = "waiting for assistant turns…"
	}
	return panel
}

func (p *screenPainter) alertsPanel() term.Panel {
	body := make([]string, 0, len(p.alerts))
	now := time.Now()
	for _, a := range p.alerts {
		age := now.Sub(a.at).Truncate(time.Second)
		body = append(body, fmt.Sprintf("%s  %s", p.style.Dim(formatAge(age)+" ago"), a.msg))
	}
	title := p.style.Dim("ALERTS")
	if len(p.alerts) > 0 {
		title = p.style.Red("ALERTS")
	}
	return term.Panel{
		Title: title,
		Body:  body,
		Empty: "no alerts",
		Pad:   true,
	}
}

// formatAge renders a duration as a compact "2m" / "13s" / "1h12m"
// suitable for an alerts column.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) - h*60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

// --- stream painter -------------------------------------------------

type streamPainter struct {
	w     io.Writer
	style term.Style
}

func newStreamPainter(w io.Writer, style term.Style) *streamPainter {
	return &streamPainter{w: w, style: style}
}

func (p *streamPainter) Style() term.Style { return p.style }

func (p *streamPainter) Render(frame Frame) {
	parts := []string{}
	if frame.HasRolling {
		parts = append(parts, rollingPanelLine(p.style, frame.Rolling.Hour, frame.Rolling.Today, frame.Rolling.Week, frame.Rolling.Month))
	}
	if frame.Live.Header != "" {
		parts = append(parts, frame.Live.Header)
	}
	parts = append(parts, frame.Live.Rows...)
	if len(parts) > 0 {
		fmt.Fprintln(p.w, strings.Join(parts, " · "))
	}
}

func (p *streamPainter) Alert(msg string) {
	fmt.Fprintln(p.w, msg)
}

func (p *streamPainter) Close() {}
