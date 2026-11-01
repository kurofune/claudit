package corpus

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/kurofune/claudit/internal/provider"
)

// TestLoadConcurrent_RealCodexCorpus is the dogfood check for Codex rollouts,
// plain and zstd-archived, including those priced from event_msg
// token_count: it loads a real ~/.codex/sessions tree
// and requires zero FileErrors and at least one turn from every rollout that
// carries a non-null last_token_usage. A rollout written while the load ran
// is skipped: its parse and its scan would see different contents. It runs
// only when CLAUDIT_CODEX_CORPUS names the corpus root, so CI never depends on
// a developer's home directory:
//
//	CLAUDIT_CODEX_CORPUS=$HOME/.codex/sessions go test ./internal/corpus -run RealCodexCorpus -v
func TestLoadConcurrent_RealCodexCorpus(t *testing.T) {
	root := os.Getenv("CLAUDIT_CODEX_CORPUS")
	if root == "" {
		t.Skip("CLAUDIT_CODEX_CORPUS not set")
	}
	src := provider.NewCodex(root)
	start := time.Now()
	snap, err := LoadConcurrent([]provider.Source{src}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	loadTime := time.Since(start)
	for _, e := range snap.FileErrors {
		t.Errorf("FileError: %v", e)
	}

	turnsByFile := map[string]int{}
	for _, tr := range snap.Turns {
		turnsByFile[tr.SourceFile]++
	}

	var withReading, legacyOnly, legacyTurns, live int
	if err := src.Walk(time.Time{}, func(path string, mod time.Time, _ int64) {
		if !mod.Before(start) {
			live++
			t.Logf("skipped, written during the load: %s", path)
			return
		}
		reading, record := scanRollout(t, path)
		if !reading {
			return
		}
		withReading++
		if turnsByFile[path] == 0 {
			t.Errorf("%s: non-null last_token_usage but zero turns", path)
		}
		if !record {
			legacyOnly++
			legacyTurns += turnsByFile[path]
			t.Logf("priced from token_count only: %s (%d turns)", path, turnsByFile[path])
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("load took %s", loadTime.Round(time.Millisecond))
	t.Logf("files=%d FileErrors=%d Malformed=%d turns=%d; skipped live=%d; with last_token_usage=%d; formerly zero-turn (no token_usage_record)=%d yielding %d turns",
		snap.FileCount, len(snap.FileErrors), snap.Malformed, len(snap.Turns), live, withReading, legacyOnly, legacyTurns)
}

// scanRollout reports whether the rollout holds an event_msg token_count with
// a non-null info.last_token_usage, and whether it holds any
// token_usage_record.
func scanRollout(t *testing.T, path string) (reading, record bool) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var line struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
			Info *struct {
				LastTokenUsage json.RawMessage `json:"last_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	var r io.Reader = f
	if strings.HasSuffix(path, ".zst") {
		dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer dec.Close()
		r = dec
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<28)
	for sc.Scan() {
		line.Type, line.Payload.Type, line.Payload.Info = "", "", nil
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch {
		case line.Type == "token_usage_record":
			record = true
		case line.Type == "event_msg" && line.Payload.Type == "token_count" &&
			line.Payload.Info != nil && len(line.Payload.Info.LastTokenUsage) > 0 &&
			string(line.Payload.Info.LastTokenUsage) != "null":
			reading = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return reading, record
}
