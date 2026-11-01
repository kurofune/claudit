package provider

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/parse/codex"
)

var _ Source = (*Codex)(nil)

// Codex reads Codex CLI rollouts under a sessions root (~/.codex/sessions by
// default), laid out as YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl, with older
// rollouts archived as zstd-compressed rollout-<ts>-<uuid>.jsonl.zst.
type Codex struct {
	root string
}

// NewCodex returns the Codex Source rooted at root.
func NewCodex(root string) *Codex { return &Codex{root: root} }

// Name is parse.ProviderCodex.
func (c *Codex) Name() string { return parse.ProviderCodex }

// Root is the sessions root the Source walks.
func (c *Codex) Root() string { return c.root }

// ParseFile is codex.ParseFile, reading a path ending .zst through a zstd
// decoder. The decoder runs single-threaded: the corpus already parses
// GOMAXPROCS files at once, and each extra decoder goroutine costs memory.
func (c *Codex) ParseFile(r io.Reader, path string) (parse.Result, error) {
	if !strings.HasSuffix(path, ".zst") {
		return codex.ParseFile(r, path)
	}
	dec, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return parse.Result{}, fmt.Errorf("zstd: %w", err)
	}
	defer dec.Close()
	return codex.ParseFile(dec, path)
}

// Walk invokes fn for every rollout-*.jsonl and rollout-*.jsonl.zst three
// date directories below the root. When earliest is non-zero, files whose mtime predates it are
// skipped. A missing root, or an entry that vanished mid-walk, yields
// nothing for that entry. Any other failure (an unreadable date directory,
// a failed stat) does not stop the walk, but Walk returns the first such
// error so callers know its enumeration may be incomplete.
func (c *Codex) Walk(earliest time.Time, fn func(path string, mod time.Time, size int64)) error {
	filter := !earliest.IsZero()
	var walkErr error
	record := func(err error) {
		if walkErr == nil && !errors.Is(err, fs.ErrNotExist) {
			walkErr = err
		}
	}
	err := filepath.WalkDir(c.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			record(err)
			if d != nil && d.IsDir() && path != c.root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(c.root, path)
		if rerr != nil || rel == "." {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if d.IsDir() {
			if len(parts) > 3 || !digits(parts[len(parts)-1], datePartWidths[len(parts)-1]) {
				return filepath.SkipDir
			}
			return nil
		}
		name := parts[len(parts)-1]
		if len(parts) != 4 || !strings.HasPrefix(name, "rollout-") || !isRolloutExt(name) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			record(err)
			return nil
		}
		if filter && info.ModTime().Before(earliest) {
			return nil
		}
		fn(path, info.ModTime(), info.Size())
		return nil
	})
	if err != nil {
		return err
	}
	return walkErr
}

func isRolloutExt(name string) bool {
	return strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst")
}

// datePartWidths are the widths of the YYYY, MM and DD directory names.
var datePartWidths = [3]int{4, 2, 2}

func digits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// OpenLog opens a session log for reading back, through a zstd decoder when
// path ends .zst, as ParseFile reads it.
func OpenLog(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".zst") {
		return f, nil
	}
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("zstd: %w", err)
	}
	return zstdLog{dec, f}, nil
}

type zstdLog struct {
	dec *zstd.Decoder
	f   *os.File
}

func (z zstdLog) Read(p []byte) (int, error) { return z.dec.Read(p) }

func (z zstdLog) Close() error {
	z.dec.Close()
	return z.f.Close()
}
