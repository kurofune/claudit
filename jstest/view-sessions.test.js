process.env.TZ = 'UTC';

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, keydown, Event } from './helpers/mini-dom.js';
import { start as startSSE, clearLiveHandler } from '../web/sse.js';
import { agentsGuide } from './helpers/agents-guide.js';

const NOW = Date.parse('2026-11-01T10:00:00Z');
const at = min => new Date(NOW + min * 60_000).toISOString();

const row = (id, f = {}) => ({
  session_id: id, family_key: 'fix the bug', remainder: '', title: 'fix the bug', outcome: 'Done.',
  project: 'claudit', provider: 'claude-code', model: 'claude-opus-5-5', cost_usd: 1, duration_ms: 60_000,
  turns: 3, commands: 2, failed: 0, last_activity: at(-30), active: false, started_at: at(-40), ...f,
});

const THREE = () => [
  row('s-a', { family_key: 'work bead _ next', remainder: 'claudit-9yx', title: 'work bead claudit-9yx next', first_failed_turn: 'turn-f1', last_activity: at(-1), cost_usd: 2.5, duration_ms: 125_000, failed: 2,
    outcome: 'All twelve acceptance criteria hold and the branch is committed.', active: true, started_at: at(-12) }),
  row('s-b', { family_key: 'work bead _ next', remainder: 'claudit-4eb', title: 'work bead claudit-4eb next', last_activity: at(-20), cost_usd: 9, compactions: 2 }),
  row('s-c', { family_key: 'review the diff', remainder: '', title: 'review the diff please', last_activity: at(-5), provider: 'codex', outcome: '' }),
  row('s-d', { family_key: 'retry _ times with a _ms timeout', remainder: '3 · 500', title: 'retry 3 times with a 500ms timeout', last_activity: at(-50), cost_usd: 1234.5, model: 'claude-fable-5-1' }),
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
async function mount(sub = '', { hash = '#agents', search = '', guide = true } = {}) {
  const env = installDOM({ hash, search });
  if (guide) {
    const tpl = env.document.createElement('template');
    tpl.setAttribute('id', 'agents-guide');
    tpl.innerHTML = agentsGuide();
    env.document.body.appendChild(tpl);
  }
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

test('three families render as groups ordered by their newest member, each headed by its stats', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const groups = view.querySelectorAll('.sl-group');
  assert.deepEqual(groups.map(g => g.querySelector('.sl-family').textContent),
    ['work bead … next', 'review the diff', 'retry … times with a …ms timeout']);
  assert.deepEqual(groups.map(g => g.querySelector('.sl-stats').textContent.replace(/\s+/g, ' ').trim()),
    ['2 runs · $11.50 · median $5.75 · 2 Opus', '1 run · $1.00 API-equivalent · Opus', '1 run · $1,235 · Fable']);
  const eq = groups[1].querySelector('.sl-stats .api-eq');
  assert.ok(eq, 'a Codex family labels its total API-equivalent');
  assert.match(eq.getAttribute('data-tooltip'), /API list rates/);
  assert.equal(groups[0].querySelector('.api-eq'), null, 'a family without Codex has no label');
  assert.deepEqual(groups[0].querySelectorAll('.sl-row').map(r => r.getAttribute('data-session')), ['s-a', 's-b']);
  assert.equal(view.ownerDocument.getElementById('nav-metric-agents').textContent, '4 sessions');
});

test('the top sentence counts the window, its cost and the unfixed failures, labelled API-equivalent with Codex in it', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const p = view.querySelector('.sl-sentence');
  assert.equal(p.textContent.replace(/\s+/g, ' ').trim(),
    '4 sessions in the last 7 days, $1,247 API-equivalent. 1 ended with unfixed failed commands ($2.50).');
  assert.match(p.querySelector('.api-eq').getAttribute('data-tooltip'), /API list rates/);
  const kids = view.querySelector('.sl-body').children;
  assert.deepEqual(kids.map(k => k.className.split(' ')[0]), ['sl-active', 'sl-sentence', 'sl-worth', 'sl-group', 'sl-group', 'sl-group'],
    'after the pinned Active rows, above the Worth opening block and the families');
});

