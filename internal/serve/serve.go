package serve

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
	"github.com/kurofune/claudit/internal/render"
)

// Options configures the server. Bind defaults to loopback only; do
// not change that without a deliberate decision — the report can
// contain prompt text and CWD paths and is not authenticated.
type Options struct {
	Bind         string // host:port, e.g. "127.0.0.1:8787"
	Prices       *pricing.Table
	PollInterval time.Duration

	// Defaults applied when the URL query doesn't pin them. These
	// narrow the served report into the "operational dashboard"
	// shape: last week's data, top sessions. The full archive view
	// of `claudit report` is one click away via ?scope=all.
	DefaultLast        time.Duration // 0 disables the default time window
	DefaultHotspots    int
	DefaultSessionsTop int
	DefaultPeriod      aggregate.Period
	DefaultRedact      bool

	// ReloadIntervalSec is how often the in-page script attempts a
	// silent reload (deferred while the user is reading). Browsers
	// poll /status independently of this for the data-change check.
	ReloadIntervalSec int

	// MaxCachedRenders bounds the (filter, generation) HTML cache.
	// 0 disables caching; 16 is plenty for a single-user tool.
	MaxCachedRenders int

	// Version is the short build label rendered in the report's
	// sidebar chrome. Set by cmd/claudit/serve.go from versionShort();
	// internal/serve stays free of debug.ReadBuildInfo so the package
	// remains import-cycle-free and unit-testable.
	Version string

	// Logger is used for the access log and refresh-error reports.
	// nil → slog.Default(). Request-scoped log records carry a
	// request_id attribute so a single failed render can be
	// correlated across the events it produced.
	Logger *slog.Logger
}

// Server is the long-lived HTTP daemon. Build with NewServer, then
// call ListenAndServe (or Serve for a custom listener — used by
// tests).
type Server struct {
	cache *Cache
	opts  Options
	mux   *http.ServeMux

	// subagentCache memoizes the .meta.json read per source file so a
	// stable session doesn't re-stat the same sibling on every render.
	subagentCache sync.Map

	// renderCache stores gzip-encoded and plain response bodies keyed
	// on (canonical-query, section, generation) for every served-mode
	// endpoint — "html" for the rendered report, "data" for the JSON
	// payload, with room for one-per-API-tab section labels in later
	// phases. Section in the key means HTML and JSON halves of one
	// pageload coexist without churning each other (replacing the
	// pre-Phase-1 dual-LRU split). Bounded; LRU-evicted on insert.
	// nil when MaxCachedRenders == 0.
	renderCache *renderLRU

	// aggregateSF collapses concurrent in-flight aggregate+timeline
	// builds for the same (snapshot generation, canonical query).
	// Without it, N parallel cold first-paint fetches (one per browser
	// tab, or the canonical "open /; page fetches /_claudit/data.json
	// in parallel" pageload) each ran the full multi-second
	// aggregation independently. With singleflight, one runs and the
	// rest share the result.
	aggregateSF singleflight.Group

	// aggregateBuildN counts the number of aggregate+timeline builds
	// actually executed (i.e. once per singleflight.Do callback
	// invocation). Test-only hook for asserting the collapse — see
	// TestServer_Singleflight_CollapsesConcurrentBuilds.
	aggregateBuildN atomic.Int64

	// shutdownTimeout caps the graceful-drain wait in serve(). Defaults
	// to 3s; tests override it to force the deadline-exceeded path.
	shutdownTimeout time.Duration

	// shutdownCh is closed at the start of serve()'s shutdown path so
	// long-lived handlers (SSE in particular) can wake from their
	// select loop and return promptly. Without it, http.Server.Shutdown
	// would pin on in-flight SSE connections until shutdownTimeout
	// fires. Closed exactly once via shutdownOnce.
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

// NewServer wires the cache, the default options, and the routes.
// Does not start the poller — call Start to begin background refresh.
func NewServer(cache *Cache, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.DefaultPeriod == "" {
		opts.DefaultPeriod = aggregate.Period("day")
	}
	if opts.ReloadIntervalSec <= 0 {
		opts.ReloadIntervalSec = 30
	}
	s := &Server{
		cache:           cache,
		opts:            opts,
		mux:             http.NewServeMux(),
		shutdownTimeout: 3 * time.Second,
		shutdownCh:      make(chan struct{}),
	}
	if opts.MaxCachedRenders > 0 {
		// One LRU holds entries for every section (html, data, and
		// future per-API-tab keys). Cap is doubled vs. the pre-Phase-1
		// per-LRU cap so that pre-warming the JSON cache from a /
		// render doesn't halve the effective per-section capacity.
		s.renderCache = newRenderLRU(opts.MaxCachedRenders * 2)
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleReport)
	s.mux.HandleFunc("/_claudit/status", s.handleStatus)
	s.mux.HandleFunc("/_claudit/healthz", s.handleHealthz)
	s.mux.HandleFunc(dataPath, s.handleData)
	s.mux.HandleFunc("/events", s.handleEvents)
}

// Handler exposes the http.Handler. Useful for httptest in tests.
// withRequestID is the outermost wrap so every downstream layer
// (including withBodyLimit's error paths) sees a non-empty
// request_id on the context.
func (s *Server) Handler() http.Handler { return withRequestID(withBodyLimit(s.mux)) }

// maxRequestBytes caps the bytes any handler can read from r.Body.
// Defense-in-depth: today's handlers don't read bodies at all (GET/HEAD
// only), but a future handler shouldn't have to remember to bound its
// own reads. 1 MiB is generous for any plausible legitimate request and
// small enough that a hostile client can't pin memory at scale.
const maxRequestBytes = 1 << 20

// withBodyLimit wraps next so every request's body is capped at
// maxRequestBytes. http.MaxBytesReader installs the limit on r.Body;
// over-cap reads surface as *http.MaxBytesError, which downstream
// handlers can treat the same as any read failure.
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		next.ServeHTTP(w, r)
	})
}

