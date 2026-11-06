// @ts-check
// The session page's header: what the session is, what it ended on (the
// main agent's last message), the failed commands it never fixed, each a
// link to its turn, four tiles (cost, duration, tokens, unresolved
// failures), where this run sits among its prompt's runs, a strip of one bar per model turn
// showing where the money or the time went, up to three Worth a look notes
// and a button that copies an explain-this-session prompt. Every number
// comes from web/session-header-logic.js over the session's summary and,
// for the comparison line, the index.

import { fetchSessionSummary, fetchSessions } from './api.js';
import { escHtml, fmtCompact } from './format.js';
import { API_EQUIVALENT_TIP, providerLabel } from './provider-filter.js';
import { sessionHash } from './router.js';
import { skelLine } from './skeleton.js';
import { copyText } from './clipboard.js';
import { clock, fmtDuration, fmtUSD } from './session-story-logic.js';
import {
  headerNumbers, stripBars, worthALook, explainPrompt, sessionOf,
  failureRows, failuresHeading, headerFamilyStats, comparisonLine, FAILURES_PAGE,
} from './session-header-logic.js';

/** @import { SessionSummary, SessionIndexRow } from './api-types.js' */

const COPY_LABEL = 'Copy explain-this-session prompt';
const COPIED_MS = 2000;

/**
 * @typedef {Object} HeaderState
 * @property {SessionSummary} story
 * @property {'cost'|'time'} mode
 * @property {number} nowMs
 * @property {number} focusBar            the strip's roving tab stop
 * @property {import('./session-header-logic.js').FailureRow[]} failures
 * @property {ReturnType<typeof setTimeout>|undefined} copyTimer
 */

/** @type {WeakMap<Element, HeaderState>} */
const states = new WeakMap();
/** @type {WeakSet<Element>} */
const wired = new WeakSet();
/** @type {WeakMap<Element, number>} */
const loads = new WeakMap();

/** @param {any} err */
const reasonOf = err => (err && err.status ? `HTTP ${err.status}` : (err && err.message) || 'network error');

// paintSessionHeader shows the skeleton header in host, fetches the
// session's summary and renders it, or the error with a Retry that
// refetches, then adds the comparison line once the index (fetched beside
// the summary) answers; a failed index fetch leaves the header without it. A
// later call on the same host drops an earlier one's answer.
/** @param {Element} host @param {string} sessionId @param {{nowMs?: number}} [opts] */
export async function paintSessionHeader(host, sessionId, opts = {}) {
  const my = (loads.get(host) || 0) + 1;
  loads.set(host, my);
  states.delete(host);
  host.innerHTML = skeletonHTML();
  const summary = fetchSessionSummary(sessionId);
  /** @type {Promise<SessionIndexRow[]|null>} */
  const index = fetchSessions().then(ix => (ix && Array.isArray(ix.sessions) ? ix.sessions : null), () => null);
  /** @type {SessionSummary} */
  let story;
  try {
    story = await summary;
  } catch (err) {
    if (loads.get(host) !== my) return;
    if (err && err.status === 404) { host.innerHTML = ''; return; }
    host.innerHTML = `<section class="session-header" aria-label="Session summary">
      <div class="warning-card sh-error" role="alert"><strong class="danger">Failed to load session: ${escHtml(reasonOf(err))}</strong>
      <button type="button" class="retry" data-sh-retry>Retry</button></div></section>`;
    const retry = host.querySelector('[data-sh-retry]');
    if (retry) retry.addEventListener('click', () => { paintSessionHeader(host, sessionId, opts); });
    return;
  }
  if (loads.get(host) !== my) return;
  renderSessionHeader(host, story, opts);
  wireSessionHeader(host, story);
  const line = comparisonLine(story, headerFamilyStats(await index, story.family_key));
  if (loads.get(host) !== my || !line) return;
  showComparison(host, line);
}

