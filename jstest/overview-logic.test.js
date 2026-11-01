import { test } from 'node:test';
import assert from 'node:assert/strict';

import { hotspotTrend, showForecastTab } from '../web/overview-logic.js';

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
