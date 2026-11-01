process.env.TZ = 'UTC';

import { test, mock } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, keydown } from './helpers/mini-dom.js';
import { API_EQUIVALENT_TIP } from '../web/provider-filter.js';
import { explainPrompt } from '../web/session-header-logic.js';

const T0 = Date.parse('2026-05-01T10:00:00Z');
const at = sec => new Date(T0 + sec * 1000).toISOString();

const tool = (input, status, extra = {}) => ({
  name: 'Bash', detail: input.split(' ')[0], kind: 'exec', input, status, count: 1, output: '',
  input_key: '', exit_code: null, ...extra,
});
const tokens = (i = 0, o = 0, c5 = 0, c1 = 0, r = 0) =>
  ({ InputTokens: i, OutputTokens: o, CacheCreate5mTokens: c5, CacheCreate1hTokens: c1, CacheReadTokens: r });
const step = (uuid, sec, tools = [], extra = {}) => ({
  uuid, timestamp: at(sec), model: 'claude-opus-4-7', cost_usd: 0.01, tokens: tokens(), context_tokens: 0,
  duration_ms: 1000, gen_ms: 0, tools, context_cost_usd: 0, ...extra,
});
const SID = '0f1e2d3c-aaaa-bbbb-cccc-1234567890ab';
const story = (steps, children = [], extra = {}) => ({
  session_id: SID, title: 'fix the login bug', project: 'claudit', provider: 'codex', model: 'gpt-5.5',
  started_at: steps[0].timestamp, ended_at: steps[steps.length - 1].timestamp, prompts: [], page: {},
  steps, children, hidden: { replays: 0, outside_filter: 0 }, source_files: ['/logs/s1.jsonl'], ...extra,
});

// FAILED ends on three failed commands after a costly first turn, and has a
// sub-agent turn between its main turns.
const FAILED = () => story([
  step('a1', 0, [tool('rg login', 'ok')], { cost_usd: 2, context_cost_usd: 0.4, duration_ms: 5000, tokens: tokens(100, 50, 1000, 0, 3000) }),
  step('a2', 20, [tool('git diff', 'ok', { count: 2 })], { context_cost_usd: 0.1, duration_ms: 1000 }),
  step('a3', 40, [tool('npm test', 'error', { output: 'FAIL login.test.js' }), tool('make lint', 'error'), tool('go vet ./...', 'error')],
    { context_cost_usd: 0.2, duration_ms: 20000 }),
], [{
  kind: 'subagent', agent_type: 'Explore', description: '', parent_tool_use_id: '', started_at: at(10), ended_at: at(10),
  cost_usd: 0.01, error_count: 0, steps: [step('k1', 10, [tool('rg auth', 'ok')], { context_cost_usd: 0.05, duration_ms: 2000 })],
}], { started_at: at(0), ended_at: at(3725) });

const CLEAN = () => story([step('a1', 0), step('a2', 5), step('a3', 10)]);

function stubFetch(route) {
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    const r = await route(String(url));
    if (r && r.httpStatus) return { ok: false, status: r.httpStatus, statusText: '', json: async () => ({}) };
    return { ok: true, status: 200, statusText: 'OK', json: async () => r };
  };
  return calls;
}

const settle = () => new Promise(r => setTimeout(r, 0));

let seq = 0;
async function mount(route, { nowMs } = {}) {
  const env = installDOM({ hash: `#agents/${SID}` });
  const host = env.document.createElement('div');
  host.setAttribute('id', 'session-header');
  env.document.body.appendChild(host);
  const calls = stubFetch(typeof route === 'function' ? route : () => route);
  const mod = await import(`../web/session-header.js?m=${++seq}`);
  const done = mod.paintSessionHeader(host, SID, { nowMs: nowMs ?? T0 + 3725e3 });
  return { ...env, host, calls, mod, done };
}

