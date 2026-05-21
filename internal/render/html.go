package render

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"

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

// hotspotForJSON is the same data as aggregate.Hotspot but with the
// pre-rendered LLM prompt baked in, so the front-end can copy it to the
// clipboard without re-rendering the template in JS.
type hotspotForJSON struct {
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
	mainTok, mainCost, mainTurns := a.MainTotals()
	sideTok, sideCost, sideTurns := a.SidechainTotals()

	rawHotspots := a.Hotspots(10)
	hotspots := make([]hotspotForJSON, 0, len(rawHotspots))
	for _, h := range rawHotspots {
		prompt, err := HotspotPrompt(h)
		if err != nil {
			continue
		}
		var pk string
		if h.Kind == aggregate.HotspotPromptPattern && h.Context != nil {
			if v, ok := h.Context["key"].(string); ok {
				pk = v
			}
		}
		hotspots = append(hotspots, hotspotForJSON{
			Kind:       h.Kind,
			Title:      h.Title,
			CostUSD:    h.CostUSD,
			PctOfTotal: h.PctOfTotal,
			Prompt:     prompt,
			PromptKey:  pk,
		})
	}

	payload := struct {
		Totals            aggregate.Totals                    `json:"totals"`
		Hotspots          []hotspotForJSON                    `json:"hotspots"`
		ByModel           []aggregate.ModelBucket             `json:"by_model"`
		ByProject         []aggregate.ProjectBucket           `json:"by_project"`
		ByTool            []aggregate.ToolBucket              `json:"by_tool"`
		ByToolDetail      map[string][]aggregate.DetailBucket `json:"by_tool_detail"`
		BySkill           []aggregate.SkillBucket             `json:"by_skill"`
		Main              sidePart                            `json:"main"`
		Sidechain         sidePart                            `json:"sidechain"`
		BySubagent        []aggregate.SubagentBucket          `json:"by_subagent"`
		AgentInvocations  []aggregate.AgentInvocation         `json:"agent_invocations"`
		UnknownModels     []string                            `json:"unknown_models"`
		Period            aggregate.Period                    `json:"period"`
		TrendTotals       []aggregate.TrendPoint              `json:"trend_totals"`
		TrendByModel      map[string][]aggregate.TrendPoint   `json:"trend_by_model"`
		TrendByProject    map[string][]aggregate.TrendPoint   `json:"trend_by_project"`
		TrendByTool       map[string][]aggregate.TrendPoint   `json:"trend_by_tool"`
		TrendBySession    map[string][]aggregate.TrendPoint   `json:"trend_by_session"`
		TrendBySubagent   map[string][]aggregate.TrendPoint   `json:"trend_by_subagent"`
		OverallHitRatio   float64                             `json:"overall_hit_ratio"`
		CacheByProject    []aggregate.CacheRow                `json:"cache_by_project"`
		CacheBySession    []aggregate.CacheRow                `json:"cache_by_session"`
		CacheBySubagent   []aggregate.CacheRow                `json:"cache_by_subagent"`
		CacheByInvocation []aggregate.CacheRow                `json:"cache_by_invocation"`
		ByPrompt          []aggregate.PromptBucket            `json:"by_prompt"`
		Anomalies         []aggregate.Anomaly                 `json:"anomalies"`
		SessionTimelines  []aggregate.SessionTimeline         `json:"session_timelines"`
	}{
		Totals:            a.Totals(),
		Hotspots:          hotspots,
		ByModel:           a.ByModel(),
		ByProject:         a.ByProject(),
		ByTool:            a.ByTool(),
		ByToolDetail:      a.ByToolDetail(),
		BySkill:           a.BySkill(),
		Main:              sidePart{Cost: mainCost, Turns: mainTurns, Tokens: mainTok},
		Sidechain:         sidePart{Cost: sideCost, Turns: sideTurns, Tokens: sideTok},
		BySubagent:        a.BySubagent(),
		AgentInvocations:  a.AgentInvocations(""),
		UnknownModels:     a.UnknownModels(),
		Period:            a.Period(),
		TrendTotals:       a.TrendTotals(),
		TrendByModel:      a.TrendByModel(),
		TrendByProject:    a.TrendByProject(),
		TrendByTool:       a.TrendByTool(),
		TrendBySession:    a.TrendBySession(),
		TrendBySubagent:   a.TrendBySubagent(),
		OverallHitRatio:   a.OverallHitRatio(),
		CacheByProject:    a.CacheByProject(),
		CacheBySession:    a.CacheBySession(),
		CacheBySubagent:   a.CacheBySubagent(),
		CacheByInvocation: a.CacheByInvocation(),
		ByPrompt:          a.ByPrompt(),
		Anomalies:         a.Anomalies(),
		SessionTimelines:  opts.SessionTimelines,
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal report data: %w", err)
	}
	statusPath := opts.Serve.StatusPath
	if statusPath == "" {
		statusPath = "/_claudit/status"
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
		ServeMode         bool
		Generation        int64
		StatusPath        string
		ReloadIntervalSec int
		ScopeIsDefault    bool
		ScopeWindowLabel  string
		ScopeSessionsCap  int
		ScopeLiftURL      string
		Version           string
	}{
		Tokens:            template.CSS(tokensCSS),
		DataJSON:          template.JS(data),
		ServeMode:         opts.Serve.Enabled,
		Generation:        opts.Serve.Generation,
		StatusPath:        statusPath,
		ReloadIntervalSec: reloadSec,
		ScopeIsDefault:    opts.Serve.ScopeIsDefault,
		ScopeWindowLabel:  opts.Serve.ScopeWindowLabel,
		ScopeSessionsCap:  opts.Serve.ScopeSessionsCap,
		ScopeLiftURL:      liftURL,
		Version:           opts.Version,
	})
}

type sidePart struct {
	Cost   float64          `json:"cost"`
	Turns  int              `json:"turns"`
	Tokens aggregate.Tokens `json:"tokens"`
}
