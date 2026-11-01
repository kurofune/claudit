import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, keydown, Event } from './helpers/mini-dom.js';
import { contextCostTip, toolRows } from '../web/overview-tools-logic.js';

// The Overview payload, enough for the hotspot list to paint.
const hotspot = (title, cost) => ({ kind: 'bash_pattern', title, cost_usd: cost, pct_of_total: 40, prompt: `prompt for ${title}`, examples: [] });
const OVERVIEW = {
  totals: { CostUSD: 10, Sessions: 2, Turns: 9 },
  overall_hit_ratio: 0.9, total_tokens: 100, trend_totals: [], forecast: {}, unknown_models: [], period: 'day',
  hotspots: [hotspot('Bash: `go test`', 6), hotspot('Bash: `rg`', 3), hotspot('Read of `.go` files', 1)],
  providers: [],
};

// Rows in the shape /_claudit/api/tools ships (render.ToolRow). The
// server order is deliberately not context-cost order. TodoWrite costs
// nothing and Skill is a tool the hotspot list skips; Agent has no
// pattern rows.
const row = (tool, pattern, f) => ({ tool, ...(pattern ? { pattern } : {}), ...f });
const stats = (calls, results, errors, ms, p95, tokens, write, read) => ({
  calls, results, error_count: errors, error_rate: results ? errors / results : 0,
  timed_calls: ms == null ? 0 : calls, duration_median_ms: ms || 0, duration_p95_ms: p95 || 0,
  context_tokens: tokens, reread_turns: Math.round(tokens / 1000), context_write_usd: write, context_read_usd: read,
  context_cost_usd: Math.round((write + read) * 100) / 100,
});
const TOOLS = {
  sessions: 2,
  by_tool: [
    row('Read', '', stats(50, 50, 0, 40, 300, 9000, 0.5, 0.25)),
    row('Bash', '', stats(4, 3, 1, 1500, 90_000, 42_000, 1, 2)),
    row('TodoWrite', '', stats(2, 0, 0, null, null, 0, 0, 0)),
    row('Skill', '', stats(1, 1, 0, 200, 200, 3000, 0.1, 0.3)),
    row('Agent', '', stats(1, 0, 0, null, null, 500, 0.05, 0.05)),
  ],
  by_tool_detail: {
    Bash: [
      row('Bash', 'git status', stats(1, 1, 0, 80, 80, 2000, 0.1, 0.1)),
      row('Bash', 'go test', stats(3, 2, 1, 1500, 90_000, 40_000, 0.9, 1.9)),
    ],
    Read: [row('Read', '.go', stats(50, 50, 0, 40, 300, 9000, 0.5, 0.25))],
    TodoWrite: [row('TodoWrite', 'todos', stats(2, 0, 0, null, null, 0, 0, 0))],
    Skill: [row('Skill', 'review', stats(1, 1, 0, 200, 200, 3000, 0.1, 0.3))],
  },
};

// stubFetch answers /overview and /anomalies, and /tools from toolsResponses
// in turn (the last one repeats). { pending: true } never settles.
function stubFetch(toolsResponses) {
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    const path = String(url).replace('/_claudit/api', '').split('?')[0];
    const ok = body => ({ ok: true, status: 200, statusText: 'OK', json: async () => body });
    if (path === '/anomalies') return ok({ anomalies: [] });
    if (path === '/overview') return ok(OVERVIEW);
    const r = toolsResponses.length > 1 ? toolsResponses.shift() : toolsResponses[0];
    if (r.pending) return new Promise(() => {});
    if (r.status) return { ok: false, status: r.status, statusText: r.statusText || '', json: async () => ({}) };
    return ok(r);
  };
  return calls;
}

function stubStorage() {
  const m = new Map();
  globalThis.sessionStorage = { getItem: k => (m.has(k) ? m.get(k) : null), setItem: (k, v) => { m.set(k, String(v)); }, removeItem: k => { m.delete(k); } };
}

const settle = () => new Promise(r => setTimeout(r, 0));

let seq = 0;
async function mount(toolsResponses = [TOOLS], { hash = '#overview', search = '' } = {}) {
  const dom = installDOM({ hash, search });
  stubStorage();
  const scrolled = [];
  Object.getPrototypeOf(dom.document.body).scrollIntoView = function () { scrolled.push(this.id); };
  dom.document.body.innerHTML = '<span id="nav-metric-overview"></span><section id="view-overview"></section>';
  const calls = stubFetch(toolsResponses);
  const view = await import(`../web/view-overview.js?tools=${++seq}`);
  await view.paint();
  await settle();
  const root = dom.document.getElementById('view-overview');
  return { ...dom, view, calls, scrolled, root, button: root.querySelector('button.show-all') };
}

