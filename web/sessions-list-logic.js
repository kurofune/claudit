// @ts-check
// The Sessions list's pure half: the counted top sentence, the Worth
// opening rows and their reasons, grouping index rows by prompt family with
// each family's stats, search, the three filters, the three sorts, the
// pinned Active rows and each family's three rows most worth opening.
// web/sessions-list.js renders what listView returns. Every sentence here
// is a template over the index row's counts.

import { fmtDuration } from './session-story-logic.js';
import { nearestRank } from './agents-insights-logic.js';
import { fmtMoney, fmtNum } from './format.js';

/** @import { SessionIndexRow } from './api-types.js' */

// GROUP_PAGE is how many rows a collapsed family shows.
export const GROUP_PAGE = 3;
// WORTH_MAX caps the Worth opening block.
export const WORTH_MAX = 8;
// TOP_TENTH_MIN_RUNS is the fewest runs a family needs before its most
// expensive tenth is named.
export const TOP_TENTH_MIN_RUNS = 5;
export const DEFAULT_WINDOW = 'the last 7 days';

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

// rowTitle is a row's first line: its prompt's first line with the
// wrapper stripped (the index row's title), else what the family blanked.
/** @param {SessionIndexRow} r */
export const rowTitle = r => r.title || r.remainder || '(no prompt)';

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

// plural is "1 run" or "N runs".
/** @param {number} n @param {string} one @param {string} [many] */
const plural = (n, one, many = one + 's') => `${fmtNum(n)} ${n === 1 ? one : many}`;

const cost = (/** @type {SessionIndexRow} */ r) => r.cost_usd || 0;
const failedOf = (/** @type {SessionIndexRow} */ r) => r.failed || 0;
const compactionsOf = (/** @type {SessionIndexRow} */ r) => r.compactions || 0;
const isCodex = (/** @type {SessionIndexRow} */ r) => r.provider === 'codex';

// worthRank orders rows most worth opening first: those with unfixed failed
// commands by cost, then the most compacted, then by cost.
/** @param {SessionIndexRow} a @param {SessionIndexRow} b */
export function worthRank(a, b) {
  const fa = failedOf(a) > 0, fb = failedOf(b) > 0;
  if (fa !== fb) return fa ? -1 : 1;
  if (fa) return (cost(b) - cost(a)) || (ts(b.last_activity) - ts(a.last_activity));
  return (compactionsOf(b) - compactionsOf(a)) || (cost(b) - cost(a)) || (ts(b.last_activity) - ts(a.last_activity));
}

// pageGroup is the slice of a family's rows the list draws: until the
// family is expanded, its GROUP_PAGE rows most worth opening, kept in the
// rows' own (sort) order, and how many stay behind "Show all N".
/** @param {SessionIndexRow[]} rows @param {boolean} expanded */
export function pageGroup(rows, expanded) {
  if (expanded || rows.length <= GROUP_PAGE) return { visible: rows, hidden: 0 };
  const keep = new Set([...rows].sort(worthRank).slice(0, GROUP_PAGE));
  return { visible: rows.filter(r => keep.has(r)), hidden: rows.length - GROUP_PAGE };
}

// modelShort names a Claude model by its family ("claude-opus-4-7" →
// "Opus"); any other id is returned unchanged.
/** @param {string} model */
export function modelShort(model) {
  const m = /^claude-(opus|sonnet|haiku|fable)(?:-|$)/.exec(String(model || ''));
  return m ? m[1][0].toUpperCase() + m[1].slice(1) : String(model || '');
}

/**
 * @typedef {Object} FamilyStats
 * @property {number} runs
 * @property {number} total          sum of cost_usd
 * @property {number|null} median    middle cost_usd (mean of the two middles); null for one run
 * @property {[string, number][]} models  modelShort name and run count, most runs first
 * @property {boolean} apiEquivalent the family has a Codex run
 * @property {string} text           "N runs · $total · median $M · 102 Fable, 79 Opus"
 */

