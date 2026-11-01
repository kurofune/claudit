import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click } from './helpers/mini-dom.js';

// A hotspot in the shape /_claudit/api/overview ships (HotspotForJSON).
const hotspot = (title, cost, extra = {}) => ({
  kind: 'bash_pattern', title, cost_usd: cost, pct_of_total: 40, prompt: `prompt for ${title}`,
  examples: [
    { input: 'go test ./...', session_id: '0f1e2d3c-aaaa-bbbb', source_file: '/u/.claude/projects/p/0f1e2d3c.jsonl', context_tokens: 42000, context_cost_usd: 1 },
    { input: 'go test -run X', session_id: '9a8b7c6d-cccc', source_file: '/u/.claude/projects/p/9a8b7c6d.jsonl', context_tokens: 900, context_cost_usd: 0.1 },
  ],
  ...extra,
});
const OVERVIEW = {
  totals: { CostUSD: 10, Sessions: 2, Turns: 9 },
  overall_hit_ratio: 0.9, total_tokens: 100, trend_totals: [], forecast: {}, unknown_models: [], period: 'day',
  hotspots: [
    hotspot('Bash: `go test`', 6, { prev_cost_usd: 3, trend_pct: 100 }),
    hotspot('Bash: `rg`', 3, { prev_cost_usd: 6, trend_pct: -50 }),
    hotspot('Read of `.go` files', 1, { prev_cost_usd: 0 }),
  ],
};

function stubFetch(responses) {
  const calls = [];
  globalThis.fetch = async url => {
    calls.push(String(url));
    const path = String(url).replace('/_claudit/api', '').split('?')[0];
    if (path === '/anomalies') return { ok: true, status: 200, statusText: 'OK', json: async () => ({ anomalies: [] }) };
    const r = responses.length > 1 ? responses.shift() : responses[0];
    if (r.status) return { ok: false, status: r.status, statusText: r.statusText, json: async () => ({}) };
    return { ok: true, status: 200, statusText: 'OK', json: async () => r };
  };
  return calls;
}

let seq = 0;
async function mount(responses) {
  const dom = installDOM({ hash: '#overview' });
  dom.document.body.innerHTML = '<span id="nav-metric-overview"></span><section id="view-overview"></section>';
  const calls = stubFetch(responses);
  const view = await import(`../web/view-overview.js?mount=${++seq}`);
  await view.paint();
  return { ...dom, view, calls, root: dom.document.getElementById('view-overview') };
}

const overviewCalls = calls => calls.filter(u => u.includes('/overview')).length;

test('hotspot cards show context cost, share, trend and examples that deep-link sessions', async () => {
  const { root } = await mount([OVERVIEW]);
  const cards = root.querySelectorAll('.hotspot');
  assert.equal(cards.length, 3);
  assert.equal(cards[0].querySelector('.h-cost').textContent, '$6.00');
  assert.equal(cards[0].querySelector('.h-pct').textContent, '40.0% of context cost');
  assert.deepEqual(cards.map(c => c.querySelector('.h-trend').textContent), ['▲ +100%', '▼ −50%', 'new']);
  const ex = cards[0].querySelectorAll('.hotspot-example');
  assert.equal(ex.length, 2);
  assert.equal(ex[0].querySelector('.ex-input').textContent, 'go test ./...');
  assert.equal(ex[0].querySelector('.ex-file').textContent, '/u/.claude/projects/p/0f1e2d3c.jsonl');
  const link = ex[0].querySelector('a.ex-session');
  assert.equal(link.getAttribute('href'), '#agents/tree/0f1e2d3c-aaaa-bbbb');
  assert.match(root.textContent, /coding agent working in this repo/);
  for (const c of cards) {
    assert.equal(c.querySelectorAll('em, i').length, 0, 'no italic text in a card');
    assert.doesNotMatch(c.textContent, /\p{Extended_Pictographic}/u, 'no emoji in a card');
  }
});

test('no trend badge when the server compared no previous window', async () => {
  const { root } = await mount([{ ...OVERVIEW, hotspots: [hotspot('Bash: `ls`', 1)] }]);
  assert.equal(root.querySelectorAll('.h-trend').length, 0);
});

test('an empty hotspot list says so and hints at widening the range', async () => {
  const { root } = await mount([{ ...OVERVIEW, hotspots: [] }]);
  const empty = root.querySelector('.hotspots .empty-state');
  assert.ok(empty);
  assert.match(empty.textContent, /^No hotspots in this window/);
  assert.match(empty.textContent, /Widen the date range/);
});

test('a non-2xx overview names the HTTP status, and Retry refetches', async () => {
  const { root, calls } = await mount([{ status: 503, statusText: 'Service Unavailable' }, OVERVIEW]);
  const banner = root.querySelector('.warning-card[role="alert"]');
  assert.ok(banner);
  assert.match(banner.textContent, /HTTP 503 Service Unavailable/);
  assert.equal(overviewCalls(calls), 1);
  click(root.querySelector('button[data-retry]'));
  await new Promise(r => setTimeout(r, 0));
  await new Promise(r => setTimeout(r, 0));
  assert.equal(overviewCalls(calls), 2);
  assert.equal(root.querySelectorAll('.hotspot').length, 3);
});

