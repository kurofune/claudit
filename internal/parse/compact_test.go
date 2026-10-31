package parse

import (
	"os"
	"testing"
	"time"
)

func TestParseFile_CompactBoundary(t *testing.T) {
	const path = "testdata/compact_session.jsonl"
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	res, err := ParseFile(f, path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(res.Turns) != 6 {
		t.Fatalf("turns = %d, want 6", len(res.Turns))
	}
	if len(res.CompactBoundaries) != 1 {
		t.Fatalf("CompactBoundaries = %d, want 1", len(res.CompactBoundaries))
	}
	b := res.CompactBoundaries[0]
	want := time.Date(2026, 4, 10, 11, 0, 7, 0, time.UTC)
	if !b.Timestamp.Equal(want) {
		t.Errorf("boundary ts = %v, want %v", b.Timestamp, want)
	}
	if b.SessionID != "sess-K" || b.SourceFile != path {
		t.Errorf("boundary = %+v, want session sess-K in %s", b, path)
	}
	if !res.Turns[2].Timestamp.Before(b.Timestamp) || !b.Timestamp.Before(res.Turns[3].Timestamp) {
		t.Errorf("boundary %v not between turn 3 (%v) and turn 4 (%v)", b.Timestamp, res.Turns[2].Timestamp, res.Turns[3].Timestamp)
	}
}

func TestParseFile_NoCompactBoundaryWithoutMarker(t *testing.T) {
	f, err := os.Open("testdata/main_session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	res, err := ParseFile(f, "testdata/main_session.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CompactBoundaries) != 0 {
		t.Errorf("CompactBoundaries = %v, want none", res.CompactBoundaries)
	}
}
