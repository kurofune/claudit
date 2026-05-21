package render

import (
	"strconv"
	"strings"
	"testing"

	"github.com/kurofune/claudit/internal/aggregate"
)

// strconvI is a tiny shim used by the *Bars_test.go fixtures so
// large-N rows can be constructed inline without an itoa import in
// each test.
func strconvI(i int) string { return strconv.Itoa(i) }

// TestRenderModelBarsHTML_TwoRowsScaled: two models 75/25 emit two
// hbar rows with fill widths reflecting their share of totalCost.
// Each row's value cell shows money + percent.
func TestRenderModelBarsHTML_TwoRowsScaled(t *testing.T) {
	rows := []aggregate.ModelBucket{
		{Model: "claude-opus-4-7", CostUSD: 7.50},
		{Model: "claude-sonnet-4-6", CostUSD: 2.50},
	}
	got := string(renderModelBarsHTML(rows, 10.00))
	if !strings.Contains(got, `style="width:75.0%"`) {
		t.Errorf("first row should be 75.0%% width; got: %s", got)
	}
	if !strings.Contains(got, `style="width:25.0%"`) {
		t.Errorf("second row should be 25.0%% width; got: %s", got)
	}
	if c := strings.Count(got, `<div class="hbar">`); c != 2 {
		t.Errorf("want 2 hbar rows; got %d", c)
	}
	if !strings.Contains(got, `$7.50 (75.0%)`) {
		t.Errorf("first row val should read '$7.50 (75.0%%)'; got: %s", got)
	}
	if !strings.Contains(got, `$2.50 (25.0%)`) {
		t.Errorf("second row val should read '$2.50 (25.0%%)'; got: %s", got)
	}
}

// TestRenderModelBarsHTML_FiltersZeroCost: rows with zero cost are
// dropped — only priced models render.
func TestRenderModelBarsHTML_FiltersZeroCost(t *testing.T) {
	rows := []aggregate.ModelBucket{
		{Model: "claude-opus-4-7", CostUSD: 5.00},
		{Model: "claude-foo-unknown", CostUSD: 0.0},
	}
	got := string(renderModelBarsHTML(rows, 5.00))
	if strings.Contains(got, `claude-foo-unknown`) {
		t.Errorf("unpriced row should be filtered out; got: %s", got)
	}
	if c := strings.Count(got, `<div class="hbar">`); c != 1 {
		t.Errorf("want exactly 1 hbar row; got %d", c)
	}
}

// TestRenderModelBarsHTML_EscapesModelName: a hostile model name
// containing HTML special chars is escaped in both the label text
// and the title attribute.
func TestRenderModelBarsHTML_EscapesModelName(t *testing.T) {
	rows := []aggregate.ModelBucket{
		{Model: `claude-"<script>"-evil`, CostUSD: 1.00},
	}
	got := string(renderModelBarsHTML(rows, 1.00))
	if strings.Contains(got, `<script>`) {
		t.Errorf("model name with <script> should be escaped; got: %s", got)
	}
	if !strings.Contains(got, `&lt;script&gt;`) {
		t.Errorf("expected escaped tag in output; got: %s", got)
	}
}

// TestRenderModelBarsHTML_OneRowFullWidth: a single model row with
// non-zero cost emits one .hbar div whose .fill carries width:100%
// (single row is 100% of its own total).
func TestRenderModelBarsHTML_OneRowFullWidth(t *testing.T) {
	rows := []aggregate.ModelBucket{
		{Model: "claude-opus-4-7", CostUSD: 5.00},
	}
	got := string(renderModelBarsHTML(rows, 5.00))
	if !strings.Contains(got, `<div class="hbar">`) {
		t.Errorf("missing .hbar div; got: %s", got)
	}
	if !strings.Contains(got, `style="width:100.0%"`) {
		t.Errorf("single row should have fill width 100.0%%; got: %s", got)
	}
	if !strings.Contains(got, `claude-opus-4-7`) {
		t.Errorf("missing model name; got: %s", got)
	}
	if !strings.Contains(got, `$5.00`) {
		t.Errorf("missing formatted money; got: %s", got)
	}
}

// TestRenderToolBarsHTML_UsesCallsSuffix: tool bars show
// money + " · {Count} calls" in the .val cell, not money + percent.
// Mirrors the toolBars() JS variant.
func TestRenderToolBarsHTML_UsesCallsSuffix(t *testing.T) {
	rows := []aggregate.ToolBucket{
		{Name: "Read", Count: 1234, CostUSD: 8.00},
	}
	got := string(renderToolBarsHTML(rows, 10.00))
	if !strings.Contains(got, `$8.00 · 1,234 calls`) {
		t.Errorf("tool val should read '$8.00 · 1,234 calls'; got: %s", got)
	}
	if !strings.Contains(got, `style="width:80.0%"`) {
		t.Errorf("fill width should reflect cost share (80%%); got: %s", got)
	}
	if !strings.Contains(got, `Read`) {
		t.Errorf("missing tool name; got: %s", got)
	}
}

// TestRenderProjectBarsHTML_TopTwentyOnly: with 25 projects supplied,
// only the first 20 render — matches the JS .slice(0, 20).
func TestRenderProjectBarsHTML_TopTwentyOnly(t *testing.T) {
	rows := make([]aggregate.ProjectBucket, 25)
	for i := range rows {
		rows[i] = aggregate.ProjectBucket{
			Project: "/p/proj-" + strconvI(i),
			CostUSD: float64(25 - i), // descending
		}
	}
	got := string(renderProjectBarsHTML(rows, 1000.00))
	if c := strings.Count(got, `<div class="hbar">`); c != 20 {
		t.Errorf("want 20 hbar rows; got %d", c)
	}
	if strings.Contains(got, `/p/proj-20`) {
		t.Errorf("project past index 19 should not render; got: %s", got)
	}
}

// TestRenderProjectBarsHTML_TruncatesLongLabel: a 100-char path is
// visibly truncated to 70 chars (with a leading "…"), while the
// title= keeps the full string.
func TestRenderProjectBarsHTML_TruncatesLongLabel(t *testing.T) {
	long := "/very/deep/" + strings.Repeat("a", 200)
	rows := []aggregate.ProjectBucket{{Project: long, CostUSD: 1.0}}
	got := string(renderProjectBarsHTML(rows, 1.0))
	if strings.Contains(got, `>`+long+`<`) {
		t.Errorf("long path should not appear unwrapped in label text; got: %s", got)
	}
	if !strings.Contains(got, `title="`+long+`"`) {
		t.Errorf("title= should carry full path; got: %s", got)
	}
	if !strings.Contains(got, `…`) {
		t.Errorf("truncated label should include ellipsis; got: %s", got)
	}
}

// TestRenderModelBarsHTML_EmptyShowsHint: an empty model list renders
// the same empty-state hint the JS IIFE used. Tells the user to add
// prices for unknown models — the typical cause of empty rows.
func TestRenderModelBarsHTML_EmptyShowsHint(t *testing.T) {
	got := string(renderModelBarsHTML([]aggregate.ModelBucket{}, 0))
	if !strings.Contains(got, `class="small empty-state"`) {
		t.Errorf("empty list should show empty-state hint; got: %s", got)
	}
	if !strings.Contains(got, `No priced model data`) {
		t.Errorf("empty hint should mention 'No priced model data'; got: %s", got)
	}
}
