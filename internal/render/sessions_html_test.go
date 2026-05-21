package render

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

// fixedTime builds a deterministic UTC timestamp on 2026-05-01 at the
// supplied hour/minute/second. Keeps test goldens stable.
func fixedTime(h, m, s int) time.Time {
	return time.Date(2026, 5, 1, h, m, s, 0, time.UTC)
}

// oneSession builds a minimal one-session timeline for use across the
// session-renderer tests. Callers tweak fields after construction when
// the test cares about specific values.
func oneSession() aggregate.SessionTimeline {
	return aggregate.SessionTimeline{
		SessionID: "abc-123",
		CWD:       "/p/x",
		StartedAt: fixedTime(10, 0, 0),
		EndedAt:   fixedTime(10, 5, 0),
		CostUSD:   1.23,
		Turns:     1,
	}
}

// TestRenderSessionsHTML_OneSessionEmitsCard: a single SessionTimeline
// produces a <details class="session-card"> element. This is the
// minimum proof that the SSR path is wired up at all.
func TestRenderSessionsHTML_OneSessionEmitsCard(t *testing.T) {
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{oneSession()}))
	if !strings.Contains(got, `<details class="session-card"`) {
		t.Errorf("want a session-card details element; got: %s", got)
	}
}

// TestRenderSessionsHTML_CWDAndStats: the summary carries .s-cwd
// (with title= matching CWD), prompt count + plural, turn count +
// plural, and .s-cost with formatted dollars.
func TestRenderSessionsHTML_CWDAndStats(t *testing.T) {
	s := oneSession()
	s.CWD = "/p/x"
	s.Turns = 2
	s.CostUSD = 4.5
	// Two prompts so we exercise the plural path.
	s.Prompts = []aggregate.PromptTimeline{
		{UUID: "u1", Text: "hi"},
		{UUID: "u2", Text: "bye"},
	}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<span class="s-cwd" title="/p/x">/p/x</span>`) {
		t.Errorf("missing .s-cwd; got: %s", got)
	}
	if !strings.Contains(got, `<span>2 prompts</span>`) {
		t.Errorf("missing prompt count plural; got: %s", got)
	}
	if !strings.Contains(got, `<span>2 turns</span>`) {
		t.Errorf("missing turn count plural; got: %s", got)
	}
	if !strings.Contains(got, `<span class="s-cost">$4.50</span>`) {
		t.Errorf("missing .s-cost ($4.50); got: %s", got)
	}
}

// TestRenderSessionsHTML_SingularPluralization: 1 prompt / 1 turn
// renders the singular form (no trailing "s").
func TestRenderSessionsHTML_SingularPluralization(t *testing.T) {
	s := oneSession()
	s.Turns = 1
	s.Prompts = []aggregate.PromptTimeline{{UUID: "u1", Text: "hi"}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<span>1 prompt</span>`) {
		t.Errorf("missing singular '1 prompt'; got: %s", got)
	}
	if !strings.Contains(got, `<span>1 turn</span>`) {
		t.Errorf("missing singular '1 turn'; got: %s", got)
	}
}

