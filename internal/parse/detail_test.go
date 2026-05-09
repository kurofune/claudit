package parse

import "testing"

func TestBashPattern(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"git status", "git status"},
		{"git status -s", "git status"},
		{"git log --oneline -20", "git log"},
		{"  git   commit   -m foo  ", "git commit"},
		{"cd ~/Projects/claudit && git status", "git status"},
		{"cd foo; npm install --save react", "npm install"},
		{"FOO=bar BAZ=1 npm test", "npm test"},
		{"sudo make install", "make install"},
		{"ls -la ~/Projects", "ls"},
		{"find . -name '*.go'", "find"},
		{"go test ./...", "go test"},
		{"go test", "go test"},
		{"go", "go"}, // bare program
		{"./bin/foo --bar", "foo"},
		{"cargo build --release", "cargo build"},
		{"docker compose up", "docker compose"},
		{"git", "git"}, // bare git → just "git"
		{"git -c foo=bar status", "git"},
		{"echo hello | tee /dev/null", "echo"}, // non-multi-command tools stop at program name
		{"", ""},
		{"   ", ""},
		{"git push && git push --tags", "git push"}, // last segment wins
		{"time go build", "go build"},               // time wrapper stripped
	}
	for _, c := range cases {
		if got := bashPattern(c.in); got != c.want {
			t.Errorf("bashPattern(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestFileExt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/x/y/z.go", ".go"},
		{"/x/y/Z.GO", ".go"},
		{"/x/Makefile", "(no ext)"},
		{"/x/.dotfile", ".dotfile"},
		{"", "(empty)"},
		{"foo.test.go", ".go"},
	}
	for _, c := range cases {
		if got := fileExt(c.in); got != c.want {
			t.Errorf("fileExt(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestUrlHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/foo/bar", "github.com"},
		{"https://Docs.Anthropic.COM/en", "docs.anthropic.com"},
		{"not-a-url", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := urlHost(c.in); got != c.want {
			t.Errorf("urlHost(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestTopLevelDir(t *testing.T) {
	// Pin HOME so the os.UserHomeDir() branch is deterministic regardless
	// of where the tests run.
	t.Setenv("HOME", "/Users/x")
	cases := []struct{ in, want string }{
		// macOS — under HOME.
		{"/Users/x/Projects/claudit/cmd/main.go", "Projects/claudit"},
		{"/Users/x/Projects/foo", "Projects/foo"},
		// Linux JSONL parsed on a non-Linux runner — regex fallback.
		{"/home/y/Projects/bar", "Projects/bar"},
		{"/home/y/code/api/handler.go", "code/api"},
		// Windows JSONL parsed on a non-Windows runner — regex fallback.
		{`C:\Users\z\Projects\baz`, "Projects/baz"},
		{`C:\Users\z\Projects\baz\src\main.go`, "Projects/baz"},
		// Outside any home — fall through to leading segment.
		{"/etc/hosts", "/etc"},
		{`C:\Windows\System32`, "/Windows"},
		{"/", "/"},
	}
	for _, c := range cases {
		if got := topLevelDir(c.in); got != c.want {
			t.Errorf("topLevelDir(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestExtractDetail_Routing(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"Bash", `{"command":"git status"}`, "git status"},
		{"Read", `{"file_path":"/x/y.go"}`, ".go"},
		{"Edit", `{"file_path":"/x/y.ts"}`, ".ts"},
		{"Write", `{"file_path":"/x/Makefile"}`, "(no ext)"},
		{"Glob", `{"pattern":"**/*.go"}`, "**/*.go"},
		{"WebFetch", `{"url":"https://github.com/x"}`, "github.com"},
		{"WebSearch", `{"query":"how to format go"}`, "how to format go"},
		{"NotebookEdit", `{"notebook_path":"/x/foo.ipynb"}`, ".ipynb"},
		{"Grep", `{"glob":"*.go","path":"/x"}`, "*.go"},
		{"Grep", `{"path":"/Users/x/Projects/foo/bar"}`, "Projects/foo"},
		{"NoTool", `{}`, ""},
		{"Bash", ``, ""}, // empty raw
	}
	for _, c := range cases {
		got := extractDetail(c.name, []byte(c.raw))
		if got != c.want {
			t.Errorf("extractDetail(%q, %q) = %q want %q", c.name, c.raw, got, c.want)
		}
	}
}
