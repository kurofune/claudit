// Package corpus is the single data-loading layer shared by every
// claudit command. It walks each provider.Source, parses its log files
// into the per-turn surface the aggregator consumes, and hands back an
// immutable Snapshot.
//
// Two access patterns sit on the same internals:
//
//   - One-shot consumers call LoadConcurrent once. It
//     fans parsing out across GOMAXPROCS workers and honors an optional
//     mtime pre-filter so a date-windowed query skips files that can't
//     contain an in-window turn.
//   - Long-lived consumers (serve, watch) use a Cache: an initial
//     concurrent load, then cheap incremental Refreshes that re-parse
//     only files whose (mtime, size) changed. RunPoller drives the
//     refresh on an interval and SubscribeGeneration lets consumers
//     react to corpus changes.
//
// Polling (rather than fsnotify) keeps the dependency set at zero new
// directs and matches the human-timescale consumers (browser refresh,
// watch panel) that don't need sub-second change detection.
package corpus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/provider"
)

// fileEntry is what the cache remembers for one JSONL path: when we
// last parsed it, plus the parsed payload itself. Held by value in the
// cache map; ParseFile produces fresh slices on every call so there's
// no aliasing concern.
type fileEntry struct {
	mtime       time.Time
	size        int64
	turns       []parse.Turn
	users       []parse.UserMessage
	links       []parse.ParentLink
	toolResults []parse.ToolResult
	compacts    []parse.CompactBoundary
	malformed   int
	// source indexes Cache.sources: the Source that yielded the path.
	source int
}

// Snapshot is an immutable view of the parsed corpus at one moment.
// Handlers read it via an atomic.Pointer so the poller can swap a new
// one in without blocking concurrent readers. Treat it as read-only.
type Snapshot struct {
	Turns       []parse.Turn
	Users       []parse.UserMessage
	Links       []parse.ParentLink
	ToolResults []parse.ToolResult
	// CompactBoundaries are every file's compaction points.
	CompactBoundaries []parse.CompactBoundary
	Generation        int64
	LastUpdate        time.Time
	FileCount         int
	Malformed         int
	// FileErrors holds non-fatal per-file open/parse errors. Populated
	// by LoadConcurrent (the one-shot path surfaces them as a warning);
	// the polled Cache leaves it nil since the daemon tolerates broken
	// files silently and re-reads them next tick.
	FileErrors []error
}

// Cache walks its Sources and keeps a parsed in-memory copy current
// via polling. Safe for concurrent reads via Snapshot(); only one
// Refresh() should run at a time (the poller guarantees this).
type Cache struct {
	sources []provider.Source

	mu    sync.Mutex // guards files map during Refresh
	files map[string]fileEntry

	snapshot atomic.Pointer[Snapshot]
	// generation increments whenever a Refresh actually changes the
	// corpus (file added/modified/removed). Used so consumers can detect
	// "something changed."
	generation int64

	// subsMu guards subs and nextSubID. Separate from mu so a slow
	// subscriber can never stall a Refresh waiting on the files-map
	// lock — publishSnapshot grabs subsMu only after it has finished
	// mutating files under mu.
	subsMu    sync.Mutex
	subs      map[uint64]chan int64
	nextSubID uint64
}

// New returns an empty cache over sources. Call
// Refresh once synchronously before serving traffic so the first read
// has real data. RunPoller does call Refresh on entry, but it must be
// launched in a goroutine, so the listener can race ahead of the first
// refresh — prime it synchronously when that matters.
func New(sources ...provider.Source) *Cache {
	c := &Cache{
		sources: sources,
		files:   map[string]fileEntry{},
	}
	// Publish an empty snapshot so readers don't hit a nil pointer
	// before the first refresh completes.
	c.snapshot.Store(&Snapshot{LastUpdate: time.Now()})
	return c
}

// Snapshot returns the most recent published snapshot. Never nil after
// New. Callers must treat the returned struct as read-only.
func (c *Cache) Snapshot() *Snapshot {
	return c.snapshot.Load()
}