// TestRenderSessionsHTML_EmptyCWDDashFallback: when CWD is empty, the
// text is "—" but the title attribute is empty (matches JS line 3437).
func TestRenderSessionsHTML_EmptyCWDDashFallback(t *testing.T) {
	s := oneSession()
	s.CWD = ""
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<span class="s-cwd" title="">—</span>`) {
		t.Errorf("empty CWD should render — with empty title; got: %s", got)
	}
}

// TestRenderSessionsHTML_AnchorLinkAccessibility: the # anchor carries
// title and aria-label attributes to match the JS contract — the link
// is the only way to copy a deep-link to this session.
func TestRenderSessionsHTML_AnchorLinkAccessibility(t *testing.T) {
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{oneSession()}))
	if !strings.Contains(got, `title="Copy link to this session"`) {
		t.Errorf("anchor missing title attr; got: %s", got)
	}
	if !strings.Contains(got, `aria-label="Copy link to session"`) {
		t.Errorf("anchor missing aria-label; got: %s", got)
	}
}

// TestRenderSessionsHTML_PromptPreviewShort: first non-orphan prompt's
// text is shown verbatim in .s-preview when shorter than 80 chars.
func TestRenderSessionsHTML_PromptPreviewShort(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{
		{UUID: "u1", Text: "do the thing"},
	}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	want := `<span class="s-preview" title="do the thing">do the thing</span>`
	if !strings.Contains(got, want) {
		t.Errorf("missing short preview; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_PromptPreviewTruncated: text longer than 80
// runes is truncated to 80 + ellipsis in the .s-preview slot.
func TestRenderSessionsHTML_PromptPreviewTruncated(t *testing.T) {
	long := strings.Repeat("a", 100)
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{UUID: "u1", Text: long}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	truncated := strings.Repeat("a", 80) + "…"
	wantPreview := `<span class="s-preview" title="` + truncated + `">` + truncated + `</span>`
	if !strings.Contains(got, wantPreview) {
		t.Errorf("missing truncated preview; want %q in %s", wantPreview, got)
	}
	// And the s-preview slot must NOT carry the un-truncated 81-char form
	// (constructive proof that truncation fires before the closing tag).
	if strings.Contains(got, `s-preview" title="`+strings.Repeat("a", 81)) {
		t.Errorf("truncation didn't fire: 81 a's leaked into title attr")
	}
}

// TestRenderSessionsHTML_NoUsablePromptPreview: when every prompt is
// orphan (no UUID) or empty, the preview renders the is-empty
// fallback span.
func TestRenderSessionsHTML_NoUsablePromptPreview(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{
		{UUID: "", Text: "orphan stuff"}, // orphan, ignored
		{UUID: "u1", Text: "   \t\n   "}, // whitespace-only, ignored
	}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	want := `<span class="s-preview is-empty">(no recognizable user prompts)</span>`
	if !strings.Contains(got, want) {
		t.Errorf("missing is-empty fallback; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_PromptPreviewRedactedPassesThrough: a body
// that starts with "[redacted " is shown verbatim (no truncation), so
// the user sees redaction took place.
func TestRenderSessionsHTML_PromptPreviewRedactedPassesThrough(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{
		{UUID: "u1", Text: "[redacted 1234 chars]"},
	}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	want := `<span class="s-preview" title="[redacted 1234 chars]">[redacted 1234 chars]</span>`
	if !strings.Contains(got, want) {
		t.Errorf("redacted preview should pass through; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_TimeRangeWithSpan: the .s-time span shows
// {StartedAt → EndedAt (span)} in local time, formatted YYYY-MM-DD HH:MM.
func TestRenderSessionsHTML_TimeRangeWithSpan(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	old := timeLocal
	defer func() { timeLocal = old }()
	timeLocal = loc

	s := oneSession()
	s.StartedAt = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	s.EndedAt = time.Date(2026, 5, 1, 10, 5, 0, 0, time.UTC)
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	want := `<span class="s-time">2026-05-01 10:00 → 2026-05-01 10:05 (5m)</span>`
	if !strings.Contains(got, want) {
		t.Errorf("missing time range; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_TimeRangeOmitsSpanWhenZero: when start == end,
// fmtSpan returns "" and the parens are dropped.
func TestRenderSessionsHTML_TimeRangeOmitsSpanWhenZero(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	old := timeLocal
	defer func() { timeLocal = old }()
	timeLocal = loc

	s := oneSession()
	s.StartedAt = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	s.EndedAt = s.StartedAt
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	want := `<span class="s-time">2026-05-01 10:00 → 2026-05-01 10:00</span>`
	if !strings.Contains(got, want) {
		t.Errorf("zero span should drop parens; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_PromptBlockBasicShape: a non-orphan prompt
// produces <div class="session-body"><details class="prompt-block">
// with the text in .p-text and turn count + cost in .p-stats.
func TestRenderSessionsHTML_PromptBlockBasicShape(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID:    "u1",
		Text:    "do the thing",
		CostUSD: 0.42,
		Turns: []aggregate.TurnSummary{
			{Timestamp: fixedTime(10, 0, 1), Model: "claude-opus-4-7"},
			{Timestamp: fixedTime(10, 0, 2), Model: "claude-opus-4-7"},
		},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<div class="session-body">`) {
		t.Errorf("missing session-body wrapper; got: %s", got)
	}
	if !strings.Contains(got, `<details class="prompt-block">`) {
		t.Errorf("missing prompt-block; got: %s", got)
	}
	if !strings.Contains(got, `<span class="p-text">do the thing</span>`) {
		t.Errorf("missing .p-text text; got: %s", got)
	}
	if !strings.Contains(got, `<span>2 turns</span>`) {
		t.Errorf("missing prompt turn count; got: %s", got)
	}
	if !strings.Contains(got, `<span class="p-cost">$0.42</span>`) {
		t.Errorf("missing .p-cost; got: %s", got)
	}
}

