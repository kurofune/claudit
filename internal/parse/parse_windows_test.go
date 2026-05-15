//go:build windows

package parse

import "testing"

// Forward-slash cases live in the shared TestIsSubagentFile. This file
// pins the backslash-separator shape that only appears on Windows —
// filepath.Split there returns dir with a trailing "\", so a regression
// to strings.HasSuffix(strings.TrimSuffix(dir, "/"), "subagents") would
// silently mis-classify subagent JSONLs and break attribution.
func TestIsSubagentFile_WindowsSeparators(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`C:\x\y\sess-id\subagents\agent-abc.jsonl`, true},
		{`C:\x\y\sess-id.jsonl`, false},
		{`C:\x\y\foo\bar.jsonl`, false},
	}
	for _, c := range cases {
		if got := IsSubagentFile(c.path); got != c.want {
			t.Errorf("IsSubagentFile(%q) = %v want %v", c.path, got, c.want)
		}
	}
}
