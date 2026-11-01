package provider

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodex_WalkYieldsDatedRollouts(t *testing.T) {
	root := t.TempDir()
	want := []string{
		filepath.Join(root, "2026", "10", "31", "rollout-2026-10-31T16-53-12-01a19461-aed4-78a0-bad1-efc7859dd26a.jsonl"),
		filepath.Join(root, "2026", "05", "30", "rollout-2026-05-30T01-20-17-019e77f8-3d08-7371-bc87-6e200272fbeb.jsonl"),
		filepath.Join(root, "2026", "04", "12", "rollout-2026-04-12T09-01-44-019d8a10-1b2c-7d3e-8f40-5a6b7c8d9e0f.jsonl.zst"),
	}
	for _, p := range want {
		touch(t, p)
	}
	for _, p := range []string{
		filepath.Join(root, "rollout-2026-10-31T16-53-12-x.jsonl"),        // not under YYYY/MM/DD
		filepath.Join(root, "2026", "10", "31", "notes.jsonl"),            // not a rollout
		filepath.Join(root, "2026", "10", "31", "rollout-x.json"),         // not .jsonl
		filepath.Join(root, "2026", "10", "31", "rollout-x.jsonl.gz"),     // not .jsonl or .jsonl.zst
		filepath.Join(root, "2026", "10", "31", "rollout-x.zst"),          // .zst but not .jsonl.zst
		filepath.Join(root, "2026", "10", "31", "notes.jsonl.zst"),        // archive, but not a rollout
		filepath.Join(root, "2026", "10", "31", "sub", "rollout-x.jsonl"), // too deep
		filepath.Join(root, "archive", "10", "31", "rollout-x.jsonl"),     // not a year
	} {
		touch(t, p)
	}

	c := NewCodex(root)
	if c.Name() != "codex" || c.Root() != root {
		t.Errorf("Name/Root = %q/%q", c.Name(), c.Root())
	}
	var got []string
	if err := c.Walk(time.Time{}, func(path string, _ time.Time, size int64) {
		got = append(got, path)
		if size != 3 {
			t.Errorf("%s size = %d, want 3", path, size)
		}
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("walk = %v, want %v", got, want)
	}

	// The mtime pre-filter skips files older than earliest.
	old := time.Now().Add(-48 * time.Hour)
	stale := filepath.Join(root, "2026", "10", "31", "rollout-2026-10-31T16-53-12-01a19461-aed4-78a0-bad1-efc7859dd26a.jsonl")
	fresh := filepath.Join(root, "2026", "05", "30", "rollout-2026-05-30T01-20-17-019e77f8-3d08-7371-bc87-6e200272fbeb.jsonl")
	archive := filepath.Join(root, "2026", "04", "12", "rollout-2026-04-12T09-01-44-019d8a10-1b2c-7d3e-8f40-5a6b7c8d9e0f.jsonl.zst")
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	got = nil
	if err := c.Walk(time.Now().Add(-time.Hour), func(path string, _ time.Time, _ int64) { got = append(got, path) }); err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if wantFiltered := []string{archive, fresh}; !reflect.DeepEqual(got, wantFiltered) {
		t.Errorf("filtered walk = %v, want %v", got, wantFiltered)
	}
}

func TestCodex_MissingRootYieldsNothing(t *testing.T) {
	c := NewCodex(filepath.Join(t.TempDir(), "absent"))
	n := 0
	if err := c.Walk(time.Time{}, func(string, time.Time, int64) { n++ }); err != nil {
		t.Errorf("Walk on a missing root = %v, want nil", err)
	}
	if n != 0 {
		t.Errorf("yielded %d files", n)
	}
}

func TestCodex_ParseFileReadsRollout(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "parse", "codex", "testdata", "basic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := NewCodex("").ParseFile(f, "basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 2 || res.Turns[0].Provider != "codex" {
		t.Errorf("turns = %+v", res.Turns)
	}
}

func parseCodexFixture(t *testing.T, name string) parse.Result {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "parse", "codex", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := NewCodex("").ParseFile(f, name)
	if err != nil {
		t.Fatalf("ParseFile(%s): %v", name, err)
	}
	return res
}

func TestCodex_ParseFileReadsZstdArchive(t *testing.T) {
	plain := parseCodexFixture(t, "basic.jsonl")
	archive := parseCodexFixture(t, "basic.jsonl.zst")
	if len(plain.Turns) == 0 {
		t.Fatal("basic.jsonl yielded no turns")
	}
	for i := range archive.Turns {
		if archive.Turns[i].SourceFile != "basic.jsonl.zst" {
			t.Errorf("turn %d SourceFile = %q, want basic.jsonl.zst", i, archive.Turns[i].SourceFile)
		}
		archive.Turns[i].SourceFile = "basic.jsonl"
	}
	if !reflect.DeepEqual(archive.Turns, plain.Turns) {
		t.Errorf("archive Turns = %+v\nwant %+v", archive.Turns, plain.Turns)
	}
	if !reflect.DeepEqual(archive.ToolResults, plain.ToolResults) {
		t.Errorf("archive ToolResults = %+v\nwant %+v", archive.ToolResults, plain.ToolResults)
	}
	if !reflect.DeepEqual(archive.RateLimitSamples, plain.RateLimitSamples) {
		t.Errorf("archive RateLimitSamples = %+v\nwant %+v", archive.RateLimitSamples, plain.RateLimitSamples)
	}
}