// Refresh walks every Source, re-parses files whose (mtime, size) changed
// since the last refresh, drops entries for files that no longer exist,
// and (if anything changed) publishes a new Snapshot with an
// incremented generation. Returns whether anything changed plus any
// file-walk error encountered.
//
// Parsing of the changed set is fanned out across GOMAXPROCS workers,
// so the cold load (every file "changed") parses concurrently while
// steady-state polls re-parse only the handful of files that moved.
//
// Concurrent-safe in the sense that readers calling Snapshot() won't
// block, but Refresh itself is not meant to be called from multiple
// goroutines — the poller is the only writer.
func (c *Cache) Refresh() (changed bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Pass 1: stat-only walk. Collect the live file set and the subset
	// whose (mtime, size) differs from what we have cached.
	seen := map[string]struct{}{}
	var toParse []fileJob
	mods := map[string]time.Time{}
	sizes := map[string]int64{}
	owners := map[string]int{}
	failed, walkErr := walkSources(c.sources, time.Time{}, func(i int, path string, mod time.Time, size int64) {
		seen[path] = struct{}{}
		owners[path] = i
		mods[path] = mod
		sizes[path] = size
		old, ok := c.files[path]
		// (mtime, size) is a good-enough change signal for append-only
		// session logs: any new turn changes both. A pure mtime check
		// can miss writes inside the FS mtime resolution window; size
		// guards against that.
		if ok && old.mtime.Equal(mod) && old.size == size {
			return
		}
		toParse = append(toParse, fileJob{src: c.sources[i], path: path})
	})

	// Pass 2: parse the changed set concurrently and fold the results
	// back into the files map.
	if len(toParse) > 0 {
		for _, pr := range parseFiles(toParse) {
			entry := fileEntry{
				turns:       pr.turns,
				users:       pr.users,
				links:       pr.links,
				toolResults: pr.toolResults,
				compacts:    pr.compacts,
				malformed:   pr.malformed,
				mtime:       mods[pr.path],
				size:        sizes[pr.path],
				source:      owners[pr.path],
			}
			// On a parse error we still persist whatever we managed to
			// read plus the new (mtime, size) so we don't re-parse the
			// same broken file on every tick.
			c.files[pr.path] = entry
			changed = true
		}
	}

	// Drop entries for files that disappeared (session deleted, rotated).
	// A Source whose walk failed may have stopped before yielding files
	// that still exist, so its unseen entries stay until a walk succeeds.
	for path, e := range c.files {
		if _, ok := seen[path]; !ok && !failed[e.source] {
			delete(c.files, path)
			changed = true
		}
	}

	if !changed && c.snapshot.Load().FileCount == len(c.files) {
		return false, walkErr
	}

	c.publishSnapshot()
	return true, walkErr
}

// publishSnapshot concatenates every cached fileEntry into fresh slices
// and atomically swaps in a new Snapshot. Called with mu held.
func (c *Cache) publishSnapshot() {
	var totalTurns, totalUsers, totalLinks, totalResults, malformed int
	for _, e := range c.files {
		totalTurns += len(e.turns)
		totalUsers += len(e.users)
		totalLinks += len(e.links)
		totalResults += len(e.toolResults)
		malformed += e.malformed
	}
	snap := &Snapshot{
		Turns:       make([]parse.Turn, 0, totalTurns),
		Users:       make([]parse.UserMessage, 0, totalUsers),
		Links:       make([]parse.ParentLink, 0, totalLinks),
		ToolResults: make([]parse.ToolResult, 0, totalResults),
		Generation:  atomic.AddInt64(&c.generation, 1),
		LastUpdate:  time.Now(),
		FileCount:   len(c.files),
		Malformed:   malformed,
	}
	for _, e := range c.files {
		snap.Turns = append(snap.Turns, e.turns...)
		snap.Users = append(snap.Users, e.users...)
		snap.Links = append(snap.Links, e.links...)
		snap.ToolResults = append(snap.ToolResults, e.toolResults...)
		snap.CompactBoundaries = append(snap.CompactBoundaries, e.compacts...)
	}
	c.snapshot.Store(snap)
	c.notifySubscribers(snap.Generation)
}

// SubscribeGeneration registers a buffered channel that receives every
// new snapshot generation published by publishSnapshot, plus an
// unsubscribe func the caller MUST invoke (typically via defer). The
// channel is closed on unsubscribe; callers should not close it.
//
// Pressure-release contract: notifySubscribers does a non-blocking
// send. A subscriber that falls behind coalesces to the most recent
// generation rather than blocking the publisher. Buffer size 1 — the
// only thing a subscriber cares about is "is there a generation newer
// than the one I last rendered?"
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
// so the latest generation always lands in the buffer once the consumer
// next reads. publishSnapshot holds c.mu when this is called; using a
// separate subsMu keeps the contention surfaces disjoint.
func (c *Cache) notifySubscribers(gen int64) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- gen:
		default:
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
// not stop the loop — a transient walk error shouldn't take the
// consumer offline. Returns when ctx is canceled.
func (c *Cache) RunPoller(ctx context.Context, interval time.Duration, onErr func(error)) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
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

// LoadConcurrent walks every Source once and parses each file concurrently,
// returning an immutable Snapshot. When earliest is non-zero, files
// whose mtime predates it are skipped before opening — the mtime
// pre-filter that makes date-windowed report/diff queries cheap.
//
// This is the one-shot entry point; it shares the walk and concurrent
// parse with Cache but keeps no state and never polls.
func LoadConcurrent(sources []provider.Source, earliest time.Time) (*Snapshot, error) {
	// The concurrent parse allocates heavily (every line decodes into
	// short-lived intermediate structs), so the default GC pace fires
	// often and steals cycles from the parse workers — it measurably
	// hurts multicore scaling on a full-corpus load. This is a one-shot
	// batch path (report/diff) that parses everything then exits, so a
	// looser GC target trades a bounded bump in peak heap for a faster
	// load. Scoped to this call and restored on return so the long-lived
	// serve/watch daemons (which use Cache, not LoadConcurrent) keep the
	// default steady-state memory profile.
	defer debug.SetGCPercent(debug.SetGCPercent(200))

	var jobs []fileJob
	_, walkErr := walkSources(sources, earliest, func(i int, path string, _ time.Time, _ int64) {
		jobs = append(jobs, fileJob{src: sources[i], path: path})
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].path < jobs[j].path })

	snap := &Snapshot{LastUpdate: time.Now(), FileCount: len(jobs)}
	for _, pr := range parseFiles(jobs) {
		snap.Turns = append(snap.Turns, pr.turns...)
		snap.Users = append(snap.Users, pr.users...)
		snap.Links = append(snap.Links, pr.links...)
		snap.ToolResults = append(snap.ToolResults, pr.toolResults...)
		snap.CompactBoundaries = append(snap.CompactBoundaries, pr.compacts...)
		snap.Malformed += pr.malformed
		if pr.err != nil {
			snap.FileErrors = append(snap.FileErrors, pr.err)
		}
	}
	return snap, nil
}