// showComparison puts the comparison line under the tiles.
/** @param {Element} host @param {string} line */
function showComparison(host, line) {
  const tiles = host.querySelector('.sh-tiles');
  if (!tiles || !tiles.parentNode || host.querySelector('.sh-compare')) return;
  const p = host.ownerDocument.createElement('p');
  p.setAttribute('class', 'sh-compare');
  p.textContent = line;
  tiles.parentNode.insertBefore(p, tiles.nextElementSibling);
}

function skeletonHTML() {
  const tile = `<div class="sh-tile is-loading">${skelLine('sh-skel-label')}${skelLine('sh-skel-value')}${skelLine('sh-skel-sub')}</div>`;
  return `<section class="session-header" aria-label="Session summary" aria-busy="true">
    <div class="sh-ident">${skelLine('sh-skel-title')}${skelLine('sh-skel-meta')}</div>
    <div class="sh-last-message is-loading" aria-label="Loading the last message">${skelLine('sh-skel-last')}</div>
    <div class="sh-tiles">${tile}${tile}${tile}${tile}</div>
    <div class="sh-strip is-empty" aria-label="Loading the turns"></div>
  </section>`;
}

// renderSessionHeader paints story's header into host.
/** @param {Element} host @param {SessionSummary} story @param {{nowMs?: number, mode?: 'cost'|'time'}} [opts] */
export function renderSessionHeader(host, story, opts = {}) {
  const prev = states.get(host);
  /** @type {HeaderState} */
  const st = {
    story,
    mode: opts.mode || (prev && prev.story === story ? prev.mode : 'cost'),
    nowMs: typeof opts.nowMs === 'number' ? opts.nowMs : Date.now(),
    focusBar: 0,
    failures: failureRows(story),
    copyTimer: undefined,
  };
  states.set(host, st);
  const n = headerNumbers(story);
  const notes = worthALook(sessionOf(story), st.nowMs);
  const sid = story.session_id || '';
  const provider = story.provider || 'claude-code';
  const last = clock(story.last_activity);

  const tile = (/** @type {string} */ key, /** @type {string} */ label, /** @type {string} */ value, /** @type {string} */ sub, cls = '') =>
    `<div class="sh-tile${cls}" data-tile="${key}"><div class="sh-label">${label}</div>
      <div class="sh-value">${value}</div><div class="sh-sub">${sub}</div></div>`;

  host.innerHTML = `<section class="session-header" aria-label="Session summary">
    <div class="sh-ident">
      <h2 class="sh-title">${escHtml(story.title || '(no prompt)')}</h2>
      <div class="sh-meta">
        <span class="sh-project">${escHtml(story.project || '')}</span>
        <span class="s-entry sh-provider s-provider-${escHtml(provider)}">${escHtml(providerLabel(provider))}</span>
        <span class="sh-model">${escHtml(story.model || '')}</span>
        <code class="sh-id" data-tooltip="${escHtml(sid)}">${escHtml(sid.slice(0, 8))}</code>
        ${last ? `<span class="sh-last">Last activity ${escHtml(last)}</span>` : ''}
      </div>
    </div>
    <p class="sh-last-message"><strong class="sh-lm-label">Last message:</strong>
      <span class="sh-lm-text">${escHtml(story.outcome || '(no agent message)')}</span></p>
    ${failuresHTML(st)}
    <div class="sh-tiles">
      ${tile('cost', `Cost <span class="api-eq" tabindex="0" data-tooltip="${escHtml(API_EQUIVALENT_TIP)}">API-equivalent</span>`,
        escHtml(fmtUSD(n.costUSD)), 'sum of every turn')}
      ${tile('duration', 'Duration', escHtml(fmtDuration(n.durationMs)), escHtml(n.durationSub))}
      ${tile('tokens', 'Tokens', escHtml(fmtCompact(n.tokens)), escHtml(n.tokensSub))}
      ${tile('failed', 'Failed', String(n.failed), escHtml(n.failedSub), n.failed > 0 ? ' is-failed' : '')}
    </div>
    <div class="sh-strip-head">
      <h3 class="sh-strip-title">${st.mode === 'time' ? 'Time per turn' : 'Cost added per turn'}</h3>
      <div class="sh-mode" role="radiogroup" aria-label="Bar height">
        ${modeButton('cost', 'Cost', st.mode)}${modeButton('time', 'Time', st.mode)}
      </div>
    </div>
    <div class="sh-strip" role="group" aria-label="One bar per model turn">${barsHTML(st)}</div>
    ${notes.length ? `<section class="sh-notes" aria-label="Worth a look">
      <h3 class="sh-notes-title">Worth a look</h3>
      ${notes.map(note => `<button type="button" class="note note-${escHtml(note.kind)}" data-link="${escHtml(note.link)}">
        <strong class="note-head">${escHtml(note.head)}</strong>
        <span class="note-body">${escHtml(note.body)}</span></button>`).join('')}
    </section>` : ''}
    <div class="sh-actions"><button type="button" class="sh-copy">${COPY_LABEL}</button></div>
  </section>`;
  host.setAttribute('data-session', sid);
}

