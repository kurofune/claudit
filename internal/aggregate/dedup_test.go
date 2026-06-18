package aggregate

import (
	"testing"

	"github.com/kurofune/claudit/internal/parse"
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
