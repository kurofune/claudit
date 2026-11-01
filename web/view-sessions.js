// @ts-check
// The Agents nav entry: #agents is the Sessions list, every session in the
// window grouped by prompt family; #agents/<session>[/<turn>] is that
// session's page, whose story web/session-story.js paints. The list
// refreshes in place on each SSE generation bump, keeping the search,
// filter, sort, focused row and scroll position.

import { fetchSessions } from './api.js';
import { escHtml, fmtNum } from './format.js';
import { parseAgentsSub } from './router.js';
import { setLiveHandler, clearLiveHandler } from './sse.js';
import { clearSkeletonFlag } from './skeleton.js';
import { paintStory } from './session-story.js';
import { paintSessionHeader } from './session-header.js';
import { listShellHTML, renderList, renderListLoading, renderListError, renderRefreshError, clearRefreshError, wireList } from './sessions-list.js';

/** @import { SessionIndexRow } from './api-types.js' */
/** @import { ListUI } from './sessions-list.js' */

const ICON = '<svg class="icon" aria-hidden="true"><use href="#icon-agents"/></svg>';

/** @type {ListUI} */
const ui = { query: '', filter: 'all', sort: 'newest', expanded: new Set() };

/** @type {SessionIndexRow[]|null} */
let rows = null;
/** @typedef {{req: number, data?: {sessions: SessionIndexRow[]}, error?: unknown}} Answer */
/** @type {Promise<Answer>|null} */
let inFlight = null;
/** @type {Promise<Answer>|null} */
let followUp = null;
// lastReq numbers index requests as they go out; only one is ever out at a
// time, so they answer in that order. landed is the newest request whose
// answer reached the screen: an answer older than it is stale and never
// paints, and every newer one does, so a stream of bumps still advances
// the rows.
let lastReq = 0;
let landed = 0;
// gen bumps on every mount, so an index that lands after the user left
// the list (or remounted it) paints nothing.
let gen = 0;
/** @type {Element|null} */
let listRoot = null;

// load fetches the index, sharing a request already in flight. It never
// rejects: the answer carries the data or the error, tagged with its
// request number. fresh asks for an index read after this call: when a
// request is already out (it may have been answered before the change
// that prompted this call), it waits for that one and fetches again.
// Every fresh call made while one request is out shares the one follow-up.
/** @param {boolean} [fresh] @returns {Promise<Answer>} */
const load = (fresh = false) => {
  if (!inFlight) {
    const req = ++lastReq;
    inFlight = fetchSessions().then(
      data => ({ req, data }),
      error => ({ req, error }),
    ).finally(() => { inFlight = null; });
    return inFlight;
  }
  if (!fresh) return inFlight;
  if (!followUp) {
    followUp = inFlight.then(() => { followUp = null; return load(); });
  }
  return followUp;
};

// fresher reports whether answer is at least as new as the one on screen,
// and if so records it as the one on screen.
/** @param {Answer} answer */
function fresher(answer) {
  if (answer.req < landed) return false;
  landed = answer.req;
  return true;
}

const container = () => document.getElementById('view-agents');

// guideHTML is the "How to read this section" guide web/index.html keeps
// in its #agents-guide template, or '' when the shell has none.
const guideHTML = () => {
  const tpl = document.getElementById('agents-guide');
  return tpl ? tpl.innerHTML : '';
};

/** @param {SessionIndexRow[]} list */
function updateNavMetric(list) {
  const el = document.getElementById('nav-metric-agents');
  if (!el) return;
  el.textContent = list.length ? `${fmtNum(list.length)} sessions` : '—';
  if (list.length) el.setAttribute('title', `${fmtNum(list.length)} session${list.length === 1 ? '' : 's'} in this window`);
  else el.removeAttribute('title');
  clearSkeletonFlag('nav-metric-agents');
}

