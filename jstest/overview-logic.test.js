import { test } from 'node:test';
import assert from 'node:assert/strict';

import { hotspotTrend } from '../web/overview-logic.js';

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
