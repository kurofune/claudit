package serve

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/kurofune/claudit/internal/agentflow"
	"github.com/kurofune/claudit/internal/parse"
	"github.com/kurofune/claudit/internal/parse/codex"
	"github.com/kurofune/claudit/internal/provider"
)

// agentActiveWindow is how recently a session must have acted (relative to
// wall-clock now) for the session index to mark it active. Sized
// comfortably above the serve poll interval so a live session doesn't
// flicker inactive between polls.
const agentActiveWindow = 60 * time.Second

// agentSnippetRunes caps each tool call's input and output in a story page.
// Over the real corpus the 2000-rune parse snippets dominated the payload;
// the story clips a snippet when it renders one, and its "show full" reads
// the untruncated text from /agents/full.
const agentSnippetRunes = 500

// handleAPIAgentsFull serves /_claudit/api/agents/full — the story's "show
// full" action. Two lookup modes, exactly one of which must be given:
//
//	?session=&tool=<tool_use id>  → UNTRUNCATED input/output for one tool_use
//	?session=&turn=<turn uuid>    → UNTRUNCATED thinking/text for one turn
//
// Both read back from the session log on disk (a story page caps
// thinking/text to 2000-rune snippets and tool input/output to
// agentSnippetRunes to stay lean); ?tool= reads it with the parser of the
// provider that wrote the session.
//
// Serve-mode only: the static HTML report inlines no disk and never calls
// this — its story falls back to the snippet. The source file is resolved
// from the trusted snapshot (matched by session + tool_use id / turn uuid),
// never from a user-supplied path, so there is no path-traversal surface.
func (s *Server) handleAPIAgentsFull(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	session := v.Get("session")
	toolID := v.Get("tool")
	turnUUID := v.Get("turn")
	if session == "" || (toolID == "") == (turnUUID == "") {
		http.Error(w, "need session plus exactly one of tool or turn", http.StatusBadRequest)
		return
	}
	// Parse the query only to 400 on a malformed filter, matching the sibling
	// endpoints, even though session/tool drive the lookup.
	if _, err := parseQuery(v, time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	snap := s.cache.Snapshot()
	if snap == nil {
		http.NotFound(w, r)
		return
	}

	if turnUUID != "" {
		s.serveAgentsFullTurn(w, r, snap, session, turnUUID)
		return
	}

	// Resolve the on-disk source file from the snapshot by matching the
	// tool_use id within the named session, its sub-agent threads included.
	// Trusted input only.
	sourceFile, prov := "", ""
	in := agentflow.InSession(snap, session)
	for i := range snap.Turns {
		t := &snap.Turns[i]
		if !in(t.SessionID) {
			continue
		}
		for _, u := range t.ToolUses {
			if u.ID == toolID {
				sourceFile, prov = t.SourceFile, t.Provider
				break
			}
		}
		if sourceFile != "" {
			break
		}
	}
	if sourceFile == "" {
		http.NotFound(w, r)
		return
	}

	f, err := provider.OpenLog(sourceFile)
	if err != nil {
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	findDetail := parse.FindToolUseDetail
	if prov == parse.ProviderCodex {
		findDetail = codex.FindToolUseDetail
	}
	detail, ok := findDetail(f, toolID)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := json.NewEncoder(w).Encode(detail); err != nil {
		s.reqLogger(r.Context()).Error("serve: encode agents/full failed", "err", err)
	}
}

// serveAgentsFullTurn answers /agents/full's ?turn= mode: the untruncated
// thinking/text of the coalesced turn whose first-line uuid == turnUUID,
// within the named session or one of its sub-agent threads, read with the
// parser of the provider that wrote it. Mirrors the ?tool= mode's
// resolution.
func (s *Server) serveAgentsFullTurn(w http.ResponseWriter, r *http.Request, snap *Snapshot, session, turnUUID string) {
	// Resolve the on-disk source file from the trusted snapshot.
	src := agentflow.LogSource{}
	in := agentflow.InSession(snap, session)
	for i := range snap.Turns {
		t := &snap.Turns[i]
		if t.UUID == turnUUID && in(t.SessionID) {
			src = agentflow.LogSource{File: t.SourceFile, Provider: t.Provider}
			break
		}
	}
	if src.File == "" {
		http.NotFound(w, r)
		return
	}

	f, err := provider.OpenLog(src.File)
	if err != nil {
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	var detail parse.TurnTextDetail
	var ok bool
	if src.Provider == parse.ProviderCodex {
		detail, ok = codex.FindTurnText(f, src.File, turnUUID)
	} else {
		detail, ok = parse.FindTurnText(f, turnUUID)
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := json.NewEncoder(w).Encode(detail); err != nil {
		s.reqLogger(r.Context()).Error("serve: encode agents/full turn failed", "err", err)
	}
}

// readLogText reads the whole text want names back from one session log
// with the parser of the provider that wrote it; a log that cannot be
// opened yields nothing, so its snippets stand.
func readLogText(src agentflow.LogSource, want parse.TextRequest) parse.FullTexts {
	f, err := provider.OpenLog(src.File)
	if err != nil {
		return parse.NewFullTexts()
	}
	defer func() { _ = f.Close() }()
	if src.Provider == parse.ProviderCodex {
		return codex.FindTexts(f, src.File, want)
	}
	return parse.FindTexts(f, want)
}
