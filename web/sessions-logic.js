// Pure, DOM-free logic for the Sessions view: classifying a session's
// origin, parsing the #sessions/{tab}/{anchor} route, and filtering the
// session list by the active tab. Kept separate from view-sessions.js so
// it's unit-testable under `node --test` (jstest/sessions.test.js) without
// a DOM, mirroring how format.js holds the view layer's pure helpers.

// SESSION_TABS are the Sessions sub-tabs, in display order. 'all' shows
// every session; 'interactive' and 'sdk' partition by entrypoint.
export const SESSION_TABS = ['all', 'interactive', 'sdk'];

// classifyEntrypoint maps a raw JSONL entrypoint to one of two buckets.
// Headless/SDK runs report "sdk-cli" (or other "sdk*" origins); everything
// else — interactive "cli", editors, or an unknown/missing value — is
// "interactive". Defaulting unknown to interactive keeps the SDK tab a
// precise, opt-in subset rather than a catch-all.
export function classifyEntrypoint(ep) {
  return typeof ep === 'string' && ep.toLowerCase().startsWith('sdk')
    ? 'sdk'
    : 'interactive';
}

// splitSessionsRoute parses route.sub into { tab, anchor }. The first
// segment is treated as a tab only when it's a known tab name; otherwise
// the whole sub is an anchor under the default 'all' tab — preserving the
// legacy deep-link contract (#sessions/session-{id} and #sessions/{id}).
export function splitSessionsRoute(sub) {
  if (!sub) return { tab: 'all', anchor: '' };
  const slash = sub.indexOf('/');
  const first = slash === -1 ? sub : sub.slice(0, slash);
  if (SESSION_TABS.includes(first)) {
    return { tab: first, anchor: slash === -1 ? '' : sub.slice(slash + 1) };
  }
  return { tab: 'all', anchor: sub };
}

// filterSessionsByTab returns the sessions visible under the given tab.
// 'all' (and any unrecognized tab, defensively) returns the list unchanged;
// 'sdk'/'interactive' keep only sessions whose entrypoint classifies to it.
export function filterSessionsByTab(sessions, tab) {
  if (tab !== 'sdk' && tab !== 'interactive') return sessions;
  return sessions.filter(s => classifyEntrypoint(s.entrypoint) === tab);
}
