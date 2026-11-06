package aggregate

import (
	"time"

	"github.com/kurofune/claudit/internal/parse"
)

// PromptResolver maps any turn (or intermediate) UUID to the UUID of the user
// prompt that originated its parentUuid chain. A session's story
// (agentflow.BuildSessionStory) uses it to attribute a turn back to the
// prompt that produced it, climbing through non-content lines (system events,
// file-history snapshots) via parse.ParentLink edges.
//
// Build once over a corpus's turns + user messages + parent links; Resolve
// memoizes so repeated lookups over a long timeline stay cheap.
type PromptResolver struct {
	parent   map[string]string
	userText map[string]string
	userTS   map[string]time.Time
	cache    map[string]string
	// more answers for a uuid the resolver's own records lack.
	more PromptChains
}

// PromptChains is a read-only set of parent edges and prompts a
// PromptResolver reads past its own records: a resolver built over one
// session's files climbs on into the rest of the corpus through it.
type PromptChains interface {
	// Parent is uuid's parent edge; ok is false when no record names uuid.
	Parent(uuid string) (parent string, ok bool)
	// Prompt is the user prompt with that uuid; ok is false when none.
	Prompt(uuid string) (parse.UserMessage, bool)
}

// WithFallback makes r look up every uuid its own records lack in more,
// and returns r. Its own edges and prompts win where both name a uuid.
func (r *PromptResolver) WithFallback(more PromptChains) *PromptResolver {
	r.more = more
	return r
}

// step reports whether cur is a prompt and, when not, its parent edge.
func (r *PromptResolver) step(cur string) (prompt bool, parent string, ok bool) {
	if _, ok := r.userText[cur]; ok {
		return true, "", false
	}
	if p, ok := r.parent[cur]; ok {
		return false, p, true
	}
	if r.more == nil {
		return false, "", false
	}
	if m, ok := r.more.Prompt(cur); ok {
		r.userText[cur], r.userTS[cur] = m.Text, m.Timestamp
		return true, "", false
	}
	p, ok := r.more.Parent(cur)
	return false, p, ok
}

// NewPromptResolver indexes the parent edges and user prompts so Resolve can
// walk any UUID back to its originating prompt. parentLinks supplies extra
// edges (non-content lines) that sit between an assistant turn and its prompt;
// turns and msgs supply their own parent edges plus the prompt text/timestamp.
func NewPromptResolver(turns []parse.Turn, msgs []parse.UserMessage, parentLinks []parse.ParentLink) *PromptResolver {
	r := &PromptResolver{
		parent:   make(map[string]string, len(turns)+len(msgs)+len(parentLinks)),
		userText: make(map[string]string, len(msgs)),
		userTS:   make(map[string]time.Time, len(msgs)),
		cache:    make(map[string]string, len(turns)),
	}
	for _, l := range parentLinks {
		if _, exists := r.parent[l.UUID]; !exists {
			r.parent[l.UUID] = l.ParentUUID
		}
	}
	for _, t := range turns {
		r.parent[t.UUID] = t.ParentUUID
	}
	for _, m := range msgs {
		r.parent[m.UUID] = m.ParentUUID
		r.userText[m.UUID] = m.Text
		r.userTS[m.UUID] = m.Timestamp
	}
	return r
}

// Resolve walks from start up the parentUuid chain to the originating user
// prompt's UUID, returning "" if the chain reaches no recognized prompt.
// Results (including the dead-end "") are memoized for every UUID on the path.
func (r *PromptResolver) Resolve(start string) string {
	if start == "" {
		return ""
	}
	if v, ok := r.cache[start]; ok {
		return v
	}
	chain := []string{start}
	seen := map[string]struct{}{start: {}}
	var found string
	cur := start
	for {
		prompt, p, ok := r.step(cur)
		if prompt {
			found = cur
			break
		}
		if !ok || p == "" {
			break
		}
		if v, ok := r.cache[p]; ok {
			found = v
			break
		}
		if _, loop := seen[p]; loop {
			break
		}
		seen[p] = struct{}{}
		chain = append(chain, p)
		cur = p
	}
	for _, u := range chain {
		r.cache[u] = found
	}
	return found
}

// Text returns the raw (untruncated) prompt text for a resolved
// prompt UUID, or "" if the UUID is not a known prompt.
func (r *PromptResolver) Text(uuid string) string { return r.userText[uuid] }

// Timestamp returns the originating prompt's timestamp, or the zero time if
// the UUID is not a known prompt.
func (r *PromptResolver) Timestamp(uuid string) time.Time { return r.userTS[uuid] }
