// @ts-check
// The Sessions list's pure half: grouping index rows by prompt family,
// search, the three filters, the three sorts, the pinned Active rows and
// each group's 20-row page. web/sessions-list.js renders what listView
// returns.

import { fmtDuration } from './session-story-logic.js';

/** @import { SessionIndexRow } from './api-types.js' */

export const GROUP_PAGE = 20;

/** @typedef {'all'|'active'|'failed'} ListFilter */
/** @typedef {'newest'|'cost'|'duration'} ListSort */

export const FILTERS = /** @type {const} */ ([
  { id: 'all', label: 'All' },
  { id: 'active', label: 'Active in the last minute' },
  { id: 'failed', label: 'Had unresolved failures' },
]);

export const SORTS = /** @type {const} */ ([
  { id: 'newest', label: 'Newest' },
  { id: 'cost', label: 'Cost' },
  { id: 'duration', label: 'Duration' },
]);

/**
 * @typedef {Object} FamilyGroup
 * @property {string} key       the family_key the rows share
 * @property {string} label     the key as shown, placeholders drawn as "…"
 * @property {SessionIndexRow[]} rows
 * @property {string} newest    the newest member's last_activity
 */

// familyLabel is a family key as the header shows it: each "_" the key
// blanked a token to is drawn as "…".
/** @param {string} key */
export const familyLabel = key => String(key || '').replace(/_/g, '…');

// rowLabel is what tells a row apart inside its family: the blanked
// tokens, or the prompt's first line when nothing was blanked.
/** @param {SessionIndexRow} r */
export const rowLabel = r => r.remainder || r.title || '';

const ts = (/** @type {string} */ s) => { const t = Date.parse(s || ''); return isNaN(t) ? 0 : t; };

// groupByFamily buckets rows by exact family_key, keeping each bucket's
// rows in input order, and orders the buckets by their newest member.
/** @param {SessionIndexRow[]} rows @returns {FamilyGroup[]} */
export function groupByFamily(rows) {
  /** @type {Map<string, FamilyGroup>} */
  const byKey = new Map();
  for (const r of rows) {
    const key = r.family_key || '';
    let g = byKey.get(key);
    if (!g) { g = { key, label: familyLabel(key), rows: [], newest: '' }; byKey.set(key, g); }
    g.rows.push(r);
    if (ts(r.last_activity) > ts(g.newest)) g.newest = r.last_activity;
  }
  return [...byKey.values()].sort((a, b) => ts(b.newest) - ts(a.newest));
}

/** @param {SessionIndexRow[]} rows @param {ListFilter|string} filter */
export function applyFilter(rows, filter) {
  if (filter === 'active') return rows.filter(r => r.active === true);
  if (filter === 'failed') return rows.filter(r => (r.failed || 0) > 0);
  return rows;
}

/** @param {SessionIndexRow[]} rows */
export function filterCounts(rows) {
  return {
    all: rows.length,
    active: applyFilter(rows, 'active').length,
    failed: applyFilter(rows, 'failed').length,
  };
}

/** @type {Record<string, (r: SessionIndexRow) => number>} */
const SORT_KEY = {
  newest: r => ts(r.last_activity),
  cost: r => r.cost_usd || 0,
  duration: r => r.duration_ms || 0,
};

// applySort returns rows ordered by the sort's field, descending; ties fall
// back to the newest last_activity. The input is left as it was.
/** @param {SessionIndexRow[]} rows @param {ListSort|string} sort */
export function applySort(rows, sort) {
  const key = SORT_KEY[sort] || SORT_KEY.newest;
  return [...rows].sort((a, b) => (key(b) - key(a)) || (ts(b.last_activity) - ts(a.last_activity)));
}

// searchRows keeps the rows whose family key, remainder, title, outcome or
// project holds query, case-insensitively. A blank query keeps every row.
/** @param {SessionIndexRow[]} rows @param {string} query */
export function searchRows(rows, query) {
  const q = String(query || '').trim().toLowerCase();
  if (!q) return rows;
  return rows.filter(r => [r.family_key, r.remainder, r.title, r.outcome, r.project]
    .some(f => String(f || '').toLowerCase().includes(q)));
}

// activeRows are the sessions the index marks active, newest first.
/** @param {SessionIndexRow[]} rows */
export function activeRows(rows) {
  return applySort(rows.filter(r => r.active === true), 'newest');
}

// pageGroup is the slice of a group's rows the list draws: the first
// GROUP_PAGE until the group is expanded, and how many stay behind
// "Show N more".
/** @param {SessionIndexRow[]} rows @param {boolean} expanded */
export function pageGroup(rows, expanded) {
  if (expanded || rows.length <= GROUP_PAGE) return { visible: rows, hidden: 0 };
  return { visible: rows.slice(0, GROUP_PAGE), hidden: rows.length - GROUP_PAGE };
}

// elapsedText is how long ago a session started, from now (ms).
/** @param {string} startedAt @param {number} now */
export function elapsedText(startedAt, now) {
  const t = Date.parse(startedAt || '');
  if (isNaN(t)) return '';
  return fmtDuration(Math.max(0, now - t));
}

// windowText names the page's date filter for the empty state, matching
// the server's default 7-day window when the URL names none.
/** @param {string} search */
export function windowText(search) {
  const p = new URLSearchParams(search || '');
  const parts = [];
  if (p.get('since')) parts.push(`since ${p.get('since')}`);
  if (p.get('until')) parts.push(`until ${p.get('until')}`);
  return parts.length ? parts.join(', ') : 'the last 7 days';
}

/**
 * @typedef {Object} ViewGroup
 * @property {string} key
 * @property {string} label
 * @property {number} count               matching members
 * @property {SessionIndexRow[]} rows     the members drawn, in sort order
 * @property {number} hidden              members behind "Show N more"
 */

/**
 * @typedef {Object} ListView
 * @property {ViewGroup[]} groups
 * @property {SessionIndexRow[]} active   the pinned Active rows
 * @property {{all: number, active: number, failed: number}} counts
 * @property {''|'none'|'No sessions match'} empty  'none' when the index has no rows
 */

// listView is everything the list draws from the index rows and the
// view's controls: search first, then the chip counts over what matched,
// then the filter, the family groups (newest member first), each group's
// rows in the sort order and paged to GROUP_PAGE unless expanded.
/**
 * @param {SessionIndexRow[]} rows
 * @param {{query?: string, filter?: ListFilter|string, sort?: ListSort|string, expanded?: Set<string>}} opts
 * @returns {ListView}
 */
export function listView(rows, { query = '', filter = 'all', sort = 'newest', expanded = new Set() } = {}) {
  const matched = searchRows(rows, query);
  const counts = filterCounts(matched);
  const shown = applyFilter(matched, filter);
  const groups = groupByFamily(shown).map(g => {
    const sorted = applySort(g.rows, sort);
    const page = pageGroup(sorted, expanded.has(g.key));
    return { key: g.key, label: g.label, count: sorted.length, rows: page.visible, hidden: page.hidden };
  });
  /** @type {ListView['empty']} */
  let empty = '';
  if (rows.length === 0) empty = 'none';
  else if (groups.length === 0) empty = 'No sessions match';
  return { groups, active: empty ? [] : activeRows(matched), counts, empty };
}