const text = el => el.textContent.replace(/\s+/g, ' ').trim();
const heights = host => host.querySelectorAll('.turn-bar').map(b => b.getAttribute('style'));

test('the header names the session and fills the four tiles', async () => {
  const { host, calls, done } = await mount(FAILED());
  await done;
  assert.match(calls[0], /\/_claudit\/api\/sessions\/story\?session=0f1e2d3c-aaaa-bbbb-cccc-1234567890ab&whole=1$/);
  assert.equal(text(host.querySelector('.sh-title')), 'fix the login bug');
  assert.equal(text(host.querySelector('.sh-project')), 'claudit');
  assert.equal(text(host.querySelector('.sh-provider')), 'Codex');
  assert.ok(host.querySelector('.sh-provider').classList.contains('s-provider-codex'));
  assert.equal(text(host.querySelector('.sh-model')), 'gpt-5.5');
  assert.equal(text(host.querySelector('.sh-id')), '0f1e2d3c');
  assert.equal(host.querySelector('.sh-id').getAttribute('data-tooltip'), SID);
  assert.equal(text(host.querySelector('.sh-last')), 'Last activity 11:02:05');

  const tile = k => host.querySelector(`[data-tile="${k}"]`);
  assert.equal(text(tile('cost').querySelector('.sh-value')), '$2.03');
  assert.equal(tile('cost').querySelector('.api-eq').getAttribute('data-tooltip'), API_EQUIVALENT_TIP);
  assert.equal(text(tile('duration').querySelector('.sh-value')), '1h 2m');
  assert.equal(text(tile('duration').querySelector('.sh-sub')), '4 turns · 7 commands');
  assert.equal(text(tile('tokens').querySelector('.sh-value')), '4.2k');
  assert.equal(text(tile('tokens').querySelector('.sh-sub')), '73% re-read from cache');
  assert.equal(text(tile('failed').querySelector('.sh-value')), '3');
  assert.equal(text(tile('failed').querySelector('.sh-sub')), 'npm test, make lint +1 more');
  assert.ok(tile('failed').classList.contains('is-failed'));
});

test('the strip holds one button per turn in time order, Cost | Time switches by click and arrow keys', async () => {
  const { host, done } = await mount(FAILED());
  await done;
  const bars = host.querySelectorAll('.turn-bar');
  assert.equal(bars.length, 4);
  assert.ok(bars.every(b => b.localName === 'button' && b.getAttribute('type') === 'button'));
  assert.deepEqual(heights(host), ['height: max(2px, 100%)', 'height: max(2px, 12.5%)', 'height: max(2px, 25%)', 'height: max(2px, 50%)']);
  assert.deepEqual(bars.map(b => b.classList.contains('is-failed')), [false, false, false, true]);

  const group = host.querySelector('.sh-mode');
  assert.equal(group.getAttribute('role'), 'radiogroup');
  const [costBtn, timeBtn] = group.querySelectorAll('button');
  assert.equal(costBtn.getAttribute('aria-checked'), 'true');
  click(timeBtn);
  assert.equal(timeBtn.getAttribute('aria-checked'), 'true');
  assert.equal(costBtn.getAttribute('aria-checked'), 'false');
  assert.deepEqual(heights(host), ['height: max(2px, 25%)', 'height: max(2px, 10%)', 'height: max(2px, 5%)', 'height: max(2px, 100%)']);
  assert.equal(text(host.querySelector('.sh-strip-title')), 'Time per turn');
  keydown(timeBtn, 'ArrowLeft');
  assert.equal(costBtn.getAttribute('aria-checked'), 'true');
  assert.equal(heights(host)[0], 'height: max(2px, 100%)');
  keydown(costBtn, 'ArrowRight');
  assert.equal(timeBtn.getAttribute('aria-checked'), 'true');
});

