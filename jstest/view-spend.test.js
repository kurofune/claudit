import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, keydown } from './helpers/mini-dom.js';
import { fmtCompact } from '../web/format.js';

// Payloads in the shapes /_claudit/api/cost and /tokens actually ship
// (CostPayload / TokensPayload in internal/render). Row order is the
// server's; it is deliberately not the order any column sorts to, so a
// sort is visible as a reordering.
const tok = (input, output, cw, cr) => ({
  InputTokens: input, OutputTokens: output, CacheCreate5mTokens: cw, CacheCreate1hTokens: 0, CacheReadTokens: cr,
});
const COST = {
  total_cost_usd: 10,
  by_model: [
    { Model: 'claude-opus-4-7', ...tok(100, 5000, 2000, 900000), CostUSD: 7, Turns: 30 },
    { Model: 'claude-haiku-4-5', ...tok(400, 9000, 100, 100), CostUSD: 3, Turns: 80 },
  ],
  by_project: [
    { Project: '/work/alpha', ...tok(1, 2, 3, 4), CostUSD: 6, Sessions: 2, DominantModel: 'claude-opus-4-7', Turns: 20 },
    { Project: '/work/beta', ...tok(1, 2, 3, 4), CostUSD: 4, Sessions: 9, DominantModel: 'claude-haiku-4-5', Turns: 90 },
  ],
  by_skill: [
    { Key: 'command:/review', Count: 2, TurnCount: 12, ...tok(1, 300, 3, 4), CostUSD: 2 },
    { Key: 'skill:tdd', Count: 7, TurnCount: 40, ...tok(1, 900, 3, 4), CostUSD: 1 },
  ],
  by_prompt: [
    { Key: 'fix the build', Sample: 'fix the build', Invocations: 1, Sessions: 1, TurnCount: 9, ...tok(1, 2, 3, 4), CostUSD: 5 },
    { Key: 'write tests', Sample: 'write tests', Invocations: 4, Sessions: 3, TurnCount: 30, ...tok(1, 2, 3, 4), CostUSD: 2 },
  ],
};
const tokRow = (label, input, output, cw, cr, pct, cost) => {
  const total = input + output + cw + cr;
  return { label, input, output, cache_write: cw, cache_read: cr, total, pct, cost_usd: cost };
};
// Every token category carries a distinct value per row, so a cell
// dropped, duplicated or shifted into a neighbour's column shows up.
const TOKENS = {
  total: 1_250_000,
  composition: [],
  trend: [
    { time: '2026-10-01T00:00:00Z', cost_usd: 4, turns: 50, sessions: 3, hit_ratio: 0.9, ...tok(200, 6000, 1000, 400000) },
    { time: '2026-10-02T00:00:00Z', cost_usd: 6, turns: 60, sessions: 4, hit_ratio: 0.9, ...tok(300, 8000, 1100, 500100) },
  ],
  by_model: [
    { model: 'claude-haiku-4-5', input: 400, output: 9000, cache_write: 100, cache_read: 200, total: 9700, pct: 0.8, cost_usd: 3 },
    { model: 'claude-opus-4-7', input: 100, output: 5000, cache_write: 2000, cache_read: 900000, total: 907100, pct: 99.2, cost_usd: 7 },
  ],
  by_project: [tokRow('/work/small', 11, 12, 13, 14, 1, 1), tokRow('/work/big', 1100, 1200, 1300, 1400, 99, 9)],
  by_subagent: [tokRow('reviewer', 5, 6, 7, 8, 9.1, 1), tokRow('Explore', 50, 60, 70, 80, 90.9, 2)],
  by_skill: [tokRow('skill:tdd', 2, 3, 4, 5, 9.1, 1), tokRow('command:/review', 20, 30, 40, 50, 90.9, 1)],
  by_prompt: [
    { ...tokRow('write tests', 3, 4, 5, 6, 9.1, 1), sample: 'write tests' },
    { ...tokRow('fix the build', 30, 40, 50, 60, 90.9, 4), sample: 'fix the build' },
  ],
  period: 'day',
};

