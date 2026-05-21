// Package watch implements a polling tail of a Claude Code session
// JSONL file, surfacing each newly-appended assistant turn (and user
// message) as an Event the live `claudit watch` UI consumes.
//
// We poll with os.Stat rather than depending on fsnotify so claudit
// stays zero-dep beyond its existing surface (yaml).
package watch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// Event is one parsed line. Exactly one of Turn / UserMessage is
// populated; check Kind to disambiguate.
//
// Live distinguishes the initial-history replay from real-time tailing.
// When Tail opens a file with FromBeginning=true it streams every line
// already on disk before catching up to EOF; those events have
// Live=false. Lines that arrive after the initial drain have Live=true.
// Callers that want to alert only on real-time activity (spike
// detection, budget crosses, desktop notifications) should gate on this.
type Event struct {
	Kind parse.LineKind
	Turn parse.Turn
	User parse.UserMessage
	Live bool
}

// Notice is an out-of-band message about the watcher itself —
// rotations, "still polling, file not found yet," etc. Surfacing these
// to the caller (rather than logging) lets the caller decide where to
// print: stderr, a status bar, an alert line.
type Notice struct {
	Kind    NoticeKind
	Message string
}

// NoticeKind enumerates the watcher status events.
type NoticeKind int

const (
	NoticeWaiting   NoticeKind = iota // file not found yet
	NoticeOpened                      // first successful open
	NoticeRotated                     // inode changed mid-stream
	NoticeTruncated                   // size shrank (treated like rotation)
	NoticeMalformed                   // a line failed to decode
	NoticeError                       // non-fatal I/O error worth surfacing (e.g. close failed)
)

// TailOptions tweaks the polling loop. Zero-valued opts are valid.
type TailOptions struct {
	// Interval is how often to stat + read. Defaults to 1s when zero.
	Interval time.Duration
	// FromBeginning reads the entire file before tailing. Default is
	// true — running totals would be wrong otherwise.
	FromBeginning bool
}

// Tail watches path for new lines until ctx is cancelled. Each
// recognized line yields one Event; out-of-band status surfaces via
// onNotice. Both callbacks run on the polling goroutine — keep them
// non-blocking.
//
// If path doesn't exist yet, Tail polls (emitting NoticeWaiting once)
// until it appears. If the file rotates (os.SameFile reports a different
// file) or shrinks, Tail re-opens from the start and emits a notice.
func Tail(ctx context.Context, path string, opts TailOptions, onEvent func(Event), onNotice func(Notice)) error {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if onEvent == nil {
		onEvent = func(Event) {}
	}
	if onNotice == nil {
		onNotice = func(Notice) {}
	}

	t := &tailer{path: path, onEvent: onEvent, onNotice: onNotice, opts: opts}
	defer func() {
		if err := t.close(); err != nil {
			t.onNotice(Notice{Kind: NoticeError, Message: "close: " + err.Error()})
		}
	}()

	if err := t.openWhenReady(ctx); err != nil {
		return err
	}
	t.onNotice(Notice{Kind: NoticeOpened, Message: path})

	// Initial drain. Events emitted here have Live=false so callers
	// can distinguish replayed history from real-time activity.
	if err := t.readAvailable(); err != nil {
		return err
	}
	t.live = true

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Final drain so anything written between the last tick and
			// shutdown doesn't get dropped.
			_ = t.readAvailable()
			return nil
		case <-ticker.C:
			if err := t.poll(); err != nil {
				return err
			}
		}
	}
}

type tailer struct {
	path     string
	opts     TailOptions
	onEvent  func(Event)
	onNotice func(Notice)

	f       *os.File
	prev    os.FileInfo
	offset  int64
	partial []byte
	// live flips to true after openWhenReady's first readAvailable
	// completes (or immediately, when FromBeginning=false). Subsequent
	// reads emit Event.Live=true. Stays true across rotations — a file
	// rotation mid-stream still represents real-time content.
	live bool
}

func (t *tailer) close() error {
	if t.f == nil {
		return nil
	}
	err := t.f.Close()
	t.f = nil
	return err
}

