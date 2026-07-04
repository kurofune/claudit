// Tree-lens derivations: the live-reorder pause policy (follow vs frozen)
// and the frozen-order session sort. Split out of agents-logic.js
// (re-exported by the facade).

// orderTreeSessions reorders the natural (newest-first) session list to a
// frozen display order, so a live tick can't reshuffle rows under a user who's
// mid-read. `frozenOrderIds` null ⇒ no freeze, return the list as-is. Otherwise
// emit the sessions whose ids appear in frozenOrderIds first (in that order),
// then append any session not in the frozen list in its natural order — that's
// where sessions that arrived since the freeze land. Frozen ids with no current
// session are skipped.
export function orderTreeSessions(sessions, frozenOrderIds) {
  if (!frozenOrderIds) return sessions;
  const byId = new Map((sessions || []).map(s => [s.session_id || '', s]));
  const seen = new Set();
  const ordered = [];
  for (const id of frozenOrderIds) {
    const s = byId.get(id);
    if (s && !seen.has(id)) { ordered.push(s); seen.add(id); }
  }
  for (const s of sessions || []) {
    const id = s.session_id || '';
    if (!seen.has(id)) { ordered.push(s); seen.add(id); }
  }
  return ordered;
}

// treeFollowMode decides whether the tree should follow the live newest-first
// order ('follow') or hold a frozen order ('frozen') so a live re-render doesn't
// yank rows around while the user reads. It follows when the user is at the top
// of the list, has never scrolled, or has been idle for at least idleMs since
// their last scroll; otherwise it stays frozen.
export function treeFollowMode(lastScrollAtMs, nowMs, atTop, idleMs = 15000) {
  if (atTop) return 'follow';
  if (lastScrollAtMs == null) return 'follow';
  return (nowMs - lastScrollAtMs >= idleMs) ? 'follow' : 'frozen';
}