const toolsCalls = calls => calls.filter(u => u.replace(/\?.*/, '').endsWith('/tools')).length;
const overviewCalls = calls => calls.filter(u => u.includes('/overview')).length;
const table = root => root.querySelector('table.tool-patterns');
const visible = root => table(root).querySelectorAll('tbody tr').filter(tr => !tr.hidden);
// names is the first column top to bottom: a parent by its tool, a pattern
// as "· <pattern>".
const names = root => visible(root).map(tr => (tr.classList.contains('tool-parent')
  ? tr.dataset.tool
  : `· ${tr.children[0].textContent.trim()}`));
const header = (root, key) => table(root).querySelector(`thead th[data-key="${key}"]`);
const parent = (root, tool) => table(root).querySelectorAll('tr.tool-parent').find(tr => tr.dataset.tool === tool);
const patternsOf = (root, tool) => table(root).querySelectorAll('tr.tool-pattern').filter(tr => tr.dataset.tool === tool);
const cellsOf = tr => tr.children.map(td => td.textContent.trim());

async function opened(opts) {
  const m = await mount(undefined, opts);
  click(m.button);
  await settle();
  return m;
}

const DEFAULT_ORDER = ['Bash', '· go test', '· git status', 'Read', '· .go', 'Skill', '· review', 'Agent', 'TodoWrite', '· todos'];

test('under the hotspot list sits a collapsed Show all button and no /tools request is made before it is clicked', async () => {
  const { root, button, calls } = await mount();
  assert.ok(button);
  assert.equal(button.getAttribute('type'), 'button');
  assert.equal(button.getAttribute('aria-expanded'), 'false');
  assert.equal(button.textContent.trim(), 'Show all tool patterns');
  const kids = root.children;
  assert.ok(kids.indexOf(root.querySelector('.tool-patterns-section')) > kids.indexOf(root.querySelector('.hotspots')), 'the section follows the hotspot list');
  assert.equal(table(root), null);
  assert.equal(toolsCalls(calls), 0);
  assert.equal(overviewCalls(calls), 1);
});

for (const [how, activate] of [['click', b => click(b)], ['Enter', b => keydown(b, 'Enter')]]) {
  test(`${how} on Show all fetches /tools once and shows every pattern row under its tool`, async () => {
    const { root, button, calls } = await mount();
    activate(button);
    await settle();
    assert.equal(button.getAttribute('aria-expanded'), 'true');
    assert.equal(toolsCalls(calls), 1);
    assert.equal(overviewCalls(calls), 1, 'opening does not refetch the overview');
    const t = table(root);
    assert.ok(t);
    assert.equal(root.querySelector('.tool-patterns-head').textContent.trim(), '5 patterns across 5 tools');
    assert.deepEqual(t.querySelectorAll('thead th').map(th => th.textContent.trim()),
      ['Tool / Pattern', 'Calls', 'Error %', 'Median', 'p95', 'Context added', 'Context cost']);
    assert.deepEqual(names(root), DEFAULT_ORDER);
    const shown = t.querySelectorAll('tr.tool-pattern').map(tr => `${tr.dataset.tool}:${tr.children[0].textContent.trim()}`);
    const sent = Object.values(TOOLS.by_tool_detail).flat().map(r => `${r.tool}:${r.pattern}`);
    assert.deepEqual([...shown].sort(), [...sent].sort(), 'every by_tool_detail row appears');
    assert.deepEqual(cellsOf(patternsOf(root, 'TodoWrite')[0]), ['todos', '2', '—', '—', '—', '0', '$0.00'], 'the zero-cost row is shown');
    assert.deepEqual(cellsOf(patternsOf(root, 'Skill')[0]), ['review', '1', '0.0%', '200 ms', '200 ms', '3,000', '$0.40'], 'the Skill row is shown');

    activate(button);
    assert.equal(button.getAttribute('aria-expanded'), 'false');
    assert.ok(root.querySelector('.tool-patterns-body').hidden);
    activate(button);
    await settle();
    assert.equal(button.getAttribute('aria-expanded'), 'true');
    assert.equal(toolsCalls(calls), 1, 'reopening reuses the first fetch');
  });
}

