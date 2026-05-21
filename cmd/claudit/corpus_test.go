package main

import (
	"os"
	"path/filepath"
	"testing"
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
