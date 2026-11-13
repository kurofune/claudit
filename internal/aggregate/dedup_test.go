package aggregate

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/kurofune/claudit/v2/internal/parse"
)

// A resumed/forked session writes the prior transcript into a new file
// verbatim — same message.id (and usage, uuid, timestamp); only sessionId
// and the file path differ. BuildReplaySet must flag every non-canonical
// copy so per-session/per-agent rollups count each generation once.

func TestBuildReplaySet_FlagsNonCanonicalCopy(t *testing.T) {
	turns := []parse.Turn{
		{MessageID: "m1", SourceFile: "a.jsonl"},
		{MessageID: "m1", SourceFile: "b.jsonl"},
	}
	rs := BuildReplaySet(turns)
	if rs.IsReplay(turns[0]) {
		t.Errorf("canonical (a.jsonl) must not be a replay")
	}
	if !rs.IsReplay(turns[1]) {
		t.Errorf("non-canonical (b.jsonl) must be a replay")
	}
}

func TestBuildReplaySet_OrderIndependent(t *testing.T) {
	// Same inputs, reversed order: the canonical pick (lexicographically
	// smallest source file) must not depend on iteration/load order, or the
	// per-session attribution would flip between concurrent loads.
	turns := []parse.Turn{
		{MessageID: "m1", SourceFile: "b.jsonl"},
		{MessageID: "m1", SourceFile: "a.jsonl"},
	}
	rs := BuildReplaySet(turns)
	if rs.IsReplay(parse.Turn{MessageID: "m1", SourceFile: "a.jsonl"}) {
		t.Errorf("a.jsonl must be canonical regardless of input order")
	}
	if !rs.IsReplay(parse.Turn{MessageID: "m1", SourceFile: "b.jsonl"}) {
		t.Errorf("b.jsonl must be the replay regardless of input order")
	}
}

func TestBuildReplaySet_SingleOccurrenceNotReplay(t *testing.T) {
	turns := []parse.Turn{{MessageID: "m1", SourceFile: "a.jsonl"}}
	rs := BuildReplaySet(turns)
	if rs.IsReplay(turns[0]) {
		t.Errorf("a message.id seen in only one file is never a replay")
	}
}

func TestBuildReplaySet_EmptyMessageIDNeverReplay(t *testing.T) {
	// Legacy single-line transcripts have no message.id; they can't be keyed
	// and must always count, even if two share a source file.
	turns := []parse.Turn{
		{MessageID: "", SourceFile: "a.jsonl"},
		{MessageID: "", SourceFile: "b.jsonl"},
	}
	rs := BuildReplaySet(turns)
	if rs.IsReplay(turns[0]) || rs.IsReplay(turns[1]) {
		t.Errorf("empty message.id must never be flagged as a replay")
	}
}

// buildReplaySetBySets is BuildReplaySet as it read when it kept every
// message.id's set of files.
func buildReplaySetBySets(turns []parse.Turn) ReplaySet {
	files := map[string]map[string]struct{}{}
	for _, t := range turns {
		if t.MessageID == "" {
			continue
		}
		if files[t.MessageID] == nil {
			files[t.MessageID] = map[string]struct{}{}
		}
		files[t.MessageID][t.SourceFile] = struct{}{}
	}
	canonical := map[string]string{}
	for id, fs := range files {
		if len(fs) < 2 {
			continue
		}
		min, first := "", true
		for f := range fs {
			if first || f < min {
				min, first = f, false
			}
		}
		canonical[id] = min
	}
	return ReplaySet{canonical: canonical}
}

// TestBuildReplaySet_MatchesSetBuild: over random turns spread across
// files, repeats within a file included, the canonical file of every
// message.id equals the set-based build's.
func TestBuildReplaySet_MatchesSetBuild(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 200; round++ {
		var turns []parse.Turn
		for i := rng.Intn(60); i >= 0; i-- {
			id := ""
			if rng.Intn(10) > 0 {
				id = fmt.Sprintf("m%d", rng.Intn(15))
			}
			turns = append(turns, parse.Turn{MessageID: id, SourceFile: fmt.Sprintf("/f%d.jsonl", rng.Intn(5))})
		}
		if got, want := BuildReplaySet(turns), buildReplaySetBySets(turns); !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: canonical %v, want %v", round, got.canonical, want.canonical)
		}
	}
}
