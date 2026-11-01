import { test } from 'node:test';
import assert from 'node:assert/strict';

import { installDOM, click, keydown, Event } from './helpers/mini-dom.js';

// A hotspot in the shape /_claudit/api/overview ships (HotspotForJSON).
const hotspot = (title, cost, extra = {}) => ({
  kind: 'bash_pattern', title, cost_usd: cost, pct_of_total: 40, prompt: `prompt for ${title}\nsession 0f1e2d3c in /u/.claude/projects/p/0f1e2d3c.jsonl`,
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

// A Map-backed sessionStorage: mini-dom has none.
function stubStorage(init = {}) {
  const m = new Map(Object.entries(init));
  globalThis.sessionStorage = {
    getItem: k => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, String(v)); },
    removeItem: k => { m.delete(k); },
  };
  return m;
}
const OPEN_KEY = 'claudit.overview.openHotspots';

let seq = 0;
async function mount(responses, { hash = '#overview', stored = {} } = {}) {
  const dom = installDOM({ hash });
  const storage = stubStorage(stored);
  const scrolled = [];
  Object.getPrototypeOf(dom.document.body).scrollIntoView = function () { scrolled.push(this.id); };
  dom.document.body.innerHTML = '<span id="nav-metric-overview"></span><section id="view-overview"></section>';
  const calls = stubFetch(responses);
  const view = await import(`../web/view-overview.js?mount=${++seq}`);
  await view.paint();
  return { ...dom, view, calls, storage, scrolled, root: dom.document.getElementById('view-overview') };
}

const overviewCalls = calls => calls.filter(u => u.includes('/overview')).length;

const SOURCE = /\.jsonl|\/u\/\.claude/;

test('each hotspot renders collapsed as one row: rank, kind, title, share bar, cost, trend and Copy prompt', async () => {
  const { root } = await mount([OVERVIEW]);
  const cards = root.querySelectorAll('.hotspot');
  assert.equal(cards.length, 3);
  cards.forEach((c, i) => {
    const n = i + 1;
    assert.equal(c.id, `hotspot-${n}`);
    const t = c.querySelector('.hotspot-row > button.hotspot-toggle');
    assert.ok(t, 'the header is a button');
    assert.equal(t.getAttribute('type'), 'button');
    assert.equal(t.getAttribute('aria-expanded'), 'false');
    assert.equal(t.getAttribute('aria-controls'), `hotspot-${n}-detail`);
    assert.equal(t.querySelector('.rank').textContent, `#${n}`);
    assert.equal(t.querySelector('.hotspot-kind').textContent, 'bash pattern');
    assert.ok(t.querySelector('.h-title'));
    assert.ok(t.querySelector('.h-share > span'));
    assert.ok(t.querySelector('.h-cost'));
    assert.ok(t.querySelector('svg.chev'));
    const copy = c.querySelector('.hotspot-row > button.copy');
    assert.ok(copy, 'Copy prompt is a sibling of the toggle, not inside it');
    assert.equal(t.querySelector('button.copy'), null);
    const detail = c.querySelector(`#hotspot-${n}-detail`);
    assert.ok(detail.hidden);
    assert.equal(c.querySelectorAll('.hotspot-examples, pre, table, ol, li').length, 0, 'collapsed row holds no examples');
    assert.doesNotMatch(c.innerHTML, SOURCE, 'collapsed row holds no source_file text');
    assert.equal(c.querySelectorAll('em, i').length, 0, 'no italic text in a row');
    assert.doesNotMatch(c.textContent, /\p{Extended_Pictographic}/u, 'no emoji in a row');
  });
  assert.equal(cards[0].querySelector('.h-title').textContent, 'Bash: `go test`');
  assert.equal(cards[0].querySelector('.h-cost').textContent, '$6.00');
  assert.equal(cards[0].querySelector('.h-pct').textContent, '40.0%');
  assert.deepEqual(cards.map(c => c.querySelector('.h-trend').textContent), ['▲ +100%', '▼ −50%', 'new']);
  assert.match(root.textContent, /coding agent working in this repo/);
});

