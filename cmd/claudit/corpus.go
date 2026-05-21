package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
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

// listJSONL walks root for .jsonl files. When earliest is non-zero,
// files whose mtime is before earliest are skipped — they can't
// contain any turn newer than earliest, so opening them is wasted I/O.
// This is a big lever on time-windowed `report --since=…` / `diff` /
// `watch` queries against large ~/.claude/projects trees.
func listJSONL(root string, earliest time.Time) ([]string, error) {
	var out []string
	filter := !earliest.IsZero()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate transient errors during walk
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if filter {
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			if info.ModTime().Before(earliest) {
				return nil
			}
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// parseConcurrently fans out parsing to GOMAXPROCS workers and returns the
// concatenated turns, user messages, parent links, the malformed count,
// and any non-fatal file errors.
func parseConcurrently(files []string) ([]parse.Turn, []parse.UserMessage, []parse.ParentLink, int, []error) {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan string, workers*2)
	type result struct {
		turns     []parse.Turn
		users     []parse.UserMessage
		links     []parse.ParentLink
		malformed int
		err       error
	}
	results := make(chan result, len(files))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				f, err := os.Open(path)
				if err != nil {
					results <- result{err: fmt.Errorf("open %s: %w", path, err)}
					continue
				}
				r, err := parse.ParseFile(f, path)
				if cerr := f.Close(); cerr != nil && err == nil {
					err = cerr
				}
				if err != nil {
					results <- result{err: fmt.Errorf("parse %s: %w", path, err), malformed: r.Malformed, turns: r.Turns, users: r.UserMessages, links: r.ParentLinks}
					continue
				}
				results <- result{turns: r.Turns, users: r.UserMessages, links: r.ParentLinks, malformed: r.Malformed}
			}
		}()
	}
	go func() {
		for _, p := range files {
			jobs <- p
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var (
		allTurns  []parse.Turn
		allUsers  []parse.UserMessage
		allLinks  []parse.ParentLink
		malformed int
		errs      []error
	)
	for r := range results {
		allTurns = append(allTurns, r.turns...)
		allUsers = append(allUsers, r.users...)
		allLinks = append(allLinks, r.links...)
		malformed += r.malformed
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return allTurns, allUsers, allLinks, malformed, errs
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

// newSubagentLookup returns a SubagentLookup that lazily reads the
// sibling .meta.json once per source file. Safe for concurrent use; the
// aggregator is single-threaded but we share one lookup across diff's
// two aggregators.
func newSubagentLookup() aggregate.SubagentLookup {
	var cache sync.Map
	return func(t parse.Turn) (string, string) {
		if !parse.IsSubagentFile(t.SourceFile) {
			return "", ""
		}
		if v, ok := cache.Load(t.SourceFile); ok {
			m := v.(parse.SubagentMeta)
			return m.AgentType, m.Description
		}
		m, _ := parse.ReadSubagentMeta(t.SourceFile)
		cache.Store(t.SourceFile, m)
		return m.AgentType, m.Description
	}
}

func emitWarnings(malformed int, fileErrs []error) {
	if malformed > 0 {
		fmt.Fprintf(os.Stderr, "\nclaudit: skipped %d malformed JSON line(s)\n", malformed)
	}
	if len(fileErrs) > 0 {
		fmt.Fprintf(os.Stderr, "claudit: %d file(s) failed to read; first: %v\n", len(fileErrs), fileErrs[0])
	}
}