// TestRenderSessionsHTML_OrphanPromptBlock: a prompt with UUID="" is
// an orphan — gets data-orphan="1", no data-prompt-key, and its
// .p-text says "(orphan turns — no recognized originating prompt)"
// (the raw Text is hidden because there is no real prompt).
func TestRenderSessionsHTML_OrphanPromptBlock(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "",
		Text: "whatever",
		Key:  "",
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<details class="prompt-block" data-orphan="1">`) {
		t.Errorf("missing data-orphan='1' on orphan prompt; got: %s", got)
	}
	if strings.Contains(got, `data-prompt-key=`) {
		t.Errorf("orphan should NOT carry data-prompt-key; got: %s", got)
	}
	if !strings.Contains(got, `<span class="p-text p-redacted">(orphan turns — no recognized originating prompt)</span>`) {
		t.Errorf("missing orphan p-text marker; got: %s", got)
	}
}

// TestRenderSessionsHTML_DataPromptKeyEmitted: a non-orphan prompt
// with Key set carries data-prompt-key="<key>" so jumpToPrompt can
// scroll to it.
func TestRenderSessionsHTML_DataPromptKeyEmitted(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Key:  "hello-world-key",
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `data-prompt-key="hello-world-key"`) {
		t.Errorf("missing data-prompt-key; got: %s", got)
	}
}

// TestRenderSessionsHTML_RedactedPromptBlock: a prompt whose Text
// starts with "[redacted " carries the .p-redacted class so the
// visual treatment differs from a normal prompt.
func TestRenderSessionsHTML_RedactedPromptBlock(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "[redacted 999 chars]",
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	want := `<span class="p-text p-redacted">[redacted 999 chars]</span>`
	if !strings.Contains(got, want) {
		t.Errorf("missing p-redacted text; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_TruncatedPromptShowsNote: Truncated=true
// emits a div.p-truncated explainer between the prompt summary and
// the turn list.
func TestRenderSessionsHTML_TruncatedPromptShowsNote(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID:      "u1",
		Text:      "snipped",
		Truncated: true,
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<div class="p-truncated">(prompt truncated`) {
		t.Errorf("missing p-truncated note; got: %s", got)
	}
}