test('the share bar is barWidth: the largest hotspot fills its track, the rest scale, a missing pct is 0', async () => {
  const hs = [
    hotspot('a', 6, { pct_of_total: 50 }),
    hotspot('b', 3, { pct_of_total: 20 }),
    hotspot('c', 1, { pct_of_total: undefined }),
  ];
  const { root } = await mount([{ ...OVERVIEW, hotspots: hs }]);
  const widths = root.querySelectorAll('.h-share > span').map(s => s.getAttribute('style'));
  assert.deepEqual(widths, ['width:100.0%', 'width:40.0%', 'width:0.0%']);
});

async function expandedBy(activate) {
  const m = await mount([OVERVIEW]);
  const card = m.root.querySelector('#hotspot-1');
  const t = card.querySelector('.hotspot-toggle');
  activate(t);
  return { ...m, card, t, detail: card.querySelector('#hotspot-1-detail') };
}

for (const [how, activate] of [
  ['click', t => click(t)],
  ['Enter', t => keydown(t, 'Enter')],
  ['Space', t => keydown(t, ' ')],
]) {
  test(`${how} on the toggle opens the examples table, and a second ${how} collapses it`, async () => {
    const { card, t, detail, storage } = await expandedBy(activate);
    assert.equal(t.getAttribute('aria-expanded'), 'true');
    assert.equal(detail.hidden, false);
    const table = detail.querySelector('table.hotspot-detail');
    assert.ok(table);
    assert.deepEqual(table.querySelectorAll('thead th').map(th => th.textContent), ['Tokens added', 'Session', 'Command']);
    const rows = table.querySelectorAll('tbody tr');
    assert.equal(rows.length, 2);
    const cells = rows[0].querySelectorAll('td');
    assert.equal(cells[0].textContent, '42,000');
    const link = cells[1].querySelector('a.ex-session');
    assert.equal(link.getAttribute('href'), '#agents/tree/0f1e2d3c-aaaa-bbbb');
    assert.equal(link.textContent, '0f1e2d3c');
    assert.ok(cells[2].classList.contains('ex-cmd'));
    assert.equal(cells[2].getAttribute('title'), 'go test ./...');
    assert.equal(cells[2].textContent, 'go test ./...');
    assert.doesNotMatch(detail.innerHTML, SOURCE, 'no source_file anywhere in the detail');
    const details = detail.querySelector('details.hotspot-prompt');
    const pre = details.querySelector('pre');
    assert.ok(pre, 'the Show-prompt pre sits inside the detail');
    assert.equal(pre.textContent, '', 'the prompt, with its JSONL paths, waits for Show prompt');
    details.open = true;
    details.dispatchEvent(new Event('toggle'));
    assert.equal(pre.textContent, OVERVIEW.hotspots[0].prompt);
    assert.equal(storage.get(OPEN_KEY), '[1]');

    activate(t);
    assert.equal(t.getAttribute('aria-expanded'), 'false');
    assert.ok(detail.hidden);
    assert.equal(card.querySelectorAll('table, pre').length, 0);
    assert.equal(storage.get(OPEN_KEY), '[]');
  });
}

test('a key other than Enter or Space leaves the row collapsed', async () => {
  const { t } = await expandedBy(t => keydown(t, 'a'));
  assert.equal(t.getAttribute('aria-expanded'), 'false');
});

test('a multi-line command renders on one line with the full input in its title', async () => {
  const multi = hotspot('m', 1, { examples: [{ input: 'cat <<EOF\nline two\n  three\nEOF', session_id: 's1', source_file: '/x.jsonl', context_tokens: 5 }] });
  const { root } = await mount([{ ...OVERVIEW, hotspots: [multi] }]);
  click(root.querySelector('.hotspot-toggle'));
  const cell = root.querySelector('td.ex-cmd');
  assert.equal(cell.textContent, 'cat <<EOF line two three EOF');
  assert.equal(cell.getAttribute('title'), 'cat <<EOF\nline two\n  three\nEOF');
});