// familyStats is a family header's numbers: its runs, total and median
// cost and model mix. The total is called API-equivalent when a Codex run
// is among them; the text carries the label after the total.
/** @param {SessionIndexRow[]} rows @returns {FamilyStats} */
export function familyStats(rows) {
  const costs = rows.map(cost).sort((a, b) => a - b);
  const runs = costs.length;
  const total = costs.reduce((a, b) => a + b, 0);
  const mid = Math.floor(runs / 2);
  const median = runs < 2 ? null : runs % 2 ? costs[mid] : (costs[mid - 1] + costs[mid]) / 2;
  /** @type {Map<string, number>} */
  const counts = new Map();
  for (const r of rows) {
    const m = modelShort(r.model);
    if (m) counts.set(m, (counts.get(m) || 0) + 1);
  }
  const models = /** @type {[string, number][]} */ ([...counts])
    .sort((a, b) => (b[1] - a[1]) || a[0].localeCompare(b[0]));
  const apiEquivalent = rows.some(isCodex);
  const parts = [plural(runs, 'run'), fmtMoney(total) + (apiEquivalent ? ' API-equivalent' : '')];
  if (median != null) parts.push(`median ${fmtMoney(median)}`);
  const mix = runs === 1 ? models.map(m => m[0]).join(', ') : models.map(([m, n]) => `${fmtNum(n)} ${m}`).join(', ');
  if (mix) parts.push(mix);
  return { runs, total, median, models, apiEquivalent, text: parts.join(' · ') };
}

// topSentence is the list's opening line: how many sessions the window
// holds and what they cost, then how many ended with unfixed failed
// commands and what those cost (dropped when none did). The dollars are
// called API-equivalent when a Codex row is among them.
/** @param {SessionIndexRow[]} rows @param {string} window */
export function topSentence(rows, window) {
  const total = rows.reduce((a, r) => a + cost(r), 0);
  const failed = rows.filter(r => failedOf(r) > 0);
  const w = String(window || DEFAULT_WINDOW);
  const where = /^(since|until)\b/.test(w) ? w : `in ${w}`;
  const label = rows.some(isCodex) ? ' API-equivalent' : '';
  let text = `${plural(rows.length, 'session')} ${where}, ${fmtMoney(total)}${label}.`;
  if (failed.length) {
    text += ` ${fmtNum(failed.length)} ended with unfixed failed commands (${fmtMoney(failed.reduce((a, r) => a + cost(r), 0))}).`;
  }
  return text;
}

/**
 * @typedef {Object} WorthRow
 * @property {SessionIndexRow} row
 * @property {string} reason  the literal reason it is listed
 * @property {string} turn    the turn to land on: the first unfixed failure's, or ''
 */

// worthOpening lists at most WORTH_MAX sessions to open first, each once,
// under the first reason it meets: unfixed failed commands (costliest
// first), context compactions (most first), a cost in the top tenth of a
// family of at least TOP_TENTH_MIN_RUNS runs (nearest-rank 90th
// percentile; costliest first), then the window's single most expensive
// session. window names it in that last reason.
/** @param {SessionIndexRow[]} rows @param {string} [window] @returns {WorthRow[]} */
export function worthOpening(rows, window = DEFAULT_WINDOW) {
  /** @type {WorthRow[]} */
  const out = [];
  const taken = new Set();
  /** @param {SessionIndexRow} r @param {string} reason @param {string} [turn] */
  const add = (r, reason, turn = '') => {
    if (out.length >= WORTH_MAX || taken.has(r.session_id)) return;
    taken.add(r.session_id);
    out.push({ row: r, reason, turn });
  };
  const byCost = (/** @type {SessionIndexRow} */ a, /** @type {SessionIndexRow} */ b) =>
    (cost(b) - cost(a)) || (ts(b.last_activity) - ts(a.last_activity));

  for (const r of rows.filter(r => failedOf(r) > 0).sort(byCost)) {
    add(r, `${plural(failedOf(r), 'failed command')} never fixed`, r.first_failed_turn || '');
  }
  for (const r of rows.filter(r => compactionsOf(r) > 0)
    .sort((a, b) => (compactionsOf(b) - compactionsOf(a)) || byCost(a, b))) {
    add(r, `Context compacted ${plural(compactionsOf(r), 'time')}`);
  }
  /** @type {{row: SessionIndexRow, runs: number}[]} */
  const top = [];
  for (const g of groupByFamily(rows)) {
    if (g.rows.length < TOP_TENTH_MIN_RUNS) continue;
    const p90 = nearestRank(g.rows.map(cost), 90);
    for (const r of g.rows) if (cost(r) >= p90) top.push({ row: r, runs: g.rows.length });
  }
  for (const t of top.sort((a, b) => byCost(a.row, b.row))) {
    add(t.row, `In the most expensive 10% of ${fmtNum(t.runs)} runs of this prompt`);
  }
  const costliest = [...rows].sort(byCost)[0];
  if (costliest) {
    const w = String(window || DEFAULT_WINDOW);
    add(costliest, `Most expensive session ${w === DEFAULT_WINDOW ? 'this week' : w}`);
  }
  return out;
}

