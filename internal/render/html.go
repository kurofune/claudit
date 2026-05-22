package render

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"

	webassets "github.com/kurofune/claudit"
	"github.com/kurofune/claudit/internal/aggregate"
)

//go:embed report_static.html.tmpl
var staticHTMLTemplate string

// tokensCSS is the shared design-token block embedded once and injected
// into both report_static.html.tmpl and diff.html.tmpl via {{ .Tokens }}.
// The :root + dark @media blocks live in tokens.css to keep the two
// surfaces from drifting (which happened repeatedly when each template
// carried its own copy). Treat it as trusted CSS at execute time.
//
//go:embed tokens.css
var tokensCSS string

// appCSS is the SPA's main stylesheet, read once at init from the
// shared embed at the repo root. Inlined into the static report so
// the downloaded HTML file is self-contained. Read at init (rather
// than //go:embed'd at this file's path) because //go:embed cannot
// reach across packages — the canonical copy lives under web/.
var appCSS string

func init() {
	data, err := fs.ReadFile(webassets.WebFS, "web/app.css")
	if err != nil {
		panic(fmt.Sprintf("render: read web/app.css: %v", err))
	}
	appCSS = string(data)
}

var staticHTMLTpl = template.Must(template.New("static-report").Parse(staticHTMLTemplate))

// HotspotForJSON is the same data as aggregate.Hotspot but with the
// pre-rendered LLM prompt baked in, so the front-end can copy it to the
// clipboard without re-rendering the template in JS. Used by
// OverviewPayload (which /_claudit/api/overview returns and the static
// report inlines).
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
}

// HTML writes a self-contained interactive HTML report to w. Equivalent
// to HTMLWithOptions with a zero-value HTMLOptions — kept as a thin alias
// for callers that don't need the drill-down extras.
func HTML(ctx context.Context, w io.Writer, a *aggregate.Aggregator) error {
	return HTMLWithOptions(ctx, w, a, HTMLOptions{})
}

// HTMLWithOptions writes the SPA-shell HTML report. The output inlines
// per-section JSON (BuildStaticBundle) and the SPA's ES module bundle
// (BuildSPABundle) so a downloaded .html file is fully interactive
// offline. Returns ctx.Err() early if the caller (typically a
// disconnected HTTP client) cancels before the JSON marshal or
// template execute steps.
func HTMLWithOptions(ctx context.Context, w io.Writer, a *aggregate.Aggregator, opts HTMLOptions) error {
	// Fast cancellation path: a disconnected client (or a test
	// passing a pre-canceled ctx) should short-circuit before the
	// CPU-heavy bundle build.
	if err := ctx.Err(); err != nil {
		return err
	}
	bundleJSON, err := BuildStaticBundle(ctx, a, opts)
	if err != nil {
		return err
	}
	spaBundle, err := BuildSPABundle(webassets.WebFS)
	if err != nil {
		return fmt.Errorf("build SPA bundle: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return staticHTMLTpl.Execute(w, struct {
		Tokens           template.CSS
		AppCSS           template.CSS
		StaticBundleJSON template.JS
		SPABundleHTML    template.HTML
		Version          string
	}{
		Tokens:           template.CSS(tokensCSS),
		AppCSS:           template.CSS(appCSS),
		StaticBundleJSON: template.JS(bundleJSON),
		SPABundleHTML:    template.HTML(spaBundle),
		Version:          opts.Version,
	})
}
