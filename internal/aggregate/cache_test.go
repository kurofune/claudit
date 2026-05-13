package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/pricing"
)

func TestTokens_HitRatio(t *testing.T) {
	cases := []struct {
		name     string
		tok      Tokens
		want     float64 // 0..1
		wantMiss int64
	}{
		{
			name: "all cache hits",
			tok:  Tokens{CacheReadTokens: 1000},
			want: 1.0, wantMiss: 0,
		},
		{
			name: "all misses (input only)",
			tok:  Tokens{InputTokens: 1000},
			want: 0.0, wantMiss: 1000,
		},
		{
			name: "all misses (cache_create only)",
			tok:  Tokens{CacheCreate5mTokens: 600, CacheCreate1hTokens: 400},
			want: 0.0, wantMiss: 1000,
		},
		{
			name: "no cacheable traffic — output only",
			tok:  Tokens{OutputTokens: 9999},
			want: 0.0, wantMiss: 0, // miss is 0 b/c there's no input/create either
		},
		{
			name: "mixed 80% hit",
			tok:  Tokens{InputTokens: 100, CacheCreate5mTokens: 100, CacheReadTokens: 800},
			want: 0.8, wantMiss: 200,
		},
		{
			name: "zero everything",
			tok:  Tokens{},
			want: 0.0, wantMiss: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tok.HitRatio(); got != c.want {
				t.Errorf("HitRatio: got %v, want %v", got, c.want)
			}
			if got := c.tok.MissTokens(); got != c.wantMiss {
				t.Errorf("MissTokens: got %d, want %d", got, c.wantMiss)
			}
		})
	}
}

// fullTurn builds a turn with all five token classes — the basic Add()
// already gets covered by aggregate_test.go; this lets us exercise cache
// fields the existing helper doesn't populate.
func fullTurn(sid, cwd, model string, in, out, cc5, cc1h, cr int, ts time.Time) parse.Turn {
	return parse.Turn{
		SessionID: sid, CWD: cwd, Model: model, Timestamp: ts,
		Usage: parse.Usage{
			InputTokens:         in,
			OutputTokens:        out,
			CacheCreate5mTokens: cc5,
			CacheCreate1hTokens: cc1h,
			CacheReadTokens:     cr,
		},
	}
}

func TestCacheBySession_BucketingAndRanking(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// session A: 2 turns, mostly cache hits — ratio ~95%, miss ~50_000.
	agg.Add(fullTurn("sess-A", "/p/foo", "claude-opus-4-7", 25_000, 1000, 0, 0, 500_000, t0))
	agg.Add(fullTurn("sess-A", "/p/foo", "claude-opus-4-7", 25_000, 1000, 0, 0, 500_000, t0))

	// session B: 1 turn, terrible ratio — 50% hit, miss 100_000.
	agg.Add(fullTurn("sess-B", "/p/foo", "claude-opus-4-7", 100_000, 1000, 0, 0, 100_000, t0))

	// session C: 1 turn, no cacheable traffic at all.
	agg.Add(fullTurn("sess-C", "/p/bar", "claude-opus-4-7", 0, 1000, 0, 0, 0, t0))

	rows := agg.CacheBySession()
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (sess-C excluded for zero cacheable), got %d", len(rows))
	}
	// Sort: B (miss 100k) > A (miss 50k).
	if rows[0].Key != "sess-B" {
		t.Errorf("rank[0] should be sess-B, got %s", rows[0].Key)
	}
	if rows[1].Key != "sess-A" {
		t.Errorf("rank[1] should be sess-A, got %s", rows[1].Key)
	}
	// Subtitle carries the project.
	if rows[0].Subtitle != "/p/foo" {
		t.Errorf("subtitle: %s", rows[0].Subtitle)
	}
	// Hit ratios are reasonable.
	if rows[1].HitRatio < 0.94 || rows[1].HitRatio > 0.96 {
		t.Errorf("sess-A hit ratio: %v", rows[1].HitRatio)
	}
}