test('paint with #overview/hotspot-2 opens row 2 only and scrolls it into view', async () => {
  const { root, scrolled } = await mount([OVERVIEW], { hash: '#overview/hotspot-2' });
  const expanded = root.querySelectorAll('.hotspot-toggle').map(t => t.getAttribute('aria-expanded'));
  assert.deepEqual(expanded, ['false', 'true', 'false']);
  assert.equal(root.querySelector('#hotspot-2-detail').hidden, false);
  assert.ok(root.querySelector('#hotspot-2-detail table.hotspot-detail'));
  assert.ok(root.querySelector('#hotspot-1-detail').hidden);
  assert.deepEqual(scrolled, ['hotspot-2']);
});

test('rows stored as open before the SSE reload reopen on paint, merged with the deep link', async () => {
  const { root, scrolled } = await mount([OVERVIEW], { hash: '#overview/hotspot-3', stored: { [OPEN_KEY]: '[1]' } });
  const expanded = root.querySelectorAll('.hotspot-toggle').map(t => t.getAttribute('aria-expanded'));
  assert.deepEqual(expanded, ['true', 'false', 'true']);
  assert.deepEqual(scrolled, ['hotspot-3']);
});

test('a stored rank past the end of the list and malformed storage are ignored', async () => {
  const { root } = await mount([OVERVIEW], { stored: { [OPEN_KEY]: '[2,9]' } });
  assert.deepEqual(root.querySelectorAll('.hotspot-toggle').map(t => t.getAttribute('aria-expanded')), ['false', 'true', 'false']);
  const bad = await mount([OVERVIEW], { stored: { [OPEN_KEY]: '{oops' } });
  assert.equal(bad.root.querySelectorAll('.hotspot-toggle[aria-expanded="true"]').length, 0);
});

test('collapsing the deep-linked row drops the hotspot from the hash so the reload keeps it shut', async () => {
  const { root, location } = await mount([OVERVIEW], { hash: '#overview/hotspot-2' });
  click(root.querySelector('#hotspot-2 .hotspot-toggle'));
  assert.equal(location.hash, '#overview');
});

test('30 hotspots of 15 examples render 30 collapsed rows, and 30 tables of 15 rows once all are open', async () => {
  const many = Array.from({ length: 30 }, (_, i) => hotspot(`Bash: \`cmd${i}\``, 30 - i, {
    pct_of_total: 30 - i,
    examples: Array.from({ length: 15 }, (_, j) => ({
      input: `cmd${i} --flag ${j} ${'x'.repeat(200)}`, session_id: `sess${i}-${j}-0000`,
      source_file: `/u/.claude/projects/p/s${i}-${j}.jsonl`, context_tokens: 1000 * (15 - j), context_cost_usd: 0.1,
    })),
  }));
  const { root } = await mount([{ ...OVERVIEW, hotspots: many }]);
  const toggles = root.querySelectorAll('.hotspot-toggle');
  assert.equal(root.querySelectorAll('.hotspot').length, 30);
  assert.equal(root.querySelectorAll('.hotspots table, .hotspots pre').length, 0);
  toggles.forEach(t => click(t));
  const tables = root.querySelectorAll('table.hotspot-detail');
  assert.equal(tables.length, 30);
  for (const t of tables) assert.equal(t.querySelectorAll('tbody tr').length, 15);
  assert.doesNotMatch(root.querySelector('.hotspots').innerHTML, SOURCE);
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

test('while the overview loads, the section shows overviewSkeleton', async () => {
  const dom = installDOM({ hash: '#overview' });
  stubStorage();
  dom.document.body.innerHTML = '<span id="nav-metric-overview"></span><section id="view-overview"></section>';
  globalThis.fetch = () => new Promise(() => {});
  const view = await import(`../web/view-overview.js?mount=${++seq}`);
  view.paint();
  const root = dom.document.getElementById('view-overview');
  assert.equal(root.querySelectorAll('.skel-list .skel-hotspot').length, 5);
  assert.equal(root.querySelectorAll('.hotspot').length, 0);
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
  assert.equal(copied, OVERVIEW.hotspots[1].prompt);
  assert.equal(btn.textContent, 'Copied');
  const card = root.querySelector('#hotspot-2');
  assert.equal(card.querySelector('.hotspot-toggle').getAttribute('aria-expanded'), 'false', 'copying does not expand the row');
  assert.ok(card.querySelector('#hotspot-2-detail').hidden);
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
