package aggregate

import (
	"sort"
	"strings"

	"github.com/kurofune/claudit/internal/parse"
)

// promptKeyMaxRunes is how much of the normalized prompt text we use as
// the bucket key. 120 runes is enough to distinguish meaningfully
// different prompts but short enough to cluster trivially-different
// repeats of the same ask (different file paths, different commit hashes).
const promptKeyMaxRunes = 120

// promptBucketInternal accumulates per-prompt-key data while Add() runs.
// We track distinct invocation UUIDs and session IDs so the renderer can
// distinguish a prompt that was issued once across many sessions from
// one issued many times in a single session.
type promptBucketInternal struct {
	Key       string
	Sample    string
	InvocSet  map[string]struct{} // user-message UUIDs
	SessSet   map[string]struct{}
	TurnCount int
	Tokens
	CostUSD float64
}

// PromptBucket is the renderer-facing per-prompt row. JSON keys use Go
// field names (no tags) to match the convention in the rest of this
// package — the HTML template's JS reads r.Sample / r.CostUSD / etc.
type PromptBucket struct {
	Key         string
	Sample      string // full text of one example
	Invocations int
	Sessions    int
	TurnCount   int
	Tokens
	CostUSD float64
}

// promptIndexEntry resolves one turn UUID to its originating prompt.
type promptIndexEntry struct {
	Key      string
	Sample   string
	UserUUID string // "" for "(no prompt)"
}

// noPromptKey is the bucket key used for assistant turns whose parent
// chain doesn't reach a recognized user message — orphan turns from
// older sessions, or compaction roots.
const noPromptKey = "(no prompt)"

// PromptIndex maps each assistant turn UUID to the originating user
// message's normalized prompt key. Build it once per parse pass; the
// aggregator's Filter narrows what gets counted but the index is a
// pure structural property of the corpus.
type PromptIndex struct {
	turnToEntry map[string]promptIndexEntry
}

// BuildPromptIndex walks parentUuid backwards from every assistant turn
// to find the originating user message. Memoized — sessions with
// hundreds of deep turns don't re-traverse the chain repeatedly.
//
// extraLinks supplies parent edges for non-content lines (system events,
// file-history snapshots, hook artifacts) that sit between an assistant
// turn and its originating user message. Without these the chain
// frequently dies one hop short of the prompt.
func BuildPromptIndex(turns []parse.Turn, msgs []parse.UserMessage, extraLinks ...[]parse.ParentLink) *PromptIndex {
	parent := make(map[string]string, len(turns)+len(msgs))
	userText := make(map[string]string, len(msgs))
	for _, links := range extraLinks {
		for _, l := range links {
			if _, exists := parent[l.UUID]; !exists {
				parent[l.UUID] = l.ParentUUID
			}
		}
	}
	for _, t := range turns {
		parent[t.UUID] = t.ParentUUID
	}
	for _, m := range msgs {
		parent[m.UUID] = m.ParentUUID
		userText[m.UUID] = m.Text
	}

	cache := make(map[string]promptIndexEntry, len(turns)+len(msgs))
	idx := &PromptIndex{turnToEntry: make(map[string]promptIndexEntry, len(turns))}

	// Iterative walk to avoid blowing the stack on pathologically deep
	// chains. visited is per-call; cache is shared across calls so a
	// later turn that hits an already-resolved ancestor terminates
	// immediately.
	resolve := func(start string) promptIndexEntry {
		if start == "" {
			return promptIndexEntry{Key: noPromptKey}
		}
		if v, ok := cache[start]; ok {
			return v
		}
		// Walk up, recording the chain so we can backfill the cache.
		chain := []string{start}
		seen := map[string]struct{}{start: {}}
		var found promptIndexEntry
		cur := start
		for {
			// Direct hit: this UUID is itself a user message.
			if text, ok := userText[cur]; ok {
				found = promptIndexEntry{
					Key:      normalizePromptKey(text),
					Sample:   text,
					UserUUID: cur,
				}
				break
			}
			p, ok := parent[cur]
			if !ok || p == "" {
				found = promptIndexEntry{Key: noPromptKey}
				break
			}
			if v, ok := cache[p]; ok {
				found = v
				break
			}
			if _, loop := seen[p]; loop {
				// Cycle (shouldn't happen, but defend).
				found = promptIndexEntry{Key: noPromptKey}
				break
			}
			seen[p] = struct{}{}
			chain = append(chain, p)
			cur = p
		}
		// Backfill every UUID in the chain so future walks short-circuit.
		for _, u := range chain {
			cache[u] = found
		}
		return found
	}

	for _, t := range turns {
		// Walk starts at the turn's parent — the turn itself isn't a user
		// message, so cache[t.UUID] would otherwise sit at noPromptKey.
		entry := resolve(t.ParentUUID)
		idx.turnToEntry[t.UUID] = entry
		cache[t.UUID] = entry
	}
	return idx
}

// Lookup returns the originating prompt key + sample for a turn. Falls
// back to noPromptKey + "" for unknown UUIDs.
func (p *PromptIndex) Lookup(turnUUID string) promptIndexEntry {
	if e, ok := p.turnToEntry[turnUUID]; ok {
		return e
	}
	return promptIndexEntry{Key: noPromptKey}
}

// normalizePromptKey: lowercase, collapse runs of whitespace to single
// spaces, truncate to promptKeyMaxRunes runes. Built so trivially
// different prompts ("write a test for foo.go" vs "write a test for
// bar.go" — once the differing path falls past the rune cap) cluster.
func normalizePromptKey(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inSpace && b.Len() > 0 {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		b.WriteRune(r)
		inSpace = false
	}
	out := strings.TrimSpace(b.String())
	runes := []rune(out)
	if len(runes) > promptKeyMaxRunes {
		runes = runes[:promptKeyMaxRunes]
	}
	return string(runes)
}

// ByPrompt returns the per-prompt rows sorted by cost descending.
// Empty when no PromptIndex was attached.
func (a *Aggregator) ByPrompt() []PromptBucket {
	out := make([]PromptBucket, 0, len(a.byPrompt))
	for _, p := range a.byPrompt {
		out = append(out, PromptBucket{
			Key:         p.Key,
			Sample:      p.Sample,
			Invocations: len(p.InvocSet),
			Sessions:    len(p.SessSet),
			TurnCount:   p.TurnCount,
			Tokens:      p.Tokens,
			CostUSD:     p.CostUSD,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		return out[i].Key < out[j].Key
	})
	return out
}
