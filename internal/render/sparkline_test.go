package render

import (
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/aggregate"
)

func mkPts(costs ...float64) []aggregate.TrendPoint {
	out := make([]aggregate.TrendPoint, len(costs))
	t0 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for i, c := range costs {
		out[i] = aggregate.TrendPoint{Time: t0.AddDate(0, 0, i), CostUSD: c}
	}
	return out
}

func TestSparkline_Empty(t *testing.T) {
	if got := sparkline(nil, 10); got != "—" {
		t.Errorf("empty: got %q", got)
	}
}

func TestSparkline_LengthMatchesInput(t *testing.T) {
	pts := mkPts(1, 2, 3, 4, 5)
	got := sparkline(pts, 0)
	// Block runes are 3 bytes each in UTF-8; count by rune.
	if n := len([]rune(got)); n != 5 {
		t.Errorf("rune count = %d, want 5; got=%q", n, got)
	}
}

func TestSparkline_DownsampleCap(t *testing.T) {
	// 10 cells, cap at 4: must produce exactly 4 runes.
	pts := mkPts(1, 1, 1, 1, 1, 1, 1, 1, 1, 1)
	got := sparkline(pts, 4)
	if n := len([]rune(got)); n != 4 {
		t.Errorf("downsample rune count = %d, want 4; got=%q", n, got)
	}
}

func TestSparkline_ZeroAndNonZeroDistinct(t *testing.T) {
	// A zero cell must render as space; a tiny nonzero cell must render
	// as the smallest block. This separation is the whole point of the
	// 9-level table.
	got := sparkline(mkPts(0, 1, 0, 1), 0)
	runes := []rune(got)
	if len(runes) != 4 {
		t.Fatalf("want 4 runes, got %d", len(runes))
	}
	if runes[0] != ' ' || runes[2] != ' ' {
		t.Errorf("zero cells not blank: %q", got)
	}
	if runes[1] == ' ' || runes[3] == ' ' {
		t.Errorf("nonzero cells rendered blank: %q", got)
	}
}

func TestSparkline_AllZeros(t *testing.T) {
	got := sparkline(mkPts(0, 0, 0), 0)
	if strings.TrimSpace(got) != "" {
		t.Errorf("all-zero series should be all blanks; got %q", got)
	}
}

func TestSparkline_Monotonic(t *testing.T) {
	// Strictly increasing input → block heights non-decreasing.
	pts := mkPts(1, 2, 3, 4, 5, 6, 7, 8)
	got := sparkline(pts, 0)
	runes := []rune(got)
	for i := 1; i < len(runes); i++ {
		if runes[i] < runes[i-1] {
			t.Errorf("non-monotonic at %d: %q", i, got)
			break
		}
	}
}