test('a bar tooltip names the turn, its clock, the sub-agent, the mode value and the first command; click and Enter jump to the turn', async () => {
  const { host, done, location } = await mount(FAILED());
  await done;
  const bars = host.querySelectorAll('.turn-bar');
  assert.equal(bars[0].getAttribute('data-tooltip'), 'Turn 1 · 10:00:00 · $0.40 added to context · rg login');
  assert.equal(bars[1].getAttribute('data-tooltip'), 'Turn 2 · 10:00:10 · Explore · $0.05 added to context · rg auth');
  click(host.querySelector('.sh-mode button[data-mode="time"]'));
  assert.equal(host.querySelectorAll('.turn-bar')[3].getAttribute('data-tooltip'), 'Turn 4 · 10:00:40 · 20s · npm test');

  click(host.querySelectorAll('.turn-bar')[2]);
  assert.equal(location.hash, `#agents/${SID}/a2`);
  keydown(host.querySelectorAll('.turn-bar')[3], 'Enter');
  assert.equal(location.hash, `#agents/${SID}/a3`);
  keydown(host.querySelectorAll('.turn-bar')[1], 'Enter');
  assert.equal(location.hash, `#agents/${SID}/k1`, 'a sub-agent bar lands on the sub-agent turn it describes');
});

test('the strip is one tab stop; arrows move between bars', async () => {
  const { host, done, document } = await mount(FAILED());
  await done;
  const bars = () => host.querySelectorAll('.turn-bar');
  assert.deepEqual(bars().map(b => b.getAttribute('tabindex')), ['0', '-1', '-1', '-1']);
  keydown(bars()[0], 'ArrowRight');
  assert.deepEqual(bars().map(b => b.getAttribute('tabindex')), ['-1', '0', '-1', '-1']);
  assert.equal(document.activeElement, bars()[1]);
  keydown(bars()[1], 'End');
  assert.equal(document.activeElement, bars()[3]);
});

test('a 5,000-turn session draws 120 bars', async () => {
  const steps = [];
  for (let i = 0; i < 5000; i++) steps.push(step(`t${i + 1}`, i, [tool(`echo ${i}`, 'ok')], { context_cost_usd: 0.001 }));
  const { host, done, location } = await mount(story(steps));
  await done;
  const bars = host.querySelectorAll('.turn-bar');
  assert.equal(bars.length, 120);
  assert.equal(bars[1].getAttribute('data-tooltip'), 'turns 43–84 · $0.04 added to context');
  click(bars[1]);
  assert.equal(location.hash, `#agents/${SID}/t43`);
});

test('Worth a look: notes render as buttons with a bold head and a body, and jump to their turn', async () => {
  const { host, done, location } = await mount(FAILED());
  await done;
  const notes = host.querySelectorAll('.sh-notes .note');
  assert.equal(notes.length, 2);
  assert.ok(notes.every(n => n.localName === 'button' && n.getAttribute('type') === 'button'));
  assert.equal(text(notes[0].querySelector('strong.note-head')), '3 tools errored back-to-back, starting with Bash');
  assert.equal(text(notes[0].querySelector('.note-body')), 'At 10:00:40 · npm test · 3 consecutive tool errors starting with Bash — threshold 3');
  click(notes[0]);
  assert.equal(location.hash, `#agents/${SID}/a3`);
  keydown(notes[1], 'Enter');
  assert.equal(location.hash, `#agents/${SID}/a1`);
});

test('a session with no tool calls reads Failed 0, has no Worth a look section and still one bar per turn', async () => {
  const { host, done } = await mount(CLEAN());
  await done;
  assert.equal(text(host.querySelector('[data-tile="failed"] .sh-value')), '0');
  assert.equal(host.querySelector('.sh-notes'), null);
  assert.equal(host.querySelectorAll('.turn-bar').length, 3);
  assert.deepEqual(heights(host), ['height: max(2px, 0%)', 'height: max(2px, 0%)', 'height: max(2px, 0%)'], 'the 2px floor keeps zero-cost turns visible');
});

