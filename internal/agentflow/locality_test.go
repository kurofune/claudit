package agentflow

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/kurofune/claudit/v2/internal/aggregate"
	"github.com/kurofune/claudit/v2/internal/corpus"
	"github.com/kurofune/claudit/v2/internal/parse"
	"github.com/kurofune/claudit/v2/internal/pricing"
	"github.com/kurofune/claudit/v2/internal/provider"
)

// addSession appends one file of a session: a prompt, a system line the
// first turn's chain climbs through, and turns turns each calling a tool
// whose result follows it.
func addSession(snap *corpus.Snapshot, sid string, turns int, t0 time.Time) {
	file := "/p/" + sid + ".jsonl"
	prompt := sid + "-u"
	users := []parse.UserMessage{{SessionID: sid, UUID: prompt, Timestamp: t0, Text: "fix the build " + sid, SourceFile: file}}
	links := []parse.ParentLink{{UUID: sid + "-sys", ParentUUID: prompt}}
	var ts []parse.Turn
	var results []parse.ToolResult
	parent := sid + "-sys"
	for i := range turns {
		at := t0.Add(time.Duration(i+1) * time.Second)
		uuid, call := fmt.Sprintf("%s-a%d", sid, i), fmt.Sprintf("%s-c%d", sid, i)
		t := ctxTurn(uuid, uuid+"-m", sid, file, at, call)
		t.ParentUUID = parent
		ts = append(ts, t)
		links = append(links, parse.ParentLink{UUID: uuid, ParentUUID: parent})
		results = append(results, ctxResult(call, 2_000, at.Add(time.Second/2)))
		parent = uuid
	}
	snap.AddFile(file, corpus.FileStamp{Size: int64(turns), ModTime: t0}, ts, users, links, results, nil)
}

// storyAllocBytes is what building session "A"'s story allocates over a
// prebuilt Layout of a snapshot holding A and others unrelated sessions.
func storyAllocBytes(t *testing.T, prices *pricing.Table, others int) (uint64, *Layout) {
	t.Helper()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	snap := &corpus.Snapshot{}
	addSession(snap, "A", 20, t0)
	for i := range others {
		addSession(snap, fmt.Sprintf("other%05d", i), 20, t0)
	}
	l := NewLayout(snap, nil)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	st, ok := BuildSessionStory(snap, prices, aggregate.Filter{}, "A", StoryOptions{Layout: l})
	runtime.ReadMemStats(&after)
	if !ok || len(st.Prompts) != 1 || st.Prompts[0].Text != "fix the build A" || st.Prompts[0].Turns != 20 {
		t.Fatalf("story of A = %+v", st)
	}
	return after.TotalAlloc - before.TotalAlloc, l
}

// TestBuildSessionStory_CostsItsOwnFiles: a session's story reads its own
// files' prompts, parent links and tool results, so what it allocates does
// not grow with unrelated sessions, and it never builds the snapshot-wide
// prompt chains when its chains stay in its own files.
func TestBuildSessionStory_CostsItsOwnFiles(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	small, l := storyAllocBytes(t, prices, 10)
	if l.chainsBuilt() {
		t.Errorf("a story whose chains stay in its files built the snapshot-wide chains")
	}
	large, l := storyAllocBytes(t, prices, 3_000)
	if l.chainsBuilt() {
		t.Errorf("a story whose chains stay in its files built the snapshot-wide chains at 3,000 sessions")
	}
	if large > small*3/2 {
		t.Errorf("story of a 20-turn session allocated %d bytes beside 10 sessions, %d beside 3,000: it grows with the corpus", small, large)
	}
	t.Logf("story allocations: %d bytes beside 10 sessions, %d beside 3,000", small, large)
}