test('each tool is a parent row with its by_tool totals and its pattern rows beneath; no details or subtabs', async () => {
  const { root } = await opened();
  const bash = parent(root, 'Bash');
  assert.ok(bash.classList.contains('tool-parent'));
  assert.equal(bash.getAttribute('aria-expanded'), 'true');
  assert.equal(bash.tabIndex, 0, 'a parent row is reachable by Tab');
  assert.deepEqual(cellsOf(bash), ['Bash 2 patterns', '4', '33.3%', '1.5 s', '1.5 min', '42,000', '$3.00']);
  const rows = table(root).querySelectorAll('tbody tr');
  const i = rows.indexOf(bash);
  assert.deepEqual(rows.slice(i + 1, i + 3).map(tr => [tr.className, tr.dataset.tool]), [['tool-pattern', 'Bash'], ['tool-pattern', 'Bash']]);
  assert.deepEqual(cellsOf(rows[i + 1]), ['go test', '3', '50.0%', '1.5 s', '1.5 min', '40,000', '$2.80']);
  const agent = parent(root, 'Agent');
  assert.equal(patternsOf(root, 'Agent').length, 0, 'a tool with no pattern rows renders its parent alone');
  assert.ok(rows[rows.indexOf(agent) + 1].classList.contains('tool-parent'));
  const section = root.querySelector('.tool-patterns-section');
  assert.equal(section.querySelectorAll('details').length, 0);
  assert.equal(section.querySelectorAll('nav, .subtabs, .subtab').length, 0);
});

for (const [how, activate] of [['click', tr => click(tr.children[0])], ['Enter', tr => keydown(tr, 'Enter')]]) {
  test(`${how} on a parent row collapses its patterns and a second ${how} re-expands them`, async () => {
    const { root } = await opened();
    const bash = parent(root, 'Bash');
    activate(bash);
    assert.equal(bash.getAttribute('aria-expanded'), 'false');
    assert.ok(patternsOf(root, 'Bash').every(tr => tr.hidden));
    assert.ok(patternsOf(root, 'Read').every(tr => !tr.hidden), 'other tools stay open');
    assert.deepEqual(names(root), DEFAULT_ORDER.filter(n => n !== '· go test' && n !== '· git status'));
    activate(bash);
    assert.equal(bash.getAttribute('aria-expanded'), 'true');
    assert.deepEqual(names(root), DEFAULT_ORDER);
  });
}

test('a column header sorts parents and patterns by that column, descending first, ascending next, default Context cost descending', async () => {
  const { root } = await opened();
  assert.equal(header(root, 'context_cost_usd').getAttribute('aria-sort'), 'descending');
  assert.equal(names(root)[0], 'Bash');

  click(header(root, 'calls').querySelector('button.th-sort'));
  assert.equal(header(root, 'calls').getAttribute('aria-sort'), 'descending');
  assert.equal(header(root, 'context_cost_usd').getAttribute('aria-sort'), 'none');
  assert.deepEqual(names(root), ['Read', '· .go', 'Bash', '· go test', '· git status', 'TodoWrite', '· todos', 'Skill', '· review', 'Agent']);

  keydown(header(root, 'calls').querySelector('button.th-sort'), 'Enter');
  assert.equal(header(root, 'calls').getAttribute('aria-sort'), 'ascending');
  assert.deepEqual(names(root), ['Skill', '· review', 'Agent', 'TodoWrite', '· todos', 'Bash', '· git status', '· go test', 'Read', '· .go']);

  click(header(root, 'tool'));
  assert.deepEqual(names(root).filter(n => !n.startsWith('·')), ['TodoWrite', 'Skill', 'Read', 'Bash', 'Agent']);
  click(header(root, 'tool'));
  assert.deepEqual(names(root).filter(n => !n.startsWith('·')), ['Agent', 'Bash', 'Read', 'Skill', 'TodoWrite']);
  assert.deepEqual(names(root).slice(1, 4), ['Bash', '· git status', '· go test'], 'patterns sort by pattern under the first column');
});

for (const key of ['error_pct', 'median_ms', 'p95_ms']) {
  test(`null ${key} shows "—" and sorts last in both directions`, async () => {
    const { root } = await opened();
    const col = ['tool', 'calls', 'error_pct', 'median_ms', 'p95_ms'].indexOf(key);
    for (const dir of ['descending', 'ascending']) {
      click(header(root, key));
      assert.equal(header(root, key).getAttribute('aria-sort'), dir);
      const parents = names(root).filter(n => !n.startsWith('·'));
      assert.deepEqual(parents.slice(-2).sort(), ['Agent', 'TodoWrite'], `${key} ${dir}`);
      assert.equal(parent(root, 'Agent').children[col].textContent, '—');
    }
  });
}

