package aggregate

import "testing"

// TestTokens_Total verifies Total() sums all five token categories —
// the headline "tokens used" figure. Output IS included (unlike
// CacheableTokens/MissTokens), and the zero value sums to 0.
func TestTokens_Total(t *testing.T) {
	cases := []struct {
		name string
		tok  Tokens
		want int64
	}{
		{
			name: "representative tuple sums all five",
			tok: Tokens{
				InputTokens:         100,
				OutputTokens:        200,
				CacheCreate5mTokens: 30,
				CacheCreate1hTokens: 40,
				CacheReadTokens:     500,
			},
			want: 870,
		},
		{
			name: "zero value returns 0",
			tok:  Tokens{},
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tok.Total(); got != c.want {
				t.Errorf("Total() = %d, want %d", got, c.want)
			}
		})
	}
}
