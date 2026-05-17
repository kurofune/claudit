package term

import (
	"os"
	"strconv"
)

// Default terminal dimensions when nothing else works. 80x24 has been
// the conventional fallback since VT100 and is wide enough for the
// claudit watch panels without truncating cost columns.
const (
	defaultCols = 80
	defaultRows = 24
)

// TerminalSize returns the current dimensions of the terminal backing
// f, falling back to $COLUMNS / $LINES and finally 80x24 when those
// are unset or invalid.
func TerminalSize(f *os.File) (cols, rows int) {
	if f != nil {
		if c, r, ok := sysWinsize(f.Fd()); ok {
			return c, r
		}
	}
	cols, rows = envSize()
	return cols, rows
}

// envSize parses $COLUMNS and $LINES, defaulting either dimension to
// the constants above when the env var is missing or non-numeric.
func envSize() (cols, rows int) {
	cols = defaultCols
	rows = defaultRows
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cols = n
		}
	}
	if v := os.Getenv("LINES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rows = n
		}
	}
	return cols, rows
}
