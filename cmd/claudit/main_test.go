package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultProjectsRoot(t *testing.T) {
	// CLAUDE_CONFIG_DIR rerouting matches Claude Code's own behavior —
	// users with dotfiles setups or relocated .claude dirs need claudit
	// to find their JSONLs at the same place.
	custom := filepath.Join(t.TempDir(), "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", custom)
	if got, want := defaultProjectsRoot(), filepath.Join(custom, "projects"); got != want {
		t.Errorf("with env: got %q want %q", got, want)
	}

	// Empty value falls back to ~/.claude — guards against an accidental
	// `export CLAUDE_CONFIG_DIR=` zeroing the path discovery.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this runner")
	}
	want := filepath.Join(home, ".claude", "projects")
	if got := defaultProjectsRoot(); got != want {
		t.Errorf("empty env: got %q want %q", got, want)
	}
}

func TestDefaultDiffWindows_Week(t *testing.T) {
	// Pin "now" to a known instant so the rolling-window math is
	// deterministic. 2026-05-16 12:34:00 UTC — midnight tonight is
	// 2026-05-16 00:00 UTC, which is what the helper anchors against.
	now := time.Date(2026, 5, 16, 12, 34, 0, 0, time.UTC)
	aStart, aEnd, bStart, bEnd, labelA, labelB, err := defaultDiffWindows("week", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantB := []time.Time{
		time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
	}
	wantA := []time.Time{
		time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC),
	}
	if !bStart.Equal(wantB[0]) || !bEnd.Equal(wantB[1]) {
		t.Errorf("B window: got [%s, %s), want [%s, %s)", bStart, bEnd, wantB[0], wantB[1])
	}
	if !aStart.Equal(wantA[0]) || !aEnd.Equal(wantA[1]) {
		t.Errorf("A window: got [%s, %s), want [%s, %s)", aStart, aEnd, wantA[0], wantA[1])
	}
	// Labels are bare date ranges — no "prior"/"last" prose. The dates
	// carry the window length on their own.
	if want := "2026-05-02..2026-05-09"; labelA != want {
		t.Errorf("labelA %q, want %q", labelA, want)
	}
	if want := "2026-05-09..2026-05-16"; labelB != want {
		t.Errorf("labelB %q, want %q", labelB, want)
	}
}

func TestDefaultDiffWindows_Month(t *testing.T) {
	// Month windows are 30d, not calendar months — keeps A and B the
	// same size so delta percentages stay honest.
	now := time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC)
	aStart, aEnd, bStart, bEnd, _, _, err := defaultDiffWindows("month", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bEnd.Sub(bStart) != 30*24*time.Hour {
		t.Errorf("B should span 30d, got %s", bEnd.Sub(bStart))
	}
	if aEnd.Sub(aStart) != 30*24*time.Hour {
		t.Errorf("A should span 30d, got %s", aEnd.Sub(aStart))
	}
	if !aEnd.Equal(bStart) {
		t.Errorf("A should butt up against B: aEnd=%s bStart=%s", aEnd, bStart)
	}
}

func TestDefaultDiffWindows_RejectsUnknownUnit(t *testing.T) {
	_, _, _, _, _, _, err := defaultDiffWindows("fortnight", time.Now())
	if err == nil {
		t.Errorf("expected error for unknown --by value")
	}
}

