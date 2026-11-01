package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// realDir resolves symlinks (macOS t.TempDir lives under /var → /private/var)
// so expected Roots compare equal to what the walk reports.
func realDir(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// newRepo makes <base>/<name>/.git as a directory and returns the repo root.
func newRepo(t *testing.T, base, name string) string {
	t.Helper()
	root := mkdir(t, filepath.Join(base, name))
	mkdir(t, filepath.Join(root, ".git"))
	return root
}

func assertProject(t *testing.T, got, want Project) {
	t.Helper()
	if got != want {
		t.Errorf("Resolve = %+v, want %+v", got, want)
	}
}

func TestResolve_RepoRoot(t *testing.T) {
	base := realDir(t, t.TempDir())
	root := newRepo(t, base, "claudit")
	r := NewResolver()
	assertProject(t, r.Resolve(root), Project{Root: root, Name: "claudit", Kind: KindRepo})
}

func TestResolve_NestedSubdirectory(t *testing.T) {
	base := realDir(t, t.TempDir())
	root := newRepo(t, base, "claudit")
	sub := mkdir(t, filepath.Join(root, "internal", "aggregate"))
	r := NewResolver()
	assertProject(t, r.Resolve(sub), Project{Root: root, Name: "claudit", Kind: KindRepo})
}

func TestResolve_LinkedWorktree(t *testing.T) {
	base := realDir(t, t.TempDir())
	main := newRepo(t, base, "the-pit")
	mkdir(t, filepath.Join(main, ".git", "worktrees", "pit-2mr.1"))
	wt := mkdir(t, filepath.Join(base, "elsewhere", "pit-2mr.1"))
	writeFile(t, filepath.Join(wt, ".git"),
		"gitdir: "+filepath.Join(main, ".git", "worktrees", "pit-2mr.1")+"\n")
	sub := mkdir(t, filepath.Join(wt, "web"))

	r := NewResolver()
	want := Project{Root: main, Name: "the-pit", Kind: KindRepo}
	assertProject(t, r.Resolve(wt), want)
	assertProject(t, r.Resolve(sub), want)
}

func TestResolve_LinkedWorktreeRelativeGitdir(t *testing.T) {
	base := realDir(t, t.TempDir())
	main := newRepo(t, base, "the-pit")
	mkdir(t, filepath.Join(main, ".git", "worktrees", "wt"))
	wt := mkdir(t, filepath.Join(base, "wt"))
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: ../the-pit/.git/worktrees/wt\n")

	r := NewResolver()
	assertProject(t, r.Resolve(wt), Project{Root: main, Name: "the-pit", Kind: KindRepo})
}

// encode is Claude Code's cwd encoding: every non-alphanumeric rune → '-'.
func encode(path string) string {
	var b strings.Builder
	for _, c := range path {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func TestResolve_ScratchDecodable(t *testing.T) {
	base := realDir(t, t.TempDir())
	// Hyphens and dots inside names are ambiguous with the separator; the
	// decode must still find /Users/me/.agents-x/my-proj-v2 on disk. A
	// decoy /Users/me/my sibling makes the shorter split exist too.
	root := newRepo(t, filepath.Join(base, "Users", "me", ".agents-x"), "my-proj-v2")
	mkdir(t, filepath.Join(base, "Users", "me", ".agents-x", "my"))
	enc := encode("/Users/me/.agents-x/my-proj-v2")
	if enc != "-Users-me--agents-x-my-proj-v2" {
		t.Fatalf("encode fixture = %q", enc)
	}

	r := NewResolver(WithBase(base))
	want := Project{Root: root, Name: "my-proj-v2", Kind: KindRepo}
	for _, cwd := range []string{
		"/tmp/claude-501/" + enc + "/0c1d-uuid/scratchpad/evals/runs/c1-r3/repo",
		"/private/tmp/claude-501/" + enc + "/0c1d-uuid/scratchpad",
		"/private/tmp/claude-42/" + enc,
	} {
		assertProject(t, r.Resolve(cwd), want)
	}
}

// A non-ASCII directory name encodes one '-' per rune, so café on disk
// matches the caf- segment Claude Code writes for it.
func TestResolve_ScratchDecodableNonASCII(t *testing.T) {
	base := realDir(t, t.TempDir())
	root := newRepo(t, filepath.Join(base, "Users", "me", "café"), "naïve-app")
	enc := encode("/Users/me/café/naïve-app")
	if enc != "-Users-me-caf--na-ve-app" {
		t.Fatalf("encode fixture = %q", enc)
	}

	r := NewResolver(WithBase(base))
	assertProject(t,
		r.Resolve("/private/tmp/claude-501/"+enc+"/uuid/scratchpad"),
		Project{Root: root, Name: "naïve-app", Kind: KindRepo})
}

func TestResolve_ScratchOpaque(t *testing.T) {
	base := realDir(t, t.TempDir())
	mkdir(t, filepath.Join(base, "Users", "me"))
	r := NewResolver(WithBase(base))
	assertProject(t,
		r.Resolve("/private/tmp/claude-501/-Users-me-gone-repo/uuid/scratchpad"),
		Project{Root: "", Name: "scratch", Kind: KindScratch})
}

func TestResolve_ScratchPatternNeedsDigits(t *testing.T) {
	base := realDir(t, t.TempDir())
	r := NewResolver(WithBase(base))
	got := r.Resolve("/tmp/claude-abc/-Users-me/x")
	if got.Kind == KindScratch {
		t.Errorf("non-numeric claude-<uid> must not match the scratch layout: %+v", got)
	}
}

func TestResolve_MissingInsideRepo(t *testing.T) {
	base := realDir(t, t.TempDir())
	root := newRepo(t, base, "claudit")
	gone := filepath.Join(root, "deleted", "deeper")
	r := NewResolver()
	assertProject(t, r.Resolve(gone), Project{Root: root, Name: "claudit", Kind: KindRepo})
}

func TestResolve_MissingOutsideRepo(t *testing.T) {
	base := realDir(t, t.TempDir())
	gone := filepath.Join(base, "summoner-worktrees-1", "summoner-claudit-apd")
	r := NewResolver()
	assertProject(t, r.Resolve(gone), Project{Root: "", Name: "summoner-claudit-apd", Kind: KindMissing})
}

func TestResolve_PlainDirectory(t *testing.T) {
	base := realDir(t, t.TempDir())
	dir := mkdir(t, filepath.Join(base, "notes"))
	r := NewResolver()
	assertProject(t, r.Resolve(dir), Project{Root: dir, Name: "notes", Kind: KindPlain})
}

func TestResolve_EmptyCWD(t *testing.T) {
	r := NewResolver()
	assertProject(t, r.Resolve(""), Project{Root: "", Name: "(unknown)", Kind: KindPlain})
}

func TestResolve_Memoized(t *testing.T) {
	base := realDir(t, t.TempDir())
	root := newRepo(t, base, "claudit")
	r := NewResolver()
	first := r.Resolve(root)
	// Removing the repo must not change the answer: the first resolution is
	// cached for the process lifetime.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	assertProject(t, r.Resolve(root), first)
}

// A missing path whose name resembles a live repo's carries no filesystem
// link to it, so it stays a missing bucket whether the look-alike repo was
// resolved before it, after it, or not at all.
func TestResolve_MissingIgnoresSimilarlyNamedRepos(t *testing.T) {
	base := realDir(t, t.TempDir())
	projects := filepath.Join(base, "Projects")
	cases := map[string]Project{
		filepath.Join(projects, "the-pit-notes"):                                 {Name: "the-pit-notes", Kind: KindMissing},
		filepath.Join(projects, "the-pit-pit-2mr.1", "web"):                      {Name: "web", Kind: KindMissing},
		filepath.Join(base, "T", "summoner-worktrees-9", "summoner-djinn-sv28x"): {Name: "summoner-djinn-sv28x", Kind: KindMissing},
	}
	r := NewResolver()
	for cwd, want := range cases {
		assertProject(t, r.Resolve(cwd), want)
	}

	pit := newRepo(t, projects, "the-pit")
	djinn := newRepo(t, projects, "djinn")
	assertProject(t, r.Resolve(pit), Project{Root: pit, Name: "the-pit", Kind: KindRepo})
	assertProject(t, r.Resolve(djinn), Project{Root: djinn, Name: "djinn", Kind: KindRepo})
	for cwd, want := range cases {
		assertProject(t, r.Resolve(cwd), want)
	}

	// A fresh Resolver that sees the live repos first answers the same.
	fresh := NewResolver()
	fresh.Resolve(pit)
	fresh.Resolve(djinn)
	for cwd, want := range cases {
		assertProject(t, fresh.Resolve(cwd), want)
	}
}