function stubFetch({ fail = false } = {}) {
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(url);
    const path = String(url).replace('/_claudit/api', '').split('?')[0];
    const body = { '/cost': COST, '/tokens': TOKENS }[path];
    if (fail || !body) return { ok: false, status: 404, statusText: 'Not Found', json: async () => ({}) };
    return { ok: true, status: 200, statusText: 'OK', json: async () => body };
  };
  return calls;
}

// mount installs a fresh DOM and a fresh copy of view-spend.js (its
// load memo and mounted container are module state), then paints the
// route the hash names.
let seq = 0;
async function mount(hash, opts) {
  const dom = installDOM({ hash });
  dom.document.body.innerHTML = '<span id="nav-metric-cost"></span><section id="view-spend"></section>';
  stubFetch(opts);
  const view = await import(`../web/view-spend.js?mount=${++seq}`);
  const { parseHash } = await import('../web/router.js');
  await view.paint(parseHash());
  const root = dom.document.getElementById('view-spend');
  return { ...dom, view, root };
}

const unitBtn = (root, u) => root.querySelector(`.unit-opt[data-unit="${u}"]`);
const pane = (root, t) => root.querySelector(`.spend-pane[data-pane="${t}"]`);
const heads = table => table.querySelectorAll('thead th').map(th => th.textContent.trim());
const firstCol = table => table.querySelectorAll('tbody tr').map(tr => tr.children[0].textContent.trim());
const cells = table => table.querySelectorAll('tbody tr').map(tr => tr.children.map(td => td.textContent.trim()));
const tablesIn = (root, t) => pane(root, t).querySelectorAll('table[data-table]').map(x => x.dataset.table);
const navMetric = doc => doc.getElementById('nav-metric-cost').textContent;

const TOK_HEADS = ['Input', 'Output', 'Cache write', 'Cache read', 'Total', '%'];
// rows are the full rendered text of every body row, cell by cell, in
// column order — label, then each numeric column as the user reads it.
const PANES = {
  dollars: {
    model: { tables: ['model'], heads: ['Model', 'Cost', '%', 'Turns', 'Output tokens', 'Cache read tokens'], rows: [
      ['claude-opus-4-7', '$7.00', '70.0%', '30', '5,000', '900,000'],
      ['claude-haiku-4-5', '$3.00', '30.0%', '80', '9,000', '100'],
    ] },
    project: { tables: ['project'], heads: ['Project', 'Cost', '%', 'Sessions', 'Turns', 'Dominant model'], rows: [
      ['/work/alpha', '$6.00', '60.0%', '2', '20', 'claude-opus-4-7'],
      ['/work/beta', '$4.00', '40.0%', '9', '90', 'claude-haiku-4-5'],
    ] },
    subagents: { tables: ['mainside', 'subagent', 'invocation'], heads: ['Bucket', 'Cost', '%', 'Turns', 'Cache read'], rows: [
      ['Subagent data unavailable'],
    ] },
    skill: { tables: ['skill'], heads: ['Key', 'Calls', 'Turns', 'Cost', 'Output tokens'], rows: [
      ['command:/review', '2', '12', '$2.00', '300'],
      ['skill:tdd', '7', '40', '$1.00', '900'],
    ] },
    prompt: { tables: ['prompt'], heads: ['Snippet', 'Invocations', 'Sessions', 'Turns', 'Cost', '%'], rows: [
      ['fix the build', '1', '1', '9', '$5.00', '50.0%'],
      ['write tests', '4', '3', '30', '$2.00', '20.0%'],
    ] },
  },
  tokens: {
    model: { tables: ['tokmodel'], heads: ['Model', ...TOK_HEADS], rows: [
      ['claude-haiku-4-5', '400', '9,000', '100', '200', '9,700', '0.8%'],
      ['claude-opus-4-7', '100', '5,000', '2,000', '900,000', '907,100', '99.2%'],
    ] },
    project: { tables: ['tokproject'], heads: ['Project', ...TOK_HEADS], rows: [
      ['/work/small', '11', '12', '13', '14', '50', '1.0%'],
      ['/work/big', '1,100', '1,200', '1,300', '1,400', '5,000', '99.0%'],
    ] },
    subagents: { tables: ['toksubagent'], heads: ['Subagent', ...TOK_HEADS], rows: [
      ['reviewer', '5', '6', '7', '8', '26', '9.1%'],
      ['Explore', '50', '60', '70', '80', '260', '90.9%'],
    ] },
    skill: { tables: ['tokskill'], heads: ['Key', ...TOK_HEADS], rows: [
      ['skill:tdd', '2', '3', '4', '5', '14', '9.1%'],
      ['command:/review', '20', '30', '40', '50', '140', '90.9%'],
    ] },
    prompt: { tables: ['tokprompt'], heads: ['Snippet', ...TOK_HEADS], rows: [
      ['write tests', '3', '4', '5', '6', '18', '9.1%'],
      ['fix the build', '30', '40', '50', '60', '180', '90.9%'],
    ] },
  },
};