test('a row shows the title, a visible "Last message:" line, a provider chip, cost, duration and failures', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount();
  await done;
  const a = view.querySelector('.sl-group [data-session="s-a"]');
  assert.equal(a.getAttribute('href'), '#agents/s-a');
  const lines = a.querySelector('.sl-main').children;
  assert.equal(lines[0].textContent, 'work bead claudit-9yx next', 'the title is the first line');
  assert.equal(lines[1].textContent, 'Last message: All twelve acceptance criteria hold and the branch is committed.');
  assert.equal(lines[1].getAttribute('title'), null, 'not a title tooltip');
  assert.equal(a.querySelector('.sl-provider').textContent, 'Claude Code');
  assert.equal(a.querySelector('.sl-cost').textContent, '$2.50');
  assert.equal(a.querySelector('.sl-dur').textContent, '2m 5s');
  assert.equal(a.querySelector('.sl-failed').textContent, '2 failed');

  const b = view.querySelector('.sl-group [data-session="s-b"]');
  assert.equal(b.querySelector('.sl-failed'), null, 'no failure count when failed is 0');
  const c = view.querySelector('.sl-group [data-session="s-c"]');
  assert.equal(c.querySelector('.sl-label').textContent, 'review the diff please');
  assert.equal(c.querySelector('.sl-outcome').textContent, 'Last message: (no agent message)');
  assert.equal(c.querySelector('.sl-provider').textContent, 'Codex');
  const d = view.querySelector('.sl-group [data-session="s-d"]');
  assert.equal(d.querySelector('.sl-cost').textContent, '$1,235');
});

test('Worth opening lists each session once with its reason, cost and last activity; a failed row opens its first failing turn', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done, location } = await mount();
  await done;
  const block = view.querySelector('.sl-worth');
  assert.equal(block.querySelector('.sl-worth-title').textContent, 'Worth opening');
  const rows = block.querySelectorAll('.sl-row');
  assert.deepEqual(rows.map(r => [r.getAttribute('data-session'), r.querySelector('.sl-reason').textContent]), [
    ['s-a', '2 failed commands never fixed'],
    ['s-b', 'Context compacted 2 times'],
    ['s-d', 'Most expensive session this week'],
  ]);
  assert.equal(rows[0].querySelector('.sl-label').textContent, 'work bead claudit-9yx next');
  assert.equal(rows[0].querySelector('.sl-cost').textContent, '$2.50');
  assert.equal(rows[0].querySelector('.sl-when').textContent, '1m ago');
  assert.equal(rows[0].getAttribute('href'), '#agents/s-a/turn-f1');
  assert.equal(rows[1].getAttribute('href'), '#agents/s-b');
  click(rows[0].querySelector('.sl-reason'));
  assert.equal(location.hash, '#agents/s-a/turn-f1');
  rows[1].focus();
  keydown(rows[1], 'Enter');
  assert.equal(location.hash, '#agents/s-b');
  rows[0].focus();
  keydown(rows[0], 'Enter');
  assert.equal(location.hash, '#agents/s-a/turn-f1');
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

