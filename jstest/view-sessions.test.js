process.env.TZ = 'UTC';

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, keydown, Event } from './helpers/mini-dom.js';
import { start as startSSE, clearLiveHandler } from '../web/sse.js';

const NOW = Date.parse('2026-11-01T10:00:00Z');
const at = min => new Date(NOW + min * 60_000).toISOString();

const row = (id, f = {}) => ({
  session_id: id, family_key: 'fix the bug', remainder: '', title: 'fix the bug', outcome: 'Done.',
  project: 'claudit', provider: 'claude-code', model: 'claude-opus-5-5', cost_usd: 1, duration_ms: 60_000,
  turns: 3, commands: 2, failed: 0, last_activity: at(-30), active: false, started_at: at(-40), ...f,
});

const THREE = () => [
  row('s-a', { family_key: 'work bead _ next', remainder: 'claudit-9yx', last_activity: at(-1), cost_usd: 2.5, duration_ms: 125_000, failed: 2,
    outcome: 'All twelve acceptance criteria hold and the branch is committed.', active: true, started_at: at(-12) }),
  row('s-b', { family_key: 'work bead _ next', remainder: 'claudit-4eb', last_activity: at(-20), cost_usd: 9 }),
  row('s-c', { family_key: 'review the diff', remainder: '', title: 'review the diff please', last_activity: at(-5), provider: 'codex', outcome: '' }),
  row('s-d', { family_key: 'retry _ times with a _ms timeout', remainder: '3 · 500', last_activity: at(-50), cost_usd: 1234.5 }),
];

// stubFetch answers the index and story routes through route(path, params).
function stubFetch(route) {
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    const u = new URL(String(url), 'http://x');
    const r = await route(u.pathname.replace('/_claudit/api', ''), u.searchParams);
    if (r && r.httpStatus) return { ok: false, status: r.httpStatus, statusText: r.statusText || '', json: async () => ({}) };
    if (r instanceof Error) throw r;
    return { ok: true, status: 200, statusText: 'OK', json: async () => r };
  };
  return calls;
}

const settle = () => new Promise(r => setTimeout(r, 0));

let n = 0;
// mount installs a fresh DOM and a fresh copy of the view module (its list
// state is module-level), then paints route.
async function mount(sub = '', { hash = '#agents', search = '' } = {}) {
  const env = installDOM({ hash, search });
  env.window.scrollY = 0;
  env.window.scrollTo = (_x, y) => { env.window.scrollY = y; };
  const view = env.document.createElement('section');
  view.setAttribute('id', 'view-agents');
  env.document.body.appendChild(view);
  const metric = env.document.createElement('span');
  metric.setAttribute('id', 'nav-metric-agents');
  env.document.body.appendChild(metric);
  clearLiveHandler();
  const mod = await import(`../web/view-sessions.js?fresh=${++n}`);
  const done = mod.paint({ view: 'agents', sub, unit: 'dollars' });
  return { ...env, view, mod, done };
}

const realNow = Date.now;
test.beforeEach(() => { Date.now = () => NOW; });
test.afterEach(() => { Date.now = realNow; });

test('three families render as groups ordered by their newest member, with the key and count', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const groups = view.querySelectorAll('.sl-group');
  assert.deepEqual(groups.map(g => g.querySelector('.sl-family').textContent),
    ['work bead … next', 'review the diff', 'retry … times with a …ms timeout']);
  assert.deepEqual(groups.map(g => g.querySelector('.sl-count').textContent), ['2', '1', '1']);
  assert.deepEqual(groups[0].querySelectorAll('.sl-row').map(r => r.getAttribute('data-session')), ['s-a', 's-b']);
  assert.equal(view.ownerDocument.getElementById('nav-metric-agents').textContent, '4 sessions');
});