// TestRenderSessionsHTML_TurnRowBasicShape: each turn renders one
// <li class="turn-row"> with .t-time (HH:MM:SS), .t-model, .t-tokens
// ("N in · M out" via fmtTokens), and .t-cost.
func TestRenderSessionsHTML_TurnRowBasicShape(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	old := timeLocal
	defer func() { timeLocal = old }()
	timeLocal = loc

	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Turns: []aggregate.TurnSummary{{
			Timestamp: time.Date(2026, 5, 1, 12, 34, 56, 0, time.UTC),
			Model:     "claude-opus-4-7",
			CostUSD:   0.07,
			Tokens: aggregate.Tokens{
				InputTokens:  3,
				OutputTokens: 1,
			},
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<li class="turn-row">`) {
		t.Errorf("missing turn-row li; got: %s", got)
	}
	if !strings.Contains(got, `<span class="t-time">12:34:56</span>`) {
		t.Errorf("missing .t-time HH:MM:SS without dur span; got: %s", got)
	}
	if !strings.Contains(got, `<span class="t-model" title="claude-opus-4-7">claude-opus-4-7</span>`) {
		t.Errorf("missing .t-model with title; got: %s", got)
	}
	if !strings.Contains(got, `<span class="t-tokens">3 in · 1 out</span>`) {
		t.Errorf("missing .t-tokens '3 in · 1 out'; got: %s", got)
	}
	if !strings.Contains(got, `<span class="t-cost">$0.07</span>`) {
		t.Errorf("missing .t-cost; got: %s", got)
	}
}

// TestRenderSessionsHTML_TurnRowDuration: a turn with DurationMs>0
// inserts a <span class="t-dur"> after the HH:MM:SS, with a title
// explaining it's the gap to the next turn.
func TestRenderSessionsHTML_TurnRowDuration(t *testing.T) {
	loc, _ := time.LoadLocation("UTC")
	old := timeLocal
	defer func() { timeLocal = old }()
	timeLocal = loc

	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Turns: []aggregate.TurnSummary{{
			Timestamp:  time.Date(2026, 5, 1, 12, 34, 56, 0, time.UTC),
			DurationMs: 11_000, // 11s
			Model:      "x",
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	wantTime := `<span class="t-time">12:34:56 <span class="t-dur" title="Time to next turn within this prompt">11s</span></span>`
	if !strings.Contains(got, wantTime) {
		t.Errorf("missing duration chip; want %q in %s", wantTime, got)
	}
}

// TestRenderSessionsHTML_TurnRowModelDash: an empty Model renders as
// "—" in .t-model text (with an empty title attribute).
func TestRenderSessionsHTML_TurnRowModelDash(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Turns: []aggregate.TurnSummary{{
			Timestamp: fixedTime(12, 0, 0),
			Model:     "",
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	want := `<span class="t-model" title="">—</span>`
	if !strings.Contains(got, want) {
		t.Errorf("empty model should render —; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_TurnRowTokensAndCache: when there are cache
// tokens, the .t-tokens cell appends " · <span class='t-cache' …>"
// with totals (read + create) summed, and a title= breakdown by part.
// 1k+ tokens collapse to "N.Nk" form.
func TestRenderSessionsHTML_TurnRowTokensAndCache(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Turns: []aggregate.TurnSummary{{
			Timestamp: fixedTime(12, 0, 0),
			Model:     "claude-opus-4-7",
			Tokens: aggregate.Tokens{
				InputTokens:         1500,
				OutputTokens:        500,
				CacheReadTokens:     2000,
				CacheCreate5mTokens: 1000,
				CacheCreate1hTokens: 0,
			},
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	// 1500 → "1.5k", 500 → "500"
	if !strings.Contains(got, `1.5k in · 500 out`) {
		t.Errorf("missing tokens with k-formatting; got: %s", got)
	}
	// Cache total: 2000 + 1000 = 3000 → "3.0k cache"
	wantCache := `<span class="t-cache" title="2.0k read, 1.0k create 5m">3.0k cache</span>`
	if !strings.Contains(got, wantCache) {
		t.Errorf("missing .t-cache; want %q in %s", wantCache, got)
	}
}

// TestRenderSessionsHTML_TurnRowToolChipsBasic: each tool emits a
// <span class="t-tool" data-c="…">Name</span> chip; same name always
// gets the same data-c slot across the report (FNV-1a hash mod 5).
func TestRenderSessionsHTML_TurnRowToolChipsBasic(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Turns: []aggregate.TurnSummary{{
			Timestamp: fixedTime(12, 0, 0),
			Model:     "x",
			Tools: []aggregate.ToolInvocation{
				{Name: "Bash"},
				{Name: "Read"},
			},
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	// .t-tools wraps the chips
	if !strings.Contains(got, `<span class="t-tools">`) {
		t.Errorf("missing .t-tools wrapper; got: %s", got)
	}
	// Color slots from FNV-1a — recompute the expected values so the
	// test fails if the hash impl drifts.
	wantBash := `<span class="t-tool" data-c="` + strconv.Itoa(toolColorSlotForTest("Bash")) + `">Bash</span>`
	wantRead := `<span class="t-tool" data-c="` + strconv.Itoa(toolColorSlotForTest("Read")) + `">Read</span>`
	if !strings.Contains(got, wantBash) {
		t.Errorf("missing Bash chip with color slot; want %q in %s", wantBash, got)
	}
	if !strings.Contains(got, wantRead) {
		t.Errorf("missing Read chip with color slot; want %q in %s", wantRead, got)
	}
}

// toolColorSlotForTest is a test-only re-implementation of the FNV-1a
// hash → slot used by production code. Keeping a parallel impl
// guarantees the test fails if either side drifts.
func toolColorSlotForTest(name string) int {
	if name == "" {
		return 0
	}
	var h uint32 = 0x811c9dc5
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 0x01000193
	}
	return int(h%5) + 1
}

// TestRenderSessionsHTML_TurnRowToolChipDetail: a tool with Detail
// renders "Name · <span class='t-tool-detail'>Detail</span>" inside
// the chip, with title="Name · Detail" on the outer span.
func TestRenderSessionsHTML_TurnRowToolChipDetail(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Turns: []aggregate.TurnSummary{{
			Timestamp: fixedTime(12, 0, 0),
			Model:     "x",
			Tools: []aggregate.ToolInvocation{
				{Name: "Bash", Detail: "git status"},
			},
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	slot := strconv.Itoa(toolColorSlotForTest("Bash"))
	want := `<span class="t-tool" data-c="` + slot + `" title="Bash · git status">Bash · <span class="t-tool-detail">git status</span></span>`
	if !strings.Contains(got, want) {
		t.Errorf("missing tool chip with detail; want %q in %s", want, got)
	}
}

// TestRenderSessionsHTML_TurnRowSidechainBadge: a turn with
// Sidechain=true appends a <span class="t-side">sidechain</span>
// badge at the end of .t-tools (after any tool chips).
func TestRenderSessionsHTML_TurnRowSidechainBadge(t *testing.T) {
	s := oneSession()
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: "hi",
		Turns: []aggregate.TurnSummary{{
			Timestamp: fixedTime(12, 0, 0),
			Model:     "x",
			Sidechain: true,
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))
	if !strings.Contains(got, `<span class="t-side">sidechain</span>`) {
		t.Errorf("missing sidechain badge; got: %s", got)
	}
}

// TestRenderSessionsHTML_HTMLEscaping_TextAndAttributes: user data in
// CWD, prompt text, tool detail, and model name must be HTML-escaped
// in both element text and title= attributes. A prompt body containing
// <script> must not appear literally; quotes in CWD must not break
// the title attribute.
func TestRenderSessionsHTML_HTMLEscaping_TextAndAttributes(t *testing.T) {
	s := oneSession()
	s.CWD = `/path/with "quotes" & <brackets>`
	s.Prompts = []aggregate.PromptTimeline{{
		UUID: "u1",
		Text: `<script>alert(1)</script>`,
		Key:  `key"&'<>`,
		Turns: []aggregate.TurnSummary{{
			Timestamp: fixedTime(12, 0, 0),
			Model:     `evil"<model>`,
			Tools: []aggregate.ToolInvocation{
				{Name: `Ba"sh`, Detail: `<rm -rf>`},
			},
		}},
	}}
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{s}))

	// <script> must NEVER appear as live markup. The literal string
	// "<script>alert(1)</script>" should be escaped to entities.
	if strings.Contains(got, `<script>alert(1)`) {
		t.Errorf("prompt text not escaped — unescaped <script> leaked: %s", got)
	}
	// Quotes in CWD/title must be escaped so they don't terminate
	// the attribute prematurely. Look for the encoded form.
	if strings.Contains(got, `title="/path/with "quotes"`) {
		t.Errorf("unescaped quote in CWD title broke attribute: %s", got)
	}
	// Brackets in the title also need escaping — `<brackets>` in an
	// attribute value should become &lt;brackets&gt;.
	if strings.Contains(got, `<brackets>`) {
		t.Errorf("unescaped <brackets> leaked: %s", got)
	}
	// Tool detail must be escaped too.
	if strings.Contains(got, `<rm -rf>`) {
		t.Errorf("unescaped <rm -rf> in tool detail leaked: %s", got)
	}
}

