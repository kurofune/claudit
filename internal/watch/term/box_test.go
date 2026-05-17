package term

import (
	"strings"
	"testing"
)

func TestRenderPanel_BasicWidth(t *testing.T) {
	st := Style{enabled: false}
	out := RenderPanel(Panel{Title: "totals", Body: []string{"hello"}}, 30, st)
	// No Pad: top border, body, bottom border = 3
	if len(out) != 3 {
		t.Fatalf("expected 3 rows (top, body, bottom), got %d", len(out))
	}
	for i, line := range out {
		if w := VisibleWidth(line); w != 30 {
			t.Errorf("row %d width = %d, want 30 (%q)", i, w, line)
		}
	}
	if !strings.Contains(out[0], "totals") {
		t.Errorf("title missing from top: %q", out[0])
	}
}

func TestRenderPanel_TitleHint(t *testing.T) {
	st := Style{enabled: false}
	out := RenderPanel(Panel{Title: "live", TitleHint: "$18 across 2"}, 40, st)
	if !strings.Contains(out[0], "$18 across 2") {
		t.Errorf("hint missing: %q", out[0])
	}
}

func TestRenderPanel_EmptyBody(t *testing.T) {
	st := Style{enabled: false}
	out := RenderPanel(Panel{Title: "alerts", Empty: "no alerts yet"}, 30, st)
	// No Pad: top border, empty hint, bottom border = 3
	if len(out) != 3 {
		t.Fatalf("got %d rows", len(out))
	}
	if !strings.Contains(out[1], "no alerts yet") {
		t.Errorf("empty hint missing: %q", out[1])
	}
}

func TestRenderPanel_PadAddsInteriorRows(t *testing.T) {
	st := Style{enabled: false}
	out := RenderPanel(Panel{Title: "live", Body: []string{"hello"}, Pad: true}, 30, st)
	// top, top-pad, body, bottom-pad, bottom = 5
	if len(out) != 5 {
		t.Fatalf("expected 5 rows with Pad, got %d", len(out))
	}
	// Body is sandwiched: rows[2] is the body line.
	if !strings.Contains(out[2], "hello") {
		t.Errorf("body should be on row 2 (between pad rows): %q", out)
	}
	// Pad rows are blank (no body content).
	for _, i := range []int{1, 3} {
		if strings.Contains(out[i], "hello") {
			t.Errorf("row %d should be blank padding, got %q", i, out[i])
		}
	}
}

func TestRenderPanel_TruncatesOverflow(t *testing.T) {
	st := Style{enabled: false}
	out := RenderPanel(Panel{
		Title: "x",
		Body:  []string{"this line is much longer than the terminal width should allow"},
	}, 20, st)
	for i, line := range out {
		if w := VisibleWidth(line); w != 20 {
			t.Errorf("row %d width = %d, want 20: %q", i, w, line)
		}
	}
}

func TestVisibleWidth_StripsAnsi(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"plain", 5},
		{"\033[1mbold\033[0m", 4},
		{"a\033[31mb\033[0mc", 3},
		{"", 0},
	}
	for _, c := range cases {
		if got := VisibleWidth(c.in); got != c.want {
			t.Errorf("VisibleWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestRenderPanel_ColoredTitleStaysAtWidth(t *testing.T) {
	// Regression: title carrying ANSI codes used to make topBorder
	// overcount by treating each ESC/`[`/digit/letter as a heading
	// rune, producing way too many dashes — the top border ended up
	// wider than the terminal and the terminal wrapped it.
	st := Style{enabled: true}
	colored := st.Magenta("totals")
	out := RenderPanel(Panel{
		Title:     colored,
		TitleHint: st.Dim("$40.7146 across 3 sessions"),
		Body:      []string{"hello"},
	}, 60, st)
	for i, line := range out {
		if w := VisibleWidth(line); w != 60 {
			t.Errorf("row %d VisibleWidth = %d, want 60: %q", i, w, line)
		}
	}
}

func TestVisibleTruncate_PreservesColorReset(t *testing.T) {
	// Colored payload truncated mid-color should still terminate with
	// a reset so subsequent content isn't unintentionally colored.
	out := visibleTruncate("\033[31mhello world\033[0m", 5)
	if !strings.HasSuffix(out, "\033[0m") {
		t.Errorf("expected reset suffix, got %q", out)
	}
}