// elapsedText is how long ago a session started, from now (ms).
/** @param {string} startedAt @param {number} now */
export function elapsedText(startedAt, now) {
  const t = Date.parse(startedAt || '');
  if (isNaN(t)) return '';
  return fmtDuration(Math.max(0, now - t));
}

// agoText is how long before now a session last did anything, to the
// coarsest unit: "just now", "12m ago", "3h ago", "2d ago".
/** @param {string} at @param {number} now */
export function agoText(at, now) {
  const t = Date.parse(at || '');
  if (isNaN(t)) return '';
  const min = Math.floor(Math.max(0, now - t) / 60_000);
  if (min < 1) return 'just now';
  if (min < 60) return `${min}m ago`;
  if (min < 48 * 60) return `${Math.floor(min / 60)}h ago`;
  return `${Math.floor(min / 1440)}d ago`;
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
 * @property {number} hidden              members behind "Show all N"
 * @property {FamilyStats} stats          over every matching member
 */

/**
 * @typedef {Object} ListView
 * @property {ViewGroup[]} groups
 * @property {string} sentence            topSentence over every index row; '' when there are none
 * @property {WorthRow[]} worth           worthOpening over the rows the search and filter keep
 * @property {SessionIndexRow[]} active   the pinned Active rows
 * @property {{all: number, active: number, failed: number}} counts
 * @property {''|'none'|'No sessions match'} empty  'none' when the index has no rows
 */

// listView is everything the list draws from the index rows and the
// view's controls: the top sentence over the whole window, then search,
// the chip counts over what matched, the filter, the Worth opening rows
// over what is left, the family groups (newest member first) with their
// stats, each group's rows in the sort order and collapsed to its
// GROUP_PAGE rows most worth opening unless expanded.
/**
 * @param {SessionIndexRow[]} rows
 * @param {{query?: string, filter?: ListFilter|string, sort?: ListSort|string, expanded?: Set<string>, window?: string}} opts
 * @returns {ListView}
 */
export function listView(rows, { query = '', filter = 'all', sort = 'newest', expanded = new Set(), window = DEFAULT_WINDOW } = {}) {
  const matched = searchRows(rows, query);
  const counts = filterCounts(matched);
  const shown = applyFilter(matched, filter);
  const groups = groupByFamily(shown).map(g => {
    const sorted = applySort(g.rows, sort);
    const page = pageGroup(sorted, expanded.has(g.key));
    return { key: g.key, label: g.label, count: sorted.length, rows: page.visible, hidden: page.hidden, stats: familyStats(sorted) };
  });
  /** @type {ListView['empty']} */
  let empty = '';
  if (rows.length === 0) empty = 'none';
  else if (groups.length === 0) empty = 'No sessions match';
  return {
    groups,
    sentence: rows.length ? topSentence(rows, window) : '',
    worth: empty ? [] : worthOpening(shown, window),
    active: empty ? [] : activeRows(matched),
    counts,
    empty,
  };
}