test('Filter rows narrows patterns by tool or pattern and hides a parent whose patterns all fail', async () => {
  const { root } = await opened();
  const input = root.querySelector('input.tp-filter');
  assert.equal(input.closest('label').textContent.trim(), 'Filter rows');
  input.value = 'git';
  input.dispatchEvent(new Event('input'));
  assert.deepEqual(names(root), ['Bash', '· git status']);
  input.value = 'READ';
  input.dispatchEvent(new Event('input'));
  assert.deepEqual(names(root), ['Read', '· .go']);
  input.value = 'agent';
  input.dispatchEvent(new Event('input'));
  assert.deepEqual(names(root), ['Agent'], 'a parent with no patterns matches by its name');
  input.value = 'zzz';
  input.dispatchEvent(new Event('input'));
  assert.equal(visible(root).length, 1);
  assert.equal(visible(root)[0].textContent.trim(), 'No matching rows.');
  input.value = '';
  input.dispatchEvent(new Event('input'));
  assert.deepEqual(names(root), DEFAULT_ORDER);
});

test('the Context cost, Error % and header tooltips are data-tooltips on focusable elements', async () => {
  const { root } = await opened();
  const bash = parent(root, 'Bash');
  const cost = bash.children[6];
  const [bashRow] = toolRows([TOOLS.by_tool[1]]);
  assert.equal(cost.getAttribute('data-tooltip'), contextCostTip(bashRow));
  assert.equal(cost.getAttribute('data-tooltip'), '42,000 tokens re-read across 42 turns · write $1.00 + read $2.00');
  assert.equal(cost.tabIndex, 0);
  assert.equal(bash.children[2].getAttribute('data-tooltip'), '1 of 3 results errored');
  assert.equal(bash.children[2].tabIndex, 0);
  const goTest = patternsOf(root, 'Bash').find(tr => tr.children[0].textContent.trim() === 'go test');
  assert.equal(goTest.children[2].getAttribute('data-tooltip'), '1 of 2 results errored');
  const tips = {
    calls: 'Times the tool was invoked.',
    error_pct: 'Errored results as a share of calls whose result was recorded. Calls with no recorded result are left out.',
    median_ms: "Median time from the end of the calling turn to the tool's result.",
    p95_ms: "95th-percentile time from the end of the calling turn to the tool's result.",
    context_tokens: "Cache tokens the tool's results added to context.",
    context_cost_usd: 'What those tokens cost: the cache write, plus every later turn that re-read them before the session ended or compacted.',
  };
  for (const [key, tip] of Object.entries(tips)) {
    const th = header(root, key);
    assert.equal(th.getAttribute('data-tooltip'), tip, key);
    assert.equal(th.querySelector('button.th-sort').tabIndex, 0, `${key} sort is reachable by Tab`);
  }
  assert.match(header(root, 'tool').getAttribute('data-tooltip'), /Tool name \(Read, Bash, Edit, Grep, Agent, …\)/);
  assert.equal(root.querySelector('.tool-patterns-section').querySelectorAll('[title]').length, 0, 'no native title tooltips');
});

test('#overview/all on first paint opens the table and scrolls it into view', async () => {
  const { root, button, scrolled, calls } = await mount(undefined, { hash: '#overview/all' });
  assert.equal(button.getAttribute('aria-expanded'), 'true');
  assert.ok(table(root));
  assert.deepEqual(scrolled, ['tool-patterns']);
  assert.equal(toolsCalls(calls), 1);
  assert.deepEqual(root.querySelectorAll('.hotspot-toggle').map(t => t.getAttribute('aria-expanded')), ['false', 'false', 'false']);
});

test('#overview/hotspot-N still opens hotspot N, and the table stays closed', async () => {
  const { root, button, scrolled, calls } = await mount(undefined, { hash: '#overview/hotspot-2' });
  assert.deepEqual(root.querySelectorAll('.hotspot-toggle').map(t => t.getAttribute('aria-expanded')), ['false', 'true', 'false']);
  assert.deepEqual(scrolled, ['hotspot-2']);
  assert.equal(button.getAttribute('aria-expanded'), 'false');
  assert.equal(toolsCalls(calls), 0);
});