func TestCacheByProject_RankingByMiss(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// /p/big-miss: huge input (bad cache).
	agg.Add(fullTurn("s1", "/p/big-miss", "claude-opus-4-7", 1_000_000, 1000, 0, 0, 100_000, t0))
	// /p/well-cached: small input, big cache_read (good cache).
	agg.Add(fullTurn("s2", "/p/well-cached", "claude-opus-4-7", 50_000, 1000, 0, 0, 5_000_000, t0))
	// /p/no-cache: no cacheable traffic — should be filtered out.
	agg.Add(fullTurn("s3", "/p/no-cache", "claude-opus-4-7", 0, 1000, 0, 0, 0, t0))

	rows := agg.CacheByProject()
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Key != "/p/big-miss" {
		t.Errorf("rank[0] should be /p/big-miss, got %s", rows[0].Key)
	}
	if rows[0].Miss != 1_000_000 {
		t.Errorf("big-miss miss tokens: %d", rows[0].Miss)
	}
	// Hit ratio for big-miss: 100k / 1.1M ≈ 0.091
	if rows[0].HitRatio > 0.10 {
		t.Errorf("big-miss should have low hit ratio, got %v", rows[0].HitRatio)
	}
}

func TestCacheBySubagent_AndInvocation(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	// Two invocations of "general-purpose" w/ different cache profiles.
	// Each turn has SourceFile so byInvocation populates.
	mk := func(sid, file string, in, cr int) parse.Turn {
		return parse.Turn{
			SessionID: sid, CWD: "/p/foo", Model: "claude-opus-4-7",
			Timestamp: t0, Sidechain: true, SourceFile: file,
			Usage: parse.Usage{InputTokens: in, CacheReadTokens: cr},
		}
	}
	lookup := func(t parse.Turn) (string, string) {
		// Map source file to (type, description).
		if t.SourceFile == "agent-1.jsonl" {
			return "general-purpose", "research the bug"
		}
		if t.SourceFile == "agent-2.jsonl" {
			return "general-purpose", "lookup config"
		}
		if t.SourceFile == "agent-3.jsonl" {
			return "review-lens", "code review pass"
		}
		return "", ""
	}

	// agent-1: 1 turn, terrible cache (all input).
	agg.AddWithSubagent(mk("s1", "agent-1.jsonl", 500_000, 0), lookup)
	// agent-2: 1 turn, OK cache (mostly read).
	agg.AddWithSubagent(mk("s1", "agent-2.jsonl", 50_000, 200_000), lookup)
	// agent-3: 1 turn, different type.
	agg.AddWithSubagent(mk("s1", "agent-3.jsonl", 100_000, 100_000), lookup)

	subRows := agg.CacheBySubagent()
	if len(subRows) != 2 {
		t.Fatalf("want 2 subagent rows, got %d", len(subRows))
	}
	// general-purpose has higher total miss tokens (550k > 100k).
	if subRows[0].Key != "general-purpose" {
		t.Errorf("rank[0] should be general-purpose, got %s", subRows[0].Key)
	}

	invRows := agg.CacheByInvocation()
	if len(invRows) != 3 {
		t.Fatalf("want 3 invocation rows, got %d", len(invRows))
	}
	// agent-1 has the worst miss tokens (500k); should be first.
	if invRows[0].Key != "research the bug" {
		t.Errorf("rank[0] should be 'research the bug', got %q", invRows[0].Key)
	}
	if invRows[0].Subtitle != "general-purpose" {
		t.Errorf("subtitle: %q", invRows[0].Subtitle)
	}
}

func TestOverallHitRatio(t *testing.T) {
	prices, _ := pricing.LoadDefault()
	agg := New(prices)
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	agg.Add(fullTurn("s1", "/p/x", "claude-opus-4-7", 100, 0, 0, 0, 900, t0))
	if got := agg.OverallHitRatio(); got != 0.9 {
		t.Errorf("overall ratio: %v, want 0.9", got)
	}
}