test('a row shows the remainder (or title), the outcome with its full text in title, a provider chip, cost, duration and failures', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const a = view.querySelector('.sl-group [data-session="s-a"]');
  assert.equal(a.getAttribute('href'), '#agents/s-a');
  assert.equal(a.querySelector('.sl-label').textContent, 'claudit-9yx');
  const out = a.querySelector('.sl-outcome');
  assert.equal(out.textContent, 'All twelve acceptance criteria hold and the branch is committed.');
  assert.equal(out.getAttribute('title'), 'All twelve acceptance criteria hold and the branch is committed.');
  assert.equal(a.querySelector('.sl-provider').textContent, 'Claude Code');
  assert.equal(a.querySelector('.sl-cost').textContent, '$2.50');
  assert.equal(a.querySelector('.sl-dur').textContent, '2m 5s');
  assert.equal(a.querySelector('.sl-failed').textContent, '2 failed');

  const b = view.querySelector('[data-session="s-b"]');
  assert.equal(b.querySelector('.sl-failed'), null, 'no failure count when failed is 0');
  const c = view.querySelector('[data-session="s-c"]');
  assert.equal(c.querySelector('.sl-label').textContent, 'review the diff please', 'title when the remainder is empty');
  assert.equal(c.querySelector('.sl-provider').textContent, 'Codex');
  const d = view.querySelector('[data-session="s-d"]');
  assert.equal(d.querySelector('.sl-cost').textContent, '$1,235');
});

test('clicking a row or pressing Enter on it opens #agents/<session-id>', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done, location } = await mount();
  await done;
  click(view.querySelector('.sl-group [data-session="s-b"] .sl-label'));
  assert.equal(location.hash, '#agents/s-b');
  const c = view.querySelector('[data-session="s-c"]');
  c.focus();
  keydown(c, 'Enter');
  assert.equal(location.hash, '#agents/s-c');
});

test('ArrowDown and ArrowUp move focus across group boundaries; every row and control is tabbable', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done, document } = await mount();
  await done;
  const rows = view.querySelectorAll('.sl-body .sl-row');
  assert.deepEqual(rows.map(r => r.getAttribute('data-focus-key')),
    ['active:s-a', 'row:s-a', 'row:s-b', 'row:s-c', 'row:s-d']);
  rows[2].focus();
  keydown(rows[2], 'ArrowDown');
  assert.equal(document.activeElement, rows[3], 'from the last row of one family to the first of the next');
  keydown(rows[3], 'ArrowUp');
  assert.equal(document.activeElement, rows[2]);
  keydown(rows[2], 'ArrowUp');
  keydown(rows[1], 'ArrowUp');
  assert.equal(document.activeElement, rows[0]);
  for (const el of [...rows, ...view.querySelectorAll('.sl-chip, .sl-sort, .sl-search')]) {
    assert.ok(el.tabIndex >= 0, `${el.outerHTML.slice(0, 60)} is tabbable`);
  }
  assert.equal(view.querySelectorAll('.sl-chip').length, 3);
  assert.equal(view.querySelectorAll('.sl-sort').length, 3);
});

test('search filters rows, drops empty groups and says when nothing matches', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const input = view.querySelector('.sl-search');
  input.value = 'CLAUDIT-4EB';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  assert.deepEqual(view.querySelectorAll('.sl-group').map(g => g.getAttribute('data-family')), ['work bead _ next']);
  assert.deepEqual(view.querySelectorAll('.sl-group .sl-row').map(r => r.getAttribute('data-session')), ['s-b']);
  input.value = 'nothing like it';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  assert.equal(view.querySelectorAll('.sl-group').length, 0);
  assert.equal(view.querySelector('.sl-empty').textContent, 'No sessions match');
});

