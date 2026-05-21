package render

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
	"github.com/kurofune/claudit/internal/pricing"
)

// htmlSetup builds the smallest valid aggregator the HTML renderer
// will accept — one Opus turn so totals are non-zero and every
// section has at least one row to iterate over.
func htmlSetup(t *testing.T) *aggregate.Aggregator {
	t.Helper()
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	a := aggregate.New(prices)
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	a.Add(mkTurn("claude-opus-4-7", "/p/x", 1_000_000, 200_000, t0))
	return a
}

// TestHTML_OneShotOmitsServeOnlyChrome guards against accidentally
// shipping the auto-reload script in a one-shot `claudit report --html`
// output. The script polls /_claudit/status — useless in a static
// file, and a hint that the renderer leaked serve-only state into
// the default path.
func TestHTML_OneShotOmitsServeOnlyChrome(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if strings.Contains(body, "claudit-reload-toast") {
		t.Errorf("one-shot HTML render leaked the serve-only reload toast")
	}
	if strings.Contains(body, "/_claudit/status") {
		t.Errorf("one-shot HTML render leaked the serve-only status path")
	}
}

func TestHTML_ServeModeInjectsReload(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{
		ServeMode:  true,
		Generation: 42,
		StatusPath: "/custom/status",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	if !strings.Contains(body, "claudit-reload-toast") {
		t.Errorf("serve render missing reload toast")
	}
	// html/template escapes "/" as "\/" inside a JS string literal —
	// JavaScript-equivalent, but the bytes differ from "/custom/status".
	// Match the escaped form so the assertion survives the auto-escape.
	if !strings.Contains(body, `"\/custom\/status"`) {
		t.Errorf("custom status path not interpolated (looked for JS-escaped form)")
	}
	// Generation: html/template pads numeric output with whitespace
	// inside a script context — accept any horizontal whitespace around
	// the literal so the test doesn't break if Go's formatting shifts.
	if !regexp.MustCompile(`INITIAL_GEN\s*=\s*42\b`).MatchString(body) {
		t.Errorf("generation value missing or wrong; want INITIAL_GEN = 42")
	}
}

func TestHTML_ServeModeAddsDateRangeButton(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{ServeMode: true}); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// Button wraps the date range; inner span keeps the existing
	// #date-range id so the JS that fills the text doesn't need to
	// change.
	if !regexp.MustCompile(`<button[^>]+id="date-range-button"`).MatchString(body) {
		t.Errorf("ServeMode render missing date-range-button")
	}
	if !regexp.MustCompile(`<span[^>]+id="date-range"`).MatchString(body) {
		t.Errorf("ServeMode render: #date-range should be a <span> inside the button")
	}
	if !strings.Contains(body, `id="icon-calendar"`) {
		t.Errorf("ServeMode render missing calendar icon symbol")
	}
	if !strings.Contains(body, `href="#icon-calendar"`) {
		t.Errorf("ServeMode render missing calendar icon <use>")
	}
}

func TestHTML_OneShotKeepsPlainDateRangeDiv(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	if err := HTML(context.Background(), &buf, a); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	// One-shot mode has no server to navigate to — the picker would
	// be a dead control. Keep the static <div>.
	if !regexp.MustCompile(`<div[^>]+id="date-range"`).MatchString(body) {
		t.Errorf("one-shot render: #date-range should remain a <div>")
	}
	if strings.Contains(body, `id="date-range-button"`) {
		t.Errorf("one-shot render leaked the serve-only date-range button")
	}
}

func TestHTML_ServeModeDefaultStatusPath(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	err := HTMLWithOptions(context.Background(), &buf, a, HTMLOptions{ServeMode: true})
	if err != nil {
		t.Fatal(err)
	}
	// Same JS-escape note as above.
	if !strings.Contains(buf.String(), `"\/_claudit\/status"`) {
		t.Errorf("default status path missing (looked for JS-escaped form)")
	}
}

// TestHTMLWithOptions_CanceledContextReturnsError ensures a disconnected
// HTTP client can short-circuit the render path before the JSON marshal
// + template execute do real work. We assert ctx.Err() is returned and
// nothing meaningful was written to the buffer.
func TestHTMLWithOptions_CanceledContextReturnsError(t *testing.T) {
	a := htmlSetup(t)
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := HTMLWithOptions(ctx, &buf, a, HTMLOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("want empty buffer on cancellation, got %d bytes", buf.Len())
	}
}