function assertUnit(root, doc, unit) {
  const other = unit === 'dollars' ? 'tokens' : 'dollars';
  assert.equal(unitBtn(root, unit).getAttribute('aria-checked'), 'true');
  assert.equal(unitBtn(root, other).getAttribute('aria-checked'), 'false');
  assert.equal(unitBtn(root, unit).tabIndex, 0, 'checked option is the tab stop');
  assert.equal(unitBtn(root, other).tabIndex, -1);
  for (const [t, want] of Object.entries(PANES[unit])) {
    assert.deepEqual(tablesIn(root, t), want.tables, `${unit}/${t} tables`);
    const table = pane(root, t).querySelector(`table[data-table="${want.tables[0]}"]`);
    assert.deepEqual(heads(table), want.heads, `${unit}/${t} columns`);
    assert.deepEqual(cells(table), want.rows, `${unit}/${t} rows`);
  }
  assert.equal(navMetric(doc), unit === 'tokens' ? fmtCompact(TOKENS.total) : 'claude-opus-4-7 · $7.00');
}

test('Spend mounts in Dollars from #spend/model with every pane filled', async () => {
  const { root, document } = await mount('#spend/model');
  assertUnit(root, document, 'dollars');
  assert.ok(pane(root, 'model').querySelector('#cost-bars-model .hbar'), 'model bars drawn');
});

test('Spend mounts straight into Tokens from #spend/project/tokens', async () => {
  const { root, document } = await mount('#spend/project/tokens');
  assertUnit(root, document, 'tokens');
  assert.ok(root.querySelector('.subview[data-subview="project"]').classList.contains('is-active'));
  assert.ok(pane(root, 'model').querySelector('.tok-trend-chart svg'), 'volume chart drawn');
});

test('clicking Tokens repaints all five panes, flips the toggle and replaces the hash', async () => {
  const { root, document, location, replaceCalls } = await mount('#spend/model');
  click(unitBtn(root, 'tokens'));
  assertUnit(root, document, 'tokens');
  assert.equal(location.hash, '#spend/model/tokens');
  assert.deepEqual(replaceCalls, ['#spend/model/tokens']);
  const hrefs = root.querySelectorAll('.subtab[data-subtab]').map(a => a.getAttribute('href'));
  assert.deepEqual(hrefs, ['model', 'project', 'subagents', 'skill', 'prompt'].map(t => `#spend/${t}/tokens`));

  click(unitBtn(root, 'dollars'));
  assertUnit(root, document, 'dollars');
  assert.equal(location.hash, '#spend/model');
});

test('clicking the already-checked unit leaves the hash and panes alone', async () => {
  const { root, replaceCalls } = await mount('#spend/model');
  const before = pane(root, 'model').querySelector('table');
  click(unitBtn(root, 'dollars'));
  assert.deepEqual(replaceCalls, []);
  assert.equal(pane(root, 'model').querySelector('table'), before);
});

