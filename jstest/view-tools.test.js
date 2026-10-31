import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, Event } from './helpers/mini-dom.js';

// Rows in the shape /_claudit/api/tools ships (render.ToolRow). The
// server order is deliberately not context-cost order, so the default
// sort shows up as a reordering.
const row = (tool, pattern, f) => ({ tool, ...(pattern ? { pattern } : {}), ...f });
const TOOLS = {
  by_tool: [
    row('Read', '', { calls: 50, results: 50, error_count: 0, error_rate: 0, timed_calls: 50, duration_median_ms: 40, duration_p95_ms: 300,
      context_tokens: 9000, reread_turns: 12, context_write_usd: 0.5, context_read_usd: 0.25, context_cost_usd: 0.75 }),
    row('Bash', '', { calls: 4, results: 3, error_count: 1, error_rate: 1 / 3, timed_calls: 3, duration_median_ms: 1500, duration_p95_ms: 90_000,
      context_tokens: 42_000, reread_turns: 30, context_write_usd: 1, context_read_usd: 2, context_cost_usd: 3 }),
    row('TodoWrite', '', { calls: 2, results: 0, error_count: 0, error_rate: 0, timed_calls: 0, duration_median_ms: 0, duration_p95_ms: 0,
      context_tokens: 0, reread_turns: 0, context_write_usd: 0, context_read_usd: 0, context_cost_usd: 0 }),
  ],
  by_tool_detail: {
    Bash: [
      row('Bash', 'git status', { calls: 1, results: 1, error_count: 0, error_rate: 0, timed_calls: 1, duration_median_ms: 80, duration_p95_ms: 80,
        context_tokens: 2000, reread_turns: 5, context_write_usd: 0.1, context_read_usd: 0.1, context_cost_usd: 0.2 }),
      row('Bash', 'go test', { calls: 3, results: 2, error_count: 1, error_rate: 0.5, timed_calls: 2, duration_median_ms: 1500, duration_p95_ms: 90_000,
        context_tokens: 40_000, reread_turns: 25, context_write_usd: 0.9, context_read_usd: 1.9, context_cost_usd: 2.8 }),
    ],
    Read: [
      row('Read', '.go', { calls: 50, results: 50, error_count: 0, error_rate: 0, timed_calls: 50, duration_median_ms: 40, duration_p95_ms: 300,
        context_tokens: 9000, reread_turns: 12, context_write_usd: 0.5, context_read_usd: 0.25, context_cost_usd: 0.75 }),
    ],
  },
};

function stubFetch(responses) {
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    const r = responses.length > 1 ? responses.shift() : responses[0];
    if (r.pending) return new Promise(() => {});
    if (r.status) return { ok: false, status: r.status, statusText: r.statusText, json: async () => ({}) };
    return { ok: true, status: 200, statusText: 'OK', json: async () => r };
  };
  return calls;
}

let seq = 0;
async function mount(responses, { await: wait = true } = {}) {
  const dom = installDOM({ hash: '#tools' });
  dom.document.body.innerHTML = '<span id="nav-metric-tools"></span><section id="view-tools" data-own-filter></section>';
  const calls = stubFetch(responses);
  const view = await import(`../web/view-tools.js?mount=${++seq}`);
  const painting = view.paint({ view: 'tools', sub: '' });
  if (wait) await painting;
  return { ...dom, view, calls, root: dom.document.getElementById('view-tools') };
}

const heads = table => table.querySelectorAll('thead th').map(th => th.textContent.trim());
const cells = table => table.querySelectorAll('tbody tr').filter(tr => !tr.hidden)
  .map(tr => tr.children.map(td => td.textContent.trim()));
const firstCol = table => cells(table).map(r => r[0]);
const toolTable = root => root.querySelector('[data-table="tool"]');
const header = (table, key) => table.querySelector(`thead th[data-key="${key}"]`);
const COLUMNS = ['Tool', 'Calls', 'Error %', 'Median', 'p95', 'Context added', 'Context cost'];

test('the by-tool table shows the seven columns sorted by context cost descending', async () => {
  const { root, document, calls } = await mount([TOOLS]);
  const table = toolTable(root);
  assert.deepEqual(heads(table), COLUMNS);
  assert.deepEqual(cells(table), [
    ['Bash', '4', '33.3%', '1.5 s', '1.5 min', '42,000', '$3.00'],
    ['Read', '50', '0.0%', '40 ms', '300 ms', '9,000', '$0.75'],
    ['TodoWrite', '2', '—', '—', '—', '0', '$0.00'],
  ]);
  assert.equal(header(table, 'context_cost_usd').getAttribute('aria-sort'), 'descending');
  assert.deepEqual(calls.map(u => u.replace(/\?.*/, '')), ['/_claudit/api/tools'], 'no /cost fetch');
  assert.equal(document.getElementById('nav-metric-tools').textContent, 'Bash · $3.00');
});

