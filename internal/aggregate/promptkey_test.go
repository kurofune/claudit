package aggregate

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/kurofune/claudit/v2/internal/parse"
)

// normalizePromptKeyWhole is normalizePromptKey as it read before it
// stopped early: lower the whole text, collapse its ASCII whitespace,
// trim, then cut.
func normalizePromptKeyWhole(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inSpace && b.Len() > 0 {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	runes := []rune(strings.TrimSpace(b.String()))
	if len(runes) > promptKeyMaxRunes {
		runes = runes[:promptKeyMaxRunes]
	}
	return string(runes)
}

// TestNormalizePromptKey_StopsEarlyWithTheSameKey: over texts mixing case,
// every kind of whitespace, non-breaking and other Unicode spaces, invalid
// UTF-8 and runs ending right around the cap, the early-stopping key equals
// the whole-text one.
func TestNormalizePromptKey_StopsEarlyWithTheSameKey(t *testing.T) {
	parts := []string{"a", "B", "É", "ß", "İ", " ", "  ", "\t", "\n", "\r\n", " ", " ", "\v", "\f", "\u0085", "\xff", "x́", "go test ./...", strings.Repeat("y", promptKeyMaxRunes-1)}
	rng := rand.New(rand.NewSource(1))
	cases := []string{"", " ", "  abc", strings.Repeat("a", promptKeyMaxRunes) + "   ", strings.Repeat("a", promptKeyMaxRunes-1) + "   b"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := rng.Intn(40); n >= 0; n-- {
			b.WriteString(parts[rng.Intn(len(parts))])
		}
		cases = append(cases, b.String())
	}
	for _, s := range cases {
		if got, want := normalizePromptKey(s), normalizePromptKeyWhole(s); got != want {
			t.Fatalf("normalizePromptKey(%q) = %q, want %q", s, got, want)
		}
	}
}

// TestBuildPromptIndex_LongChains: a turn 100 links below its prompt
// resolves to it, and one whose 100-link chain loops back on itself
// resolves to no prompt, whichever side of the scan limit the loop closes.
func TestBuildPromptIndex_LongChains(t *testing.T) {
	var links []parse.ParentLink
	link := func(prefix string, n int, top string) {
		for i := 0; i < n; i++ {
			p := fmt.Sprintf("%s%d", prefix, i+1)
			if i == n-1 {
				p = top
			}
			links = append(links, parse.ParentLink{UUID: fmt.Sprintf("%s%d", prefix, i), ParentUUID: p})
		}
	}
	link("ok", 100, "u1")
	link("loop", 100, "loop50")
	link("short", 5, "short2")
	turns := []parse.Turn{{UUID: "t-ok", ParentUUID: "ok0"}, {UUID: "t-loop", ParentUUID: "loop0"}, {UUID: "t-short", ParentUUID: "short0"}}
	msgs := []parse.UserMessage{{UUID: "u1", Text: "Fix  the BUG"}}
	idx := BuildPromptIndex(turns, msgs, links)
	if got := idx.Lookup("t-ok"); got.UserUUID != "u1" || got.Key != "fix the bug" {
		t.Errorf("t-ok = %+v, want u1 keyed fix the bug", got)
	}
	for _, id := range []string{"t-loop", "t-short"} {
		if got := idx.Lookup(id); got.UserUUID != "" || got.Key != noPromptKey {
			t.Errorf("%s = %+v, want no prompt", id, got)
		}
	}
}
