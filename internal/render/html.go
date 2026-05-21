package render

import (
	"context"
	_ "embed"
	"html/template"
	"io"
	"strings"

	"github.com/kurofune/claudit/internal/aggregate"
)

//go:embed report.html.tmpl
var htmlTemplate string

// tokensCSS is the shared design-token block embedded once and injected
// into both report.html.tmpl and diff.html.tmpl via {{ .Tokens }}. The
// :root + dark @media blocks live in tokens.css to keep the two
// surfaces from drifting (which happened repeatedly when each template
// carried its own copy). Treat it as trusted CSS at execute time.
//
//go:embed tokens.css
var tokensCSS string

var htmlTpl = template.Must(template.New("report").Parse(htmlTemplate))

// HotspotForJSON is the same data as aggregate.Hotspot but with the
// pre-rendered LLM prompt baked in, so the front-end can copy it to the
// clipboard without re-rendering the template in JS. Exported so it can
// appear in the OverviewPayload that /_claudit/api/overview returns
// (Phase 3 of the serve-API plan) — the inner blob the static report
// SSRs lives in the same shape.
type HotspotForJSON struct {
	Kind       aggregate.HotspotKind `json:"kind"`
	Title      string                `json:"title"`
	CostUSD    float64               `json:"cost_usd"`
	PctOfTotal float64               `json:"pct_of_total"`
	Prompt     string                `json:"prompt"`
	// PromptKey is the normalized prompt key for prompt_pattern hotspots,
	// echoed from Hotspot.Context["key"]. Used by the frontend to cross-
	// link the hotspot to its session in the drill-down view. Empty for
	// non-prompt hotspot kinds.
	PromptKey string `json:"prompt_key,omitempty"`
}

// HTMLOptions carries optional extras for the HTML renderer that don't
// fit naturally on the Aggregator (most commonly because they require raw
// parse arrays the aggregator doesn't retain). Zero value is fine — the
// renderer skips any section whose payload is empty.
type HTMLOptions struct {
	// SessionTimelines is the v2.0 drill-down payload: per-session
	// ordered prompts + per-turn summaries. Built by
	// aggregate.BuildSessionTimelines. nil/empty hides the Sessions view.
	SessionTimelines []aggregate.SessionTimeline

	// Version is the compact build label rendered under the brand
	// in the sidebar (e.g. "v1.2.0" or "(devel) abc1234"). Empty
	// strings render no chip — keeps test goldens happy and is the
	// right behavior for `go run` invocations without build info.
	Version string

	// Serve bundles the serve-only chrome (auto-reload toast, scope
	// pill, date-range picker button). Zero value leaves all chrome
	// off — the right default for one-shot `claudit report --html`
	// invocations.
	Serve ServeOptions
}

// ServeOptions configures the serve-only chrome that the template
// injects when claudit is running as an HTTP server. Lives in the
// render package because the template owns the chrome's markup, but
// the fields are meaningful only when wired up by internal/serve.
type ServeOptions struct {
	// Enabled turns on the auto-reload script, the scope pill, and
	// the date-range picker button. When false, all other fields
	// are ignored.
	Enabled bool
	// Generation is the cache generation that this render reflects,
	// echoed to the auto-reload script.
	Generation int64
	// StatusPath is the URL the auto-reload script polls. Defaults
	// to "/_claudit/status".
	StatusPath string
	// ReloadIntervalSec is the cadence (in seconds) at which the
	// in-page script attempts a silent reload. Defaults to 30 when
	// Enabled is true.
	ReloadIntervalSec int

	// ScopeIsDefault is true when the served view has any server-
	// applied narrowing in effect (default time window or default
	// sessions cap). Drives the scope pill.
	ScopeIsDefault bool
	// ScopeWindowLabel is the human label for the active default
	// window (e.g. "7 days"). Empty when no time window is applied
	// by server defaults.
	ScopeWindowLabel string
	// ScopeSessionsCap is the server-default sessions cap currently
	// in effect. 0 when no cap was applied.
	ScopeSessionsCap int
	// ScopeLiftURL is the relative URL the pill's "show all" link
	// targets. Always rooted at "/".
	ScopeLiftURL string

	// DeferData swaps the inline <script id="claudit-data"> blob for a
	// small preamble that fetches the report payload from DataPath at
	// runtime. The HTML can then paint without waiting for the
	// (potentially MB-sized) JSON to stream into the document.
	// Meaningful only when Enabled is true.
	DeferData bool
	// DataPath is the URL the deferred-data preamble fetches. Defaults
	// to "/_claudit/data.json" when DeferData is true.
	DataPath string
}

// HTML writes a self-contained interactive HTML report to w. Equivalent
// to HTMLWithOptions with a zero-value HTMLOptions — kept as a thin alias
// for callers that don't need the drill-down extras.
func HTML(ctx context.Context, w io.Writer, a *aggregate.Aggregator) error {
	return HTMLWithOptions(ctx, w, a, HTMLOptions{})
}

