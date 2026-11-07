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
			name:   "part of a document paragraph is not a paragraph of it",
			prompt: "Read the whole task.\n\ntask",
			docs:   []string{doc},
			want:   "Read the whole task.\n\ntask",
		},
		{
			name:   "wrapper lines end a document paragraph",
			prompt: "# Work rules\n\nLOOP CONTEXT: Iteration 1.\n\ntask",
			docs:   []string{"<system-reminder>\n# Work rules\n<!-- end -->\nLOOP CONTEXT: Iteration 1.\n</system-reminder>"},
			want:   "task",
		},
		{
			name:   "wrapper lines end a prompt paragraph",
			prompt: "<INSTRUCTIONS>\n# Work rules\n</INSTRUCTIONS>\ntask",
			docs:   []string{doc},
			want:   "task",
		},
		{
			name:   "other markup belongs to the prompt paragraph",
			prompt: "<request>\n# Work rules\n</request>\n\nFix login",
			docs:   []string{doc},
			want:   "<request>\n# Work rules\n</request>\n\nFix login",
		},
		{
			name:   "markup after a dropped copy stays",
			prompt: "# Work rules\n\n<request>\nFix login\n</request>",
			docs:   []string{doc},
			want:   "<request>\nFix login\n</request>",
		},
		{
			name:   "markup after a wrapped copy stays",
			prompt: "<INSTRUCTIONS>\n# Work rules\n</INSTRUCTIONS>\n<request>\nFix login\n</request>",
			docs:   []string{doc},
			want:   "<request>\nFix login\n</request>",
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
