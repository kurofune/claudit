package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/kurofune/claudit/v2/internal/corpus"
	"github.com/kurofune/claudit/v2/internal/pricing"
	"github.com/kurofune/claudit/v2/internal/provider"
)

func defaultProjectsRoot() string {
	// Honor CLAUDE_CONFIG_DIR the same way Claude Code itself does: when
	// set, every ~/.claude path is rerouted under it. Users on dotfiles
	// setups, non-default drives, or sandboxed configs rely on this.
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// defaultCodexRoot is where Codex writes its rollouts: $CODEX_HOME/sessions
// when CODEX_HOME is set (Codex's own override for ~/.codex), else
// ~/.codex/sessions.
func defaultCodexRoot() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// codexRootFlag registers --codex-root on fs, defaulting to defaultCodexRoot.
func codexRootFlag(fs *flag.FlagSet) *string {
	return fs.String("codex-root", defaultCodexRoot(), "Codex sessions directory to watch beside --root (defaults to $CODEX_HOME/sessions or ~/.codex/sessions; empty string disables)")
}

// newServeCache is the corpus serve polls: the Claude Code projects root and,
// unless codexRoot is empty, the Codex sessions root.
func newServeCache(root, codexRoot string) *corpus.Cache {
	sources := []provider.Source{provider.NewClaudeCode(root)}
	if codexRoot != "" {
		sources = append(sources, provider.NewCodex(codexRoot))
	}
	return corpus.New(sources...)
}

// loadPrices resolves the prices file (default or override) and parses it.
func loadPrices(override string) (*pricing.Table, error) {
	pricesFile := override
	if pricesFile == "" {
		p, err := pricing.DefaultPath()
		if err != nil {
			return nil, err
		}
		pricesFile = p
	}
	return pricing.Load(pricesFile)
}
