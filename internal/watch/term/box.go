package term

import (
	"strings"
)

// Box characters. Rounded corners read as friendlier than sharp; the
// rest of the palette is the standard light box-drawing set.
const (
	cornerTL = "╭"
	cornerTR = "╮"
	cornerBL = "╰"
	cornerBR = "╯"
	hLine    = "─"
	vLine    = "│"
)

// Panel is one stacked section in the watch UI. Title shows on the
// top border ("╭─ totals ──╮"). Body is a list of pre-rendered lines
// (callers add their own colors before passing in). Style controls
// whether borders are colorized (dim) and what color the title uses.
type Panel struct {
	Title     string  // appears in the top border; may include ANSI
	TitleHint string  // appears after Title, dim, e.g. "$18.18 across 1 session"
	Body      []string
	Empty     string  // shown when Body is empty (e.g. "no alerts yet")
	// Pad inserts a blank interior row above and below the body so the
	// content has breathing room from the borders. Off by default —
	// dense one-line panels (totals, alerts) read fine without it; on
	// for content-heavy panels (live) where the spacing matters.
	Pad bool
}

// RenderPanel emits a panel's lines at the given width. When p.Pad is
// set, blank interior rows sit above and below the body; otherwise
// the body cleaves to the borders. Style is used to dim the borders;
// callers must color the Title and Body content themselves.
func RenderPanel(p Panel, width int, st Style) []string {
	if width < 8 {
		width = 8 // anything narrower mangles even one-char content
	}
	out := []string{topBorder(p.Title, p.TitleHint, width, st)}
	if p.Pad {
		out = append(out, sideRow("", width, st))
	}
	body := p.Body
	if len(body) == 0 && p.Empty != "" {
		body = []string{st.Dim(p.Empty)}
	}
	for _, line := range body {
		out = append(out, sideRow(line, width, st))
	}
	if p.Pad {
		out = append(out, sideRow("", width, st))
	}
	out = append(out, bottomBorder(width, st))
	return out
}

// topBorder builds "╭─ title  hint ─────────╮" at exactly `width`
// terminal columns. The title segment is rendered with one leading
// and one trailing space inside dashes for breathing room. If the
// composed title overflows, it's truncated and an ellipsis is added.
func topBorder(title, hint string, width int, st Style) string {
	left := cornerTL + hLine
	right := hLine + cornerTR
	// Fixed overhead: left ("╭─"), space before title, space after, right ("─╮")
	const fixed = 2 + 1 + 1 + 2
	available := width - fixed
	if available < 1 {
		// Pathologically narrow — fall back to "╭─╮" trimmed.
		return cornerTL + strings.Repeat(hLine, width-2) + cornerTR
	}

	heading := title
	if hint != "" {
		heading = title + "  " + hint
	}
	// Heading may carry ANSI color codes — measure visible width only,
	// otherwise the dash count overshoots and the top border ends up
	// wider than the terminal (the line then wraps, dragging the right
	// corner onto a new visual row).
	hLen := VisibleWidth(heading)
	if hLen > available {
		heading = visibleTruncate(heading, available-1) + "…"
		hLen = VisibleWidth(heading)
	}
	dashes := strings.Repeat(hLine, available-hLen)

	// Borders dimmed; heading rendered as-is (callers colorize title themselves).
	return st.Dim(left) + " " + heading + " " + st.Dim(dashes+right)
}

// bottomBorder is "╰────────╯" at width columns, all dimmed.
func bottomBorder(width int, st Style) string {
	if width < 2 {
		width = 2
	}
	return st.Dim(cornerBL + strings.Repeat(hLine, width-2) + cornerBR)
}

// sideRow wraps a single body line with "│ content │", padding or
// truncating content so the right border lands exactly at `width`.
//
// Content can include ANSI escape sequences; VisibleWidth strips them
// before measuring. Truncation is best-effort — anything past the
// budget gets cut, which can split a multi-byte rune. Callers should
// not pass body lines wider than the terminal in the first place.
func sideRow(content string, width int, st Style) string {
	left := st.Dim(vLine) + " "
	right := " " + st.Dim(vLine)
	const fixed = 1 + 1 + 1 + 1 // "│ ", " │"
	available := width - fixed
	if available < 1 {
		return left + right
	}
	vw := VisibleWidth(content)
	if vw > available {
		content = visibleTruncate(content, available-1) + "…"
		vw = VisibleWidth(content)
	}
	pad := available - vw
	if pad < 0 {
		pad = 0
	}
	return left + content + strings.Repeat(" ", pad) + right
}

// VisibleWidth returns the number of printable runes in s, skipping
// over CSI escape sequences (ESC `[` params final-byte). Used to size
// padded box rows correctly when content carries ANSI color codes.
func VisibleWidth(s string) int {
	w := 0
	state := 0 // 0 normal, 1 just-saw-ESC, 2 in CSI params
	for _, r := range s {
		switch state {
		case 1:
			// Consume the introducer (usually '[') and move into params.
			state = 2
			continue
		case 2:
			// CSI final byte is in 0x40-0x7E. Params are 0x30-0x3F.
			if r >= 0x40 && r <= 0x7E {
				state = 0
			}
			continue
		}
		if r == 0x1B {
			state = 1
			continue
		}
		w++
	}
	return w
}

// visibleTruncate trims s so its VisibleWidth is <= n while preserving
// any ANSI escape sequences encountered (so colored content doesn't
// "bleed" past truncation by leaving a reset behind). Returns s with
// a final reset code appended if any non-reset SGR was in effect at
// the truncation point.
func visibleTruncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	w := 0
	state := 0 // 0 normal, 1 just-saw-ESC, 2 in CSI params
	colored := false
	for i, r := range s {
		switch state {
		case 1:
			b.WriteRune(r)
			state = 2
			continue
		case 2:
			b.WriteRune(r)
			if r >= 0x40 && r <= 0x7E {
				state = 0
				// Track whether color is currently on. SGR final byte is
				// 'm'; a bare reset is "\033[0m" or "\033[m".
				if r == 'm' {
					tail := s[:i+1]
					if strings.HasSuffix(tail, "\033[0m") || strings.HasSuffix(tail, "\033[m") {
						colored = false
					} else {
						colored = true
					}
				}
			}
			continue
		}
		if r == 0x1B {
			b.WriteRune(r)
			state = 1
			continue
		}
		if w == n {
			break
		}
		b.WriteRune(r)
		w++
	}
	if colored {
		b.WriteString("\033[0m")
	}
	return b.String()
}