// paintNav resolves the sidebar metric before the tab is first opened.
export async function paintNav() {
  if (rows) return;
  const answer = await load();
  if (rows || 'error' in answer || !fresher(answer)) return;
  rows = (answer.data && answer.data.sessions) || [];
  updateNavMetric(rows);
}

/** @param {{sub?: string}} route */
export async function paint(route) {
  const host = container();
  if (!host) return;
  const { sessionId, turnUuid } = parseAgentsSub(route && route.sub);
  const my = ++gen;
  if (sessionId) {
    clearLiveHandler(liveUpdate);
    listRoot = null;
    await paintSession(host, sessionId, turnUuid);
    return;
  }
  host.innerHTML = `<header class="view-head"><h1>${ICON}Sessions</h1></header>${guideHTML()}${listShellHTML(ui)}`;
  const root = host.querySelector('.sessions-list');
  if (!root) return;
  listRoot = root;
  wireList(root, ui, {
    onChange: () => repaint(root),
    onRetry: () => { if (rows) refresh(root, my); else fetchInto(root, my); },
  });
  setLiveHandler(liveUpdate);
  if (rows) {
    repaint(root);
    refresh(root, my);
    return;
  }
  await fetchInto(root, my);
}

/** @param {Element} root */
function repaint(root) {
  if (rows) renderList(root, rows, ui, { search: window.location.search || '' });
}

// fetchInto shows the skeleton, fetches the index and paints it, or the
// error banner when the fetch fails.
/** @param {Element} root @param {number} my */
async function fetchInto(root, my) {
  renderListLoading(root);
  const answer = await load();
  if (my !== gen || listRoot !== root) return;
  if (!fresher(answer)) {
    if (rows) repaint(root);
    return;
  }
  if ('error' in answer) {
    renderListError(root, answer.error);
    return;
  }
  rows = (answer.data && answer.data.sessions) || [];
  updateNavMetric(rows);
  repaint(root);
}

// refresh refetches without the skeleton. A failure keeps the rows on
// screen and says so above them with a Retry; the next success clears it.
// Each answer paints in request order unless a newer one already has, so
// bumps that keep queueing follow-ups never starve the list.
/** @param {Element} root @param {number} my */
async function refresh(root, my) {
  const answer = await load(true);
  if (my !== gen || listRoot !== root || !fresher(answer)) return;
  if ('error' in answer) {
    if (rows) renderRefreshError(root, answer.error);
    else renderListError(root, answer.error);
    return;
  }
  rows = (answer.data && answer.data.sessions) || [];
  clearRefreshError(root);
  updateNavMetric(rows);
  repaint(root);
}

// liveUpdate is the list's SSE handler: a new generation refetches the
// index and repaints it in place.
function liveUpdate() {
  if (listRoot) return refresh(listRoot, gen);
  return undefined;
}

// paintSession paints the session page: its header above its story. A
// jump to another turn of the session already on screen keeps the header
// and repaints only the story.
/** @param {Element} host @param {string} sessionId @param {string} turnUuid */
async function paintSession(host, sessionId, turnUuid) {
  const page = host.querySelector('.session-page');
  if (page && page.getAttribute('data-session') === sessionId) {
    const storyHost = host.querySelector('.session-story-host');
    if (storyHost) await paintStory(storyHost, sessionId, turnUuid);
    return;
  }
  host.innerHTML = `<div class="session-page" data-session="${escHtml(sessionId)}">
    <header class="view-head session-head">
      <a class="story-back" href="#agents">Back to sessions</a>
      <h1>${ICON}Session <code class="session-id">${escHtml(sessionId)}</code></h1>
    </header>${guideHTML()}
    <div id="session-header" class="session-header-host"></div>
    <div class="session-story-host"></div></div>`;
  const headerHost = host.querySelector('#session-header');
  const storyHost = host.querySelector('.session-story-host');
  await Promise.all([
    storyHost ? paintStory(storyHost, sessionId, turnUuid) : undefined,
    headerHost ? paintSessionHeader(headerHost, sessionId) : undefined,
  ]);
}