// requestIDCtxKey is the unexported type used to store the request_id
// on a context. Unexported so external packages can't collide.
type requestIDCtxKey struct{}

// maxInboundRequestIDLen bounds X-Request-ID echoes. Long enough for
// realistic UUIDs and trace IDs, short enough that an abusive client
// can't bloat log lines or response headers.
const maxInboundRequestIDLen = 128

// withRequestID installs a request_id on the context and echoes it
// back in the X-Request-ID response header. Honors an inbound
// X-Request-ID if it looks safe (printable ASCII, ≤128 chars, no
// whitespace or control characters); otherwise generates a fresh
// 16-hex-char value from crypto/rand. The strictness on inbound IDs is
// belt-and-suspenders against log injection — slog quotes values that
// contain whitespace, but rejecting them outright keeps the header and
// the log line in lockstep.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeInboundRequestID(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDCtxKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sanitizeInboundRequestID returns the inbound ID if it's safe to echo
// (non-empty, printable ASCII, no spaces, ≤maxInboundRequestIDLen)
// and "" otherwise so the middleware can generate a fresh one.
func sanitizeInboundRequestID(in string) string {
	if in == "" || len(in) > maxInboundRequestIDLen {
		return ""
	}
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c <= 0x20 || c >= 0x7f {
			return ""
		}
	}
	return in
}

// newRequestID returns 16 hex chars of random data. crypto/rand.Read
// only fails when the kernel RNG is misconfigured; we fall back to a
// time-based ID so a degraded entropy source can't take the server
// down. The ID is not security-sensitive — it's a correlation token.
func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// requestIDFromContext returns the request_id stored by withRequestID,
// or "" if the context did not pass through the middleware (e.g., a
// test that calls a handler directly on s.mux).
func requestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// reqLogger returns the server's base logger with the request_id
// attached if one is on the context. Handler logging should always
// go through this so log records carry the correlation token.
func (s *Server) reqLogger(ctx context.Context) *slog.Logger {
	if id := requestIDFromContext(ctx); id != "" {
		return s.opts.Logger.With("request_id", id)
	}
	return s.opts.Logger
}

