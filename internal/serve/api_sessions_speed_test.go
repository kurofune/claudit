package serve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kurofune/claudit/internal/agentflow"
	"github.com/kurofune/claudit/internal/aggregate"
)

// twoSessionCorpus writes sessions s-a (300 turns, so it pages) and s-b
// (3 turns) into dir and returns their log paths.
func twoSessionCorpus(t *testing.T, dir string) (a, b string) {
	t.Helper()
	t0 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	a, b = filepath.Join(dir, "s-a.jsonl"), filepath.Join(dir, "s-b.jsonl")
	lines := []string{mkUserLineWithSession("s-a", "ua", "/p/a", "keep going", t0)}
	parent := "ua"
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("a%03d", i)
		lines = append(lines, mkAssistantLineWithCWD("s-a", id, parent, "/p/a", "claude-opus-4-7", 100, 10, t0.Add(time.Duration(i+1)*time.Second)))
		parent = id
	}
	writeJSONL(t, a, lines...)
	writeJSONL(t, b,
		mkUserLineWithSession("s-b", "ub", "/p/b", "fix it", t0),
		mkAssistantLineWithCWD("s-b", "b1", "ub", "/p/b", "claude-opus-4-7", 100, 10, t0.Add(time.Second)),
		mkAssistantLineWithCWD("s-b", "b2", "b1", "/p/b", "claude-opus-4-7", 100, 10, t0.Add(2*time.Second)),
	)
	return a, b
}

// appendTurn appends one assistant line to path, moves its mtime forward
// and refreshes srv's cache to a new generation.
func appendTurn(t *testing.T, srv *Server, path, session, uuid, parent string, ts time.Time) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(mkAssistantLineWithCWD(session, uuid, parent, "/p/"+session, "claude-opus-4-7", 100, 10, ts) + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := chtimes(path, future); err != nil {
		t.Fatal(err)
	}
	gen := srv.cache.Snapshot().Generation
	if _, err := srv.cache.Refresh(); err != nil {
		t.Fatal(err)
	}
	if srv.cache.Snapshot().Generation == gen {
		t.Fatalf("appending to %s did not move the generation", path)
	}
}

// TestAPISessionsStory_MemoSurvivesOtherSessionsWrites: another session's
// write moves the generation but not A's story, which serves its next page
// from the memo with no whole-corpus Aggregator; a write to A's own log
// rebuilds it.
func TestAPISessionsStory_MemoSurvivesOtherSessionsWrites(t *testing.T) {
	dir := t.TempDir()
	a, b := twoSessionCorpus(t, dir)
	srv := newTestServer(t, dir)

	getStory(t, srv, "?scope=all&session=s-a")
	appendTurn(t, srv, b, "s-b", "b3", "b2", time.Date(2026, 5, 1, 12, 10, 0, 0, time.UTC))
	p := getStoryPage(t, srv, "?scope=all&session=s-a&offset=200")
	if len(p.Steps) != 100 {
		t.Fatalf("second page: %d steps, want 100", len(p.Steps))
	}
	if got := srv.storyBuildCount(); got != 1 {
		t.Errorf("story builds after another session's write = %d, want 1", got)
	}
	if got := srv.aggregateBuildCount(); got != 0 {
		t.Errorf("whole-corpus aggregator builds = %d, want 0", got)
	}

	appendTurn(t, srv, a, "s-a", "a300", "a299", time.Date(2026, 5, 1, 12, 20, 0, 0, time.UTC))
	p = getStoryPage(t, srv, "?scope=all&session=s-a&offset=200")
	if len(p.Steps) != 101 {
		t.Errorf("after A's write: %d steps on its second page, want 101", len(p.Steps))
	}
	if got := srv.storyBuildCount(); got != 2 {
		t.Errorf("story builds after A's own write = %d, want 2", got)
	}
	if got := srv.aggregateBuildCount(); got != 0 {
		t.Errorf("whole-corpus aggregator builds = %d, want 0", got)
	}
}

