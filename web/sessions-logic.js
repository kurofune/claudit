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

// SESSIONS_PAGE_SIZE is how many session cards render per page. The list is
// no longer capped server-side (it ships every session in the time window,
// newest-first); the view pages it instead so the DOM stays small.
export const SESSIONS_PAGE_SIZE = 10;

// pageCount is how many pages of `size` items `total` needs. Zero items →
// zero pages (the empty state replaces the list and pager entirely).
export function pageCount(total, size) {
  if (total <= 0) return 0;
  return Math.ceil(total / size);
}

// clampPage forces a (1-based) page request into the valid range for a list
// of `total` items. Anything below 1 → 1; anything past the last page → the
// last page; an empty list → 1 (page 1 hosts the empty state).
export function clampPage(page, total, size) {
  const pages = pageCount(total, size);
  if (pages <= 0) return 1;
  if (page < 1) return 1;
  if (page > pages) return pages;
  return page;
}

// paginate returns the slice of `list` for the (1-based, clamped) page. An
// out-of-range page clamps rather than returning an empty slice, so a stale
// page index can never blank the view.
export function paginate(list, page, size) {
  const p = clampPage(page, list.length, size);
  const start = (p - 1) * size;
  return list.slice(start, start + size);
}

// pageForIndex returns the 1-based page that holds the 0-based `index`. A
// negative index (e.g. a deep-link target not present in the list) maps to
// page 1 so the caller still lands somewhere valid.
export function pageForIndex(index, size) {
  if (index < 0) return 1;
  return Math.floor(index / size) + 1;
}