// Start primes the cache with one synchronous refresh and then launches
// the background poller. Blocking on the first refresh is intentional:
// without it, the listener (and an --open browser) can race ahead of
// data and serve the empty initial snapshot — the page loads blank and
// the user has to reload to see anything.
func (s *Server) Start(ctx context.Context) {
	if _, err := s.cache.Refresh(); err != nil {
		s.opts.Logger.LogAttrs(ctx, slog.LevelError, "serve: initial refresh failed", slog.Any("err", err))
	}
	go s.cache.RunPoller(ctx, s.opts.PollInterval, func(err error) {
		s.opts.Logger.LogAttrs(ctx, slog.LevelError, "serve: refresh failed", slog.Any("err", err))
	})
}

// ListenAndServe binds opts.Bind and serves until ctx is canceled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Bind)
	if err != nil {
		return fmt.Errorf("bind %s: %w", s.opts.Bind, err)
	}
	return s.serve(ctx, ln)
}

// Serve is like ListenAndServe but takes a pre-bound listener; used
// by tests that want an ephemeral port.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	return s.serve(ctx, ln)
}

// buildHTTPServer constructs the *http.Server with hardening timeouts
// applied. Slow-loris and stalled-client protection: every kind of
// indefinite hold (headers, body, response write, idle keep-alive)
// gets a bound. Values are conservative defaults — claudit renders
// can be heavy so WriteTimeout is generous, and IdleTimeout permits
// keep-alive reuse for the auto-reload poller.
func (s *Server) buildHTTPServer() *http.Server {
	return &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func (s *Server) serve(ctx context.Context, ln net.Listener) error {
	srv := s.buildHTTPServer()
	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		// Wake long-lived handlers (SSE) first so they return promptly
		// instead of pinning Shutdown until shutdownTimeout fires.
		s.signalShutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			s.opts.Logger.LogAttrs(ctx, slog.LevelError, "serve: shutdown failed", slog.Any("err", err))
		}
		return nil
	case err := <-errCh:
		return err
	}
}

// signalShutdown closes s.shutdownCh exactly once so every selector
// blocked on it wakes. Idempotent; called only by serve() today, but a
// future test could call it directly to exercise the drain path.
func (s *Server) signalShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdownCh) })
}

// Addr returns the bind address as a printable URL for the startup
// banner. Strips the loopback wildcard "" → "127.0.0.1".
func (s *Server) Addr() string {
	a := s.opts.Bind
	if strings.HasPrefix(a, ":") {
		a = "127.0.0.1" + a
	}
	return "http://" + a
}

// handleReport is the main HTML endpoint. Re-aggregates the snapshot
// against the URL query and renders the report with auto-reload +
// scope-pill chrome injected. Cached per (canonical query, generation).
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q, err := parseQuery(r.URL.Query(), time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scope := s.applyDefaults(&q)

	snap := s.cache.Snapshot()
	wantGzip := acceptsGzip(r)

	// Common response headers. Cache-Control no-store: the report
	// reflects a live snapshot that can change at any moment;
	// browser caching is the wrong layer to lean on. Vary on Accept-
	// Encoding so the response cache is correct across clients that
	// don't ask for gzip.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Accept-Encoding")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Render-cache lookup: same canonical query, same generation
	// → reuse the bytes we computed last time. Auto-reload polls
	// often land back on the same URL within a generation, so this
	// is the highest-leverage perf win.
	if body, ok := s.lookupCached(q, sectionHTML, snap.Generation, wantGzip); ok {
		if err := writeCached(w, body, wantGzip); err != nil {
			s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write report response failed",
				slog.Any("err", err),
				slog.String("path", "/"),
				slog.Bool("cached", true))
		}
		return
	}

	html, payload, err := s.renderHTML(r.Context(), snap, q, scope)
	if err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: render failed",
			slog.Any("err", err),
			slog.String("path", "/"))
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	plain := html
	var gz []byte
	if wantGzip {
		gz = gzipBytes(html)
	}

	s.storeCached(q, sectionHTML, snap.Generation, plain, gz)
	// Pre-warm the JSON cache so the page's imminent fetch of
	// /_claudit/data.json is a cache hit, not a second aggregation
	// pass. On a real corpus this cuts cold-refresh server work
	// roughly in half (~1.9s × 2 → ~1.9s).
	s.storeCached(q, sectionData, snap.Generation, payload, gzipBytes(payload))

	body := plain
	if wantGzip {
		body = gz
	}
	if err := writeCached(w, body, wantGzip); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write report response failed",
			slog.Any("err", err),
			slog.String("path", "/"),
			slog.Bool("cached", false))
	}
}