test('Copy prompt copies the card prompt and reads "Copied"', async () => {
  const { root, window } = await mount([OVERVIEW]);
  let copied = null;
  Object.defineProperty(globalThis, 'navigator', {
    configurable: true,
    value: { clipboard: { writeText: async t => { copied = t; } } },
  });
  const btn = root.querySelectorAll('button.copy')[1];
  assert.equal(btn.getAttribute('type'), 'button');
  click(btn);
  await new Promise(r => setTimeout(r, 0));
  assert.equal(copied, 'prompt for Bash: `rg`');
  assert.equal(btn.textContent, 'Copied');
});

// Two daily buckets so the trend section renders; the forecast is the
// server's no-projection shape (aggregate.Forecast with a reason).
const TREND = [
  { time: '2026-10-30T00:00:00Z', cost_usd: 4, hit_ratio: 0.9 },
  { time: '2026-10-31T00:00:00Z', cost_usd: 6, hit_ratio: 0.9 },
];
const noProjection = reason => ({
  as_of: '2026-10-02T05:43:00Z', month_start: '2026-10-01T00:00:00-07:00', month_end: '2026-11-01T00:00:00-07:00',
  window_start: '0001-01-01T00:00:00Z', days_elapsed: 0, days_in_month: 31, mtd_cost_usd: 0, window_cost_usd: 0,
  daily_rate_usd: 0, projected_month_end_usd: 0, reason,
});

test('a month too young to project keeps the Forecast tab, and its panel names the month and the two-day need', async () => {
  const { root } = await mount([{ ...OVERVIEW, trend_totals: TREND, forecast: noProjection('too_few_days') }]);
  const trend = root.querySelector('#trend-section');
  assert.equal(trend.querySelector('h2').textContent, 'Spend over time');
  const tabs = trend.querySelectorAll('.subtab[data-trend-tab]');
  assert.deepEqual(tabs.map(t => t.textContent), ['Cost by day', 'Forecast']);
  const panel = trend.querySelector('[data-trend-panel="forecast"]');
  assert.ok(panel.hidden, 'forecast panel starts hidden behind the Cost-by-day tab');
  click(tabs[1]);
  assert.equal(panel.hidden, false);
  assert.equal(panel.querySelector('.empty-state').textContent,
    'No projection for October yet — needs at least two days of cost in the current calendar month.');
});

for (const [label, trend_totals] of [['empty', []], ['null', null]]) {
  test(`${label} trend totals with too_few_days still render the Forecast tab over an empty cost panel`, async () => {
    const { root } = await mount([{ ...OVERVIEW, trend_totals, forecast: noProjection('too_few_days') }]);
    const trend = root.querySelector('#trend-section');
    assert.equal(trend.querySelector('h2').textContent, 'Spend over time');
    const tabs = trend.querySelectorAll('.subtab[data-trend-tab]');
    assert.deepEqual(tabs.map(t => t.textContent), ['Cost by day', 'Forecast']);
    const cost = trend.querySelector('[data-trend-panel="cost"]');
    assert.equal(cost.querySelector('.empty-state').textContent, 'No spend in this window yet.');
    const panel = trend.querySelector('[data-trend-panel="forecast"]');
    click(tabs[1]);
    assert.equal(cost.hidden, true);
    assert.equal(panel.hidden, false);
    assert.equal(panel.querySelector('.empty-state').textContent,
      'No projection for October yet — needs at least two days of cost in the current calendar month.');
  });
}

test('empty trend totals with nothing to project render no trend section', async () => {
  const { root } = await mount([{ ...OVERVIEW, trend_totals: [], forecast: noProjection('historical') }]);
  assert.equal(root.querySelector('#trend-section').innerHTML.trim(), '');
});

for (const reason of ['historical', 'not_daily']) {
  test(`reason ${reason} renders the plain Cost-by heading with no subtabs`, async () => {
    const period = reason === 'not_daily' ? 'week' : 'day';
    const { root } = await mount([{ ...OVERVIEW, period, trend_totals: TREND, forecast: noProjection(reason) }]);
    const trend = root.querySelector('#trend-section');
    assert.equal(trend.querySelector('h2').textContent, `Cost by ${period}`);
    assert.equal(trend.querySelectorAll('.subtab').length, 0);
    assert.equal(trend.querySelector('[data-trend-panel="forecast"]'), null);
  });
}

test('forecastChart keeps the generic empty text when the payload names no month', async () => {
  const { forecastChart } = await import('../web/charts.js');
  const generic = /^<div class="small empty-state">Forecast unavailable — needs at least two days of cost in the current calendar month and daily bucketing\.<\/div>$/;
  assert.match(forecastChart({ projected_month_end_usd: 0 }, []), generic);
  assert.match(forecastChart({ projected_month_end_usd: 0, month_start: '0001-01-01T00:00:00Z' }, []), generic);
  assert.match(forecastChart(null, []), generic);
  assert.match(forecastChart({ projected_month_end_usd: 0, month_start: '2026-02-01T00:00:00+09:00' }, []), /No projection for February yet/);
});
