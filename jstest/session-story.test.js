process.env.TZ = 'UTC';

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, keydown, Event } from './helpers/mini-dom.js';
import { paintStory } from '../web/session-story.js';

const T0 = Date.parse('2026-11-01T09:00:00Z');
const at = sec => new Date(T0 + sec * 1000).toISOString();

const tool = (id, input, status, startSec, endSec, extra = {}) => ({
  id, name: 'Bash', detail: input.split(' ')[0], kind: 'exec', input, status, output: `out of ${input}`, count: 1,
  started_at: at(startSec), ended_at: at(endSec), exit_code: null, ...extra,
});
const step = (uuid, sec, f = {}) => ({
  uuid, timestamp: at(sec), model: 'claude-opus-5-5', cost_usd: 0.25, tokens: {}, context_tokens: 0,
  duration_ms: 1000, gen_ms: 0, tools: [], ...f,
});

const WRAPPED = '<environment_context>\n  <cwd>/repo</cwd>\n</environment_context>\nmake the tests pass';

const PROMPTS = [
  { uuid: 'u1', text: WRAPPED, timestamp: at(0), turns: 3 },
  { uuid: 'u2', text: 'now ship it', timestamp: at(1000), turns: 450 },
  { uuid: 'u3', text: 'thanks', timestamp: at(9000), turns: 1 },
];

const page0 = () => ({
  session_id: 's1',
  prompts: PROMPTS,
  page: { prompt_index: 0, turn_offset: 0, turn_limit: 200, has_more: false },
  steps: [
    step('a1', 5, { text: 'I will run the tests.', thinking: 'the tests live in ./...', tools: [
      tool('t1', 'go test ./...', 'error', 5, 9), tool('t2', 'go vet ./...', 'ok', 9, 10)] }),
    step('a2', 12, { tools: [tool('t3', 'cat long.txt', 'ok', 12, 13, { output: 'x'.repeat(500) + '…' })] }),
    step('a3', 20, { text: 'Tests still fail.' }),
  ],
  children: [],
  hidden: { replays: 0, outside_filter: 0 },
});

const longPage = offset => ({
  session_id: 's1', prompts: PROMPTS,
  page: { prompt_index: 1, turn_offset: offset, turn_limit: 200, has_more: offset + 200 < 450 },
  steps: Array.from({ length: Math.min(200, 450 - offset) }, (_, k) =>
    step(`b${offset + k}`, 1001 + offset + k, { text: `turn ${offset + k}`, cost_usd: 0.01 })),
  children: [], hidden: { replays: 0, outside_filter: 0 },
});

// stubFetch answers story and full requests through route(path, params).
function stubFetch(route) {
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    const u = new URL(String(url), 'http://x');
    const r = await route(u.pathname.replace('/_claudit/api', ''), u.searchParams);
    if (r && r.httpStatus) return { ok: false, status: r.httpStatus, statusText: r.statusText || '', json: async () => ({}) };
    return { ok: true, status: 200, statusText: 'OK', json: async () => r };
  };
  return calls;
}

const settle = () => new Promise(r => setTimeout(r, 0));

function mount() {
  const { document } = installDOM({ hash: '#agents/s1' });
  const host = document.createElement('div');
  document.body.appendChild(host);
  return { document, host };
}

test('the wrapper blocks of a prompt collapse into a disclosure that shows the raw text', async () => {
  const { host } = mount();
  stubFetch(() => page0());
  await paintStory(host, 's1');
  const you = host.querySelector('.story-you');
  assert.match(you.textContent, /You · 09:00:00/);
  assert.equal(you.querySelector('.story-text').textContent, 'make the tests pass');
  let btn = you.querySelector('.story-disclose');
  assert.equal(btn.textContent, 'AGENTS.md and environment context hidden · show');
  assert.equal(btn.getAttribute('aria-expanded'), 'false');
  assert.equal(host.querySelector('.story-wrapper-text'), null);
  click(btn);
  assert.equal(host.querySelector('.story-wrapper-text').textContent, '<environment_context>\n  <cwd>/repo</cwd>\n</environment_context>');
  btn = host.querySelector('.story-you .story-disclose');
  assert.equal(btn.textContent, 'AGENTS.md and environment context hidden · hide');
  click(btn);
  assert.equal(host.querySelector('.story-wrapper-text'), null);
});

