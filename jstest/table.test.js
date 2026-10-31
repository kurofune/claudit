import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rowSearchText, rowPassesFilter, emptyRowText, sortRows } from '../web/table.js';

test('rowSearchText concatenates only string fields, lowercased', () => {
  const r = { Model: 'Claude-Opus', Project: '/p/Foo', CostUSD: 12.5, Turns: 9 };
  const s = rowSearchText(r);
  assert.ok(s.includes('claude-opus'));
  assert.ok(s.includes('/p/foo'));
  assert.ok(!s.includes('12.5')); // numbers skipped
});
test('rowPassesFilter passes everything when there is no filter', () => {
  assert.equal(rowPassesFilter({ Model: 'x', CostUSD: 0.01 }, '', 0), true);
});
test('rowPassesFilter rejects rows below the min cost', () => {
  assert.equal(rowPassesFilter({ Model: 'x', CostUSD: 0.5 }, '', 1), false);
  assert.equal(rowPassesFilter({ Model: 'x', CostUSD: 0.5 }, '', 0.5), true); // >= passes
});
test('rowPassesFilter treats a missing CostUSD as 0', () => {
  assert.equal(rowPassesFilter({ Model: 'x' }, '', 1), false);
  assert.equal(rowPassesFilter({ Model: 'x' }, '', 0), true);
});
test('rowPassesFilter matches the query against any string field', () => {
  const r = { Model: 'claude-opus', Project: '/p/foo', CostUSD: 5 };
  assert.equal(rowPassesFilter(r, 'opus', 0), true);
  assert.equal(rowPassesFilter(r, '/p/foo', 0), true);
  assert.equal(rowPassesFilter(r, 'sonnet', 0), false);
});
test('rowPassesFilter requires BOTH the query and the min cost to pass', () => {
  const r = { Model: 'claude-opus', CostUSD: 0.5 };
  assert.equal(rowPassesFilter(r, 'opus', 1), false);
  assert.equal(rowPassesFilter(r, 'sonnet', 0), false);
  assert.equal(rowPassesFilter(r, 'opus', 0.5), true);
});
test('emptyRowText names the empty window when the table has no rows at all', () => {
  assert.equal(emptyRowText(0, 'No spend in this window'), 'No spend in this window');
});
test('emptyRowText says no matching rows when the filter hid every row', () => {
  assert.equal(emptyRowText(5, 'No spend in this window'), 'No matching rows.');
});
test('emptyRowText falls back to no matching rows without an empty text', () => {
  assert.equal(emptyRowText(0, undefined), 'No matching rows.');
});

test('sortRows sinks null and undefined to the end in both directions', () => {
  const rows = [{ k: 'a', v: null }, { k: 'b', v: 2 }, { k: 'c' , v: undefined }, { k: 'd', v: 5 }];
  assert.deepEqual(sortRows(rows, 'v', 'desc').map(r => r.k), ['d', 'b', 'a', 'c']);
  assert.deepEqual(sortRows(rows, 'v', 'asc').map(r => r.k), ['b', 'd', 'a', 'c']);
});
