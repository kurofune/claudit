package aggregate

import "github.com/kurofune/claudit/internal/parse"

// ReplaySet identifies the replayed (non-canonical) copies of assistant
// turns produced when a session is resumed or forked. Claude Code writes the
// prior transcript into the new session's file verbatim — same message.id,
// usage, uuid, parentUuid, and timestamp; only sessionId and the file path
// differ (confirmed on real corpora). Those generations were billed once, in
// the original run, so any rollup that attributes cost or tokens per
// session/agent/prompt must count each message.id once or it double-counts.
//
// The headline Aggregator already dedups by message.id; ReplaySet brings the
// per-session and per-agent drill-downs in line so every view reconciles with
// the headline.
type ReplaySet struct {
	// canonical maps message.id -> the source file that keeps the cost, but
	// only for message.ids that appear in more than one file. A message.id
	// absent here occurs in a single file and is never a replay.
	canonical map[string]string
}

// BuildReplaySet scans turns and records, for every message.id that appears
// in more than one source file, the canonical file that keeps the cost. The
// canonical file is the lexicographically smallest path — an arbitrary but
// stable choice, so attribution is deterministic regardless of the order
// concurrent loading produced the turns in. Because every copy carries
// identical usage, totals reconcile no matter which copy is chosen; the
// deterministic pick only fixes *which* session is credited. Turns with an
// empty message.id (legacy single-line transcripts) can't be keyed and are
// never treated as replays.
func BuildReplaySet(turns []parse.Turn) ReplaySet {
	files := map[string]map[string]struct{}{} // message.id -> set of source files
	for _, t := range turns {
		if t.MessageID == "" {
			continue
		}
		s := files[t.MessageID]
		if s == nil {
			s = map[string]struct{}{}
			files[t.MessageID] = s
		}
		s[t.SourceFile] = struct{}{}
	}
	canonical := map[string]string{}
	for id, fs := range files {
		if len(fs) < 2 {
			continue // single file — nothing to dedup
		}
		var min string
		first := true
		for f := range fs {
			if first || f < min {
				min, first = f, false
			}
		}
		canonical[id] = min
	}
	return ReplaySet{canonical: canonical}
}

// IsReplay reports whether t is a non-canonical replayed copy whose cost and
// tokens were already counted on the canonical occurrence. Callers skip such
// turns when rolling up cost/tokens so each generation is billed once. A turn
// with an empty message.id, or one whose message.id occurs in a single file,
// is never a replay.
func (r ReplaySet) IsReplay(t parse.Turn) bool {
	if t.MessageID == "" {
		return false
	}
	c, ok := r.canonical[t.MessageID]
	return ok && c != t.SourceFile
}