func getIndex(t *testing.T, srv *Server) agentflow.SessionIndex {
	t.Helper()
	w := doAPI(t, srv, http.MethodGet, apiPathSessions+"?scope=all", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET sessions: status %d; body=%s", w.Code, w.Body.String())
	}
	var idx agentflow.SessionIndex
	if err := json.Unmarshal(w.Body.Bytes(), &idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

// TestAPISessions_RowsReusedAcrossGenerations: after a refresh in which
// only s-b's log grew, no row but s-b's is built again, and the index
// equals the previous one with only s-b's row changed.
func TestAPISessions_RowsReusedAcrossGenerations(t *testing.T) {
	dir := t.TempDir()
	_, b := twoSessionCorpus(t, dir)
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	writeContextSession(t, dir, "s-c", 10_000, t0)
	srv := newTestServer(t, dir)

	before := getIndex(t, srv)
	if len(before.Sessions) != 3 {
		t.Fatalf("index rows = %d, want 3", len(before.Sessions))
	}
	for _, id := range []string{"s-a", "s-b", "s-c"} {
		if got := srv.indexRowBuildCount(id); got != 1 {
			t.Fatalf("row %s built %d times on the first index, want 1", id, got)
		}
	}

	appendTurn(t, srv, b, "s-b", "b3", "b2", time.Date(2026, 5, 1, 12, 10, 0, 0, time.UTC))
	after := getIndex(t, srv)
	for id, want := range map[string]int{"s-a": 1, "s-b": 2, "s-c": 1} {
		if got := srv.indexRowBuildCount(id); got != want {
			t.Errorf("row %s built %d times after s-b grew, want %d", id, got, want)
		}
	}
	prev := map[string]agentflow.SessionIndexRow{}
	for _, r := range before.Sessions {
		prev[r.SessionID] = r
	}
	var changed []string
	for _, r := range after.Sessions {
		if !reflect.DeepEqual(prev[r.SessionID], r) {
			changed = append(changed, r.SessionID)
		}
	}
	if len(after.Sessions) != len(before.Sessions) || strings.Join(changed, ",") != "s-b" {
		t.Errorf("rows changed %v of %d, want only s-b of %d", changed, len(after.Sessions), len(before.Sessions))
	}
	snap := srv.cache.Snapshot()
	fresh, _ := json.Marshal(agentflow.BuildSessionIndex(snap, srv.opts.Prices, aggregate.Filter{}, agentflow.IndexOptions{}))
	got, _ := json.Marshal(after)
	if string(got) != string(fresh) {
		t.Errorf("reused index differs from a fresh build:\n got %s\nwant %s", got, fresh)
	}
}

// TestAPISessionsSummary: the summary carries the session's whole index
// row, its steps and children with every text, thinking, output and
// input_full dropped and each input cut to its first line of at most 120
// runes; an unknown session answers 404.
func TestAPISessionsSummary(t *testing.T) {
	dir := storyCorpus(t)
	t0 := time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)
	long := strings.Repeat("é", 150) + "\nsecond line"
	writeJSONL(t, filepath.Join(dir, "s-sum.jsonl"),
		mkUserLineWithSession("s-sum", "u1", "/p/sum", "run the tests", t0),
		mkThinkingLine("s-sum", "t1", "m-t1", "/p/sum", "let me think", "I will run them", t0.Add(time.Second)),
		mkToolUseLine("s-sum", "t2", "/p/sum", "toolu_long", long, t0.Add(2*time.Second)),
		mkToolResultLine("s-sum", "r2", "/p/sum", "toolu_long", "FAIL\nlots of output", t0.Add(3*time.Second)),
		mkToolUseLine("s-sum", "t3", "/p/sum", "toolu_short", "go vet ./...", t0.Add(4*time.Second)),
	)
	srv := newTestServer(t, dir)

	w := doAPI(t, srv, http.MethodGet, apiPathSessionsSummary+"?scope=all&session=s-sum", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("summary: status %d; body=%s", w.Code, w.Body.String())
	}
	body := readJSONResponse(t, w)
	rowKeys := []string{}
	rowType := reflect.TypeOf(agentflow.SessionIndexRow{})
	for i := 0; i < rowType.NumField(); i++ {
		rowKeys = append(rowKeys, strings.Split(rowType.Field(i).Tag.Get("json"), ",")[0])
	}
	assertTopLevelKeys(t, body, append(rowKeys, "steps", "children", "source_files"))

	var row agentflow.SessionIndexRow
	raw, _ := json.Marshal(body)
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	var want agentflow.SessionIndexRow
	for _, r := range agentflow.BuildSessionIndex(srv.cache.Snapshot(), srv.opts.Prices, aggregate.Filter{}, agentflow.IndexOptions{}).Sessions {
		if r.SessionID == "s-sum" {
			want = r
		}
	}
	if !reflect.DeepEqual(row, want) || row.Title != "run the tests" || row.Commands != 2 {
		t.Errorf("summary row = %+v\nindex row   = %+v", row, want)
	}

	var steps []map[string]json.RawMessage
	if err := json.Unmarshal(body["steps"], &steps); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("summary steps = %d, want 3", len(steps))
	}
	var inputs []string
	for _, s := range steps {
		for _, k := range []string{"text", "thinking"} {
			if _, ok := s[k]; ok {
				t.Errorf("step carries %s: %s", k, s[k])
			}
		}
		var tools []map[string]json.RawMessage
		_ = json.Unmarshal(s["tools"], &tools)
		for _, tool := range tools {
			for _, k := range []string{"output", "input_full"} {
				if _, ok := tool[k]; ok {
					t.Errorf("tool carries %s: %s", k, tool[k])
				}
			}
			var in, status string
			_ = json.Unmarshal(tool["input"], &in)
			_ = json.Unmarshal(tool["status"], &status)
			inputs = append(inputs, in)
			if in == "go vet ./..." && status != "" {
				t.Errorf("unanswered call status = %q", status)
			}
		}
	}
	if len(inputs) != 2 || []rune(inputs[0])[0] != 'é' || len([]rune(inputs[0])) > 120 || strings.Contains(inputs[0], "\n") || inputs[1] != "go vet ./..." {
		t.Errorf("summary inputs = %q, want the first line cut to 120 runes, then go vet", inputs)
	}

	w = doAPI(t, srv, http.MethodGet, apiPathSessionsSummary+"?scope=all&session=s-story", nil)
	var withChild struct {
		Children []struct {
			Steps []json.RawMessage `json:"steps"`
		} `json:"children"`
		SourceFiles []string `json:"source_files"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &withChild); err != nil || len(withChild.Children) != 1 || len(withChild.Children[0].Steps) != 1 || len(withChild.SourceFiles) != 2 {
		t.Errorf("s-story summary children = %+v source_files %v (%v), want its sub-agent's one step and two logs", withChild.Children, withChild.SourceFiles, err)
	}

	for _, q := range []string{"?session=nope", "?scope=all&session=nope"} {
		if w := doAPI(t, srv, http.MethodGet, apiPathSessionsSummary+q, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", q, w.Code)
		}
	}
	if w := doAPI(t, srv, http.MethodGet, apiPathSessionsSummary, nil); w.Code != http.StatusBadRequest {
		t.Errorf("no session: status %d, want 400", w.Code)
	}
}

// TestStart_WarmsEachGenerationsLayout: a running server builds each new
// generation's session layout without a request asking for it.
func TestStart_WarmsEachGenerationsLayout(t *testing.T) {
	dir := t.TempDir()
	_, b := twoSessionCorpus(t, dir)
	srv := NewServer(NewCache(dir), Options{Prices: loadPricesForTest(t), PollInterval: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.Start(ctx)

	waitLayout := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for srv.layoutGeneration() != srv.cache.Snapshot().Generation || srv.layoutGeneration() == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("layout generation %d, snapshot %d", srv.layoutGeneration(), srv.cache.Snapshot().Generation)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitLayout()
	gen := srv.cache.Snapshot().Generation
	f, err := os.OpenFile(b, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(mkAssistantLineWithCWD("s-b", "b9", "b2", "/p/s-b", "claude-opus-4-7", 1, 1, time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)) + "\n")
	_ = f.Close()
	deadline := time.Now().Add(5 * time.Second)
	for srv.cache.Snapshot().Generation == gen {
		if time.Now().After(deadline) {
			t.Fatal("the poller never saw the append")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitLayout()
}
