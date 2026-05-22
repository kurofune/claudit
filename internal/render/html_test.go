package render

import (
	"bytes"
	"context"
	"errors"
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

// trim400 returns up to 400 chars of s, with an ellipsis if truncated.
// Shared by html_static_test.go for diagnostic excerpts.
func trim400(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
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
