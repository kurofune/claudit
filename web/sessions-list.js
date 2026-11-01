// @ts-check
// The Sessions list's DOM: the search box, filter chips and sort control,
// the pinned "Active in the last minute" row and the prompt-family groups.
// What to draw comes from sessions-list-logic.js listView; this module owns
// the markup, the keyboard model and keeping focus and scroll across a
// repaint.

import { escHtml, fmtMoney } from './format.js';
import { sessionHash } from './router.js';
import { providerLabel } from './provider-filter.js';
import { sessionListSkeleton } from './skeleton.js';
import { fmtDuration } from './session-story-logic.js';
import { listView, rowLabel, elapsedText, windowText, FILTERS, SORTS } from './sessions-list-logic.js';

/** @import { SessionIndexRow } from './api-types.js' */

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
    <div class="sl-body"></div>
  </div>`;
}

/** @param {SessionIndexRow} r */
function providerChip(r) {
  const p = r.provider || 'claude-code';
  return `<span class="s-entry sl-provider s-provider-${escHtml(p)}">${escHtml(providerLabel(p))}</span>`;
}

/** @param {SessionIndexRow} r */
function outcomeHTML(r) {
  return r.outcome
    ? `<span class="sl-outcome" title="${escHtml(r.outcome)}">${escHtml(r.outcome)}</span>`
    : '<span class="sl-outcome is-empty">(no agent message)</span>';
}

/** @param {SessionIndexRow} r */
function rowHTML(r) {
  const id = r.session_id;
  const failed = (r.failed || 0) > 0 ? `<span class="sl-failed">${escHtml(String(r.failed))} failed</span>` : '';
  return `<a class="sl-row" href="${escHtml(sessionHash(id))}" data-session="${escHtml(id)}" data-focus-key="row:${escHtml(id)}">
      <span class="sl-main">
        <span class="sl-label">${escHtml(rowLabel(r))}</span>
        ${outcomeHTML(r)}
      </span>
      <span class="sl-meta">
        ${providerChip(r)}
        <span class="sl-cost">${escHtml(fmtMoney(r.cost_usd || 0))}</span>
        <span class="sl-dur">${escHtml(fmtDuration(r.duration_ms || 0))}</span>
        ${failed}
      </span>
    </a>`;
}

/** @param {SessionIndexRow} r @param {number} now */
function activeRowHTML(r, now) {
  const id = r.session_id;
  return `<a class="sl-row sl-active-row" href="${escHtml(sessionHash(id))}" data-session="${escHtml(id)}" data-focus-key="active:${escHtml(id)}">
      <span class="sl-main">
        <span class="sl-label">${escHtml(rowLabel(r))}</span>
        ${outcomeHTML(r)}
      </span>
      <span class="sl-meta"><span class="sl-elapsed" title="Time since the session started">${escHtml(elapsedText(r.started_at, now))}</span></span>
    </a>`;
}

/**
 * @param {Element} root  the .sessions-list element
 * @param {SessionIndexRow[]} rows
 * @param {ListUI} ui
 * @param {{now?: number, search?: string}} [opts]
 */
export function renderList(root, rows, ui, { now = Date.now(), search = '' } = {}) {
  const v = listView(rows, ui);
  const filters = root.querySelector('.sl-filters');
  const sorts = root.querySelector('.sl-sorts');
  const body = root.querySelector('.sl-body');
  if (!filters || !sorts || !body) return;
  const keep = captureFocus(root);
  const scrollY = typeof window.scrollY === 'number' ? window.scrollY : 0;

  filters.innerHTML = FILTERS.map(f => `<button type="button" class="sl-chip" data-filter="${f.id}"
      data-focus-key="filter:${f.id}" aria-pressed="${ui.filter === f.id}">${escHtml(f.label)}
      <span class="sl-chip-count">${escHtml(String(v.counts[f.id]))}</span></button>`).join('');
  sorts.innerHTML = `<span class="sl-sort-label" aria-hidden="true">Sort</span>` + SORTS.map(s => `<button type="button"
      class="sl-sort" data-sort="${s.id}" data-focus-key="sort:${s.id}" aria-pressed="${ui.sort === s.id}">${escHtml(s.label)}</button>`).join('');

  if (v.empty === 'none') {
    body.innerHTML = `<p class="empty-state sl-empty" role="status">No sessions in this window (${escHtml(windowText(search))})</p>`;
  } else if (v.empty) {
    body.innerHTML = `<p class="empty-state sl-empty" role="status">${escHtml(v.empty)}</p>`;
  } else {
    const active = v.active.length
      ? `<section class="sl-active" aria-label="Active in the last minute">
          <h2 class="sl-active-title">Active in the last minute · ${escHtml(String(v.active.length))}</h2>
          <div class="sl-rows">${v.active.map(r => activeRowHTML(r, now)).join('')}</div>
        </section>`
      : '';
    body.innerHTML = active + v.groups.map(g => `<section class="sl-group" data-family="${escHtml(g.key)}">
        <h2 class="sl-group-head"><span class="sl-family">${escHtml(g.label)}</span>
          <span class="sl-count" title="${escHtml(String(g.count))} session${g.count === 1 ? '' : 's'}">${escHtml(String(g.count))}</span></h2>
        <div class="sl-rows">${g.rows.map(rowHTML).join('')}</div>
        ${g.hidden ? `<button type="button" class="sl-more" data-expand="${escHtml(g.key)}"
            data-focus-key="more:${escHtml(g.key)}">Show ${escHtml(String(g.hidden))} more</button>` : ''}
      </section>`).join('');
  }
  restoreFocus(root, keep);
  if (typeof window.scrollTo === 'function' && window.scrollY !== scrollY) window.scrollTo(0, scrollY);
}

// renderListLoading paints the skeleton while the index is in flight.
/** @param {Element} root */
export function renderListLoading(root) {
  const body = root.querySelector('.sl-body');
  if (body) body.innerHTML = `<div aria-busy="true" aria-label="Loading sessions">${sessionListSkeleton(8)}</div>`;
}

// renderListError paints the failed fetch with its HTTP status and a Retry.
/** @param {Element} root @param {any} err */
export function renderListError(root, err) {
  const body = root.querySelector('.sl-body');
  if (!body) return;
  const reason = err && err.status
    ? `HTTP ${err.status}${err.statusText ? ' ' + err.statusText : ''}`
    : `the server did not answer (${(err && err.message) || 'network error'})`;
  body.innerHTML = `<div class="warning-card sl-error" role="alert"><strong class="danger">Could not load sessions:</strong>
    ${escHtml(reason)} <button type="button" class="retry" data-retry data-focus-key="retry">Retry</button></div>`;
}

/** @param {Element} root */
function captureFocus(root) {
  const a = /** @type {Element|null} */ (document.activeElement);
  if (!a || !root.contains(a) || !a.getAttribute) return '';
  return a.getAttribute('data-focus-key') || '';
}

/** @param {Element} root @param {string} key */
function restoreFocus(root, key) {
  if (!key || key === 'search') return;
  const el = /** @type {HTMLElement|null} */ (root.querySelector(`[data-focus-key="${cssEsc(key)}"]`));
  if (el && document.activeElement !== el) el.focus({ preventScroll: true });
}

/** @param {Element} root */
const rowsOf = root => /** @type {HTMLElement[]} */ (Array.from(root.querySelectorAll('.sl-body .sl-row')));

/** @param {string} id */
function openSession(id) {
  window.location.hash = sessionHash(id);
}

// wireList binds the list's controls once: typing searches, a chip picks
// the filter, a sort button the order, "Show N more" opens a group, a row
// (click or Enter) opens its session, and ArrowDown/ArrowUp walk the rows
// across group boundaries. onChange repaints; onRetry refetches.
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
      openSession(row.getAttribute('data-session') || '');
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
      const before = rowsOf(root).filter(r => r.closest('.sl-group') === more.closest('.sl-group')).length;
      onChange();
      const group = root.querySelector(`.sl-group[data-family="${cssEsc(key)}"]`);
      const next = group ? /** @type {HTMLElement|undefined} */ (group.querySelectorAll('.sl-row')[before]) : null;
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
      openSession(t.getAttribute('data-session') || '');
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