// failuresHTML is the unfixed failed commands block: a heading over one
// link per failure, the first FAILURES_PAGE of them until Show all; '' when
// there are none.
/** @param {HeaderState} st */
function failuresHTML(st) {
  const n = st.failures.length;
  if (!n) return '';
  return `<section class="sh-failures" aria-label="Failed commands never fixed">
    <h3 class="sh-failures-title">${escHtml(failuresHeading(n))}</h3>
    <ul class="sh-failure-list">${failureItems(st, FAILURES_PAGE)}</ul>
    ${n > FAILURES_PAGE ? `<button type="button" class="sh-failures-more">Show all ${n}</button>` : ''}
  </section>`;
}

/** @param {HeaderState} st @param {number} limit */
const failureItems = (st, limit) => st.failures.slice(0, limit).map(f =>
  `<li><a class="sh-failure" href="${escHtml(sessionHash(st.story.session_id, f.link))}" data-link="${escHtml(f.link)}">${escHtml(f.text)}</a></li>`).join('');

// showAllFailures draws every failure row, drops the Show all button and
// moves focus to the first row it revealed.
/** @param {Element} host @param {HeaderState} st */
function showAllFailures(host, st) {
  const list = host.querySelector('.sh-failure-list');
  if (!list) return;
  list.innerHTML = failureItems(st, st.failures.length);
  const more = host.querySelector('.sh-failures-more');
  if (more) more.remove();
  const next = list.querySelectorAll('.sh-failure')[FAILURES_PAGE];
  if (next) /** @type {any} */ (next).focus();
}

/** @param {'cost'|'time'} mode @param {string} label @param {'cost'|'time'} active */
const modeButton = (mode, label, active) =>
  `<button type="button" role="radio" class="sh-mode-btn${mode === active ? ' is-active' : ''}" data-mode="${mode}"
    aria-checked="${mode === active}" tabindex="${mode === active ? 0 : -1}">${label}</button>`;

/** @param {HeaderState} st */
function barsHTML(st) {
  const bars = stripBars(st.story, st.mode);
  if (st.focusBar >= bars.length) st.focusBar = 0;
  return bars.map((b, i) => `<button type="button" class="turn-bar${b.failed ? ' is-failed' : ''}"
    data-link="${escHtml(b.link)}" data-first="${b.first}" data-tooltip="${escHtml(b.tip)}" aria-label="${escHtml(b.tip)}"
    tabindex="${i === st.focusBar ? 0 : -1}" style="height: max(2px, ${b.heightPct}%)"></button>`).join('');
}

