// @ts-check
// The Sessions list's DOM: the search box, filter chips and sort control,
// the pinned "Active in the last minute" row, the counted top sentence, the
// Worth opening rows and the prompt-family groups.
// What to draw comes from sessions-list-logic.js listView; this module owns
// the markup, the keyboard model and keeping focus, scroll and every
// unchanged row's element across a repaint.

import { escHtml, escHtmlCode, fmtMoney } from './format.js';
import { sessionHash } from './router.js';
import { providerLabel, API_EQUIVALENT_TIP } from './provider-filter.js';
import { sessionListSkeleton } from './skeleton.js';
import { fmtDuration } from './session-story-logic.js';
import { listView, rowTitle, elapsedText, agoText, windowText, FILTERS, SORTS, failureBadge, turnsText } from './sessions-list-logic.js';

/** @import { SessionIndexRow } from './api-types.js' */
/** @import { WorthRow, ViewGroup } from './sessions-list-logic.js' */

/**
 * @typedef {Object} ListUI
 * @property {string} query
 * @property {string} filter      'all' | 'active' | 'failed'
 * @property {string} sort        'newest' | 'cost' | 'duration'
 * @property {Set<string>} expanded  family keys showing every row
 */

/** @param {string} s */
const cssEsc = s => String(s).replace(/["\\]/g, '\\$&');

// listShellHTML is the list's frame: the toolbar the user types into
// stays put across repaints; only the chips, sorts and body are redrawn.
/** @param {ListUI} ui */
export function listShellHTML(ui) {
  return `<div class="sessions-list">
    <div class="sl-toolbar">
      <input type="search" class="sl-search" placeholder="Search prompts, outcomes, projects"
             aria-label="Search sessions" value="${escHtml(ui.query)}" data-focus-key="search">
      <div class="sl-filters" role="group" aria-label="Show sessions"></div>
      <div class="sl-sorts" role="group" aria-label="Order sessions within each family"></div>
    </div>
    <div class="sl-status"></div>
    <div class="sl-body"></div>
  </div>`;
}

/** @param {SessionIndexRow} r */
function providerChip(r) {
  const p = r.provider || 'claude-code';
  return `<span class="s-entry sl-provider s-provider-${escHtml(p)}">${escHtml(providerLabel(p))}</span>`;
}

const API_EQ = `<span class="api-eq" tabindex="0" data-tooltip="${escHtml(API_EQUIVALENT_TIP)}">API-equivalent</span>`;

// withApiEq escapes text and draws its "API-equivalent" label with the
// tooltip saying what kind of dollars these are.
/** @param {string} text */
const withApiEq = text => escHtml(text).replace('API-equivalent', API_EQ);

/** @param {SessionIndexRow} r */
function outcomeHTML(r) {
  return `<span class="sl-outcome${r.outcome ? '' : ' is-empty'}">Last message: ${escHtml(r.outcome || '(no agent message)')}</span>`;
}

/** @param {SessionIndexRow} r */
function mainHTML(r) {
  return `<span class="sl-main">
        <span class="sl-label">${escHtml(rowTitle(r))}</span>
        ${outcomeHTML(r)}
      </span>`;
}

/** @param {SessionIndexRow} r */
function rowHTML(r) {
  const id = r.session_id;
  const badge = failureBadge(r);
  const failed = badge ? `<span class="sl-failed">${escHtml(badge)}</span>` : '';
  return `<a class="sl-row" href="${escHtml(sessionHash(id))}" data-session="${escHtml(id)}" data-focus-key="row:${escHtml(id)}">
      ${mainHTML(r)}
      <span class="sl-meta">
        ${providerChip(r)}
        <span class="sl-cost">${escHtml(fmtMoney(r.cost_usd || 0))}</span>
        <span class="sl-turns">${escHtml(turnsText(r))}</span>
        <span class="sl-dur">${escHtml(fmtDuration(r.duration_ms || 0))}</span>
        ${failed}
      </span>
    </a>`;
}

/** @param {SessionIndexRow} r @param {number} now */
function activeRowHTML(r, now) {
  const id = r.session_id;
  return `<a class="sl-row sl-active-row" href="${escHtml(sessionHash(id))}" data-session="${escHtml(id)}" data-focus-key="active:${escHtml(id)}">
      ${mainHTML(r)}
      <span class="sl-meta"><span class="sl-elapsed" title="Time since the session started">${escHtml(elapsedText(r.started_at, now))}</span></span>
    </a>`;
}

// worthRowHTML is one Worth opening row: the session's title, why it is
// listed, its cost and last activity; a failing-check row opens on that
// check's last run, a failed-edits row on its first unfixed failure.
/** @param {WorthRow} w @param {number} now */
function worthRowHTML(w, now) {
  const r = w.row, id = r.session_id;
  return `<a class="sl-row sl-worth-row" href="${escHtml(sessionHash(id, w.turn))}" data-session="${escHtml(id)}" data-focus-key="worth:${escHtml(id)}">
      <span class="sl-main">
        <span class="sl-label">${escHtml(rowTitle(r))}</span>
        <span class="sl-reason">${escHtmlCode(w.reason, w.check)}</span>
      </span>
      <span class="sl-meta">
        <span class="sl-cost">${escHtml(fmtMoney(r.cost_usd || 0))}</span>
        <span class="sl-when">${escHtml(agoText(r.last_activity, now))}</span>
      </span>
    </a>`;
}

/** @param {ViewGroup} g @param {Draw} draw */
function groupHTML(g, draw) {
  return `<section class="sl-group" data-family="${escHtml(g.key)}">
        <h2 class="sl-group-head"><span class="sl-family">${escHtml(g.label)}</span>
          <span class="sl-stats">${withApiEq(g.stats.text)}</span></h2>
        <div class="sl-rows">${g.rows.map(r => draw(`row:${r.session_id}`, rowHTML(r))).join('')}</div>
        ${g.hidden ? `<button type="button" class="sl-more" data-expand="${escHtml(g.key)}"
            data-focus-key="more:${escHtml(g.key)}">Show all ${escHtml(String(g.count))}</button>` : ''}
      </section>`;
}

// Draw emits a row's markup under its focus key, or a placeholder for the
// row already on screen with that markup.
/** @typedef {(key: string, html: string) => string} Draw */

// drawn is, per list, each row on screen by its focus key: the markup it
// was drawn from and its element. A repaint puts back the element of every
// row whose markup is unchanged, so only changed rows are rebuilt and the
// rest keep their elements, hover and any state the browser holds on them.
/** @type {WeakMap<Element, Map<string, {html: string, el: Element}>>} */
const drawn = new WeakMap();

// FIRST_GROUPS family groups are drawn in the same task as the rest of the
// list, enough to fill several screens; the others follow LATER_GROUPS at a
// time, each batch after the browser has painted. The whole corpus holds
// over a thousand families, and building them all before the first paint
// held the list back for seconds.
export const FIRST_GROUPS = 40;
const LATER_GROUPS = 200;

// painting is, per list, the token of the render whose later batches may
// still append; a newer render replaces it, so stale batches stop.
/** @type {WeakMap<Element, object>} */
const painting = new WeakMap();

/** @param {() => void} fn */
const afterPaint = fn => {
  if (typeof requestAnimationFrame === 'function') requestAnimationFrame(() => setTimeout(fn, 0));
  else setTimeout(fn, 0);
};

/** @param {Element} body @param {string} html */
function appendHTML(body, html) {
  const tmp = body.ownerDocument.createElement('div');
  tmp.innerHTML = html;
  for (const c of [...tmp.childNodes]) body.appendChild(c);
}

/**
 * @param {Element} root  the .sessions-list element
 * @param {SessionIndexRow[]} rows
 * @param {ListUI} ui
 * @param {{now?: number, search?: string}} [opts]
 */
export function renderList(root, rows, ui, { now = Date.now(), search = '' } = {}) {
  const v = listView(rows, { ...ui, window: windowText(search) });
  const filters = root.querySelector('.sl-filters');
  const sorts = root.querySelector('.sl-sorts');
  const body = root.querySelector('.sl-body');
  if (!filters || !sorts || !body) return;
  const token = {};
  painting.set(root, token);
  const keep = captureFocus(root);
  const scrollY = typeof window.scrollY === 'number' ? window.scrollY : 0;
  const before = drawn.get(root) || new Map();
  /** @type {Map<string, string>} */
  const marks = new Map();
  /** @type {Draw} */
  const draw = (key, html) => {
    marks.set(key, html);
    const prev = before.get(key);
    return prev && prev.html === html ? `<i data-keep="${escHtml(key)}"></i>` : html;
  };

  filters.innerHTML = FILTERS.map(f => `<button type="button" class="sl-chip" data-filter="${f.id}"
      data-focus-key="filter:${f.id}" aria-pressed="${ui.filter === f.id}">${escHtml(f.label)}
      <span class="sl-chip-count">${escHtml(String(v.counts[f.id]))}</span></button>`).join('');
  sorts.innerHTML = `<span class="sl-sort-label" aria-hidden="true">Sort</span>` + SORTS.map(s => `<button type="button"
      class="sl-sort" data-sort="${s.id}" data-focus-key="sort:${s.id}" aria-pressed="${ui.sort === s.id}">${escHtml(s.label)}</button>`).join('');

  /** @type {string[]} */
  let later = [];
  if (v.empty === 'none') {
    body.innerHTML = `<p class="empty-state sl-empty" role="status">No sessions in this window (${escHtml(windowText(search))})</p>`;
  } else {
    const active = v.active.length
      ? `<section class="sl-active" aria-label="Active in the last minute">
          <h2 class="sl-active-title">Active in the last minute · ${escHtml(String(v.active.length))}</h2>
          <div class="sl-rows">${v.active.map(r => draw(`active:${r.session_id}`, activeRowHTML(r, now))).join('')}</div>
        </section>`
      : '';
    const sentence = `<p class="sl-sentence">${withApiEq(v.sentence)}</p>`;
    const worth = v.worth.length
      ? `<section class="sl-worth" aria-label="Worth opening">
          <h2 class="sl-worth-title">Worth opening</h2>
          <div class="sl-rows">${v.worth.map(w => draw(`worth:${w.row.session_id}`, worthRowHTML(w, now))).join('')}</div>
        </section>`
      : '';
    const groups = v.empty ? [] : v.groups.map(g => groupHTML(g, draw));
    later = groups.slice(FIRST_GROUPS);
    body.innerHTML = active + sentence + (v.empty
      ? `<p class="empty-state sl-empty" role="status">${escHtml(v.empty)}</p>`
      : worth + groups.slice(0, FIRST_GROUPS).join(''));
  }

  const putBack = () => {
    for (const ph of body.querySelectorAll('[data-keep]')) {
      const prev = before.get(ph.getAttribute('data-keep') || '');
      if (prev && ph.parentNode) {
        ph.parentNode.insertBefore(prev.el, ph);
        ph.parentNode.removeChild(ph);
      }
    }
  };
  // Each batch puts back its kept rows, then focus and scroll once the
  // element and the height they need are there; a scroll the reader made
  // meanwhile is left alone.
  let focused = false;
  let lastY = 0;
  const settle = () => {
    putBack();
    if (!focused) focused = restoreFocus(root, keep);
    if (typeof window.scrollTo === 'function' && window.scrollY !== scrollY && window.scrollY === lastY) window.scrollTo(0, scrollY);
    lastY = window.scrollY;
  };
  const finish = () => {
    /** @type {Map<string, {html: string, el: Element}>} */
    const after = new Map();
    for (const el of body.querySelectorAll('.sl-row')) {
      const key = el.getAttribute('data-focus-key') || '';
      const html = marks.get(key);
      if (html !== undefined) after.set(key, { html, el });
    }
    drawn.set(root, after);
  };
  lastY = typeof window.scrollY === 'number' ? window.scrollY : 0;
  settle();
  if (!later.length) {
    finish();
    return;
  }
  const step = () => {
    if (painting.get(root) !== token) return;
    appendHTML(body, later.splice(0, LATER_GROUPS).join(''));
    settle();
    if (later.length) afterPaint(step);
    else finish();
  };
  afterPaint(step);
}

// renderListLoading paints the skeleton while the index is in flight.
/** @param {Element} root */
export function renderListLoading(root) {
  const body = root.querySelector('.sl-body');
  if (body) body.innerHTML = `<div aria-busy="true" aria-label="Loading sessions">${sessionListSkeleton(8)}</div>`;
}

/** @param {any} err */
function failureReason(err) {
  return err && err.status
    ? `HTTP ${err.status}${err.statusText ? ' ' + err.statusText : ''}`
    : `the server did not answer (${(err && err.message) || 'network error'})`;
}

// renderListError paints the failed fetch with its HTTP status and a Retry.
/** @param {Element} root @param {any} err */
export function renderListError(root, err) {
  const body = root.querySelector('.sl-body');
  if (!body) return;
  body.innerHTML = `<div class="warning-card sl-error" role="alert"><strong class="danger">Could not load sessions:</strong>
    ${escHtml(failureReason(err))} <button type="button" class="retry" data-retry data-focus-key="retry">Retry</button></div>`;
}

// renderRefreshError says a refresh failed above the rows already on
// screen, which stay as they were, with a Retry.
/** @param {Element} root @param {any} err */
export function renderRefreshError(root, err) {
  const status = root.querySelector('.sl-status');
  if (!status) return;
  status.innerHTML = `<div class="warning-card sl-error sl-refresh-error" role="alert"><strong class="danger">Could not refresh sessions:</strong>
    ${escHtml(failureReason(err))}; showing the list as last loaded.
    <button type="button" class="retry" data-retry data-focus-key="retry">Retry</button></div>`;
}

/** @param {Element} root */
export function clearRefreshError(root) {
  const status = root.querySelector('.sl-status');
  if (status && status.innerHTML) status.innerHTML = '';
}

/** @param {Element} root */
function captureFocus(root) {
  const a = /** @type {Element|null} */ (document.activeElement);
  if (!a || !root.contains(a) || !a.getAttribute) return '';
  return a.getAttribute('data-focus-key') || '';
}

// restoreFocus focuses the element under key again and says whether
// there is nothing left to restore: no key, the search box (which keeps its
// own focus), or the element found.
/** @param {Element} root @param {string} key */
function restoreFocus(root, key) {
  if (!key || key === 'search') return true;
  const el = /** @type {HTMLElement|null} */ (root.querySelector(`[data-focus-key="${cssEsc(key)}"]`));
  if (!el) return false;
  if (document.activeElement !== el) el.focus({ preventScroll: true });
  return true;
}

/** @param {Element} root */
const rowsOf = root => /** @type {HTMLElement[]} */ (Array.from(root.querySelectorAll('.sl-body .sl-row')));

// openRow opens the session (or turn) a row links to.
/** @param {Element} row */
function openRow(row) {
  window.location.hash = row.getAttribute('href') || sessionHash(row.getAttribute('data-session') || '');
}

// wireList binds the list's controls once: typing searches, a chip picks
// the filter, a sort button the order, "Show all N" opens a family, a row
// (click or Enter) opens its session or turn, and ArrowDown/ArrowUp walk
// the Active, Worth opening and family rows across section boundaries. onChange repaints; onRetry refetches.
/**
 * @param {Element} root
 * @param {ListUI} ui
 * @param {{onChange: () => void, onRetry: () => void}} hooks
 */
export function wireList(root, ui, { onChange, onRetry }) {
  root.addEventListener('input', ev => {
    const t = /** @type {HTMLInputElement} */ (ev.target);
    if (!t || !t.matches || !t.matches('.sl-search')) return;
    ui.query = t.value;
    onChange();
  });
  root.addEventListener('click', ev => {
    const t = /** @type {Element} */ (ev.target);
    if (!t || !t.closest) return;
    const me = /** @type {MouseEvent} */ (ev);
    const row = t.closest('.sl-row');
    if (row) {
      if (me.button || me.metaKey || me.ctrlKey || me.shiftKey || me.altKey) return;
      ev.preventDefault();
      openRow(row);
      return;
    }
    const chip = t.closest('[data-filter]');
    if (chip) { ui.filter = chip.getAttribute('data-filter') || 'all'; onChange(); return; }
    const sort = t.closest('[data-sort]');
    if (sort) { ui.sort = sort.getAttribute('data-sort') || 'newest'; onChange(); return; }
    const more = t.closest('[data-expand]');
    if (more) {
      const key = more.getAttribute('data-expand') || '';
      ui.expanded.add(key);
      // The button goes away; land focus on the first row it revealed.
      const shown = new Set(rowsOf(root).filter(r => r.closest('.sl-group') === more.closest('.sl-group'))
        .map(r => r.getAttribute('data-session')));
      onChange();
      const group = root.querySelector(`.sl-group[data-family="${cssEsc(key)}"]`);
      const next = group ? /** @type {HTMLElement[]} */ (Array.from(group.querySelectorAll('.sl-row')))
        .find(r => !shown.has(r.getAttribute('data-session'))) : null;
      if (next) next.focus();
      return;
    }
    if (t.closest('[data-retry]')) onRetry();
  });
  root.addEventListener('keydown', ev => {
    const t = /** @type {HTMLElement} */ (ev.target);
    if (!t || !t.matches || !t.matches('.sl-row')) return;
    const key = /** @type {KeyboardEvent} */ (ev).key;
    if (key === 'Enter') {
      ev.preventDefault();
      openRow(t);
      return;
    }
    if (key !== 'ArrowDown' && key !== 'ArrowUp') return;
    const rows = rowsOf(root);
    const i = rows.indexOf(t);
    const next = rows[i + (key === 'ArrowDown' ? 1 : -1)];
    ev.preventDefault();
    if (next) next.focus();
  });
}
