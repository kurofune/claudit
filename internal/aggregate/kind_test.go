package aggregate

import "testing"

func TestToolKind(t *testing.T) {
	cases := []struct {
		name string // tool name
		want string // normalized kind
	}{
		// agent — the audit-critical category (a sub-agent spawn).
		{"Agent", "agent"},

		// exec
		{"Bash", "exec"},

		// read family
		{"Read", "read"},
		{"Glob", "read"},
		{"Grep", "read"},
		{"LS", "read"},
		{"NotebookRead", "read"},

		// edit family
		{"Edit", "edit"},
		{"Write", "edit"},
		{"MultiEdit", "edit"},
		{"NotebookEdit", "edit"},

		// web family
		{"WebFetch", "web"},
		{"WebSearch", "web"},

		// singletons
		{"Skill", "skill"},
		{"SlashCommand", "command"},

		// todo family
		{"TodoWrite", "todo"},
		{"TodoRead", "todo"},

		// mcp — any mcp__ prefix
		{"mcp__github__create_issue", "mcp"},
		{"mcp__foo__bar", "mcp"},

		// default
		{"SomethingUnknown", "other"},
		{"", "other"},
	}

	for _, c := range cases {
		if got := ToolKind(c.name); got != c.want {
			t.Errorf("ToolKind(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
