package aggregate

import (
	"testing"
	"time"

	"github.com/kurofune/claudit/v2/internal/parse"
)

// mapChains is a PromptChains over two maps.
type mapChains struct {
	parent map[string]string
	prompt map[string]parse.UserMessage
}

func (c mapChains) Parent(uuid string) (string, bool) { p, ok := c.parent[uuid]; return p, ok }
func (c mapChains) Prompt(uuid string) (parse.UserMessage, bool) {
	m, ok := c.prompt[uuid]
	return m, ok
}

// TestPromptResolver_WithFallback: a chain that leaves the resolver's own
// records climbs on through the fallback to a prompt there, whose text
// and time the resolver then answers; its own edges win where both name a
// uuid.
func TestPromptResolver_WithFallback(t *testing.T) {
	ts := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	more := mapChains{
		parent: map[string]string{"sys": "u-far", "a1": "u-wrong"},
		prompt: map[string]parse.UserMessage{"u-far": {UUID: "u-far", Text: "far prompt", Timestamp: ts}},
	}
	turns := []parse.Turn{{UUID: "a1", ParentUUID: "sys"}, {UUID: "a2", ParentUUID: "a1"}}
	r := NewPromptResolver(turns, nil, nil)
	if got := r.Resolve("a2"); got != "" {
		t.Fatalf("without a fallback Resolve = %q, want the chain to dead-end", got)
	}
	r = NewPromptResolver(turns, nil, nil).WithFallback(more)
	if got := r.Resolve("a2"); got != "u-far" {
		t.Fatalf("Resolve = %q, want u-far through the fallback", got)
	}
	if r.Text("u-far") != "far prompt" || !r.Timestamp("u-far").Equal(ts) {
		t.Errorf("Text/Timestamp = %q %v", r.Text("u-far"), r.Timestamp("u-far"))
	}
}