// parseResult is one file's parsed payload, tagged with its path so the
// Cache can fold it back into the right map slot.
type parseResult struct {
	path        string
	turns       []parse.Turn
	users       []parse.UserMessage
	links       []parse.ParentLink
	toolResults []parse.ToolResult
	compacts    []parse.CompactBoundary
	malformed   int
	err         error
}

// fileJob is one file to parse and the Source whose parser reads it.
type fileJob struct {
	src  provider.Source
	path string
}

// walkSources walks every Source in order and hands fn each file with
// the index of the Source that owns it. A path an earlier Source already
// yielded is skipped, so overlapping roots never count a file twice.
// Walk errors are joined; one failing Source does not stop the others.
// failed[i] reports whether sources[i]'s walk returned an error, so its
// enumeration may be incomplete.
func walkSources(sources []provider.Source, earliest time.Time, fn func(i int, path string, mod time.Time, size int64)) (failed []bool, err error) {
	owner := map[string]struct{}{}
	failed = make([]bool, len(sources))
	var errs []error
	for i, src := range sources {
		err := src.Walk(earliest, func(path string, mod time.Time, size int64) {
			if _, dup := owner[path]; dup {
				return
			}
			owner[path] = struct{}{}
			fn(i, path, mod, size)
		})
		if err != nil {
			failed[i] = true
			errs = append(errs, fmt.Errorf("%s: %w", src.Name(), err))
		}
	}
	return failed, errors.Join(errs...)
}

// parseFiles fans parsing of jobs out across GOMAXPROCS workers and
// returns one parseResult per job (in arbitrary order). A parse
// error is attached to its result but still carries whatever turns were
// read before the error — a corrupt line shouldn't discard the file.
func parseFiles(paths []fileJob) []parseResult {
	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > len(paths) {
		workers = len(paths)
	}
	jobs := make(chan fileJob, workers*2)
	results := make(chan parseResult, len(paths))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				results <- parseOne(job)
			}
		}()
	}
	go func() {
		for _, p := range paths {
			jobs <- p
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]parseResult, 0, len(paths))
	for r := range results {
		out = append(out, r)
	}
	return out
}

// parseOne opens the job's file and runs its Source's parser. The close
// error is surfaced iff parsing succeeded — a parse error tells the caller
// more.
func parseOne(job fileJob) parseResult {
	path := job.path
	pr := parseResult{path: path}
	f, err := os.Open(path)
	if err != nil {
		pr.err = fmt.Errorf("open %s: %w", path, err)
		return pr
	}
	r, perr := job.src.ParseFile(f, path)
	if cerr := f.Close(); cerr != nil && perr == nil {
		perr = cerr
	}
	pr.turns = r.Turns
	pr.users = r.UserMessages
	pr.links = r.ParentLinks
	pr.toolResults = r.ToolResults
	pr.compacts = r.CompactBoundaries
	pr.malformed = r.Malformed
	if perr != nil {
		pr.err = fmt.Errorf("parse %s: %w", path, perr)
	}
	return pr
}

// HostInfo is a small struct of cache vitals for diagnostic endpoints.
type HostInfo struct {
	Root       string `json:"root"`
	Hostname   string `json:"hostname"`
	GoOS       string `json:"goos"`
	GoArch     string `json:"goarch"`
	GoRuntime  string `json:"go"`
	NumCPU     int    `json:"num_cpu"`
	GOMAXPROCS int    `json:"gomaxprocs"`
}

// HostInfo returns the cache vitals used by serve's /snapshot endpoint.
// Root lists the root of every Source that has one, comma-separated.
func (c *Cache) HostInfo() HostInfo {
	hn, _ := os.Hostname()
	var roots []string
	for _, src := range c.sources {
		if r, ok := src.(interface{ Root() string }); ok {
			roots = append(roots, r.Root())
		}
	}
	return HostInfo{
		Root:       strings.Join(roots, ", "),
		Hostname:   hn,
		GoOS:       runtime.GOOS,
		GoArch:     runtime.GOARCH,
		GoRuntime:  runtime.Version(),
		NumCPU:     runtime.NumCPU(),
		GOMAXPROCS: runtime.GOMAXPROCS(0),
	}
}
