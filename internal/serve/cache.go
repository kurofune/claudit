// Package serve runs the long-lived `claudit serve` daemon: a local
// HTTP server that watches the projects root and re-renders the same
// HTML report as `claudit report` against the freshest data.
//
// The cache here is the data layer — it walks the root on a poll
// interval, re-parses only files whose mtime changed, and exposes a
// Snapshot for the HTTP handlers to aggregate against. Polling is used
// (rather than fsnotify) to match the existing watch.go pattern and
// keep the dep set at zero new directs — the human-timescale consumer
// (browser refresh) doesn't need sub-second change detection.
package serve

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// fileEntry is what the cache remembers for one JSONL path: when we
// last parsed it, plus the parsed payload itself. Held by value in the
// cache map; ParseFile produces fresh slices on every call so there's
// no aliasing concern.
type fileEntry struct {
	mtime     time.Time
	size      int64
	turns     []parse.Turn
	users     []parse.UserMessage
	links     []parse.ParentLink
	malformed int
}

// Snapshot is an immutable view of the cached corpus at one moment.
// Handlers read it via an atomic.Pointer so the poller can swap a new
// one in without blocking concurrent requests.
type Snapshot struct {
	Turns      []parse.Turn
	Users      []parse.UserMessage
	Links      []parse.ParentLink
	Generation int64
	LastUpdate time.Time
	FileCount  int
	Malformed  int
}

// Cache walks a projects root and keeps a parsed in-memory copy that
// stays current via polling. Safe for concurrent reads via Snapshot();
// only one Refresh() should run at a time (the poller guarantees this).
type Cache struct {
	root string

	mu    sync.Mutex // guards files map during Refresh
	files map[string]fileEntry

	snapshot atomic.Pointer[Snapshot]
	// generation increments whenever a Refresh actually changes the
	// corpus (file added/modified/removed). Used by /status so the
	// browser-side poller can detect "something changed."
	generation int64

	// subsMu guards subs and nextSubID. Separate from mu so a slow
	// subscriber can never stall a Refresh waiting on the files-map
	// lock — publishSnapshot grabs subsMu only after it has finished
	// mutating files under mu.
	subsMu    sync.Mutex
	subs      map[uint64]chan int64
	nextSubID uint64
}

// NewCache returns an empty cache rooted at the given projects root.
// Call Refresh once synchronously before serving traffic so the first
// request has real data. RunPoller does call Refresh on entry, but it
// must be launched in a goroutine, which means the listener can race
// ahead of the first refresh — Server.Start handles the sync prime.
func NewCache(root string) *Cache {
	c := &Cache{
		root:  root,
		files: map[string]fileEntry{},
	}
	// Publish an empty snapshot so handlers don't hit a nil pointer
	// before the first refresh completes.
	c.snapshot.Store(&Snapshot{LastUpdate: time.Now()})
	return c
}

// Snapshot returns the most recent published snapshot. Never nil after
// NewCache. Callers must treat the returned struct as read-only.
func (c *Cache) Snapshot() *Snapshot {
	return c.snapshot.Load()
}

// Refresh walks the root, re-parses files whose (mtime, size) changed
// since the last refresh, drops entries for files that no longer
// exist, and (if anything changed) publishes a new Snapshot with an
// incremented generation. Returns whether anything changed plus any
// file-walk error encountered.
//
// Concurrent-safe in the sense that handlers reading Snapshot() won't
// block, but Refresh itself is not meant to be called from multiple
// goroutines — the poller is the only writer.
func (c *Cache) Refresh() (changed bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	seen := map[string]struct{}{}
	walkErr := walkJSONL(c.root, func(path string, mod time.Time, size int64) {
		seen[path] = struct{}{}
		old, ok := c.files[path]
		// (mtime, size) is a good-enough change signal for append-only
		// session JSONLs: any new turn changes both. A pure mtime check
		// can miss writes that happen inside the FS mtime resolution
		// window; size guards against that.
		if ok && old.mtime.Equal(mod) && old.size == size {
			return
		}
		entry, perr := parseOne(path)
		if perr != nil {
			// Persist whatever we managed to read plus the new mtime
			// so we don't re-parse the same broken file on every tick.
			entry.mtime = mod
			entry.size = size
			c.files[path] = entry
			changed = true
			return
		}
		entry.mtime = mod
		entry.size = size
		c.files[path] = entry
		changed = true
	})

	// Drop entries for files that disappeared (session was deleted,
	// rotated, etc.). Without this, stale data would linger forever.
	for path := range c.files {
		if _, ok := seen[path]; !ok {
			delete(c.files, path)
			changed = true
		}
	}

	if !changed && c.snapshot.Load().FileCount == len(c.files) {
		// Truly nothing new — keep the current snapshot.
		return false, walkErr
	}

	c.publishSnapshot()
	return true, walkErr
}