// HTMLWithOptions writes the HTML report and includes any optional
// sections supplied via opts (currently: SessionTimelines). Returns
// ctx.Err() early if the caller (typically a disconnected HTTP client)
// cancels before the JSON marshal or template execute steps.
func HTMLWithOptions(ctx context.Context, w io.Writer, a *aggregate.Aggregator, opts HTMLOptions) error {
	// When DeferData is on, the browser fetches the JSON from DataPath
	// asynchronously — embedding it inline would just duplicate the
	// bytes and undo the whole point. The render cache and the JSON
	// cache cover the cross-request reuse case.
	var data []byte
	if !opts.Serve.Enabled || !opts.Serve.DeferData {
		var err error
		data, err = BuildPayload(ctx, a, opts)
		if err != nil {
			return err
		}
	}
	statusPath := opts.Serve.StatusPath
	if statusPath == "" {
		statusPath = "/_claudit/status"
	}
	dataPath := opts.Serve.DataPath
	if dataPath == "" {
		dataPath = "/_claudit/data.json"
	}
	reloadSec := opts.Serve.ReloadIntervalSec
	if reloadSec <= 0 {
		reloadSec = 30
	}
	liftURL := opts.Serve.ScopeLiftURL
	if liftURL == "" {
		liftURL = "/?scope=all"
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// json.Marshal already escapes <, >, & as < etc., so the bytes
	// are safe to drop into a <script type="application/json"> island.
	// String fields below render inside a JS-string context; html/
	// template auto-applies JS-string escaping for them.
	return htmlTpl.Execute(w, struct {
		Tokens            template.CSS
		DataJSON          template.JS
		DeferData         bool
		DataPath          string
		ServeMode         bool
		Generation        int64
		StatusPath        string
		ReloadIntervalSec int
		ScopeIsDefault    bool
		ScopeWindowLabel  string
		ScopeSessionsCap  int
		ScopeLiftURL      string
		Version           string
		// SessionsHTML is the pre-rendered #session-list inner markup
		// (one <details class="session-card"> per session). Empty when
		// no sessions were supplied — the template's #session-empty
		// element handles the no-data fallback.
		SessionsHTML template.HTML
		// HasSessions controls whether the #session-empty element
		// renders with the `hidden` attribute. Mirrors the old JS
		// behavior that toggled empty.hidden = false on an empty
		// data array.
		HasSessions bool
		// RedactNotice is the server-rendered notice text shown
		// inside #session-redact-notice when any session in the
		// timeline carries a redacted prompt. Empty when no
		// redaction occurred — the element renders empty.
		RedactNotice string
		// TotalsHTML is the server-rendered #totals inner markup:
		// the headline cost tile + three metric tiles. The old JS
		// IIFE that built this from D.totals has been removed —
		// the page paints the headline numbers on first byte.
		TotalsHTML template.HTML
		// ModelBarsHTML is the server-rendered #bars-model inner
		// markup. Replaces the modelBars() JS IIFE.
		ModelBarsHTML template.HTML
		// ProjectBarsHTML is the server-rendered #bars-project
		// inner markup (top 20 projects). Replaces projectBars().
		ProjectBarsHTML template.HTML
		// ToolBarsHTML is the server-rendered #bars-tool inner
		// markup. Replaces the toolBars() JS IIFE.
		ToolBarsHTML template.HTML
		// HotspotsHTML is the server-rendered #hotspots inner
		// markup (the ranked hotspot card stack). Replaces the
		// hotspots() JS IIFE — the copy-prompt button still has
		// JS behavior but reads from the SSR'd <pre>.
		HotspotsHTML template.HTML
	}{
		Tokens:            template.CSS(tokensCSS),
		DataJSON:          template.JS(data),
		DeferData:         opts.Serve.Enabled && opts.Serve.DeferData,
		DataPath:          dataPath,
		ServeMode:         opts.Serve.Enabled,
		Generation:        opts.Serve.Generation,
		StatusPath:        statusPath,
		ReloadIntervalSec: reloadSec,
		ScopeIsDefault:    opts.Serve.ScopeIsDefault,
		ScopeWindowLabel:  opts.Serve.ScopeWindowLabel,
		ScopeSessionsCap:  opts.Serve.ScopeSessionsCap,
		ScopeLiftURL:      liftURL,
		Version:           opts.Version,
		SessionsHTML:      renderSessionsHTML(opts.SessionTimelines),
		HasSessions:       len(opts.SessionTimelines) > 0,
		RedactNotice:      redactNoticeText(opts.SessionTimelines),
		TotalsHTML:        renderTotalsHTML(a.Totals(), a.OverallHitRatio(), a.Period(), a.TrendTotals()),
		ModelBarsHTML:     renderModelBarsHTML(a.ByModel(), a.Totals().CostUSD),
		ProjectBarsHTML:   renderProjectBarsHTML(a.ByProject(), a.Totals().CostUSD),
		ToolBarsHTML:      renderToolBarsHTML(a.ByTool(), a.Totals().CostUSD),
		HotspotsHTML:      renderHotspotsHTML(buildHotspotsForJSON(a), promptKeySet(opts.SessionTimelines)),
	})
}

// promptKeySet rolls a session-timelines slice up into a key→{}
// set the SSR hotspot renderer uses to decide whether a
// prompt_pattern hotspot's cross-link button is enabled or
// disabled. Mirrors the JS-side `promptKeyAvailable` Set built
// from D.prompt_keys.
func promptKeySet(sessions []aggregate.SessionTimeline) map[string]struct{} {
	out := make(map[string]struct{})
	for _, s := range sessions {
		for _, p := range s.Prompts {
			if p.Key == "" {
				continue
			}
			out[p.Key] = struct{}{}
		}
	}
	return out
}

// redactNoticeText returns the user-visible explanation when any
// prompt in any session has been redacted at generation time. Mirrors
// the JS effect at report.html.tmpl:3307-3313 — the message used to
// be set client-side; now it's SSR so users see it on first paint.
func redactNoticeText(sessions []aggregate.SessionTimeline) string {
	for _, s := range sessions {
		for _, p := range s.Prompts {
			if strings.HasPrefix(p.Text, "[redacted ") {
				return "Prompts in this report were redacted at generation time (--redact). Costs and tokens are unaffected."
			}
		}
	}
	return ""
}

type sidePart struct {
	Cost   float64          `json:"cost"`
	Turns  int              `json:"turns"`
	Tokens aggregate.Tokens `json:"tokens"`
}
