import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  toolRows, drillSections, contextCostTip, fmtDuration, DEFAULT_SORT,
} from '../web/view-tools-logic.js';
import { sortRows } from '../web/table.js';

// A row in the shape /_claudit/api/tools ships (render.ToolRow).
const row = (tool, pattern, cost, extra = {}) => ({
  tool, ...(pattern ? { pattern } : {}),
  calls: 4, results: 3, error_count: 1, error_rate: 1 / 3, timed_calls: 3,
  duration_median_ms: 120, duration_p95_ms: 900,
  context_tokens: 4200, reread_turns: 7,
  context_write_usd: cost / 4, context_read_usd: (3 * cost) / 4, context_cost_usd: cost,
  ...extra,
});

test('toolRows keeps every field and exposes context cost to the min-cost filter', () => {
  const [r] = toolRows([row('Bash', '', 2)]);
  assert.equal(r.tool, 'Bash');
  assert.equal(r.calls, 4);
  assert.equal(r.error_pct, 1 / 3);
  assert.equal(r.median_ms, 120);
  assert.equal(r.p95_ms, 900);
  assert.equal(r.context_tokens, 4200);
  assert.equal(r.context_cost_usd, 2);
  assert.equal(r.CostUSD, 2);
});

test('a row with no joined results or timed calls reads as unknown, not zero', () => {
  const [r] = toolRows([row('Bash', '', 0, { results: 0, error_count: 0, error_rate: 0, timed_calls: 0, duration_median_ms: 0, duration_p95_ms: 0 })]);
  assert.equal(r.error_pct, null);
  assert.equal(r.median_ms, null);
  assert.equal(r.p95_ms, null);
});

test('toolRows on a missing or empty payload is empty', () => {
  assert.deepEqual(toolRows(undefined), []);
  assert.deepEqual(toolRows([]), []);
});

test('the default sort is context cost descending', () => {
  assert.deepEqual(DEFAULT_SORT, { key: 'context_cost_usd', dir: 'desc' });
  const rows = toolRows([row('A', '', 1), row('B', '', 3), row('C', '', 2)]);
  assert.deepEqual(sortRows(rows, DEFAULT_SORT.key, DEFAULT_SORT.dir).map(r => r.tool), ['B', 'C', 'A']);
});

test('contextCostTip names tokens, re-read turns, write and read', () => {
  const [r] = toolRows([row('Bash', '', 2)]);
  assert.equal(contextCostTip(r), '4,200 tokens re-read across 7 turns · write $0.50 + read $1.50');
});

test('fmtDuration', () => {
  assert.equal(fmtDuration(null), '—');
  assert.equal(fmtDuration(0), '0 ms');
  assert.equal(fmtDuration(850), '850 ms');
  assert.equal(fmtDuration(1250), '1.3 s');
  assert.equal(fmtDuration(125_000), '2.1 min');
});

// 63 tools × 50 patterns: the drill-down builds every one of the 3,150
// rows (none capped), each tool's section sorted by context cost.
test('drillSections keeps all 3,150 rows of 63 tools × 50 patterns, each sorted by context cost', () => {
  const byTool = [];
  const detail = {};
  for (let t = 0; t < 63; t++) {
    const tool = `Tool${t}`;
    byTool.push(row(tool, '', 63 - t));
    detail[tool] = [];
    for (let p = 0; p < 50; p++) detail[tool].push(row(tool, `pat-${p}`, (p * 7919) % 50));
  }
  const sections = drillSections(toolRows(byTool), detail);
  assert.equal(sections.length, 63);
  assert.deepEqual(sections.map(s => s.tool), byTool.map(r => r.tool));
  let total = 0;
  for (const s of sections) {
    total += s.rows.length;
    assert.equal(new Set(s.rows.map(r => r.pattern)).size, 50);
    for (let i = 1; i < s.rows.length; i++) {
      assert.ok(s.rows[i - 1].context_cost_usd >= s.rows[i].context_cost_usd, `${s.tool} row ${i} out of order`);
    }
  }
  assert.equal(total, 3150);
});

test('drillSections omits tools with no patterns', () => {
  const sections = drillSections(toolRows([row('Bash', '', 2), row('TodoWrite', '', 0)]), { Bash: [row('Bash', 'ls', 2)] });
  assert.deepEqual(sections.map(s => s.tool), ['Bash']);
});

test('the sort is stable: equal keys keep their input order, in both directions', () => {
  const rows = [];
  for (let i = 0; i < 3150; i++) rows.push({ ...row('T', `p${i}`, i % 5), seq: i });
  const view = toolRows(rows);
  for (const dir of ['desc', 'asc']) {
    const sorted = sortRows(view, 'context_cost_usd', dir);
    assert.equal(sorted.length, 3150);
    for (let i = 1; i < sorted.length; i++) {
      if (sorted[i - 1].context_cost_usd === sorted[i].context_cost_usd) {
        assert.ok(sorted[i - 1].seq < sorted[i].seq, `${dir}: tie at ${i} reordered`);
      }
    }
  }
});

test('unknown error rates and durations sink below known ones in a descending sort', () => {
  const rows = toolRows([
    row('A', '', 1, { results: 0 }),
    row('B', '', 1, { error_rate: 0.5 }),
    row('C', '', 1, { error_rate: 0 }),
  ]);
  assert.deepEqual(sortRows(rows, 'error_pct', 'desc').map(r => r.tool), ['B', 'C', 'A']);
});