test('a tool-free session with a turn over the cost-whale floor still has no Worth a look section', async () => {
  const { host, done } = await mount(story([step('a1', 0, [], { cost_usd: 1, context_cost_usd: 0.3 })]));
  await done;
  assert.equal(text(host.querySelector('[data-tile="failed"] .sh-value')), '0');
  assert.equal(host.querySelector('.sh-notes'), null);
  assert.equal(host.querySelectorAll('.turn-bar').length, 1);
});

test('skeleton tiles and an empty strip show while the story loads', async () => {
  let release;
  const { host, done } = await mount(() => new Promise(r => { release = r; }));
  await settle();
  assert.equal(host.querySelectorAll('.sh-tile.is-loading').length, 4);
  assert.ok(host.querySelector('.sh-strip.is-empty'));
  assert.equal(host.querySelectorAll('.turn-bar').length, 0);
  release(CLEAN());
  await done;
  assert.equal(host.querySelectorAll('.sh-tile.is-loading').length, 0);
  assert.equal(host.querySelectorAll('.turn-bar').length, 3);
});

test('a 5xx shows the failure with a Retry that refetches', async () => {
  let fail = true;
  const { host, done, calls } = await mount(() => (fail ? { httpStatus: 500 } : CLEAN()));
  await done;
  assert.equal(text(host.querySelector('.sh-error strong')), 'Failed to load session: HTTP 500');
  fail = false;
  click(host.querySelector('.sh-error button.retry'));
  await settle(); await settle();
  assert.equal(calls.length, 2);
  assert.equal(host.querySelector('.sh-error'), null);
  assert.equal(host.querySelectorAll('.turn-bar').length, 3);
});

test('Copy writes the explain prompt to the clipboard and reads Copied for 2 s', async () => {
  const copied = [];
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText: async t => { copied.push(t); } } } });
  const { host, done } = await mount(FAILED());
  await done;
  mock.timers.enable({ apis: ['setTimeout'] });
  try {
    const btn = host.querySelector('.sh-copy');
    assert.equal(text(btn), 'Copy explain-this-session prompt');
    click(btn);
    await Promise.resolve(); await Promise.resolve();
    assert.deepEqual(copied, [explainPrompt(FAILED())]);
    assert.equal(text(btn), 'Copied');
    mock.timers.tick(1999);
    assert.equal(text(btn), 'Copied');
    mock.timers.tick(1);
    assert.equal(text(btn), 'Copy explain-this-session prompt');
  } finally {
    mock.timers.reset();
  }
});

test('Copy falls back to the textarea copy when the Clipboard API refuses', async () => {
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { clipboard: { writeText: async () => { throw new Error('denied'); } } } });
  const { host, done, document } = await mount(FAILED());
  await done;
  const copied = [];
  document.execCommand = cmd => { copied.push({ cmd, text: document.body.querySelector('textarea').value }); return true; };
  const btn = host.querySelector('.sh-copy');
  click(btn);
  await settle();
  assert.deepEqual(copied, [{ cmd: 'copy', text: explainPrompt(FAILED()) }]);
  assert.equal(document.body.querySelector('textarea'), null, 'the textarea is removed');
  assert.equal(text(btn), 'Copied');
});

test('the session page mounts the header above the story', async () => {
  const env = installDOM({ hash: `#agents/${SID}` });
  const view = env.document.createElement('section');
  view.setAttribute('id', 'view-agents');
  env.document.body.appendChild(view);
  stubFetch(url => (url.includes('whole=1') ? FAILED() : { httpStatus: 404 }));
  const mod = await import(`../web/view-sessions.js?m=${++seq}`);
  await mod.paint({ view: 'agents', sub: SID, unit: 'dollars' });
  const host = view.querySelector('#session-header');
  assert.ok(host, 'the page has the header host');
  assert.equal(host.querySelectorAll('.turn-bar').length, 4);
  assert.ok(view.querySelector('.session-story-host'));
});