/** @param {Element} host @param {HeaderState} st */
function setMode(host, st, /** @type {'cost'|'time'} */ mode, focus = false) {
  st.mode = mode;
  host.querySelectorAll('.sh-mode-btn').forEach(b => {
    const on = b.getAttribute('data-mode') === mode;
    b.classList.toggle('is-active', on);
    b.setAttribute('aria-checked', String(on));
    b.setAttribute('tabindex', on ? '0' : '-1');
    if (on && focus) /** @type {any} */ (b).focus();
  });
  const title = host.querySelector('.sh-strip-title');
  if (title) title.textContent = mode === 'time' ? 'Time per turn' : 'Cost added per turn';
  const strip = host.querySelector('.sh-strip');
  if (strip) strip.innerHTML = barsHTML(st);
}

/** @param {Element} host @param {string} link */
function go(host, link) {
  const st = states.get(host);
  if (!st || !link) return;
  window.location.hash = sessionHash(st.story.session_id, link);
}

// wireSessionHeader attaches the header's handlers to host once: failed
// command, bar and note clicks and Enter jump to their turn (a modified
// click on a failed command opens it as a link does), Show all reveals
// every failed command, the Cost | Time switch answers
// clicks and the arrow keys, the strip's arrows move between bars and the
// copy button copies the explain prompt.
/** @param {Element} host @param {SessionSummary} story */
export function wireSessionHeader(host, story) {
  const st = states.get(host);
  if (st && st.story !== story) st.story = story;
  if (wired.has(host)) return;
  wired.add(host);
  host.addEventListener('click', e => {
    const st = states.get(host);
    const t = /** @type {Element|null} */ (e.target);
    if (!st || !t || typeof t.closest !== 'function') return;
    const mode = t.closest('.sh-mode-btn');
    if (mode) { setMode(host, st, /** @type {'cost'|'time'} */ (mode.getAttribute('data-mode') || 'cost')); return; }
    const ev = /** @type {MouseEvent} */ (e);
    const fail = t.closest('.sh-failure');
    if (fail) {
      if (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey || (ev.button || 0) !== 0) return;
      e.preventDefault();
      go(host, fail.getAttribute('data-link') || '');
      return;
    }
    if (t.closest('.sh-failures-more')) { showAllFailures(host, st); return; }
    const jump = t.closest('.turn-bar, .note');
    if (jump) { go(host, jump.getAttribute('data-link') || ''); return; }
    const copy = t.closest('.sh-copy');
    if (copy) copyPrompt(copy, st);
  });
  host.addEventListener('keydown', e => {
    const st = states.get(host);
    const ev = /** @type {KeyboardEvent} */ (e);
    const t = /** @type {Element|null} */ (e.target);
    if (!st || !t || typeof t.closest !== 'function') return;
    if (t.closest('.sh-mode') && (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight')) {
      e.preventDefault();
      setMode(host, st, st.mode === 'cost' ? 'time' : 'cost', true);
      return;
    }
    const bar = t.closest('.turn-bar');
    if (bar && (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight' || ev.key === 'Home' || ev.key === 'End')) {
      e.preventDefault();
      const bars = [...host.querySelectorAll('.turn-bar')];
      const i = bars.indexOf(bar);
      const next = ev.key === 'Home' ? 0 : ev.key === 'End' ? bars.length - 1
        : Math.max(0, Math.min(bars.length - 1, i + (ev.key === 'ArrowRight' ? 1 : -1)));
      bars.forEach((b, k) => b.setAttribute('tabindex', k === next ? '0' : '-1'));
      st.focusBar = next;
      /** @type {any} */ (bars[next]).focus();
      return;
    }
    const jump = t.closest('.turn-bar, .note, .sh-failure');
    if (jump && ev.key === 'Enter') {
      e.preventDefault();
      go(host, jump.getAttribute('data-link') || '');
    }
  });
}

/** @param {Element} btn @param {HeaderState} st */
async function copyPrompt(btn, st) {
  const ok = await copyText(explainPrompt(st.story));
  btn.textContent = ok ? 'Copied' : 'Copy failed — the browser blocked the clipboard';
  btn.classList.toggle('copied', ok);
  clearTimeout(st.copyTimer);
  st.copyTimer = setTimeout(() => { btn.textContent = COPY_LABEL; btn.classList.remove('copied'); }, COPIED_MS);
}