test('rows render in order with the agent text verbatim; dollars never sit on a step row', async () => {
  const { host } = mount();
  stubFetch(() => page0());
  await paintStory(host, 's1');
  const rows = [...host.querySelector('.story-rows').children].map(el => el.className.split(' ')[0] === 'story-row' ? el.className.split(' ')[1] : el.className);
  assert.deepEqual(rows, ['story-you', 'story-agent', 'story-group', 'story-agent']);
  const agents = host.querySelectorAll('.story-agent');
  assert.match(agents[0].querySelector('.story-who').textContent, /^Agent · 09:00:05 \$0\.25$/);
  assert.equal(agents[0].querySelector('.story-text').textContent, 'I will run the tests.');
  assert.equal(host.querySelector('.story-group-count').textContent, '3 commands · 1 unresolved · 8s');
  assert.equal(host.querySelector('.story-group-h .story-cost').textContent, '$0.50');
  const steps = host.querySelectorAll('.story-step');
  assert.equal(steps.length, 3);
  for (const s of steps) assert.ok(!s.textContent.includes('$'), `step row has a dollar: ${s.textContent}`);
  assert.deepEqual(steps.map(s => s.querySelector('.story-mark').textContent), ['✕', '✓', '✓']);
  assert.equal(steps[0].querySelector('.story-clock').textContent, '09:00:05');
  assert.equal(steps[0].querySelector('.story-cmd').getAttribute('title'), 'go test ./...');
  assert.equal(steps[0].querySelector('.story-dur').textContent, '4s');
  // Expanded rows still carry no dollar on the row itself.
  click(steps[0]);
  for (const s of host.querySelectorAll('.story-step')) assert.ok(!s.textContent.includes('$'));
});

test('a step row expands on click and on Enter to thinking, input and output; Show full swaps the snippet', async () => {
  const { host, document } = mount();
  const calls = stubFetch((path, q) => {
    if (path === '/agents/full') return { id: q.get('tool'), name: 'Bash', input: 'cat long.txt', output: 'FULL OUTPUT', status: 'ok' };
    return page0();
  });
  await paintStory(host, 's1');
  let row = host.querySelectorAll('.story-step')[0];
  click(row);
  let detail = host.querySelector('[data-detail="s0.0"]');
  assert.ok(detail, 'expanded');
  assert.equal(host.querySelectorAll('.story-step')[0].getAttribute('aria-expanded'), 'true');
  const pres = detail.querySelectorAll('.story-pre').map(p => p.textContent);
  assert.deepEqual(pres, ['the tests live in ./...', 'go test ./...', 'out of go test ./...']);
  click(host.querySelectorAll('.story-step')[0]);
  assert.equal(host.querySelector('[data-detail="s0.0"]'), null, 'collapsed again');

  row = host.querySelectorAll('.story-step')[2];
  row.focus();
  keydown(row, 'Enter');
  detail = host.querySelector('[data-detail="s1.0"]');
  assert.ok(detail, 'Enter expands');
  assert.equal(document.activeElement.getAttribute('data-step'), 's1.0', 'focus stays on the row');
  const full = detail.querySelector('.story-full');
  assert.equal(full.textContent, 'Show full');
  click(full);
  await settle(); await settle();
  assert.ok(calls.some(u => u.includes('/agents/full?session=s1&tool=t3')));
  detail = host.querySelector('[data-detail="s1.0"]');
  assert.equal(detail.querySelectorAll('.story-pre')[1].textContent, 'FULL OUTPUT');
  assert.equal(detail.querySelector('.story-full'), null);
});

test('a Codex step with an exit code shows the chip and its full output without the header lines', async () => {
  const { host } = mount();
  stubFetch((path, q) => {
    if (path === '/agents/full') return { id: q.get('tool'), name: 'Bash', input: 'make', output: 'Exit code: 2\nWall time: 1 seconds\nOutput:\nmake: *** boom', status: 'error' };
    const p = page0();
    p.steps = [step('r1', 1, { text: 'build', tools: [tool('c1', 'make', 'error', 1, 2, { exit_code: 2, output: 'make: ***…' })] })];
    return p;
  });
  await paintStory(host, 's1');
  click(host.querySelector('.story-step'));
  const detail = host.querySelector('.story-detail');
  assert.equal(detail.querySelector('.story-exit').textContent, 'exit code 2');
  assert.equal(detail.querySelectorAll('.story-pre')[1].textContent, 'make: ***…');
  click(detail.querySelector('.story-full'));
  await settle(); await settle();
  const out = host.querySelector('.story-detail').querySelectorAll('.story-pre')[1].textContent;
  assert.equal(out, 'make: *** boom');
});

