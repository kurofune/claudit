package parse

import "testing"

func TestDropInstructionCopy(t *testing.T) {
	doc := "# AGENTS.md instructions for /home/u/proj\n\n<INSTRUCTIONS>\n# Work rules\n\nClaim before code.\nRead the whole task.\n\n  Commit after each milestone.  \n\nLOOP CONTEXT: Iteration 1.\n</INSTRUCTIONS>"
	cases := []struct {
		name, prompt, want string
		docs               []string
	}{
		{
			name:   "copy then task",
			prompt: "# Work rules\n\nClaim before code.\nRead the whole task.\n\nCommit after each milestone.\n\n\nYou are running the review step.\n\nMore.",
			docs:   []string{doc},
			want:   "You are running the review step.\n\nMore.",
		},
		{
			name:   "first paragraph not in a document",
			prompt: "fix the login bug\n\n# Work rules",
			docs:   []string{doc},
			want:   "fix the login bug\n\n# Work rules",
		},
		{
			name:   "no documents",
			prompt: "# Work rules\n\ntask",
			want:   "# Work rules\n\ntask",
		},
		{
			name:   "only a copy keeps the prompt",
			prompt: "# Work rules\n\nClaim before code.\nRead the whole task.\n",
			docs:   []string{doc},
			want:   "# Work rules\n\nClaim before code.\nRead the whole task.\n",
		},
		{
			name:   "part of a line is not a paragraph",
			prompt: "Claim before\n\ntask",
			docs:   []string{doc},
			want:   "Claim before\n\ntask",
		},
		{
			name:   "paragraph spanning only part of a document paragraph still matches whole lines",
			prompt: "Read the whole task.\n\ntask",
			docs:   []string{doc},
			want:   "task",
		},
		{
			name:   "any document",
			prompt: "LOOP CONTEXT: Iteration 1.\n\ntask",
			docs:   []string{"other", doc},
			want:   "task",
		},
		{
			name:   "stops at the first unmatched paragraph",
			prompt: "# Work rules\n\ntask\n\nClaim before code.\nRead the whole task.",
			docs:   []string{doc},
			want:   "task\n\nClaim before code.\nRead the whole task.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DropInstructionCopy(c.prompt, c.docs); got != c.want {
				t.Errorf("DropInstructionCopy = %q, want %q", got, c.want)
			}
		})
	}
}