test('a Context cost cell carries the tokens, re-read turns, write and read tooltip', async () => {
  const { root } = await mount([TOOLS]);
  const bash = toolTable(root).querySelectorAll('tbody tr')[0];
  assert.equal(bash.children[6].getAttribute('title'),
    '42,000 tokens re-read across 30 turns · write $1.00 + read $2.00');
});

test('no heat shading and no italics anywhere in the view', async () => {
  const { root } = await mount([TOOLS]);
  assert.equal(root.querySelectorAll('.heat').length, 0);
  assert.equal(root.querySelectorAll('em').length + root.querySelectorAll('i').length, 0);
  assert.ok(!root.innerHTML.includes('What "tool cost" means'));
});

test('clicking a header sorts by it; clicking again reverses', async () => {
  const { root } = await mount([TOOLS]);
  const table = toolTable(root);
  click(header(table, 'calls').querySelector('button'));
  assert.deepEqual(firstCol(table), ['Read', 'Bash', 'TodoWrite']);
  assert.equal(header(table, 'calls').getAttribute('aria-sort'), 'descending');
  click(header(table, 'calls').querySelector('button'));
  assert.deepEqual(firstCol(table), ['TodoWrite', 'Bash', 'Read']);
  assert.equal(header(table, 'calls').getAttribute('aria-sort'), 'ascending');
  click(header(table, 'error_pct').querySelector('button'));
  assert.deepEqual(firstCol(table), ['Bash', 'Read', 'TodoWrite'], 'unknown error rate sorts last');
});

test('every sortable header is a focusable button', async () => {
  const { root } = await mount([TOOLS]);
  const btns = toolTable(root).querySelectorAll('thead th[data-key] button.th-sort');
  assert.equal(btns.length, 7);
  btns.forEach(b => assert.equal(b.tabIndex, 0));
});

test('the drill-down lists each tool’s patterns on the same columns, by context cost', async () => {
  const { root } = await mount([TOOLS]);
  const sections = root.querySelectorAll('details.tool-drill');
  assert.deepEqual(sections.map(d => d.querySelector('summary').textContent.trim().split(' ')[0]), ['Bash', 'Read']);
  const bash = sections[0].querySelector('table');
  assert.deepEqual(heads(bash), ['Pattern', ...COLUMNS.slice(1)]);
  assert.deepEqual(cells(bash), [
    ['go test', '3', '50.0%', '1.5 s', '1.5 min', '40,000', '$2.80'],
    ['git status', '1', '0.0%', '80 ms', '80 ms', '2,000', '$0.20'],
  ]);
});

test('the text filter narrows both tables and hides drill sections with no match', async () => {
  const { root } = await mount([TOOLS]);
  const input = root.querySelector('.vf-text');
  input.value = 'git';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  assert.deepEqual(cells(toolTable(root)), [['No matching rows.']]);
  const [bash, read] = root.querySelectorAll('details.tool-drill');
  assert.deepEqual(firstCol(bash.querySelector('table')), ['git status']);
  assert.equal(read.hidden, true);
  assert.equal(bash.hidden, false);

  input.value = 'bash';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  assert.deepEqual(firstCol(toolTable(root)), ['Bash']);
  assert.deepEqual(firstCol(bash.querySelector('table')), ['go test', 'git status']);
});

test('a window with no tool calls reads "No tool calls in this window"', async () => {
  const { root, document } = await mount([{ by_tool: [], by_tool_detail: {} }]);
  assert.deepEqual(cells(toolTable(root)), [['No tool calls in this window']]);
  assert.equal(root.querySelector('#drill-container').textContent.trim(), 'No tool calls in this window');
  assert.equal(document.getElementById('nav-metric-tools').textContent, '—');
});

test('while /tools is in flight the table shows the skeleton', async () => {
  const { root } = await mount([{ pending: true }], { await: false });
  await new Promise(r => setTimeout(r, 0));
  assert.ok(toolTable(root).querySelector('tbody .skeleton'), 'table skeleton');
  assert.ok(root.querySelector('#drill-container .skeleton'), 'drill skeleton');
});

test('a non-2xx response shows an error banner naming the status; Retry refetches', async () => {
  const { root, calls } = await mount([{ status: 503, statusText: 'Service Unavailable' }, TOOLS]);
  const banner = root.querySelector('.warning-card[role="alert"]');
  assert.ok(banner.textContent.includes('HTTP 503 Service Unavailable'));
  click(banner.querySelector('button[data-retry]'));
  await new Promise(r => setTimeout(r, 0));
  assert.equal(calls.length, 2);
  assert.deepEqual(firstCol(toolTable(root)), ['Bash', 'Read', 'TodoWrite']);
});
