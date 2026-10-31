// Package project maps a session's working directory to the project it
// belongs to: the git repo root (a linked worktree folds into its main
// checkout), a decoded Claude Code scratch dir, or a fallback bucket. It reads
// only the filesystem — never the git binary — so resolution is deterministic
// and testable against temp-dir layouts.
package project

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Kind classifies how a cwd resolved.
const (
	KindRepo    = "repo"    // inside a git work tree; Root is the main checkout
	KindScratch = "scratch" // a Claude Code tmp scratch dir that decodes to nothing on disk
	KindMissing = "missing" // the cwd is gone and no surviving ancestor is in a repo
	KindPlain   = "plain"   // an existing non-repo directory, or no cwd at all
)

// Project is a resolved cwd. Name is the grouping key every by-project
// surface uses; Root is the directory it stands for ("" when there is none).
type Project struct {
	Root string
	Name string
	Kind string
}

// scratchRe matches Claude Code's per-uid tmp layout,
// /tmp/claude-<uid>/<encoded-cwd>/..., where <encoded-cwd> is the launching
// session's cwd encoded the same way ~/.claude/projects/<encoded> names are.
var scratchRe = regexp.MustCompile(`^/(?:private/)?tmp/claude-[0-9]+/([^/]+)(?:/|$)`)

// Resolver resolves and memoizes cwds for the process lifetime. Safe for
// concurrent use.
//
// A cwd that no longer exists (a removed worktree, most often) carries no
// on-disk link to its repo, only its name: the-pit-pit-2mr.1,
// summoner-djinn-sv28x. So the Resolver learns the name of every repo it
// resolves from a live path and folds a missing cwd into the longest known
// repo name found as a hyphen-delimited run in its missing path segments.
// Those answers depend on what has been learned, so they are re-derived when
// a new repo name arrives; Prime resolves a whole corpus so the outcome does
// not depend on the order its cwds arrive in.
type Resolver struct {
	base    string
	byCWD   sync.Map // cwd -> entry
	decoded sync.Map // encoded scratch segment -> decoded dir ("" when undecodable)

	mu      sync.Mutex
	known   map[string]string // repo Name -> Root, learned from live paths
	version atomic.Uint64     // bumped whenever known changes
}

type entry struct {
	p Project
	// guessed marks an answer for a missing path, which depends on the known
	// repo names as of version.
	guessed bool
	version uint64
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithBase roots scratch-path decoding at base instead of "/", so tests can
// lay the decoded tree out under t.TempDir().
func WithBase(base string) Option {
	return func(r *Resolver) { r.base = base }
}

// NewResolver returns an empty Resolver.
func NewResolver(opts ...Option) *Resolver {
	r := &Resolver{base: string(filepath.Separator), known: map[string]string{}}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Default is the process-wide Resolver for callers with no reason to inject
// their own.
var Default = NewResolver()

// Resolve maps cwd to its Project, memoized per cwd.
func (r *Resolver) Resolve(cwd string) Project {
	if v, ok := r.byCWD.Load(cwd); ok {
		e := v.(entry)
		if !e.guessed || e.version == r.version.Load() {
			return e.p
		}
	}
	version := r.version.Load()
	p, guessed := r.resolve(cwd)
	r.byCWD.Store(cwd, entry{p: p, guessed: guessed, version: version})
	if p.Kind == KindRepo && !guessed {
		r.learn(p)
	}
	return p
}

// Prime resolves every cwd twice: the first pass learns each live repo's
// name, the second re-derives any missing cwd resolved before a repo it
// matches was known. Afterwards Resolve answers for these cwds are the same
// whatever order they were passed in.
func (r *Resolver) Prime(cwds []string) {
	for range 2 {
		for _, c := range cwds {
			r.Resolve(c)
		}
	}
}

func (r *Resolver) resolve(cwd string) (Project, bool) {
	if cwd == "" {
		return Project{Name: "(unknown)", Kind: KindPlain}, false
	}
	if m := scratchRe.FindStringSubmatch(cwd); m != nil {
		dir := r.decodeScratch(m[1])
		if dir == "" {
			return Project{Name: "scratch", Kind: KindScratch}, false
		}
		return r.resolveDir(dir)
	}
	return r.resolveDir(cwd)
}

// resolveDir resolves a real path: the repo holding it, else the directory
// itself when it exists, else (for a missing path) the known repo its name
// carries, else a missing bucket named after it. The bool reports a missing
// path, whose answer depends on the known repo names.
func (r *Resolver) resolveDir(cwd string) (Project, bool) {
	cwd = filepath.Clean(cwd)
	existing, missing := nearestExisting(cwd)
	if root, ok := repoRoot(existing); ok {
		return Project{Root: root, Name: filepath.Base(root), Kind: KindRepo}, missing
	}
	if !missing {
		return Project{Root: cwd, Name: filepath.Base(cwd), Kind: KindPlain}, false
	}
	rel, err := filepath.Rel(existing, cwd)
	if err == nil {
		if name, root := r.matchKnown(strings.Split(rel, string(filepath.Separator))); name != "" {
			return Project{Root: root, Name: name, Kind: KindRepo}, true
		}
	}
	return Project{Name: filepath.Base(cwd), Kind: KindMissing}, true
}

// learn records a live repo's name. Two repos sharing a basename keep the
// lexically smallest root so the choice does not depend on arrival order.
func (r *Resolver) learn(p Project) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.known[p.Name]; ok && cur <= p.Root {
		return
	}
	r.known[p.Name] = p.Root
	r.version.Add(1)
}

// matchKnown returns the longest known repo name that appears as a
// hyphen-delimited run in a segment, checking segments top-down so the
// removed worktree directory itself wins over the subdirectories under it.
func (r *Resolver) matchKnown(segments []string) (name, root string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, seg := range segments {
		for n := range r.known {
			if !hasHyphenRun(seg, n) {
				continue
			}
			if len(n) > len(name) || len(n) == len(name) && n < name {
				name = n
			}
		}
		if name != "" {
			return name, r.known[name]
		}
	}
	return "", ""
}

func hasHyphenRun(seg, name string) bool {
	return seg == name ||
		strings.HasPrefix(seg, name+"-") ||
		strings.HasSuffix(seg, "-"+name) ||
		strings.Contains(seg, "-"+name+"-")
}

// nearestExisting walks up from path to the first directory that exists,
// reporting whether path itself was missing.
func nearestExisting(path string) (string, bool) {
	missing := false
	for {
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			return path, missing
		}
		missing = true
		parent := filepath.Dir(path)
		if parent == path {
			return path, missing
		}
		path = parent
	}
}