test('a later #overview/all route opens the painted table without repainting the Overview', async () => {
  const { root, view, location, button, scrolled, calls } = await mount();
  location.hash = '#overview/all';
  await view.paint();
  await settle();
  assert.equal(button.getAttribute('aria-expanded'), 'true');
  assert.ok(table(root));
  assert.deepEqual(scrolled, ['tool-patterns']);
  assert.equal(overviewCalls(calls), 1);
});

test('opening Show all sets location.hash to #overview/all and collapsing it restores #overview', async () => {
  const { button, location } = await mount();
  click(button);
  await settle();
  assert.equal(location.hash, '#overview/all');
  click(button);
  assert.equal(location.hash, '#overview');
});

test('skeleton rows show while /tools loads', async () => {
  const { root, button } = await mount([{ pending: true }]);
  click(button);
  await settle();
  const t = table(root);
  assert.ok(t);
  const rows = t.querySelectorAll('tbody tr');
  assert.ok(rows.length > 0);
  assert.ok(rows.every(tr => tr.classList.contains('skel-tr') && tr.querySelector('.skeleton')));
  assert.equal(rows[0].children[0].getAttribute('colspan'), '7');
});

test('a 5xx from /tools shows the failure with a Retry that refetches, and the hotspots stay painted', async () => {
  const { root, button, calls } = await mount([{ status: 500 }, TOOLS]);
  click(button);
  await settle();
  const body = root.querySelector('.tool-patterns-body');
  const alert = body.querySelector('[role="alert"]');
  assert.ok(alert);
  assert.match(alert.textContent, /Failed to load tool patterns: HTTP 500/);
  assert.equal(root.querySelectorAll('.hotspot').length, 3, 'the hotspots above stay painted');
  const retry = alert.querySelector('button.retry');
  assert.equal(retry.getAttribute('type'), 'button');
  click(retry);
  await settle();
  assert.equal(toolsCalls(calls), 2);
  assert.ok(table(root));
  assert.deepEqual(names(root), DEFAULT_ORDER);
});

test('an empty by_tool shows "No tool calls in this window"', async () => {
  const { root, button } = await mount([{ sessions: 1, by_tool: [], by_tool_detail: {} }]);
  click(button);
  await settle();
  assert.equal(table(root), null);
  assert.equal(root.querySelector('.tool-patterns-body .empty-state').textContent.trim(), 'No tool calls in this window');
});

test('a provider filter with no sessions says which logs claudit reads', async () => {
  const { root, button } = await mount([{ sessions: 0, by_tool: [], by_tool_detail: {} }], { search: '?provider=codex' });
  click(button);
  await settle();
  assert.match(root.querySelector('.tool-patterns-body .empty-state').textContent, /^No Codex sessions in this range/);
});

// 25 tools × 16 patterns = 400 rows: above 100 the parents start collapsed,
// and opening each one renders every row.
test('400 pattern rows start collapsed and every one renders once its parent is opened', async () => {
  const byTool = [];
  const detail = {};
  for (let t = 0; t < 25; t++) {
    const tool = `Tool${String(t).padStart(2, '0')}`;
    byTool.push(row(tool, '', stats(16, 16, 1, 100, 900, 1000 * (t + 1), 0.01 * t, 0.02 * t)));
    detail[tool] = [];
    for (let p = 0; p < 16; p++) detail[tool].push(row(tool, `pattern-${p}`, stats(1, 1, 0, 50, 50, 10 * p, 0.001 * p, 0.001 * p)));
  }
  const { root, button } = await mount([{ sessions: 3, by_tool: byTool, by_tool_detail: detail }]);
  click(button);
  await settle();
  assert.equal(root.querySelector('.tool-patterns-head').textContent.trim(), '400 patterns across 25 tools');
  const parents = table(root).querySelectorAll('tr.tool-parent');
  assert.equal(parents.length, 25);
  assert.ok(parents.every(tr => tr.getAttribute('aria-expanded') === 'false'));
  assert.equal(table(root).querySelectorAll('tr.tool-pattern').filter(tr => !tr.hidden).length, 0);
  parents.forEach(tr => click(tr));
  const shown = table(root).querySelectorAll('tr.tool-pattern').filter(tr => !tr.hidden);
  assert.equal(shown.length, 400);
  assert.equal(new Set(shown.map(tr => `${tr.dataset.tool}/${tr.children[0].textContent.trim()}`)).size, 400);
});

test('up to 100 pattern rows start expanded', async () => {
  const { root } = await opened();
  assert.ok(table(root).querySelectorAll('tr.tool-parent[aria-expanded]').every(tr => tr.getAttribute('aria-expanded') === 'true'));
});