test('a deep-linked turn opens the page that carries it and flashes its row', async () => {
  const { host } = mount();
  const calls = stubFetch((path, q) => (q.get('turn') ? longPage(200) : page0()));
  await paintStory(host, 's1', 'b250');
  assert.ok(calls[0].includes('/sessions/story?session=s1&turn=b250'), calls[0]);
  assert.equal(host.querySelector('.story-pos').textContent, 'Prompt 2 of 3');
  const row = host.querySelector('[data-turn="b250"]');
  assert.ok(row.classList.contains('is-jumped'));
  assert.equal(host.querySelectorAll('.is-jumped').length, 1);
  const earlier = host.querySelector('[data-earlier]');
  assert.match(earlier.textContent, /^200 earlier turns in this prompt/);
});

test('an unknown deep-linked turn falls back to the first prompt', async () => {
  const { host } = mount();
  const calls = stubFetch((path, q) => (q.get('turn') ? { httpStatus: 404 } : page0()));
  await paintStory(host, 's1', 'gone');
  assert.equal(calls.length, 2);
  assert.equal(host.querySelector('.story-pos').textContent, 'Prompt 1 of 3');
});

test('prompt paging: Next and Previous prompt, the jump list, and N more turns appending 200', async () => {
  const { host } = mount();
  const calls = stubFetch((path, q) => {
    const p = Number(q.get('prompt'));
    if (p === 1) return longPage(Number(q.get('offset')));
    if (p === 2) return { ...page0(), page: { prompt_index: 2, turn_offset: 0, turn_limit: 200, has_more: false }, steps: [step('c1', 9001, { text: 'bye' })] };
    return page0();
  });
  await paintStory(host, 's1');
  assert.ok(host.querySelector('[data-prev]').disabled);
  assert.equal(host.querySelectorAll('.story-jump-item').length, 3);
  click(host.querySelector('[data-next]'));
  await settle(); await settle();
  assert.ok(calls.at(-1).endsWith('prompt=1&offset=0'));
  assert.equal(host.querySelector('.story-pos').textContent, 'Prompt 2 of 3');
  assert.equal(host.querySelectorAll('.story-agent').length, 200);
  const more = host.querySelector('[data-more]');
  assert.equal(more.textContent, '250 more turns · $2.00 so far');
  click(more);
  await settle(); await settle();
  assert.ok(calls.at(-1).endsWith('prompt=1&offset=200'));
  assert.equal(host.querySelectorAll('.story-agent').length, 400);
  assert.equal(host.querySelector('[data-more]').textContent, '50 more turns · $4.00 so far');
  click(host.querySelector('[data-jump="2"]'));
  await settle(); await settle();
  assert.equal(host.querySelector('.story-pos').textContent, 'Prompt 3 of 3');
  assert.ok(host.querySelector('[data-next]').disabled);
  click(host.querySelector('[data-prev]'));
  await settle(); await settle();
  assert.equal(host.querySelector('.story-pos').textContent, 'Prompt 2 of 3');
});

test('hidden history is disclosed at the top, and nothing shows at zero', async () => {
  const { host } = mount();
  stubFetch(() => ({ ...page0(), hidden: { outside_filter: 12, replays: 3 } }));
  await paintStory(host, 's1');
  assert.deepEqual(host.querySelectorAll('.story-notice').map(n => n.textContent), [
    '12 earlier turns outside the date filter', '3 replayed turns from a resumed session folded']);
  stubFetch(() => page0());
  await paintStory(host, 's1');
  assert.equal(host.querySelectorAll('.story-notice').length, 0);
});