// repoRoot walks up from dir to the nearest .git. A .git directory marks the
// root itself; a .git file is a linked worktree (or submodule) whose gitdir
// line names the main repository.
func repoRoot(dir string) (string, bool) {
	for {
		gitPath := filepath.Join(dir, ".git")
		if fi, err := os.Lstat(gitPath); err == nil {
			if fi.IsDir() {
				return dir, true
			}
			if fi.Mode().IsRegular() {
				return gitFileRoot(dir, gitPath), true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// gitFileRoot reads a .git file's `gitdir: <path>` line. A linked worktree's
// gitdir is <main>/.git/worktrees/<name>, so the main checkout is the segment
// before /.git. Anything else (a submodule, --separate-git-dir) keeps the
// directory holding the .git file as its root.
func gitFileRoot(dir, gitPath string) string {
	f, err := os.Open(gitPath)
	if err != nil {
		return dir
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return dir
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "gitdir:")
	if !ok {
		return dir
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	gitdir = filepath.ToSlash(filepath.Clean(gitdir))
	if i := strings.Index(gitdir, "/.git/worktrees/"); i > 0 {
		return filepath.FromSlash(gitdir[:i])
	}
	return dir
}

// decodeScratch turns an encoded cwd back into the directory it names under
// r.base, or "" when nothing on disk matches. Claude Code encodes a path by
// replacing every non-alphanumeric rune with '-', so each '-' may be a
// separator, a literal hyphen or another punctuation rune; the decode walks
// the real tree, preferring the longest directory name that matches at each
// level and backtracking when a choice dead-ends.
func (r *Resolver) decodeScratch(encoded string) string {
	if v, ok := r.decoded.Load(encoded); ok {
		return v.(string)
	}
	dir := ""
	if rest, ok := strings.CutPrefix(encoded, "-"); ok && rest != "" {
		dir, _ = decodeUnder(r.base, rest)
	}
	r.decoded.Store(encoded, dir)
	return dir
}

func decodeUnder(dir, rest string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	type cand struct{ name, enc string }
	var cands []cand
	for _, e := range entries {
		enc := encodeName(e.Name())
		if !strings.HasPrefix(rest, enc) {
			continue
		}
		if len(rest) > len(enc) && rest[len(enc)] != '-' {
			continue
		}
		cands = append(cands, cand{e.Name(), enc})
	}
	sort.SliceStable(cands, func(i, j int) bool { return len(cands[i].enc) > len(cands[j].enc) })
	for _, c := range cands {
		path := filepath.Join(dir, c.name)
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			continue
		}
		next := rest[len(c.enc):]
		if next == "" {
			return path, true
		}
		if found, ok := decodeUnder(path, next[1:]); ok {
			return found, true
		}
	}
	return "", false
}

func encodeName(name string) string {
	b := []byte(name)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}
