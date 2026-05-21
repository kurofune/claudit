package render

import (
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/aggregate"
)

// oneHotspot builds a single non-prompt hotspot for fixture use.
func oneHotspot() HotspotForJSON {
	return HotspotForJSON{
		Kind:       aggregate.HotspotBashPattern,
		Title:      "Bash pattern: `grep`",
		CostUSD:    3.00,
		PctOfTotal: 30.0,
		Prompt:     "do something",
	}
}

// TestRenderHotspotsHTML_OneHotspotEmitsDetails: a single hotspot
// renders a <details class="hotspot" id="hotspot-1"> with the
// rank, formatted kind, title, money and percent in its summary.
func TestRenderHotspotsHTML_OneHotspotEmitsDetails(t *testing.T) {
	got := string(renderHotspotsHTML([]HotspotForJSON{oneHotspot()}, nil))
	if !strings.Contains(got, `<details class="hotspot" id="hotspot-1">`) {
		t.Errorf("expected <details class=\"hotspot\" id=\"hotspot-1\">; got: %s", got)
	}
	if !strings.Contains(got, `#1`) {
		t.Errorf("missing rank '#1'; got: %s", got)
	}
	// Kind underscores → spaces.
	if !strings.Contains(got, `bash pattern`) {
		t.Errorf("missing formatted kind 'bash pattern'; got: %s", got)
	}
	if !strings.Contains(got, `$3.00`) {
		t.Errorf("missing formatted cost $3.00; got: %s", got)
	}
	if !strings.Contains(got, `(30.0%)`) {
		t.Errorf("missing percent (30.0%%); got: %s", got)
	}
	// The body contains the prompt in a <pre> and a Copy prompt button.
	if !strings.Contains(got, `<pre>do something</pre>`) {
		t.Errorf("expected SSR'd prompt <pre>; got: %s", got)
	}
	if !strings.Contains(got, `Copy prompt`) {
		t.Errorf("expected 'Copy prompt' button label; got: %s", got)
	}
}

// TestRenderHotspotsHTML_PromptPattern_KeyAvailable: a prompt_pattern
// hotspot whose prompt_key is in the timelines set gets a clickable
// view-session button.
func TestRenderHotspotsHTML_PromptPattern_KeyAvailable(t *testing.T) {
	h := HotspotForJSON{
		Kind:       aggregate.HotspotPromptPattern,
		Title:      "Prompt: refactor X",
		CostUSD:    1.00,
		PctOfTotal: 10.0,
		Prompt:     "refactor X",
		PromptKey:  "key-1",
	}
	set := map[string]struct{}{"key-1": {}}
	got := string(renderHotspotsHTML([]HotspotForJSON{h}, set))
	if !strings.Contains(got, `<button class="view-session" data-key="key-1"`) {
		t.Errorf("expected enabled view-session button; got: %s", got)
	}
	if !strings.Contains(got, `view session →`) {
		t.Errorf("expected enabled label 'view session →'; got: %s", got)
	}
}

// TestRenderHotspotsHTML_PromptPattern_KeyUnavailable: a
// prompt_pattern hotspot whose prompt_key is NOT in the set gets
// the disabled "(unavailable)" hint instead of a broken link.
func TestRenderHotspotsHTML_PromptPattern_KeyUnavailable(t *testing.T) {
	h := HotspotForJSON{
		Kind:       aggregate.HotspotPromptPattern,
		Title:      "Prompt: foo",
		Prompt:     "foo",
		PromptKey:  "key-missing",
		CostUSD:    1.0,
		PctOfTotal: 1.0,
	}
	got := string(renderHotspotsHTML([]HotspotForJSON{h}, map[string]struct{}{}))
	if !strings.Contains(got, `class="view-session is-disabled"`) {
		t.Errorf("expected disabled view-session span; got: %s", got)
	}
	if !strings.Contains(got, `view session (unavailable)`) {
		t.Errorf("expected disabled label; got: %s", got)
	}
	if strings.Contains(got, `<button class="view-session"`) {
		t.Errorf("unavailable key should not render as a button; got: %s", got)
	}
}

// TestRenderHotspotsHTML_NonPromptHotspot_NoViewSession: a non-
// prompt-pattern hotspot doesn't render any view-session control.
func TestRenderHotspotsHTML_NonPromptHotspot_NoViewSession(t *testing.T) {
	got := string(renderHotspotsHTML([]HotspotForJSON{oneHotspot()}, map[string]struct{}{"unused": {}}))
	if strings.Contains(got, `view-session`) {
		t.Errorf("non-prompt hotspot should NOT render view-session; got: %s", got)
	}
}

// TestRenderHotspotsHTML_EscapesHostileTitle: a hotspot whose title
// contains HTML-special chars renders escaped both in the visible
// title span and in the title= attribute.
func TestRenderHotspotsHTML_EscapesHostileTitle(t *testing.T) {
	h := oneHotspot()
	h.Title = `<script>alert("x")</script>`
	got := string(renderHotspotsHTML([]HotspotForJSON{h}, nil))
	if strings.Contains(got, `<script>`) {
		t.Errorf("title with <script> should be escaped; got: %s", got)
	}
	if !strings.Contains(got, `&lt;script&gt;`) {
		t.Errorf("expected escaped <script>; got: %s", got)
	}
}

// TestRenderHotspotsHTML_EmptyShowsHint: an empty hotspot list emits
// the empty-state hint suggesting --since / --hotspots adjustments.
func TestRenderHotspotsHTML_EmptyShowsHint(t *testing.T) {
	got := string(renderHotspotsHTML(nil, nil))
	if !strings.Contains(got, `No hotspots in this window`) {
		t.Errorf("empty hotspots should show empty-state hint; got: %s", got)
	}
	if !strings.Contains(got, `class="small empty-state"`) {
		t.Errorf("empty hotspots hint should carry .small.empty-state class; got: %s", got)
	}
}
