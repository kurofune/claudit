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
import { listShellHTML, renderList, renderListLoading, renderListError, renderRefreshError, clearRefreshError, wireList } from './sessions-list.js';

/** @import { SessionIndexRow } from './api-types.js' */
/** @import { ListUI } from './sessions-list.js' */

const ICON = '<svg class="icon" aria-hidden="true"><use href="#icon-agents"/></svg>';

/** @type {ListUI} */
const ui = { query: '', filter: 'all', sort: 'newest', expanded: new Set() };

/** @type {SessionIndexRow[]|null} */
let rows = null;
/** @type {Promise<{sessions: SessionIndexRow[]}>|null} */
let inFlight = null;
/** @type {Promise<{sessions: SessionIndexRow[]}>|null} */
let followUp = null;
// refreshSeq numbers refreshes so only the latest one paints: an older
// answer never lands over a newer one.
let refreshSeq = 0;
// gen bumps on every mount, so an index that lands after the user left
// the list (or remounted it) paints nothing.
let gen = 0;
/** @type {Element|null} */
let listRoot = null;

// load fetches the index, sharing a request already in flight. fresh asks
// for an index read after this call: when a request is already out (it may
// have been answered before the change that prompted this call), it waits
// for that one and fetches again. Every fresh call made while one request
// is out shares the one follow-up.
/** @param {boolean} [fresh] @returns {Promise<{sessions: SessionIndexRow[]}>} */
const load = (fresh = false) => {
  if (!inFlight) {
    inFlight = fetchSessions().finally(() => { inFlight = null; });
    return inFlight;
  }
  if (!fresh) return inFlight;
  if (!followUp) {
    followUp = inFlight.catch(() => undefined).then(() => { followUp = null; return load(); });
  }
  return followUp;
};

const container = () => document.getElementById('view-agents');

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
  try { rows = (await load()).sessions || []; } catch { return; }
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
  host.innerHTML = `<header class="view-head"><h1>${ICON}Sessions</h1></header>${listShellHTML(ui)}`;
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
  try {
    const data = await load();
    if (my !== gen) return;
    rows = (data && data.sessions) || [];
  } catch (err) {
    if (my !== gen) return;
    renderListError(root, err);
    return;
  }
  updateNavMetric(rows);
  repaint(root);
}

// refresh refetches without the skeleton. A failure keeps the rows on
// screen and says so above them with a Retry; the next success clears it.
/** @param {Element} root @param {number} my */
async function refresh(root, my) {
  const seq = ++refreshSeq;
  const current = () => my === gen && listRoot === root && seq === refreshSeq;
  let data;
  try {
    data = await load(true);
  } catch (err) {
    if (!current()) return;
    if (rows) renderRefreshError(root, err);
    else renderListError(root, err);
    return;
  }
  if (!current()) return;
  rows = (data && data.sessions) || [];
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

/** @param {Element} host @param {string} sessionId @param {string} turnUuid */
async function paintSession(host, sessionId, turnUuid) {
  host.innerHTML = `<header class="view-head session-head">
      <a class="story-back" href="#agents">Back to sessions</a>
      <h1>${ICON}Session <code class="session-id">${escHtml(sessionId)}</code></h1>
    </header>
    <div class="session-story-host"></div>`;
  const storyHost = host.querySelector('.session-story-host');
  if (storyHost) await paintStory(storyHost, sessionId, turnUuid);
}