// publishSnapshot concatenates every cached fileEntry into fresh
// slices and atomically swaps in a new Snapshot. Called with mu held.
//
// Concatenation is O(total turns), but the parse work is the dominant
// cost upstream; the snapshot build is microseconds even for tens of
// thousands of turns. Building a fresh snapshot each time (rather than
// trying to incrementally mutate one) keeps the immutability contract
// trivial: anyone holding an old *Snapshot is safe forever.
func (c *Cache) publishSnapshot() {
	var (
		totalTurns int
		totalUsers int
		totalLinks int
		malformed  int
	)
	for _, e := range c.files {
		totalTurns += len(e.turns)
		totalUsers += len(e.users)
		totalLinks += len(e.links)
		malformed += e.malformed
	}
	snap := &Snapshot{
		Turns:      make([]parse.Turn, 0, totalTurns),
		Users:      make([]parse.UserMessage, 0, totalUsers),
		Links:      make([]parse.ParentLink, 0, totalLinks),
		Generation: atomic.AddInt64(&c.generation, 1),
		LastUpdate: time.Now(),
		FileCount:  len(c.files),
		Malformed:  malformed,
	}
	for _, e := range c.files {
		snap.Turns = append(snap.Turns, e.turns...)
		snap.Users = append(snap.Users, e.users...)
		snap.Links = append(snap.Links, e.links...)
	}
	c.snapshot.Store(snap)
	c.notifySubscribers(snap.Generation)
}

// SubscribeGeneration registers a buffered channel that receives every
// new snapshot generation published by publishSnapshot, plus an
// unsubscribe func the caller MUST invoke (typically via defer) so the
// subscriber slot is released. The channel is closed on unsubscribe;
// callers should not close it themselves.
//
// Pressure-release contract: notifySubscribers does a non-blocking
// send. A subscriber that falls behind will see its channel coalesce
// to the most recent generation rather than block the publisher — see
// notifySubscribers for the drain-then-resend dance. The buffer is
// kept small (size 1) because the only thing a subscriber cares about
// is "is there a generation newer than the one I last rendered?" —
// missing intermediate values is fine.
func (c *Cache) SubscribeGeneration() (<-chan int64, func()) {
	c.subsMu.Lock()
	if c.subs == nil {
		c.subs = map[uint64]chan int64{}
	}
	id := c.nextSubID
	c.nextSubID++
	ch := make(chan int64, 1)
	c.subs[id] = ch
	c.subsMu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			c.subsMu.Lock()
			if _, ok := c.subs[id]; ok {
				delete(c.subs, id)
				close(ch)
			}
			c.subsMu.Unlock()
		})
	}
	return ch, unsub
}

// notifySubscribers fans the new generation out to every registered
// subscriber. Non-blocking: a full channel triggers a drain-then-resend
// so the latest generation always lands in the buffer once the
// consumer next reads. publishSnapshot holds c.mu when this is called;
// using a separate subsMu keeps the contention surfaces disjoint.
func (c *Cache) notifySubscribers(gen int64) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- gen:
		default:
			// Buffer full: drain the stale value and try once more
			// so the subscriber's next read returns gen rather than
			// an older one. If the drain races with a parallel
			// consumer (it shouldn't — subsMu serializes us), the
			// second send still goes through default and we drop
			// rather than block.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- gen:
			default:
			}
		}
	}
}

// RunPoller refreshes the cache once immediately, then every interval
// until ctx is done. Errors are reported via onErr (if non-nil) but do
// not stop the loop — a transient walk error shouldn't take the daemon
// offline. Returns when ctx is canceled.
func (c *Cache) RunPoller(ctx context.Context, interval time.Duration, onErr func(error)) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	// First refresh is synchronous so callers can `go RunPoller(...)`
	// and trust that Snapshot() returns real data almost immediately.
	if _, err := c.Refresh(); err != nil && onErr != nil {
		onErr(err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := c.Refresh(); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}

// parseOne opens path and runs the standard parser. Returns the entry
// (with malformed count from the parse) plus any open error. The
// close error is surfaced as the function's error iff parsing
// succeeded — a parse error takes precedence since it tells the
// caller more about the file.
func parseOne(path string) (_ fileEntry, retErr error) {
	f, err := os.Open(path)
	if err != nil {
		return fileEntry{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	r, err := parse.ParseFile(f, path)
	entry := fileEntry{
		turns:     r.Turns,
		users:     r.UserMessages,
		links:     r.ParentLinks,
		malformed: r.Malformed,
	}
	if err != nil {
		return entry, fmt.Errorf("parse %s: %w", path, err)
	}
	return entry, nil
}

// walkJSONL invokes fn for every .jsonl file under root. Tolerates
// per-entry walk errors (matches listJSONL / watch.walkJSONL).
//
// Local copy rather than reusing internal/watch.walkJSONL: that one is
// unexported and only emits (path, mtime). We also need size to detect
// writes inside the mtime resolution window.
func walkJSONL(root string, fn func(path string, mod time.Time, size int64)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fn(path, info.ModTime(), info.Size())
		return nil
	})
}

// hostInfo returns a small struct of cache vitals for diagnostic
// endpoints. Compiler reminds us to keep this in sync with /status.
type hostInfo struct {
	Root       string `json:"root"`
	Hostname   string `json:"hostname"`
	GoOS       string `json:"goos"`
	GoArch     string `json:"goarch"`
	GoRuntime  string `json:"go"`
	NumCPU     int    `json:"num_cpu"`
	GOMAXPROCS int    `json:"gomaxprocs"`
}

func (c *Cache) hostInfo() hostInfo {
	hn, _ := os.Hostname()
	return hostInfo{
		Root:       c.root,
		Hostname:   hn,
		GoOS:       runtime.GOOS,
		GoArch:     runtime.GOARCH,
		GoRuntime:  runtime.Version(),
		NumCPU:     runtime.NumCPU(),
		GOMAXPROCS: runtime.GOMAXPROCS(0),
	}
}