test('filter chips carry counts and select one at a time; the sort reorders rows within a group', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const chips = () => view.querySelectorAll('.sl-chip');
  assert.deepEqual(chips().map(c => c.textContent.replace(/\s+/g, ' ').trim()),
    ['All 4', 'Active in the last minute 1', 'Had unresolved failures 1']);
  assert.deepEqual(chips().map(c => c.getAttribute('aria-pressed')), ['true', 'false', 'false']);
  click(chips()[2]);
  assert.deepEqual(chips().map(c => c.getAttribute('aria-pressed')), ['false', 'false', 'true']);
  assert.deepEqual(view.querySelectorAll('.sl-group .sl-row').map(r => r.getAttribute('data-session')), ['s-a']);
  click(chips()[0]);
  assert.equal(view.querySelectorAll('.sl-group .sl-row').length, 4);

  const order = () => view.querySelectorAll('.sl-group')[0].querySelectorAll('.sl-row').map(r => r.getAttribute('data-session'));
  assert.deepEqual(order(), ['s-a', 's-b']);
  click(view.querySelector('.sl-sort[data-sort="cost"]'));
  assert.deepEqual(order(), ['s-b', 's-a']);
  assert.equal(view.querySelector('.sl-sort[data-sort="cost"]').getAttribute('aria-pressed'), 'true');
  click(view.querySelector('.sl-sort[data-sort="duration"]'));
  assert.deepEqual(order(), ['s-a', 's-b']);
});

test('an active session pins the "Active in the last minute" row above the groups, never saying Live or running', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const body = view.querySelector('.sl-body');
  const active = body.children[0];
  assert.ok(active.classList.contains('sl-active'), 'pinned first');
  assert.equal(active.querySelector('.sl-active-title').textContent, 'Active in the last minute · 1');
  const r = active.querySelector('.sl-row');
  assert.equal(r.querySelector('.sl-label').textContent, 'claudit-9yx');
  assert.equal(r.querySelector('.sl-outcome').textContent, 'All twelve acceptance criteria hold and the branch is committed.');
  assert.equal(r.querySelector('.sl-elapsed').textContent, '12m');
  assert.doesNotMatch(view.textContent, /\bLive\b|running/i);
  assert.doesNotMatch(view.innerHTML, /\bLive\b|running/i);
});

test('no active session, no Active row', async () => {
  stubFetch(() => ({ sessions: THREE().map(r => ({ ...r, active: false })) }));
  const { view, done } = await mount();
  await done;
  assert.equal(view.querySelector('.sl-active'), null);
});

test('#agents/<session-id>/<turn> mounts paintStory with the session and turn under a Back to sessions link', async () => {
  const calls = stubFetch((path, p) => {
    assert.equal(path, '/sessions/story');
    return { httpStatus: 404 };
  });
  const { view, done } = await mount('s-a/turn-7');
  await done;
  await settle();
  assert.equal(view.querySelector('.session-id').textContent, 's-a');
  const back = view.querySelector('.session-head .story-back');
  assert.equal(back.getAttribute('href'), '#agents');
  assert.equal(back.textContent, 'Back to sessions');
  assert.ok(view.querySelector('.session-story-host .story'), 'paintStory rendered its section');
  assert.match(calls[0], /\/sessions\/story\?session=s-a&turn=turn-7/);
  assert.equal(view.querySelector('.sessions-list'), null);
});

test('an empty index names the window it covers', async () => {
  stubFetch(() => ({ sessions: [] }));
  const { view, done } = await mount('', { search: '?since=2026-10-25&until=2026-11-01' });
  await done;
  assert.equal(view.querySelector('.sl-empty').textContent, 'No sessions in this window (since 2026-10-25, until 2026-11-01)');
});

test('the skeleton shows until the fetch resolves', async () => {
  let release;
  stubFetch(() => new Promise(r => { release = r; }));
  const { view, done } = await mount();
  await settle();
  assert.ok(view.querySelector('.sl-body [aria-busy="true"] .skel-card'), 'sessionListSkeleton is up');
  release({ sessions: THREE() });
  await done;
  assert.equal(view.querySelector('.skel-card'), null);
  assert.equal(view.querySelectorAll('.sl-group').length, 3);
});