// renderHTML runs the standard aggregation pipeline and returns both the
// rendered HTML and the JSON payload the page will fetch asynchronously.
// Building the payload here is near-free — we already have the
// aggregator and session timelines in hand — and pre-warming the JSON
// cache from the caller eliminates the doubled aggregation work that a
// cold Cmd-R would otherwise pay (HTML handler computes, then data
// handler computes the same thing again ~1.9s later).
func (s *Server) renderHTML(ctx context.Context, snap *Snapshot, q Query, scope ScopeInfo) (html, payload []byte, err error) {
	agg, timelines, err := s.sharedAggregateData(ctx, snap, q)
	if err != nil {
		return nil, nil, err
	}

	payload, err = render.BuildPayload(ctx, agg, render.HTMLOptions{
		SessionTimelines: timelines,
	})
	if err != nil {
		return nil, nil, err
	}

	var buf bytes.Buffer
	err = render.HTMLWithOptions(ctx, &buf, agg, render.HTMLOptions{
		SessionTimelines: timelines,
		Version:          s.opts.Version,
		Serve: render.ServeOptions{
			Enabled:           true,
			Generation:        snap.Generation,
			StatusPath:        "/_claudit/status",
			ReloadIntervalSec: s.opts.ReloadIntervalSec,
			ScopeIsDefault:    scope.IsDefault,
			ScopeWindowLabel:  scope.WindowLabel,
			ScopeSessionsCap:  scope.SessionsCap,
			ScopeLiftURL:      scope.LiftURL,
			// Phase 4: the JSON payload is served at dataPath and
			// fetched asynchronously by the page so initial paint
			// doesn't block on the ~1 MB inline blob.
			DeferData: true,
			DataPath:  dataPath,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), payload, nil
}

// ScopeInfo is the "what's been pruned" summary the scope pill renders.
// Empty WindowLabel + SessionsCap=0 mean nothing was narrowed.
type ScopeInfo struct {
	IsDefault   bool
	WindowLabel string
	SessionsCap int
	// LiftURL is a relative URL ("/?...") the pill links to. It
	// preserves any user-set params and adds scope=all.
	LiftURL string
}

// applyDefaults overlays server defaults onto an unset query. Returns
// the ScopeInfo describing what narrowing the user is currently seeing
// (used for the scope pill).
//
// Rules:
//   - ?scope=all skips every server narrowing.
//   - Server's DefaultLast applies only when the URL specified no
//     time-window keys at all (?last, ?since, ?until).
//   - Server's DefaultSessionsTop applies only when the URL didn't
//     specify ?sessions.
//   - Server's DefaultHotspots / DefaultPeriod / DefaultRedact apply
//     when the URL didn't specify them. Period default does NOT count
//     as "narrowing" (it changes the trend bucket, not the corpus).
func (s *Server) applyDefaults(q *Query) ScopeInfo {
	scope := ScopeInfo{}

	urlSetWindow := q.URLHasLast || q.URLHasSince || q.URLHasUntil
	applyDefaultWindow := !q.ScopeAll && !urlSetWindow && s.opts.DefaultLast > 0
	if applyDefaultWindow {
		now := time.Now()
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		q.Filter.Since = midnight.Add(-s.opts.DefaultLast)
		scope.IsDefault = true
		scope.WindowLabel = humanDuration(s.opts.DefaultLast)
	}

	if !q.ScopeAll && !q.URLHasSess {
		if q.SessionsTop < 0 {
			q.SessionsTop = s.opts.DefaultSessionsTop
		}
		if s.opts.DefaultSessionsTop > 0 {
			scope.IsDefault = true
			scope.SessionsCap = s.opts.DefaultSessionsTop
		}
	} else if q.SessionsTop < 0 {
		// URL didn't specify sessions but did pass scope=all — lift
		// the cap (0 = unlimited, matching the report --sessions=0
		// semantic of "disable view" vs the server-default behavior).
		// Use a generous explicit cap rather than 0 so the view is
		// still rendered.
		q.SessionsTop = 200
	}
	if q.Hotspots < 0 {
		q.Hotspots = s.opts.DefaultHotspots
	}
	if !q.URLHasPeriod {
		q.Period = s.opts.DefaultPeriod
	}

	if scope.IsDefault {
		scope.LiftURL = buildLiftURL(q.rawQuery)
	}
	return scope
}

// buildLiftURL takes the canonical request query and returns a URL
// with scope=all added (or replaced). The pill links to this to
// switch the page out of default-scope mode in one click.
func buildLiftURL(rawQuery string) string {
	vals, _ := url.ParseQuery(rawQuery)
	vals.Set("scope", "all")
	// Drop the keys the user almost certainly wants reset when
	// they hit "show all" — the default window. Leave project,
	// hotspots, by, etc. alone so partial filters are preserved.
	vals.Del("last")
	vals.Del("since")
	vals.Del("until")
	q := vals.Encode()
	if q == "" {
		return "/"
	}
	return "/?" + q
}

// humanDuration formats a Duration into a short human label suitable
// for the scope pill: "7 days", "2 weeks", "30 days". Used only for
// the server's default window.
func humanDuration(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	switch {
	case days >= 14 && days%7 == 0:
		return fmt.Sprintf("%d weeks", days/7)
	case days == 7:
		return "7 days"
	default:
		return fmt.Sprintf("%d days", days)
	}
}

// sharedAggregateData runs buildAggregator + buildTimelines for
// (snap.Generation, q.rawQuery), collapsing concurrent in-flight
// computations into one via singleflight. The pageload pattern that
// motivated this is the cold open: the browser fetches / and then,
// once the HTML lands, fetches /_claudit/data.json — but those two
// hits can also arrive in parallel (Cmd-R + the page's own preload),
// and so can N tabs of the same URL. Without the collapse each one
// independently re-ran the multi-second build; with it, exactly one
// build is in flight at a time per (rawQuery, generation).
//
// The build counter is incremented inside the singleflight callback,
// so it's incremented exactly once per actual build — not per caller.
// Test-only accessor: aggregateBuildCount().
type aggregateData struct {
	agg       *aggregate.Aggregator
	timelines []aggregate.SessionTimeline
}

func (s *Server) sharedAggregateData(ctx context.Context, snap *Snapshot, q Query) (*aggregate.Aggregator, []aggregate.SessionTimeline, error) {
	key := fmt.Sprintf("%d|%s", snap.Generation, q.rawQuery)
	v, err, _ := s.aggregateSF.Do(key, func() (any, error) {
		s.aggregateBuildN.Add(1)
		agg := s.buildAggregator(snap, q)
		timelines, terr := s.buildTimelines(ctx, snap, q)
		if terr != nil {
			return nil, terr
		}
		return aggregateData{agg: agg, timelines: timelines}, nil
	})
	if err != nil {
		return nil, nil, err
	}
	d := v.(aggregateData)
	return d.agg, d.timelines, nil
}

// aggregateBuildCount returns the number of aggregate+timeline builds
// executed since the server started. Test-only accessor.
func (s *Server) aggregateBuildCount() int64 {
	return s.aggregateBuildN.Load()
}

// buildAggregator runs the standard aggregation pipeline against the
// snapshot. Mirrors what cmd/claudit/main.go does for `report --html`,
// minus the file-walking stage (the snapshot already has parsed data).
func (s *Server) buildAggregator(snap *Snapshot, q Query) *aggregate.Aggregator {
	promptIdx := aggregate.BuildPromptIndex(snap.Turns, snap.Users, snap.Links)
	agg := aggregate.New(s.opts.Prices).
		WithFilter(q.Filter).
		WithPeriod(q.Period).
		WithPromptIndex(promptIdx)
	for _, t := range snap.Turns {
		agg.AddWithSubagent(t, s.subagentLookup())
	}
	return agg
}

// subagentLookup returns a SubagentLookup backed by the per-server
// memoization map.
func (s *Server) subagentLookup() aggregate.SubagentLookup {
	return func(t parse.Turn) (string, string) {
		if !parse.IsSubagentFile(t.SourceFile) {
			return "", ""
		}
		if v, ok := s.subagentCache.Load(t.SourceFile); ok {
			m := v.(parse.SubagentMeta)
			return m.AgentType, m.Description
		}
		m, _ := parse.ReadSubagentMeta(t.SourceFile)
		s.subagentCache.Store(t.SourceFile, m)
		return m.AgentType, m.Description
	}
}

// handleStatus emits a tiny JSON object the auto-reload script polls
// for. Keep it cheap: no aggregation, just snapshot vitals.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.cache.Snapshot()
	host := s.cache.hostInfo()
	body := struct {
		Generation  int64    `json:"generation"`
		LastUpdated string   `json:"last_updated"`
		FileCount   int      `json:"file_count"`
		TurnCount   int      `json:"turn_count"`
		Malformed   int      `json:"malformed"`
		Host        hostInfo `json:"host"`
	}{
		Generation:  snap.Generation,
		LastUpdated: snap.LastUpdate.UTC().Format(time.RFC3339),
		FileCount:   snap.FileCount,
		TurnCount:   len(snap.Turns),
		Malformed:   snap.Malformed,
		Host:        host,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := writeJSON(w, body); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write status response failed",
			slog.Any("err", err),
			slog.String("path", "/_claudit/status"))
	}
}

