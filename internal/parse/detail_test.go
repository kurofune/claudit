package parse

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

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

// TestBashPatternNeverReturnsShellKeyword: compound commands end in a
// keyword (`done`, `fi`, `esac`) or open with one (`for`, `if`, `do`), and
// none of those names a program.
func TestBashPatternNeverReturnsShellKeyword(t *testing.T) {
	if got := bashPattern(`for f in *.go; do wc -l "$f"; done`); got != "wc" {
		t.Errorf("for-loop pattern = %q, want wc", got)
	}
	shapes := []string{
		"%s",
		"%s x",
		"%s; ls",
		"ls; %s",
		"ls && %s",
		"%s && %s",
		"%s; %s; %s",
		"x=1 %s",
		"sudo %s",
		"time %s",
		"cd /tmp && %s",
		"%s\n%s",
	}
	cases := map[string]string{
		`for f in *.go; do wc -l "$f"; done`:              "wc",
		`while true; do sleep 1; done`:                    "sleep",
		`until git pull; do sleep 5; done`:                "sleep",
		`if [ -f go.mod ]; then go build; fi`:             "go build",
		`if grep -q x f; then echo y; else echo n; fi`:    "echo",
		`case $1 in a) echo a;; esac`:                     "echo",
		`for f in a b; do`:                                "",
		`{ ls; }`:                                         "ls",
		`if true; then :; elif false; then make test; fi`: "make test",
		"for f in *.go\ndo\n  gofmt -l \"$f\"\ndone":      "gofmt",
	}
	for in, want := range cases {
		if got := bashPattern(in); got != want {
			t.Errorf("bashPattern(%q) = %q want %q", in, got, want)
		}
	}
	keywords := []string{"done", "do", "then", "fi", "for", "while", "until", "esac", "case", "if", "else", "elif", "in", "{", "}"}
	for _, kw := range keywords {
		t.Run(kw, func(t *testing.T) {
			for _, shape := range shapes {
				n := strings.Count(shape, "%s")
				args := make([]any, n)
				for i := range args {
					args[i] = kw
				}
				in := fmt.Sprintf(shape, args...)
				got := bashPattern(in)
				for _, k := range keywords {
					if got == k || strings.HasPrefix(got, k+" ") {
						t.Errorf("bashPattern(%q) = %q, a shell keyword", in, got)
					}
				}
			}
		})
	}
}

// TestBashPatternDropsHeredocBodies: a heredoc's body and terminator are
// data, not commands — `cat > f <<'EOF' … EOF` is a `cat`, never an `EOF`.
func TestBashPatternDropsHeredocBodies(t *testing.T) {
	cases := map[string]string{
		"cat > f.txt <<'EOF'\nhello; world\nEOF":                    "cat",
		"python3 - <<EOF\nimport sys\nprint(1)\nEOF":                "python3",
		"git commit -F - <<'MSG'\nfeat: x\nMSG\ngit push":           "git push",
		"cat <<-\"END\"\n\tdo stuff\n\tEND":                         "cat",
		"cat <<A <<B\na\nA\nb\nB":                                   "cat",
		"grep -c x <<< \"$v\"":                                      "grep",
		"cat > a <<'EOF'\nx\nEOF\ncat > b <<'EOF'\ny\nEOF\ngo test": "go test",
		"cat <<'EOF'\nunterminated body\nls":                        "cat",
	}
	for in, want := range cases {
		if got := bashPattern(in); got != want {
			t.Errorf("bashPattern(%q) = %q want %q", in, got, want)
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

// Path tools carry the file path as their input, so a per-call key (the
// Agents view's retry grouping, the per-turn dedup) names the real target
// rather than the extension bucket extractDetail returns.
func TestExtractToolInput_PathTools(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"Read", `{"file_path":"/a/b.png"}`, "/a/b.png"},
		{"Read", `{"file_path":"/a/b.png","offset":10,"limit":20}`, "/a/b.png"},
		{"Edit", `{"file_path":"/a/c.go","old_string":"x","new_string":"y"}`, "/a/c.go"},
		{"Write", `{"file_path":"/a/d.html","content":"<p>"}`, "/a/d.html"},
		{"NotebookEdit", `{"notebook_path":"/a/e.ipynb","new_source":"1"}`, "/a/e.ipynb"},
	}
	for _, c := range cases {
		if got := extractToolInput(c.name, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("extractToolInput(%s, %s) = %q, want %q", c.name, c.raw, got, c.want)
		}
		if got := extractToolInputFull(c.name, json.RawMessage(c.raw)); got != c.want {
			t.Errorf("extractToolInputFull(%s, %s) = %q, want %q", c.name, c.raw, got, c.want)
		}
	}
}
