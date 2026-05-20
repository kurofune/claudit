package serve

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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

	// Logger is used for the access log and refresh-error reports.
	// nil → log.Default().
	Logger *log.Logger
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

	// renderCache stores gzip-encoded and plain HTML responses keyed
	// on (canonical-query, generation). Bounded; older entries are
	// evicted on insert. nil when MaxCachedRenders == 0.
	renderCache *renderLRU
}

// NewServer wires the cache, the default options, and the routes.
// Does not start the poller — call Start to begin background refresh.
func NewServer(cache *Cache, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = log.Default()
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
	s := &Server{cache: cache, opts: opts, mux: http.NewServeMux()}
	if opts.MaxCachedRenders > 0 {
		s.renderCache = newRenderLRU(opts.MaxCachedRenders)
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleReport)
	s.mux.HandleFunc("/_claudit/status", s.handleStatus)
	s.mux.HandleFunc("/_claudit/healthz", s.handleHealthz)
}

// Handler exposes the http.Handler. Useful for httptest in tests.
func (s *Server) Handler() http.Handler { return s.mux }

// Start primes the cache with one synchronous refresh and then launches
// the background poller. Blocking on the first refresh is intentional:
// without it, the listener (and an --open browser) can race ahead of
// data and serve the empty initial snapshot — the page loads blank and
// the user has to reload to see anything.
func (s *Server) Start(ctx context.Context) {
	if _, err := s.cache.Refresh(); err != nil {
		s.opts.Logger.Printf("serve: initial refresh error: %v", err)
	}
	go s.cache.RunPoller(ctx, s.opts.PollInterval, func(err error) {
		s.opts.Logger.Printf("serve: refresh error: %v", err)
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
		Handler:           s.mux,
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
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
	if body, ok := s.lookupCached(q, snap.Generation, wantGzip); ok {
		writeCached(w, body, wantGzip)
		return
	}

	html, err := s.renderHTML(snap, q, scope)
	if err != nil {
		s.opts.Logger.Printf("serve: render error: %v", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	plain := html
	var gz []byte
	if wantGzip {
		gz = gzipBytes(html)
	}

	s.storeCached(q, snap.Generation, plain, gz)

	if wantGzip {
		writeCached(w, gz, true)
	} else {
		writeCached(w, plain, false)
	}
}

// renderHTML runs the standard aggregation pipeline and renders the
// HTML report into a single byte slice. The bytes go into the render
// cache as-is; serve-time gzip wraps them.
func (s *Server) renderHTML(snap *Snapshot, q Query, scope ScopeInfo) ([]byte, error) {
	agg := s.buildAggregator(snap, q)

	var timelines []aggregate.SessionTimeline
	if q.SessionsTop > 0 {
		timelines = aggregate.BuildSessionTimelines(
			snap.Turns, snap.Users, snap.Links, s.opts.Prices, q.Filter,
			aggregate.SessionTimelinesOptions{
				TopN:           q.SessionsTop,
				Redact:         q.Redact,
				MaxPromptChars: 2000,
			},
		)
	}

	var buf bytes.Buffer
	err := render.HTMLWithOptions(&buf, agg, render.HTMLOptions{
		SessionTimelines:  timelines,
		ServeMode:         true,
		Generation:        snap.Generation,
		StatusPath:        "/_claudit/status",
		ReloadIntervalSec: s.opts.ReloadIntervalSec,
		ScopeIsDefault:    scope.IsDefault,
		ScopeWindowLabel:  scope.WindowLabel,
		ScopeSessionsCap:  scope.SessionsCap,
		ScopeLiftURL:      scope.LiftURL,
	})
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	_ = writeJSON(w, body)
}

// handleHealthz is the cheapest possible liveness probe.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
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
func writeCached(w http.ResponseWriter, body []byte, gz bool) {
	if gz {
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	_, _ = w.Write(body)
}