test('search: Enter jumps to the next match, wrapping; Esc clears; no match says so', async () => {
  const { host } = mount();
  stubFetch(() => page0());
  await paintStory(host, 's1');
  const box = host.querySelector('.story-search');
  box.value = 'GO ';
  keydown(box, 'Enter');
  assert.ok(host.querySelector('[data-key="s0.0"]').classList.contains('is-jumped'));
  assert.equal(host.querySelector('.story-find-status').textContent, '1 of 2');
  keydown(box, 'Enter');
  assert.ok(host.querySelector('[data-key="s0.1"]').classList.contains('is-jumped'));
  assert.ok(!host.querySelector('[data-key="s0.0"]').classList.contains('is-jumped'));
  keydown(box, 'Enter');
  assert.ok(host.querySelector('[data-key="s0.0"]').classList.contains('is-jumped'), 'wraps around');
  keydown(box, 'Escape');
  assert.equal(box.value, '');
  assert.equal(host.querySelector('.story-find-status').textContent, '');
  box.value = 'tests pass';
  box.dispatchEvent(new Event('input', { bubbles: true }));
  keydown(box, 'Enter');
  assert.ok(host.querySelector('[data-key="p"]').classList.contains('is-jumped'), 'prompt text matches');
  box.value = 'zebra';
  keydown(box, 'Enter');
  assert.equal(host.querySelector('.story-find-status').textContent, 'No step matches');
});

test('states: skeleton while loading, 404, an error banner whose Retry refetches, and no turns', async () => {
  let { host } = mount();
  stubFetch(() => new Promise(() => {}));
  paintStory(host, 's1');
  await settle();
  assert.ok(host.querySelector('.story-skel .skeleton'));

  ({ host } = mount());
  stubFetch(() => ({ httpStatus: 404 }));
  await paintStory(host, 's1');
  assert.match(host.textContent, /Session not in this window/);
  assert.equal(host.querySelector('a.story-back').getAttribute('href'), '#agents');
  assert.equal(host.querySelector('a.story-back').textContent, 'Back to sessions');

  ({ host } = mount());
  let n = 0;
  const calls = stubFetch(() => (++n === 1 ? { httpStatus: 500, statusText: 'Internal Server Error' } : page0()));
  await paintStory(host, 's1');
  const banner = host.querySelector('.story-error');
  assert.match(banner.textContent, /HTTP 500 Internal Server Error/);
  click(banner.querySelector('[data-retry]'));
  await settle(); await settle();
  assert.equal(calls.length, 2);
  assert.ok(host.querySelector('.story-you'));

  ({ host } = mount());
  stubFetch(() => ({ session_id: 's1', prompts: [], page: { prompt_index: 0, turn_offset: 0, turn_limit: 200, has_more: false }, steps: [], children: [], hidden: { replays: 0, outside_filter: 0 } }));
  await paintStory(host, 's1');
  assert.match(host.querySelector('.story-empty').textContent, /No turns recorded/);
});

test('a sub-agent nests collapsed under its spawning step and expands to its own steps', async () => {
  const { host } = mount();
  stubFetch(() => {
    const p = page0();
    p.steps = [step('a1', 1, { text: 'delegate', tools: [tool('ag1', 'find callers', 'ok', 1, 90, { name: 'Agent' })] })];
    p.children = [{ kind: 'subagent', agent_type: 'Explore', description: 'find callers', parent_tool_use_id: 'ag1',
      started_at: at(2), ended_at: at(85), cost_usd: 0.42, error_count: 0, steps: [step('x1', 3, { text: 'looking', tools: [tool('xt', 'rg Foo', 'ok', 3, 4)] })] }];
    return p;
  });
  await paintStory(host, 's1');
  const h = host.querySelector('.story-group .story-child-h');
  assert.match(h.textContent, /Explore · find callers · \$0\.42 · 1m 23s · 1 step/);
  assert.equal(h.getAttribute('aria-expanded'), 'false');
  assert.equal(host.querySelector('.story-child-body'), null);
  click(h);
  const body = host.querySelector('.story-child-body');
  assert.equal(body.querySelector('.story-agent .story-text').textContent, 'looking');
  click(body.querySelector('.story-step'));
  assert.ok(host.querySelector('.story-child-body .story-detail'));
});

// deferred is a promise with its resolve exposed, to land a response late.
function deferred() {
  let resolve;
  const promise = new Promise(r => { resolve = r; });
  return { promise, resolve };
}

