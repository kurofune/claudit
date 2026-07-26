package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/pricing"
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

func emitWarnings(malformed int, fileErrs []error) {
	if malformed > 0 {
		fmt.Fprintf(os.Stderr, "\nclaudit: skipped %d malformed JSON line(s)\n", malformed)
	}
	if len(fileErrs) > 0 {
		fmt.Fprintf(os.Stderr, "claudit: %d file(s) failed to read; first: %v\n", len(fileErrs), fileErrs[0])
	}
}

// emitUnpricedWarning reports models missing from the price table on stderr.
// The HTML path writes the report to stdout and says nothing in the terminal,
// so without this a stale price table is easy to miss — the totals just come
// out quietly low.
func emitUnpricedWarning(unknown []aggregate.UnknownModel) {
	if len(unknown) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\nclaudit: %d unpriced model(s) — these turns count as $0, so totals are understated:\n", len(unknown))
	for _, u := range unknown {
		fmt.Fprintf(os.Stderr, "  %s — %d tokens across %d turn(s)\n", u.Model, u.Tokens, u.Turns)
	}
	fmt.Fprintf(os.Stderr, "  add rates to ~/.config/claudit/prices.yaml\n")
}