// handleHealthz is the cheapest possible liveness probe.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.WriteString(w, "ok\n"); err != nil {
		s.reqLogger(r.Context()).LogAttrs(r.Context(), slog.LevelError, "serve: write healthz response failed",
			slog.Any("err", err),
			slog.String("path", "/_claudit/healthz"))
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// acceptsGzip is the standard Accept-Encoding probe. Tokens are
// comma-separated; we check membership rather than a full parse so
// "gzip;q=0.5" or "gzip, deflate" both work.
func acceptsGzip(r *http.Request) bool {
	enc := r.Header.Get("Accept-Encoding")
	if enc == "" {
		return false
	}
	for _, tok := range strings.Split(enc, ",") {
		// Drop q-values; we only care about the encoding name.
		name := strings.TrimSpace(strings.SplitN(tok, ";", 2)[0])
		if strings.EqualFold(name, "gzip") {
			return true
		}
	}
	return false
}

// gzipBytes compresses b at Level=BestSpeed. We're optimizing for
// CPU+latency, not bandwidth — losing a few % compression is fine
// when the alternative is a noticeable hitch on a render-cache miss.
func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// writeCached writes the appropriate Content-Encoding/Length headers
// and the body. Body must already be compressed when gz is true.
// Returns the write error so the caller can log truncated transfers
// (client disconnects mid-response).
func writeCached(w http.ResponseWriter, body []byte, gz bool) error {
	if gz {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, err := w.Write(body)
	return err
}
