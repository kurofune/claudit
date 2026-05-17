package term

import (
	"strings"
	"testing"
)

func TestRenderPanel_BasicWidth(t *testing.T) {
	st := Style{enabled: false}
	out := RenderPanel(Panel{Title: "totals", Body: []string{"hello"}}, 30, st)
	if len(out) != 3 {
		t.Fatalf("expected 3 rows (top, body, bottom), got %d", len(out))
	}
	for i, line := range out {
		if w := visibleWidth(line); w != 30 {
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
	if len(out) != 3 {
		t.Fatalf("got %d rows", len(out))
	}
	if !strings.Contains(out[1], "no alerts yet") {
		t.Errorf("empty hint missing: %q", out[1])
	}
}

func TestRenderPanel_TruncatesOverflow(t *testing.T) {
	st := Style{enabled: false}
	out := RenderPanel(Panel{
		Title: "x",
		Body:  []string{"this line is much longer than the terminal width should allow"},
	}, 20, st)
	for i, line := range out {
		if w := visibleWidth(line); w != 20 {
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
		if got := visibleWidth(c.in); got != c.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", c.in, got, c.want)
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
		if w := visibleWidth(line); w != 60 {
			t.Errorf("row %d visibleWidth = %d, want 60: %q", i, w, line)
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
