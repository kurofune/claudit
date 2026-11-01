import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  toolRows, groupedRows, filterGroups, startExpanded, headingText, nextSort,
  contextCostTip, errorTip, fmtDuration, DEFAULT_SORT, COLLAPSE_ABOVE,
} from '../web/overview-tools-logic.js';
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

// 63 tools × 50 patterns: groupedRows keeps every one of the 3,150
// pattern rows (none capped), each tool's patterns sorted by context cost.
test('groupedRows keeps all 3,150 rows of 63 tools × 50 patterns, parents and patterns sorted by context cost', () => {
  const byTool = [];
  const detail = {};
  for (let t = 0; t < 63; t++) {
    const tool = `Tool${t}`;
    byTool.push(row(tool, '', 63 - t));
    detail[tool] = [];
    for (let p = 0; p < 50; p++) detail[tool].push(row(tool, `pat-${p}`, (p * 7919) % 50));
  }
  const groups = groupedRows({ by_tool: [...byTool].reverse(), by_tool_detail: detail });
  assert.equal(groups.length, 63);
  assert.deepEqual(groups.map(g => g.tool.tool), byTool.map(r => r.tool));
  let total = 0;
  for (const g of groups) {
    total += g.patterns.length;
    assert.equal(new Set(g.patterns.map(r => r.pattern)).size, 50);
    for (let i = 1; i < g.patterns.length; i++) {
      assert.ok(g.patterns[i - 1].context_cost_usd >= g.patterns[i].context_cost_usd, `${g.tool.tool} row ${i} out of order`);
    }
  }
  assert.equal(total, 3150);
  assert.equal(headingText(groups), '3,150 patterns across 63 tools');
});

test('groupedRows keeps a tool with no pattern rows as a parent alone', () => {
  const groups = groupedRows({ by_tool: [row('Bash', '', 2), row('TodoWrite', '', 0)], by_tool_detail: { Bash: [row('Bash', 'ls', 2)] } });
  assert.deepEqual(groups.map(g => [g.tool.tool, g.patterns.length]), [['Bash', 1], ['TodoWrite', 0]]);
  assert.equal(headingText(groups), '1 pattern across 2 tools');
});

test('groupedRows on a missing payload is empty', () => {
  assert.deepEqual(groupedRows(undefined), []);
  assert.deepEqual(groupedRows({ by_tool: [], by_tool_detail: {} }), []);
});

test('sorting by the first column orders parents by tool and patterns by pattern', () => {
  const tools = {
    by_tool: [row('Bash', '', 1), row('Read', '', 2), row('Edit', '', 3)],
    by_tool_detail: { Bash: [row('Bash', 'ls', 1), row('Bash', 'git', 2), row('Bash', 'rg', 3)] },
  };
  const desc = groupedRows(tools, 'tool', 'desc');
  assert.deepEqual(desc.map(g => g.tool.tool), ['Read', 'Edit', 'Bash']);
  assert.deepEqual(desc[2].patterns.map(r => r.pattern), ['rg', 'ls', 'git']);
  const asc = groupedRows(tools, 'tool', 'asc');
  assert.deepEqual(asc.map(g => g.tool.tool), ['Bash', 'Edit', 'Read']);
  assert.deepEqual(asc[0].patterns.map(r => r.pattern), ['git', 'ls', 'rg']);
});

test('a pattern row the server sent without a pattern still sorts by the first column', () => {
  const groups = groupedRows({ by_tool: [row('T', '', 1)], by_tool_detail: { T: [row('T', '', 1), row('T', 'b', 2)] } }, 'tool', 'asc');
  assert.deepEqual(groups[0].patterns.map(r => r.pattern), ['', 'b']);
});

test('null error rates and durations sort last in both directions, for parents and patterns', () => {
  const tools = {
    by_tool: [row('A', '', 1, { results: 0, timed_calls: 0 }), row('B', '', 1, { error_rate: 0.5 }), row('C', '', 1, { error_rate: 0 })],
    by_tool_detail: { B: [row('B', 'x', 1, { timed_calls: 0 }), row('B', 'y', 1, { duration_median_ms: 5 })] },
  };
  for (const dir of ['desc', 'asc']) {
    assert.equal(groupedRows(tools, 'error_pct', dir).at(-1).tool.tool, 'A', dir);
    assert.equal(groupedRows(tools, 'median_ms', dir).at(-1).tool.tool, 'A', dir);
    assert.deepEqual(groupedRows(tools, 'p95_ms', dir).find(g => g.tool.tool === 'B').patterns.map(r => r.pattern), ['y', 'x'], dir);
  }
});

test('filterGroups narrows patterns by tool or pattern substring and drops a parent whose patterns all fail', () => {
  const groups = groupedRows({
    by_tool: [row('Bash', '', 3), row('Read', '', 2), row('TodoWrite', '', 0)],
    by_tool_detail: { Bash: [row('Bash', 'git status', 1), row('Bash', 'go test', 2)], Read: [row('Read', '.go', 2)] },
  });
  const byPattern = filterGroups(groups, 'GIT');
  assert.deepEqual(byPattern.map(g => [g.tool.tool, g.patterns.map(r => r.pattern)]), [['Bash', ['git status']]]);
  const byTool = filterGroups(groups, 'read');
  assert.deepEqual(byTool.map(g => [g.tool.tool, g.patterns.map(r => r.pattern)]), [['Read', ['.go']]]);
  assert.deepEqual(filterGroups(groups, 'todo').map(g => g.tool.tool), ['TodoWrite'], 'a patternless parent matches by name');
  assert.deepEqual(filterGroups(groups, 'go').map(g => g.tool.tool), ['Bash', 'Read']);
  assert.deepEqual(filterGroups(groups, 'nothing-matches'), []);
  assert.equal(filterGroups(groups, '  '), groups);
});

test('parents start expanded at up to 100 pattern rows and collapsed above', () => {
  const groupsOf = n => groupedRows({ by_tool: [row('T', '', 1)], by_tool_detail: { T: Array.from({ length: n }, (_, i) => row('T', `p${i}`, 1)) } });
  assert.equal(COLLAPSE_ABOVE, 100);
  assert.equal(startExpanded(groupsOf(100)), true);
  assert.equal(startExpanded(groupsOf(101)), false);
});

test('nextSort sorts a new column descending and flips the current one', () => {
  assert.deepEqual(nextSort(DEFAULT_SORT, 'calls'), { key: 'calls', dir: 'desc' });
  assert.deepEqual(nextSort({ key: 'calls', dir: 'desc' }, 'calls'), { key: 'calls', dir: 'asc' });
  assert.deepEqual(nextSort({ key: 'calls', dir: 'asc' }, 'calls'), { key: 'calls', dir: 'desc' });
});

test('errorTip names errored results, or says none were recorded', () => {
  const [r] = toolRows([row('Bash', '', 2, { error_count: 1, results: 3 })]);
  assert.equal(errorTip(r), '1 of 3 results errored');
  const [none] = toolRows([row('Bash', '', 2, { results: 0 })]);
  assert.equal(errorTip(none), 'No result recorded for these calls.');
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
