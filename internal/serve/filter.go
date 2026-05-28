package serve

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// Query is the parsed view of a request's URL query string. Mirrors
// the `claudit report` flags that are meaningful in HTML mode, plus
// per-request overrides for view caps. Sentinel `-1` on int fields
// means "URL did not set this; let applyDefaults decide."
type Query struct {
	Filter aggregate.Filter
	Period aggregate.Period

	// View knobs — equivalent to the report flags of the same name.
	Hotspots    int // -1 means "use server default"
	SessionsTop int // -1 means "use server default"
	Redact      bool

	// Window provenance — which keys the URL provided. Used by
	// applyDefaults to know whether to overlay the server default
	// window, and by the scope pill to render the right label.
	URLHasLast   bool
	URLHasSince  bool
	URLHasUntil  bool
	URLHasSess   bool
	URLHasPeriod bool

	// LastLabel echoes the raw ?last value (e.g. "7d") so the pill
	// can show it. Only set when URLHasLast is true.
	LastLabel string

	// ScopeAll mirrors ?scope=all — instructs applyDefaults to skip
	// every server-provided narrowing. Useful for the "show all"
	// link in the pill.
	ScopeAll bool

	// rawQuery is the original query string, for cache-key purposes.
	// Kept private — callers should treat Query as opaque.
	rawQuery string
}

// parseQuery reads the supported keys off a url.Values and returns a
// Query plus the first user-facing error encountered (so the server
// can surface "bad ?since=..." as a 400 instead of silently rendering
// the wrong range).
func parseQuery(v url.Values, now time.Time) (Query, error) {
	q := Query{
		Hotspots:    -1,
		SessionsTop: -1,
		rawQuery:    canonicalQueryString(v),
	}

	if s := strings.TrimSpace(v.Get("project")); s != "" {
		q.Filter.ProjectSubstring = s
	}

	last := strings.TrimSpace(v.Get("last"))
	since := strings.TrimSpace(v.Get("since"))
	until := strings.TrimSpace(v.Get("until"))
	if last != "" && since != "" {
		return q, fmt.Errorf("?last and ?since are mutually exclusive")
	}
	if last != "" {
		d, err := parseLastDuration(last)
		if err != nil {
			return q, fmt.Errorf("?last: %w", err)
		}
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		q.Filter.Since = midnight.Add(-d)
		q.URLHasLast = true
		q.LastLabel = last
	}
	if since != "" {
		t, err := parseDateLocal(since, now.Location())
		if err != nil {
			return q, fmt.Errorf("?since: %w", err)
		}
		q.Filter.Since = t
		q.URLHasSince = true
	}
	if until != "" {
		t, err := parseDateLocal(until, now.Location())
		if err != nil {
			return q, fmt.Errorf("?until: %w", err)
		}
		q.Filter.Until = t
		q.URLHasUntil = true
	}

	if by, ok := lookupTrim(v, "by"); ok {
		q.URLHasPeriod = true
		switch by {
		case "day", "week", "month":
			q.Period = aggregate.Period(by)
		case "off":
			q.Period = aggregate.Period("")
		default:
			return q, fmt.Errorf("?by: must be one of day|week|month|off, got %q", by)
		}
	}

	if s, ok := lookupTrim(v, "hotspots"); ok {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return q, fmt.Errorf("?hotspots: must be a non-negative integer, got %q", s)
		}
		q.Hotspots = n
	}
	if s, ok := lookupTrim(v, "sessions"); ok {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return q, fmt.Errorf("?sessions: must be a non-negative integer, got %q", s)
		}
		q.SessionsTop = n
		q.URLHasSess = true
	}
	if s, ok := lookupTrim(v, "redact"); ok {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return q, fmt.Errorf("?redact: must be true/false, got %q", s)
		}
		q.Redact = b
	}
	if s, ok := lookupTrim(v, "scope"); ok {
		switch s {
		case "all":
			q.ScopeAll = true
		default:
			return q, fmt.Errorf("?scope: only ?scope=all is supported, got %q", s)
		}
	}

	return q, nil
}

// lookupTrim returns the trimmed value and true when the key exists in
// v. The empty-string-distinguishes-from-unset detail matters for
// scope-default logic: `?by=` (empty) should be treated as "user
// touched the knob" rather than "fall back to default."
func lookupTrim(v url.Values, key string) (string, bool) {
	if _, ok := v[key]; !ok {
		return "", false
	}
	return strings.TrimSpace(v.Get(key)), true
}

// canonicalQueryString returns the query as a stable, sorted "k=v&k=v"
// string. Used for the render-cache key — two requests with the same
// params in different order must collapse to the same cache entry.
func canonicalQueryString(v url.Values) string {
	if len(v) == 0 {
		return ""
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		vals := append([]string(nil), v[k]...)
		sort.Strings(vals)
		for _, val := range vals {
			if sb.Len() > 0 {
				sb.WriteByte('&')
			}
			sb.WriteString(url.QueryEscape(k))
			sb.WriteByte('=')
			sb.WriteString(url.QueryEscape(val))
		}
	}
	return sb.String()
}

// parseDateLocal parses a YYYY-MM-DD date and anchors it at midnight in
// loc. Date-only filters are wall-clock intent ("everything from the
// 1st"), so they resolve against the user's local zone — the same
// convention ?last and watch's rolling buckets use. (Plain
// time.Parse would silently pin the boundary to UTC.)
func parseDateLocal(s string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// parseLastDuration parses "Nd" or "Nw" (positive integer N) into a
// duration. Copy of the helper in cmd/claudit/main.go — kept here so
// the serve package doesn't reach back into main. Tiny enough that
// duplication is cheaper than carving a shared package.
func parseLastDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("expected Nd or Nw, got %q", s)
	}
	unit := s[len(s)-1]
	var mult time.Duration
	switch unit {
	case 'd':
		mult = 24 * time.Hour
	case 'w':
		mult = 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unit must be 'd' or 'w', got %q", string(unit))
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("expected positive integer prefix, got %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("must be positive, got %d", n)
	}
	return time.Duration(n) * mult, nil
}