test('a failed fetch shows the HTTP status and Retry refetches', async () => {
  let fail = true;
  const calls = stubFetch(() => (fail ? { httpStatus: 500, statusText: 'Internal Server Error' } : { sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const banner = view.querySelector('.sl-error');
  assert.equal(banner.getAttribute('role'), 'alert');
  assert.match(banner.textContent, /HTTP 500 Internal Server Error/);
  fail = false;
  click(banner.querySelector('button.retry'));
  await settle(); await settle();
  assert.equal(calls.length, 2);
  assert.equal(view.querySelector('.sl-error'), null);
  assert.equal(view.querySelectorAll('.sl-group').length, 3);
});

test('a stopped server reads as unreachable with a Retry', async () => {
  stubFetch(() => new TypeError('Failed to fetch'));
  const { view, done } = await mount();
  await done;
  assert.match(view.querySelector('.sl-error').textContent, /did not answer \(Failed to fetch\)/);
  assert.ok(view.querySelector('.sl-error button.retry'));
});

test('an SSE generation bump refetches and repaints, keeping search, filter, sort, focus and scroll', async () => {
  let data = THREE();
  const calls = stubFetch(() => ({ sessions: data }));
  let onMessage;
  globalThis.EventSource = class { constructor() {} addEventListener(_t, fn) { onMessage = fn; } };
  const { view, done, window, document } = await mount();
  await done;
  startSSE({});
  onMessage({ data: JSON.stringify({ generation: 1 }) });

  const input = view.querySelector('.sl-search');
  input.value = 'BEAD';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  click(view.querySelector('.sl-sort[data-sort="cost"]'));
  click(view.querySelector('.sl-chip[data-filter="all"]'));
  view.querySelector('.sl-group [data-session="s-b"]').focus();
  window.scrollY = 340;

  data = [...THREE(), row('s-e', { family_key: 'work bead _ next', remainder: 'claudit-new', cost_usd: 5, last_activity: at(-2) })];
  onMessage({ data: JSON.stringify({ generation: 2 }) });
  await settle(); await settle();
  assert.equal(calls.length, 2, 'the bump refetched the index');
  assert.deepEqual(view.querySelectorAll('.sl-group .sl-row').map(r => r.getAttribute('data-session')), ['s-b', 's-e', 's-a']);
  assert.equal(view.querySelector('.sl-search'), input, 'the search box survives with its text');
  assert.equal(input.value, 'BEAD');
  assert.equal(view.querySelector('.sl-sort[data-sort="cost"]').getAttribute('aria-pressed'), 'true');
  assert.equal(document.activeElement.getAttribute('data-focus-key'), 'row:s-b');
  assert.equal(window.scrollY, 340);
  clearLiveHandler();
});

test('a group over 20 rows collapses behind "Show N more" until opened', async () => {
  const rows = Array.from({ length: 3000 }, (_, i) => row(`s${i}`, { family_key: `family _ ${i % 88}`, last_activity: at(-i) }));
  stubFetch(() => ({ sessions: rows }));
  const { view, done } = await mount();
  await done;
  const groups = view.querySelectorAll('.sl-group');
  assert.equal(groups.length, 88);
  assert.equal(groups[0].querySelectorAll('.sl-row').length, 20);
  const more = groups[0].querySelector('.sl-more');
  assert.equal(more.textContent, 'Show 15 more');
  click(more);
  const g0 = view.querySelectorAll('.sl-group')[0];
  assert.equal(g0.querySelectorAll('.sl-row').length, 35);
  assert.equal(g0.querySelector('.sl-more'), null);
  assert.equal(view.querySelectorAll('.sl-group')[1].querySelectorAll('.sl-row').length, 20);
});

test('the list carries no filter bar, no drawer and no dollar figure below a session row', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  assert.equal(view.querySelector('.trace-filter'), null);
  assert.equal(view.querySelector('.agents-drawer'), null);
  for (const r of view.querySelectorAll('.sl-row')) {
    const dollars = r.querySelectorAll('*').filter(e => e.children.length === 0 && /\$/.test(e.textContent));
    assert.ok(dollars.every(e => e.classList.contains('sl-cost')), 'the only dollar figure is the row cost');
  }
  const outside = view.querySelectorAll('.sl-family, .sl-count, .sl-active-title, .sl-chip, .sl-sort');
  assert.ok(outside.every(e => !/\$/.test(e.textContent)));
});

// liveList mounts the list, loads it once and starts SSE, returning a bump(gen) that delivers a generation frame.
async function liveList(route) {
  const calls = stubFetch(route);
  let onMessage;
  globalThis.EventSource = class { constructor() {} addEventListener(_t, fn) { onMessage = fn; } };
  const env = await mount();
  await env.done;
  startSSE({});
  onMessage({ data: JSON.stringify({ generation: 1 }) });
  const bump = g => onMessage({ data: JSON.stringify({ generation: g }) });
  return { ...env, calls, bump };
}

test('a refresh that fails after a good load keeps the rows and search, says HTTP 503 above them, and Retry recovers', async () => {
  let answer = () => ({ sessions: THREE() });
  const { view, calls, bump } = await liveList(() => answer());
  const input = view.querySelector('.sl-search');
  input.value = 'BEAD';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  const before = view.querySelectorAll('.sl-group .sl-row').map(r => r.getAttribute('data-session'));

  answer = () => ({ httpStatus: 503, statusText: 'Service Unavailable' });
  bump(2);
  await settle(); await settle();
  assert.equal(calls.length, 2);
  const banner = view.querySelector('.sl-status .sl-error');
  assert.ok(banner, 'the refresh failure is on screen');
  assert.equal(banner.getAttribute('role'), 'alert');
  assert.match(banner.textContent, /Could not refresh sessions:\s+HTTP 503 Service Unavailable/);
  assert.deepEqual(view.querySelectorAll('.sl-group .sl-row').map(r => r.getAttribute('data-session')), before, 'the rows stay');
  assert.equal(view.querySelector('.sl-search'), input);
  assert.equal(input.value, 'BEAD');

  answer = () => ({ sessions: [...THREE(), row('s-e', { family_key: 'work bead _ next', remainder: 'claudit-new', last_activity: at(-2) })] });
  click(banner.querySelector('button.retry'));
  await settle(); await settle();
  assert.equal(calls.length, 3, 'Retry refetched');
  assert.equal(view.querySelector('.sl-error'), null, 'the banner cleared');
  assert.ok(view.querySelector('.sl-group [data-session="s-e"]'), 'the retried index painted');
  assert.equal(input.value, 'BEAD');
  clearLiveHandler();
});

test('a refresh that cannot reach the server says why with a Retry', async () => {
  let answer = () => ({ sessions: THREE() });
  const { view, bump } = await liveList(() => answer());
  answer = () => new TypeError('Failed to fetch');
  bump(2);
  await settle(); await settle();
  assert.match(view.querySelector('.sl-status .sl-error').textContent, /did not answer \(Failed to fetch\)/);
  assert.ok(view.querySelector('.sl-status button.retry'));
  assert.equal(view.querySelectorAll('.sl-group').length, 3);
  clearLiveHandler();
});

test('returning to a cached list whose refresh fails shows the failure over the cached rows', async () => {
  let answer = () => ({ sessions: THREE() });
  stubFetch(() => answer());
  const { view, mod, done } = await mount();
  await done;
  answer = () => ({ httpStatus: 503, statusText: 'Service Unavailable' });
  await mod.paint({ view: 'agents', sub: 's-a', unit: 'dollars' }).catch(() => {});
  await mod.paint({ view: 'agents', sub: '', unit: 'dollars' });
  await settle(); await settle();
  assert.equal(view.querySelectorAll('.sl-group').length, 3, 'the cached rows paint');
  assert.match(view.querySelector('.sl-status .sl-error').textContent, /HTTP 503 Service Unavailable/);
  clearLiveHandler();
});

test('a bump during an outstanding refresh fetches again once it settles and paints the newest index', async () => {
  const releases = [];
  let first = true;
  const { view, calls, bump } = await liveList(() => {
    if (first) { first = false; return { sessions: THREE() }; }
    return new Promise(r => { releases.push(r); });
  });
  const input = view.querySelector('.sl-search');
  input.value = 'BEAD';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  click(view.querySelector('.sl-sort[data-sort="cost"]'));

  bump(2);
  await settle();
  assert.equal(calls.length, 2);
  bump(3);
  await settle();
  assert.equal(calls.length, 2, 'the second bump waits for the outstanding request');

  releases[0]({ sessions: THREE() });
  await settle(); await settle();
  assert.equal(calls.length, 3, 'a follow-up request went out after the first settled');
  releases[1]({ sessions: [...THREE(), row('s-new', { family_key: 'work bead _ next', remainder: 'claudit-gen3', cost_usd: 5, last_activity: at(-2) })] });
  await settle(); await settle();
  assert.deepEqual(view.querySelectorAll('.sl-group .sl-row').map(r => r.getAttribute('data-session')), ['s-b', 's-new', 's-a']);
  assert.equal(input.value, 'BEAD');
  assert.equal(view.querySelector('.sl-sort[data-sort="cost"]').getAttribute('aria-pressed'), 'true');
  await settle();
  assert.equal(calls.length, 3, 'no further requests once caught up');
  clearLiveHandler();
});

test('sustained bumps, each arriving before the outstanding index answers, still advance the rows in request order', async () => {
  const releases = [];
  let first = true;
  const { view, calls, bump, window, document } = await liveList(() => {
    if (first) { first = false; return { sessions: THREE() }; }
    return new Promise(r => { releases.push(r); });
  });
  const input = view.querySelector('.sl-search');
  input.value = 'BEAD';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  click(view.querySelector('.sl-sort[data-sort="cost"]'));
  view.querySelector('.sl-group [data-session="s-b"]').focus();
  window.scrollY = 340;

  const gen = k => [...THREE(), row(`s-gen${k}`, { family_key: 'work bead _ next', remainder: `claudit-gen${k}`, cost_usd: 5, last_activity: at(-2) })];
  const shown = () => view.querySelectorAll('.sl-group .sl-row').map(r => r.getAttribute('data-session'));

  let g = 2;
  bump(g++);
  await settle();
  for (let k = 0; k < 5; k++) {
    bump(g++);
    await settle();
    assert.equal(calls.length, 2 + k, `request ${k} is the only one out`);
    releases[k]({ sessions: gen(k) });
    await settle(); await settle();
    assert.deepEqual(shown(), ['s-b', `s-gen${k}`, 's-a'], `response ${k} painted while a follow-up is still queued`);
    assert.equal(input.value, 'BEAD');
    assert.equal(view.querySelector('.sl-search'), input);
    assert.equal(view.querySelector('.sl-sort[data-sort="cost"]').getAttribute('aria-pressed'), 'true');
    assert.equal(document.activeElement.getAttribute('data-focus-key'), 'row:s-b');
    assert.equal(window.scrollY, 340);
  }
  assert.equal(calls.length, 7, 'the last bump queued one more read');
  releases[5]({ sessions: gen(5) });
  await settle(); await settle();
  assert.deepEqual(shown(), ['s-b', 's-gen5', 's-a']);
  await settle();
  assert.equal(calls.length, 7, 'no further requests once caught up');
  clearLiveHandler();
});

test('an index that lands after the user opened a session paints nothing over the session page', async () => {
  const releases = [];
  let first = true;
  const { view, mod, bump } = await liveList((path) => {
    if (path === '/sessions/story') return { httpStatus: 404 };
    if (first) { first = false; return { sessions: THREE() }; }
    return new Promise(r => { releases.push(r); });
  });
  bump(2);
  await settle();
  await mod.paint({ view: 'agents', sub: 's-a', unit: 'dollars' });
  releases[0]({ sessions: [...THREE(), row('s-late')] });
  await settle(); await settle();
  assert.equal(view.querySelector('.sessions-list'), null);
  assert.equal(view.querySelector('.session-id').textContent, 's-a');
  clearLiveHandler();
});