const pageOf = (sid, text) => ({ ...page0(), session_id: sid, prompts: [{ uuid: 'u', text, timestamp: at(0), turns: 1 }],
  steps: [step(`${sid}-a`, 1, { text: `${sid} message`, tools: [tool(`${sid}-t`, 'ls', 'ok', 1, 2, { output: 'cut…' })] })] });

test('a remount drops the previous story\'s page when it lands late', async () => {
  const { host } = mount();
  const old = deferred();
  stubFetch((path, q) => (q.get('session') === 'OLD' ? old.promise : pageOf('NEW', 'new prompt')));
  const first = paintStory(host, 'OLD');
  await paintStory(host, 'NEW');
  assert.equal(host.querySelector('.story-you .story-text').textContent, 'new prompt');
  old.resolve(pageOf('OLD', 'old prompt'));
  await first; await settle();
  assert.equal(host.querySelector('.story-you .story-text').textContent, 'new prompt');
  assert.equal(host.querySelector('.story-agent .story-text').textContent, 'NEW message');
});

test('a Show full answer after a remount or a page change does not repaint the stale story', async () => {
  const { host } = mount();
  const full = deferred();
  stubFetch((path, q) => {
    if (path === '/agents/full') return full.promise;
    return pageOf(q.get('session'), `${q.get('session')} prompt`);
  });
  await paintStory(host, 'OLD');
  click(host.querySelector('.story-step'));
  click(host.querySelector('.story-detail .story-full'));
  await paintStory(host, 'NEW');
  full.resolve({ id: 'OLD-t', name: 'Bash', input: 'ls', output: 'OLD FULL', status: 'ok' });
  await settle(); await settle();
  assert.equal(host.querySelector('.story-you .story-text').textContent, 'NEW prompt');
  assert.ok(!host.textContent.includes('OLD'), 'nothing of the old story repainted');
});

test('a step row shows a counted call\'s runs and the whole input in its title and detail', async () => {
  const { host } = mount();
  const whole = 'echo ' + 'x'.repeat(600) + ' END';
  stubFetch(() => {
    const p = page0();
    p.steps = [step('a1', 1, { text: 'go', tools: [
      tool('t1', 'go test ./...', 'ok', 1, 2, { count: 7 }),
      tool('t2', 'echo ' + 'x'.repeat(495) + '…', 'ok', 2, 3, { input_full: whole, input_key: 'k2' })] })];
    return p;
  });
  await paintStory(host, 's1');
  const rows = host.querySelectorAll('.story-step');
  assert.equal(rows[0].querySelector('.story-runs').textContent, '×7');
  assert.match(host.querySelector('.story-group-count').textContent, /^8 commands · /);
  assert.equal(rows[1].querySelector('.story-cmd').getAttribute('title'), whole);
  click(rows[1]);
  const detail = host.querySelector('[data-detail="s0.1"]');
  assert.equal(detail.querySelectorAll('.story-pre')[0].textContent, whole);
});

test('a parallel member spawned by an earlier turn links back to its spawning call', async () => {
  const { host } = mount();
  stubFetch(() => {
    const p = page0();
    p.steps = [
      step('a1', 0, { text: 'fan out', tools: [tool('ag1', 'Agent one', 'ok', 0, 200, { name: 'Agent' })] }),
      step('a2', 10, { tools: [tool('ag2', 'Agent two', 'ok', 10, 200, { name: 'Agent' })] }),
    ];
    p.children = [
      { kind: 'subagent', agent_type: 'Explore', description: 'scan web', parent_tool_use_id: 'ag1', started_at: at(1), ended_at: at(150), cost_usd: 0.1, error_count: 0, steps: [] },
      { kind: 'subagent', agent_type: 'Explore', description: 'scan api', parent_tool_use_id: 'ag2', started_at: at(11), ended_at: at(100), cost_usd: 0.1, error_count: 0, steps: [] },
    ];
    return p;
  });
  await paintStory(host, 's1');
  const par = host.querySelector('.story-parallel');
  assert.match(par.querySelector('.story-parallel-h').textContent, /^2 agents in parallel · longest: scan web/);
  const link = par.querySelector('.story-spawn');
  assert.equal(link.textContent, 'spawned at 09:00:00');
  click(link);
  assert.ok(host.querySelector('[data-key="s0.0"]').classList.contains('is-jumped'));
});