test('ArrowDown and ArrowUp walk the Active, Worth opening and family rows; every row and control is tabbable', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done, document } = await mount();
  await done;
  const rows = view.querySelectorAll('.sl-body .sl-row');
  assert.deepEqual(rows.map(r => r.getAttribute('data-focus-key')),
    ['active:s-a', 'worth:s-a', 'worth:s-b', 'worth:s-d', 'row:s-a', 'row:s-b', 'row:s-c', 'row:s-d']);
  rows[3].focus();
  keydown(rows[3], 'ArrowDown');
  assert.equal(document.activeElement, rows[4], 'from the last Worth opening row into the first family row');
  keydown(rows[4], 'ArrowUp');
  assert.equal(document.activeElement, rows[3]);
  rows[5].focus();
  keydown(rows[5], 'ArrowDown');
  assert.equal(document.activeElement, rows[6], 'from the last row of one family to the first of the next');
  keydown(rows[6], 'ArrowUp');
  assert.equal(document.activeElement, rows[5]);
  rows[1].focus();
  keydown(rows[1], 'ArrowUp');
  assert.equal(document.activeElement, rows[0]);
  for (const el of [...rows, ...view.querySelectorAll('.sl-chip, .sl-sort, .sl-search, .api-eq')]) {
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
  assert.equal(r.querySelector('.sl-label').textContent, 'work bead claudit-9yx next');
  assert.equal(r.querySelector('.sl-outcome').textContent, 'Last message: All twelve acceptance criteria hold and the branch is committed.');
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
    assert.ok(path === '/sessions/story' || path === '/sessions/summary', path);
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
  assert.equal(view.querySelector('.sl-sentence'), null, 'no sentence');
  assert.equal(view.querySelector('.sl-worth'), null, 'no Worth opening block');
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
  assert.match(banner.textContent.replace(/\s+/g, ' '), /Could not load sessions: HTTP 500 Internal Server Error/);
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

test('a family starts collapsed to its 3 rows most worth opening behind "Show all N"; opening it focuses the first revealed row', async () => {
  const seven = Array.from({ length: 7 }, (_, i) => row(`s7-${i}`, {
    family_key: 'seven runs', title: `seven runs ${i}`, last_activity: at(-i), cost_usd: i + 1,
    failed: i === 5 ? 1 : 0, compactions: i === 2 ? 3 : 0,
  }));
  stubFetch(() => ({ sessions: [...seven, row('other', { family_key: 'other', last_activity: at(-60) })] }));
  const { view, done, document } = await mount();
  await done;
  const g = () => view.querySelector('.sl-group[data-family="seven runs"]');
  assert.deepEqual(g().querySelectorAll('.sl-row').map(r => r.getAttribute('data-session')), ['s7-2', 's7-5', 's7-6'],
    'failed, then compacted, then costliest, in the newest-first sort');
  const more = g().querySelector('.sl-more');
  assert.equal(more.textContent, 'Show all 7');
  assert.equal(more.tagName, 'BUTTON');
  assert.equal(view.querySelector('.sl-group[data-family="other"] .sl-more'), null, 'a 1-row family has no Show all');
  more.focus();
  keydown(more, 'Enter');
  click(more);
  assert.equal(g().querySelectorAll('.sl-row').length, 7);
  assert.equal(g().querySelector('.sl-more'), null);
  assert.equal(document.activeElement.getAttribute('data-focus-key'), 'row:s7-0', 'focus lands on the first revealed row');
});

test('the 3,000-row 88-family index draws the sentence, 8 Worth opening rows and every family collapsed to 3', async () => {
  const rows = Array.from({ length: 3000 }, (_, i) => row(`s${i}`, {
    family_key: `family _ ${i % 88}`, last_activity: at(-i), cost_usd: (i % 37) + 0.5, failed: i % 101 === 0 ? 1 : 0,
  }));
  stubFetch(() => ({ sessions: rows }));
  const { view, done } = await mount();
  await done;
  assert.match(view.querySelector('.sl-sentence').textContent, /^3,000 sessions in the last 7 days/);
  assert.equal(view.querySelectorAll('.sl-worth .sl-row').length, 8);
  const groups = view.querySelectorAll('.sl-group');
  assert.equal(groups.length, 88);
  assert.ok(groups.every(g => g.querySelectorAll('.sl-row').length === 3));
  assert.equal(groups[0].querySelector('.sl-more').textContent, 'Show all 35');
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
  const outside = view.querySelectorAll('.sl-family, .sl-active-title, .sl-chip, .sl-sort');
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
    if (path === '/sessions/story' || path === '/sessions/summary') return { httpStatus: 404 };
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

test('the list and the session page show the index.html guide under their heading', async () => {
  stubFetch(path => (path === '/sessions' ? { sessions: THREE() } : { httpStatus: 404 }));
  const list = await mount();
  await list.done;
  const head = list.view.querySelector('.view-head');
  assert.ok(head, 'the list has its heading');
  const guide = list.view.querySelector('.guide');
  assert.ok(guide, 'the list shows the guide');
  assert.equal(head.nextElementSibling, guide, 'the guide sits right under the heading');
  assert.equal(guide.querySelector('summary').textContent, 'How to read this section');
  assert.deepEqual(guide.querySelectorAll('strong').map(s => s.textContent), ['List', 'Header', 'Story']);

  const page = await mount('s-a', { hash: '#agents/s-a' });
  await page.done;
  const pageHead = page.view.querySelector('.session-head');
  assert.ok(pageHead, 'the session page has its heading');
  assert.ok(pageHead.nextElementSibling && pageHead.nextElementSibling.classList.contains('guide'), 'the session page shows the guide under its heading');
});

test('a shell without the guide template paints the list without one', async () => {
  stubFetch(() => ({ sessions: THREE() }));
  const { view, done } = await mount('', { guide: false });
  await done;
  assert.equal(view.querySelector('.guide'), null);
  assert.equal(view.querySelectorAll('.sl-group').length, 3);
});