// TestRenderSessionsHTML_EmptyInput: nil/empty slice returns empty
// markup — the template's #session-empty fallback handles the
// no-sessions case server-side.
func TestRenderSessionsHTML_EmptyInput(t *testing.T) {
	if got := string(renderSessionsHTML(nil)); got != "" {
		t.Errorf("nil should render empty; got %q", got)
	}
	if got := string(renderSessionsHTML([]aggregate.SessionTimeline{})); got != "" {
		t.Errorf("empty slice should render empty; got %q", got)
	}
}

// TestRenderSessionsHTML_RotatesColorSlot: the .s-id pill carries
// data-c=(idx%5)+1, so adjacent cards never collide on color. Sessions
// 0..5 should produce slots 1,2,3,4,5,1.
func TestRenderSessionsHTML_RotatesColorSlot(t *testing.T) {
	in := make([]aggregate.SessionTimeline, 6)
	for i := range in {
		s := oneSession()
		s.SessionID = "s" + string(rune('0'+i))
		in[i] = s
	}
	got := string(renderSessionsHTML(in))
	// The s-id span carries data-c. Look for each slot tied to its id.
	wants := []struct {
		sid  string
		slot string
	}{
		{"s0", "1"}, {"s1", "2"}, {"s2", "3"},
		{"s3", "4"}, {"s4", "5"}, {"s5", "1"},
	}
	for _, w := range wants {
		marker := `<span class="s-id" data-c="` + w.slot + `" title="` + w.sid + `">` + w.sid + `</span>`
		if !strings.Contains(got, marker) {
			t.Errorf("session %q should have data-c=%q (rotating slot); not found in: %s", w.sid, w.slot, got)
		}
	}
}

// TestRenderSessionsHTML_SessionIDAndAnchor: the card carries the
// SessionID as id="session-<id>" and data-session="<id>", and the
// summary has an anchor link href="#sessions/session-<id>".
func TestRenderSessionsHTML_SessionIDAndAnchor(t *testing.T) {
	got := string(renderSessionsHTML([]aggregate.SessionTimeline{oneSession()}))
	if !strings.Contains(got, `id="session-abc-123"`) {
		t.Errorf("missing id attribute; got: %s", got)
	}
	if !strings.Contains(got, `data-session="abc-123"`) {
		t.Errorf("missing data-session attribute; got: %s", got)
	}
	if !strings.Contains(got, `href="#sessions/session-abc-123"`) {
		t.Errorf("missing anchor href; got: %s", got)
	}
}