// TestBuildSessionStory_ChainLeavingItsFiles: a turn whose parent chain
// climbs into another file's lines still finds the prompt there, as the
// snapshot-wide walk does.
func TestBuildSessionStory_ChainLeavingItsFiles(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	snap := &corpus.Snapshot{}
	addSession(snap, "other", 2, t0)
	first := ctxTurn("B-a0", "B-m0", "B", "/p/B.jsonl", t0.Add(time.Minute), "B-c0")
	first.ParentUUID = "other-a1"
	snap.AddFile("/p/B.jsonl", corpus.FileStamp{Size: 1, ModTime: t0}, []parse.Turn{first}, nil, nil, nil, nil)

	l := NewLayout(snap, nil)
	local, ok := BuildSessionStory(snap, prices, aggregate.Filter{}, "B", StoryOptions{Layout: l})
	if !ok {
		t.Fatal("B not found")
	}
	whole := &corpus.Snapshot{Turns: snap.Turns, Users: snap.Users, Links: snap.Links, ToolResults: snap.ToolResults}
	scanned, _ := BuildSessionStory(whole, prices, aggregate.Filter{}, "B", StoryOptions{})
	if len(local.Prompts) != 1 || local.Prompts[0].UUID != "other-u" || local.Prompts[0].Text != "fix the build other" {
		t.Errorf("prompts = %+v, want the other file's prompt the chain climbs to", local.Prompts)
	}
	if fmt.Sprint(local.Prompts) != fmt.Sprint(scanned.Prompts) {
		t.Errorf("session-local prompts %+v, snapshot-wide %+v", local.Prompts, scanned.Prompts)
	}
	if !l.chainsBuilt() {
		t.Errorf("the chain left B's file without the snapshot-wide chains")
	}
	if got := local.promptLog["other-u"].File; got != "/p/other.jsonl" {
		t.Errorf("prompt log = %q, want the other file", got)
	}
}

// spanless is snap without its file spans: every reader scans the whole
// snapshot, as stories were built before they read their own files.
func spanless(snap *corpus.Snapshot) *corpus.Snapshot {
	return &corpus.Snapshot{Turns: snap.Turns, Users: snap.Users, Links: snap.Links, ToolResults: snap.ToolResults,
		CompactBoundaries: snap.CompactBoundaries, Files: snap.Files, Generation: snap.Generation}
}

// sameStory reports whether two stories of one session carry the same
// prompts, steps, sub-agents, index row and prompt logs.
func sameStory(a, b *SessionStory) bool {
	ra, _ := a.Row()
	rb, _ := b.Row()
	return fmt.Sprint(a.Whole(), ra, a.promptLog) == fmt.Sprint(b.Whole(), rb, b.promptLog)
}

// TestBuildSessionStory_LocalEqualsSnapshotWide: a story read from its
// session's own files equals the one read from the whole snapshot, for
// every session of a synthetic corpus and, with CLAUDIT_CORPUS set, of a
// real projects root. On the real root the story is checked against the
// snapshot's PromptIndex, and only a session whose prompts differ there
// is rebuilt from the whole snapshot, so the run stays session-sized.
func TestBuildSessionStory_LocalEqualsSnapshotWide(t *testing.T) {
	prices, err := pricing.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	snap := &corpus.Snapshot{}
	for i := range 5 {
		addSession(snap, fmt.Sprintf("s%d", i), 3+i, t0)
	}
	first := ctxTurn("B-a0", "B-m0", "B", "/p/B.jsonl", t0.Add(time.Minute), "B-c0")
	first.ParentUUID = "s1-a2"
	snap.AddFile("/p/B.jsonl", corpus.FileStamp{Size: 1, ModTime: t0}, []parse.Turn{first}, nil, nil,
		[]parse.ToolResult{ctxResult("B-c0", 900, t0.Add(time.Minute+time.Second))}, nil)
	whole := spanless(snap)
	l, lw := NewLayout(snap, nil), NewLayout(whole, nil)
	for _, id := range l.Sessions() {
		a, _ := BuildSessionStory(snap, prices, aggregate.Filter{}, id, StoryOptions{Layout: l})
		b, _ := BuildSessionStory(whole, prices, aggregate.Filter{}, id, StoryOptions{Layout: lw})
		if !sameStory(a, b) {
			t.Errorf("session %s: session-local story differs from the snapshot-wide one", id)
		}
	}

	root := os.Getenv("CLAUDIT_CORPUS")
	if root == "" {
		return
	}
	real, err := corpus.LoadConcurrent([]provider.Source{provider.NewClaudeCode(root)}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	idx := aggregate.BuildPromptIndex(real.Turns, real.Users, real.Links)
	l = NewLayout(real, nil)
	rebuilt := 0
	for _, id := range l.Sessions() {
		a, _ := BuildSessionStory(real, prices, aggregate.Filter{}, id, StoryOptions{Layout: l})
		b, _ := BuildSessionStory(real, prices, aggregate.Filter{}, id, StoryOptions{Layout: l, PromptIndex: idx})
		if fmt.Sprint(a.Prompts) == fmt.Sprint(b.Prompts) {
			continue
		}
		rebuilt++
		c, _ := BuildSessionStory(spanless(real), prices, aggregate.Filter{}, id, StoryOptions{})
		if !sameStory(a, c) {
			t.Errorf("CLAUDIT_CORPUS session %s: session-local story differs from the snapshot-wide one", id)
		}
	}
	t.Logf("CLAUDIT_CORPUS: %d sessions, %d rebuilt from the whole snapshot", len(l.Sessions()), rebuilt)
}
