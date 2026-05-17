package term

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestStyle_NonFileDisabled(t *testing.T) {
	s := NewStyle(&bytes.Buffer{})
	if s.Enabled() {
		t.Error("buffer should not enable color")
	}
	if got := s.Red("x"); got != "x" {
		t.Errorf("disabled Red = %q, want %q", got, "x")
	}
	if got := s.Bold("x"); got != "x" {
		t.Errorf("disabled Bold = %q", got)
	}
}

func TestStyle_NoColorEnvDisables(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	// Force the "would otherwise be enabled" path by faking enabled:true
	// via the package constructor + manual override is awkward; instead
	// we just confirm NewStyle returns disabled when NO_COLOR is set,
	// regardless of writer.
	s := NewStyle(os.Stdout)
	if s.Enabled() {
		t.Error("NO_COLOR should override TTY detection")
	}
}

func TestStyle_EnabledWrapsWithReset(t *testing.T) {
	// Construct enabled directly to test the wrap path without depending
	// on test-runner TTY-ness.
	s := Style{enabled: true}
	got := s.Red("hello")
	if !strings.HasPrefix(got, "\033[") {
		t.Errorf("expected ANSI prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "\033[0m") {
		t.Errorf("expected reset suffix, got %q", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("expected payload, got %q", got)
	}
}

func TestStyle_EmptyStringPassesThrough(t *testing.T) {
	s := Style{enabled: true}
	if got := s.Bold(""); got != "" {
		t.Errorf("Bold('') = %q, want empty", got)
	}
}

func TestStyle_BoldRed(t *testing.T) {
	s := Style{enabled: true}
	got := s.BoldRed("x")
	// Bold + bright-red SGR codes both appear before payload, single reset.
	if !strings.Contains(got, "\033[1m") || !strings.Contains(got, "\033[91m") {
		t.Errorf("missing bold/bright-red codes: %q", got)
	}
	if strings.Count(got, "\033[0m") != 1 {
		t.Errorf("expected exactly one reset, got %q", got)
	}
}