// openWhenReady blocks until path exists and is openable, or ctx ends.
// Polls with the configured interval; emits one NoticeWaiting up front
// so the user knows we're alive.
func (t *tailer) openWhenReady(ctx context.Context) error {
	first := true
	for {
		f, err := os.Open(t.path)
		if err == nil {
			st, statErr := f.Stat()
			if statErr != nil {
				if cerr := f.Close(); cerr != nil {
					t.onNotice(Notice{Kind: NoticeError, Message: "close: " + cerr.Error()})
				}
				return statErr
			}
			t.f = f
			t.prev = st
			if t.opts.FromBeginning {
				t.offset = 0
			} else {
				t.offset = st.Size()
				if _, err := t.f.Seek(t.offset, io.SeekStart); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if first {
			t.onNotice(Notice{Kind: NoticeWaiting, Message: "waiting for " + t.path})
			first = false
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.opts.Interval):
		}
	}
}

// poll runs once per tick. Detects rotation/truncation, then drains.
func (t *tailer) poll() error {
	st, err := os.Stat(t.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// File vanished. Reopen when it returns.
			if err := t.close(); err != nil {
				t.onNotice(Notice{Kind: NoticeError, Message: "close: " + err.Error()})
			}
			t.partial = nil
			t.onNotice(Notice{Kind: NoticeRotated, Message: "file disappeared, waiting for re-create"})
			return t.reopenWhenReady()
		}
		return err
	}
	if t.f == nil || t.prev == nil || !os.SameFile(t.prev, st) {
		t.onNotice(Notice{Kind: NoticeRotated, Message: "file changed, reopening"})
		if err := t.close(); err != nil {
			t.onNotice(Notice{Kind: NoticeError, Message: "close: " + err.Error()})
		}
		t.partial = nil
		if err := t.reopenWhenReady(); err != nil {
			return err
		}
		return t.readAvailable()
	}
	if st.Size() < t.offset {
		t.onNotice(Notice{Kind: NoticeTruncated, Message: "file shrank, reopening"})
		if err := t.close(); err != nil {
			t.onNotice(Notice{Kind: NoticeError, Message: "close: " + err.Error()})
		}
		t.partial = nil
		if err := t.reopenWhenReady(); err != nil {
			return err
		}
		return t.readAvailable()
	}
	return t.readAvailable()
}

func (t *tailer) reopenWhenReady() error {
	// Best-effort re-open. We don't have ctx here; caller's ticker
	// loop will retry next tick if we fail.
	f, err := os.Open(t.path)
	if err != nil {
		return nil // swallow; next poll will try again
	}
	st, err := f.Stat()
	if err != nil {
		if cerr := f.Close(); cerr != nil {
			t.onNotice(Notice{Kind: NoticeError, Message: "close: " + cerr.Error()})
		}
		return err
	}
	t.f = f
	t.prev = st
	t.offset = 0
	return nil
}

// readAvailable reads from the current offset to EOF, splitting on
// newlines. Partial last lines are buffered until the rest arrives.
func (t *tailer) readAvailable() error {
	if t.f == nil {
		return nil
	}
	// Read in chunks — sessions can append several lines per tick.
	buf := make([]byte, 64*1024)
	for {
		n, err := t.f.Read(buf)
		if n > 0 {
			t.offset += int64(n)
			t.consume(buf[:n])
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// consume appends bytes to the partial-line buffer and emits one event
// per complete (newline-terminated) line.
func (t *tailer) consume(b []byte) {
	t.partial = append(t.partial, b...)
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			return
		}
		line := t.partial[:i]
		t.partial = t.partial[i+1:]
		if len(line) == 0 {
			continue
		}
		turn, user, kind := parse.ParseLine(line, t.path)
		switch kind {
		case parse.LineMalformed:
			t.onNotice(Notice{Kind: NoticeMalformed, Message: "skipped malformed line"})
		case parse.LineAssistant:
			t.onEvent(Event{Kind: kind, Turn: turn, Live: t.live})
		case parse.LineUserMessage:
			t.onEvent(Event{Kind: kind, User: user, Live: t.live})
		}
	}
}

// MostRecentJSONL walks root and returns the JSONL with the latest
// mtime. Empty roots return os.ErrNotExist.
func MostRecentJSONL(root string) (string, error) {
	var best string
	var bestMod time.Time
	if err := walkJSONL(root, func(path string, mod time.Time) {
		if best == "" || mod.After(bestMod) {
			best = path
			bestMod = mod
		}
	}); err != nil {
		return "", err
	}
	if best == "" {
		return "", os.ErrNotExist
	}
	return best, nil
}

// FindBySessionID searches root for a JSONL whose filename starts with
// (or equals, with extension) the given session-id prefix. Subagent
// files (under ".../subagents/") are excluded — they share the parent
// session's ID prefix and would mask the real match.
func FindBySessionID(root, idPrefix string) (string, error) {
	var match string
	if err := walkJSONL(root, func(path string, _ time.Time) {
		if match != "" {
			return
		}
		// Skip subagent JSONLs — only the parent session JSONL counts.
		if parse.IsSubagentFile(path) {
			return
		}
		base := baseName(path)
		if base == idPrefix+".jsonl" || hasPrefixIgnoreExt(base, idPrefix) {
			match = path
		}
	}); err != nil {
		return "", err
	}
	if match == "" {
		return "", fmt.Errorf("no JSONL found for session id %q under %s", idPrefix, root)
	}
	return match, nil
}
