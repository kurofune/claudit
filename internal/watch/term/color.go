package term

import (
	"os"
)

// Style applies ANSI color/weight escapes to strings when enabled.
// When disabled, every method returns its input unchanged, so call
// sites can wrap text unconditionally without TTY-checking themselves.
//
// Honors the de-facto NO_COLOR convention (https://no-color.org/):
// if the environment variable is set to any non-empty value, the
// returned Style is permanently disabled regardless of TTY status.
type Style struct {
	enabled bool
}

// NewStyle picks an enabled or disabled Style based on the writer.
// Pass os.Stdout / os.Stderr — anything that isn't a *os.File is
// treated as not-a-terminal and returns a disabled Style.
func NewStyle(w any) Style {
	if os.Getenv("NO_COLOR") != "" {
		return Style{}
	}
	f, ok := w.(*os.File)
	if !ok {
		return Style{}
	}
	fi, err := f.Stat()
	if err != nil {
		return Style{}
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return Style{}
	}
	return Style{enabled: true}
}

// Enabled reports whether color output is on.
func (s Style) Enabled() bool { return s.enabled }

// ANSI SGR (Select Graphic Rendition) codes. Kept package-private —
// callers should compose via the Style methods so we control palette
// drift in one place.
const (
	reset     = "\033[0m"
	bold      = "\033[1m"
	dim       = "\033[2m"
	red       = "\033[31m"
	green     = "\033[32m"
	yellow    = "\033[33m"
	blue      = "\033[34m"
	magenta   = "\033[35m"
	cyan      = "\033[36m"
	brightRed = "\033[91m"
)

func (s Style) wrap(code, text string) string {
	if !s.enabled || text == "" {
		return text
	}
	return code + text + reset
}

// Bold / Dim are weight modifiers — useful for emphasis without color.
func (s Style) Bold(text string) string { return s.wrap(bold, text) }
func (s Style) Dim(text string) string  { return s.wrap(dim, text) }

// Color helpers. Names mirror the ANSI palette; intent-named helpers
// (Money, Label, etc.) live on the call sites in cmd/claudit to keep
// the palette decisions visible there.
func (s Style) Red(text string) string     { return s.wrap(red, text) }
func (s Style) Green(text string) string   { return s.wrap(green, text) }
func (s Style) Yellow(text string) string  { return s.wrap(yellow, text) }
func (s Style) Blue(text string) string    { return s.wrap(blue, text) }
func (s Style) Magenta(text string) string { return s.wrap(magenta, text) }
func (s Style) Cyan(text string) string    { return s.wrap(cyan, text) }

// BoldRed combines bold + bright red for the highest-severity callouts
// (BUDGET CROSSED, SPIKE). One escape pair per wrap keeps the line
// width predictable in case anyone is counting characters downstream.
func (s Style) BoldRed(text string) string {
	if !s.enabled || text == "" {
		return text
	}
	return bold + brightRed + text + reset
}
