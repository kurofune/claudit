import { test } from 'node:test';
import assert from 'node:assert/strict';

import { hotspotTrend, showForecastTab, barWidth, restoreExpanded, hashRank } from '../web/overview-logic.js';

test('hotspotTrend: up, down and new against the previous window', () => {
  assert.deepEqual(
    { ...hotspotTrend({ cost_usd: 20, prev_cost_usd: 10, trend_pct: 100 }), title: undefined },
    { text: '▲ +100%', dir: 'up', title: undefined });
  assert.equal(hotspotTrend({ cost_usd: 5, prev_cost_usd: 10, trend_pct: -50 }).text, '▼ −50%');
  assert.equal(hotspotTrend({ cost_usd: 5, prev_cost_usd: 10, trend_pct: -50 }).dir, 'down');
  assert.deepEqual(
    { ...hotspotTrend({ cost_usd: 5, prev_cost_usd: 0 }), title: undefined },
    { text: 'new', dir: 'new', title: undefined });
});
test('hotspotTrend: no badge without a previous window', () => {
  assert.equal(hotspotTrend({ cost_usd: 5 }), null);
  assert.equal(hotspotTrend(null), null);
});
test('hotspotTrend rounds to whole percent and names the previous cost', () => {
  const t = hotspotTrend({ cost_usd: 10.4, prev_cost_usd: 10, trend_pct: 4.4 });
  assert.equal(t.text, '▲ +4%');
  assert.match(t.title, /\$10\.00/);
});

test('showForecastTab keeps the tab on daily bucketing while the month is too young to project', () => {
  assert.equal(showForecastTab({ reason: '', projected_month_end_usd: 120 }, 'day'), true);
  assert.equal(showForecastTab({ reason: 'too_few_days', projected_month_end_usd: 0 }, 'day'), true);
});
test('showForecastTab hides the tab for a historical or non-daily window', () => {
  assert.equal(showForecastTab({ reason: 'historical', projected_month_end_usd: 0 }, 'day'), false);
  assert.equal(showForecastTab({ reason: 'not_daily', projected_month_end_usd: 0 }, 'day'), false);
});
test('showForecastTab falls back to the projection for a payload without a reason', () => {
  assert.equal(showForecastTab({ projected_month_end_usd: 50 }, 'day'), true);
  assert.equal(showForecastTab({ projected_month_end_usd: 0 }, 'day'), false);
  assert.equal(showForecastTab({}, 'day'), false);
  assert.equal(showForecastTab(null, 'day'), false);
});
test('showForecastTab is false off daily bucketing, whatever the forecast says', () => {
  assert.equal(showForecastTab({ reason: '', projected_month_end_usd: 120 }, 'hour'), false);
  assert.equal(showForecastTab({ reason: 'too_few_days' }, 'hour'), false);
  assert.equal(showForecastTab({ projected_month_end_usd: 120 }, 'hour'), false);
});

const HS = [{ pct_of_total: 40 }, { pct_of_total: 10 }, { pct_of_total: 0 }, {}];
test('barWidth fills the largest hotspot and scales the rest against it', () => {
  assert.equal(barWidth(HS[0], HS), 100);
  assert.equal(barWidth(HS[1], HS), 25);
});
test('barWidth is 0 for a zero or missing pct, and when no hotspot has a share', () => {
  assert.equal(barWidth(HS[2], HS), 0);
  assert.equal(barWidth(HS[3], HS), 0);
  assert.equal(barWidth(null, HS), 0);
  assert.equal(barWidth({ pct_of_total: 5 }, [{}, { pct_of_total: 0 }]), 0);
});

test('hashRank reads N from #overview/hotspot-N and nothing else', () => {
  assert.equal(hashRank('#overview/hotspot-3'), 3);
  assert.equal(hashRank('overview/hotspot-12'), 12);
  assert.equal(hashRank('#overview/hotspot-0'), null);
  assert.equal(hashRank('#overview/hotspot-x'), null);
  assert.equal(hashRank('#overview/hotspot-2/extra'), null);
  assert.equal(hashRank('#spend/hotspot-2'), null);
  assert.equal(hashRank('#overview'), null);
  assert.equal(hashRank(undefined), null);
});
test('restoreExpanded: hash only opens the deep-linked rank', () => {
  assert.deepEqual(restoreExpanded({ hash: '#overview/hotspot-2', stored: null }), [2]);
});
test('restoreExpanded: stored only returns the stored ranks in order', () => {
  assert.deepEqual(restoreExpanded({ hash: '#overview', stored: '[5,1,3]' }), [1, 3, 5]);
});
test('restoreExpanded: both merge without duplicates', () => {
  assert.deepEqual(restoreExpanded({ hash: '#overview/hotspot-4', stored: '[1,4]' }), [1, 4]);
});
test('restoreExpanded: a malformed hash is ignored', () => {
  assert.deepEqual(restoreExpanded({ hash: '#overview/hotspot-abc', stored: '[2]' }), [2]);
  assert.deepEqual(restoreExpanded({ hash: '#overview/hotspot--1', stored: null }), []);
});
test('restoreExpanded: malformed stored values are ignored', () => {
  for (const stored of ['not json', '{"a":1}', '"3"', '[0,-1,1.5,"2",null]', '', undefined]) {
    assert.deepEqual(restoreExpanded({ hash: '#overview/hotspot-1', stored }), [1], String(stored));
  }
  assert.deepEqual(restoreExpanded({ hash: '', stored: '[2,"x",7]' }), [2, 7]);
});
