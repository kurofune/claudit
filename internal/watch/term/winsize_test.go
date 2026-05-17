package term

import (
	"os"
	"testing"
)

func TestEnvSize_DefaultsWhenUnset(t *testing.T) {
	t.Setenv("COLUMNS", "")
	t.Setenv("LINES", "")
	cols, rows := envSize()
	if cols != defaultCols || rows != defaultRows {
		t.Errorf("got %dx%d, want %dx%d", cols, rows, defaultCols, defaultRows)
	}
}

func TestEnvSize_ParsesNumeric(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	t.Setenv("LINES", "40")
	cols, rows := envSize()
	if cols != 120 || rows != 40 {
		t.Errorf("got %dx%d, want 120x40", cols, rows)
	}
}

func TestEnvSize_IgnoresGarbage(t *testing.T) {
	t.Setenv("COLUMNS", "wide")
	cols, _ := envSize()
	if cols != defaultCols {
		t.Errorf("non-numeric COLUMNS should fall back, got %d", cols)
	}
}

func TestTerminalSize_NilFileFallsBack(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	cols, _ := TerminalSize(nil)
	if cols != 100 {
		t.Errorf("nil file should fall through to env, got %d", cols)
	}
}

func TestTerminalSize_DevNullFallsBack(t *testing.T) {
	// /dev/null is openable as a *os.File but isn't a terminal — the
	// ioctl path returns ok=false and we should fall through to env.
	t.Setenv("COLUMNS", "100")
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	cols, _ := TerminalSize(f)
	if cols != 100 {
		t.Errorf("non-tty file should fall through, got %d", cols)
	}
}