test('arrow keys move the unit selection and focus with it, wrapping around', async () => {
  const { root, document, location } = await mount('#spend/skill');
  const group = root.querySelector('.unit-toggle');
  unitBtn(root, 'dollars').focus();
  keydown(unitBtn(root, 'dollars'), 'ArrowRight');
  assertUnit(root, document, 'tokens');
  assert.equal(document.activeElement, unitBtn(root, 'tokens'));
  assert.equal(location.hash, '#spend/skill/tokens');

  keydown(unitBtn(root, 'tokens'), 'ArrowRight');
  assertUnit(root, document, 'dollars');
  assert.equal(document.activeElement, unitBtn(root, 'dollars'));
  assert.equal(location.hash, '#spend/skill');

  keydown(group, 'ArrowLeft');
  assertUnit(root, document, 'tokens');
  keydown(group, 'Enter');
  assertUnit(root, document, 'tokens');
});

test('a section click keeps the unit in the replaced hash', async () => {
  const { root, location } = await mount('#spend/model/tokens');
  click(root.querySelector('.subtab[data-subtab="prompt"]'));
  assert.equal(location.hash, '#spend/prompt/tokens');
  assert.ok(root.querySelector('.subview[data-subview="prompt"]').classList.contains('is-active'));
  assert.ok(!root.querySelector('.subview[data-subview="model"]').classList.contains('is-active'));
});

test('a route change from the router repaints in the new unit', async () => {
  const { root, document, view } = await mount('#spend/model');
  await view.paint({ view: 'spend', sub: 'skill', unit: 'tokens' });
  assertUnit(root, document, 'tokens');
  assert.ok(root.querySelector('.subview[data-subview="skill"]').classList.contains('is-active'));
});

for (const unit of ['dollars', 'tokens']) {
  test(`${unit}: sortable headers are buttons in the tab order and sort the rows`, async () => {
    const { root } = await mount(unit === 'tokens' ? '#spend/model/tokens' : '#spend/model');
    for (const table of root.querySelectorAll('.spend-pane table[data-table]')) {
      for (const th of table.querySelectorAll('thead th[data-key]')) {
        const btn = th.querySelector(':scope > button.th-sort');
        assert.ok(btn, `${table.dataset.table}/${th.dataset.key} has a sort button`);
        assert.equal(btn.tabIndex, 0);
        assert.equal(btn.textContent, th.textContent);
        assert.equal(th.getAttribute('aria-sort'), 'none');
      }
    }
    const table = pane(root, 'model').querySelector('table[data-table]');
    const key = unit === 'tokens' ? 'total' : 'Turns';
    const th = table.querySelector(`thead th[data-key="${key}"]`);
    const start = firstCol(table);
    click(th.querySelector('button.th-sort'));
    assert.equal(th.getAttribute('aria-sort'), 'descending');
    assert.deepEqual(firstCol(table), unit === 'tokens'
      ? ['claude-opus-4-7', 'claude-haiku-4-5']
      : ['claude-haiku-4-5', 'claude-opus-4-7']);
    assert.notDeepEqual(firstCol(table), start);
    click(th.querySelector('button.th-sort'));
    assert.equal(th.getAttribute('aria-sort'), 'ascending');
    assert.deepEqual(firstCol(table), start);
  });
}

test('a failed load shows the error, and a unit switch retries', async () => {
  const { root, document } = await mount('#spend/model', { fail: true });
  assert.match(root.querySelector('.spend-status [role="alert"]').textContent, /Failed to load spend data/);
  assert.equal(root.querySelector('.spend-body').hidden, true);

  stubFetch();
  click(unitBtn(root, 'tokens'));
  await new Promise(r => setTimeout(r, 0));
  assert.equal(root.querySelector('.spend-status').textContent, '');
  assertUnit(root, document, 'tokens');
});
