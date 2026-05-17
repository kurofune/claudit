package term

import (
	"bytes"
	"strings"
	"testing"
)

// A bytes.Buffer is not an *os.File, so the renderer treats it as non-TTY.
// That's exactly what we want in tests: deterministic, scrollback-style output.

func TestRender_NonTTY_JoinsHeaderAndBody(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	if r.IsTTY() {
		t.Fatal("buffer should not be TTY")
	}
	r.Render(Frame{
		Header: []string{"$1.42 today"},
		Body:   []string{"42 turns · $0.31 · last: Edit"},
	})
	want := "$1.42 today · 42 turns · $0.31 · last: Edit\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_NonTTY_PrintsCalloutsBeforeStatus(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Render(Frame{
		Callouts: []string{"spike: $1.20 (6.2x median)"},
		Body:     []string{"42 turns"},
	})
	got := buf.String()
	if !strings.HasPrefix(got, "spike:") {
		t.Errorf("callout should come first; got %q", got)
	}
	if !strings.Contains(got, "42 turns") {
		t.Errorf("status should follow; got %q", got)
	}
}

func TestRender_NonTTY_EmptyFrameNoOutput(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Render(Frame{})
	if got := buf.String(); got != "" {
		t.Errorf("empty frame should write nothing, got %q", got)
	}
}

func TestPrintln_NonTTY(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)
	r.Println("hello")
	if got := buf.String(); got != "hello\n" {
		t.Errorf("got %q", got)
	}
}

// fakeTTY pretends to be a TTY by claiming so via a wrapper. We can't
// easily fool the *os.File check in New, so we construct the renderer
// manually with tty=true to exercise the ANSI path.
func newForcedTTY(w *bytes.Buffer) *Renderer {
	r := New(w)
	r.tty = true
	return r
}

func TestRender_TTY_ClearsPreviousRegion(t *testing.T) {
	var buf bytes.Buffer
	r := newForcedTTY(&buf)
	r.Render(Frame{Body: []string{"line1", "line2", "line3"}})
	buf.Reset()
	r.Render(Frame{Body: []string{"new"}})

	got := buf.String()
	// Three lines were previously drawn. Clearing the region should issue
	// two "up-arrow + clear-line" pairs, plus one final clear for the
	// landing row. Then the new content prints.
	upClears := strings.Count(got, "\033[1A")
	if upClears != 2 {
		t.Errorf("expected 2 cursor-up moves to clear 3-line region, got %d in %q", upClears, got)
	}
	if !strings.Contains(got, "new") {
		t.Errorf("new content missing: %q", got)
	}
}

func TestRender_TTY_LastLineHasNoTrailingNewline(t *testing.T) {
	var buf bytes.Buffer
	r := newForcedTTY(&buf)
	r.Render(Frame{Header: []string{"H"}, Body: []string{"B"}})
	got := buf.String()
	if strings.HasSuffix(got, "\n") {
		t.Errorf("last line should not terminate with newline; got %q", got)
	}
}

func TestClear_TTY_ResetsDrawnCount(t *testing.T) {
	var buf bytes.Buffer
	r := newForcedTTY(&buf)
	r.Render(Frame{Body: []string{"a", "b"}})
	r.Clear()
	buf.Reset()
	// After Clear, a subsequent Render should not try to move the cursor
	// up — there is no prior region to wipe.
	r.Render(Frame{Body: []string{"x"}})
	if strings.Contains(buf.String(), "\033[1A") {
		t.Errorf("Clear() should have reset drawn count; got %q", buf.String())
	}
}
