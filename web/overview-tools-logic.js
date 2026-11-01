// Pure row building, grouping, sorting and filtering for the Overview's
// Show all tool-patterns table (overview-tools.js), kept DOM-free so
// jstest/overview-tools-logic.test.js can import it.
//
// Rows arrive as render.ToolRow (/_claudit/api/tools). toolRows adds the
// sort keys the table headers name: error_pct, median_ms and p95_ms are
// null — shown as "—" and sorted last — when no result joined or no call
// was timed, so an unmeasured tool never reads as 0% errors or 0 ms.

import { fmtNum, fmtMoney } from './format.js';
import { sortRows, rowPassesFilter } from './table.js';

export const DEFAULT_SORT = Object.freeze({ key: 'context_cost_usd', dir: 'desc' });

// Above this many pattern rows the tool parents start collapsed.
export const COLLAPSE_ABOVE = 100;

export function toolRows(rows) {
  return (rows || []).map(r => ({
    ...r,
    error_pct: r.results > 0 ? r.error_rate : null,
    median_ms: r.timed_calls > 0 ? r.duration_median_ms : null,
    p95_ms: r.timed_calls > 0 ? r.duration_p95_ms : null,
    // The shared row filter (table.js rowPassesFilter) reads CostUSD.
    CostUSD: r.context_cost_usd,
  }));
}

// The first column names a tool on a parent row and a pattern beneath it,
// so sorting by it orders parents by tool and children by pattern.
const childKey = key => (key === 'tool' ? 'pattern' : key);

// groupedRows is one group per by_tool row — the parent — with its
// by_tool_detail rows as patterns, parents and patterns both ordered by
// key and dir. A tool with no detail rows gets an empty patterns list.
export function groupedRows(tools, key = DEFAULT_SORT.key, dir = DEFAULT_SORT.dir) {
  const detail = (tools && tools.by_tool_detail) || {};
  const parents = sortRows(toolRows(tools && tools.by_tool), key, dir);
  return parents.map(tool => ({
    tool,
    patterns: sortRows(
      toolRows(detail[tool.tool]).map(r => ({ ...r, pattern: r.pattern || '' })),
      childKey(key), dir),
  }));
}

// filterGroups keeps the pattern rows whose tool or pattern holds q
// (case-insensitive) and drops a parent whose patterns all fail; a parent
// without patterns stays when its own tool name matches.
export function filterGroups(groups, q) {
  const needle = String(q || '').trim().toLowerCase();
  if (!needle) return groups;
  const out = [];
  for (const g of groups) {
    if (g.patterns.length === 0) {
      if (rowPassesFilter(g.tool, needle, 0)) out.push(g);
      continue;
    }
    const patterns = g.patterns.filter(r => rowPassesFilter(r, needle, 0));
    if (patterns.length) out.push({ ...g, patterns });
  }
  return out;
}

export const patternCount = groups => groups.reduce((n, g) => n + g.patterns.length, 0);

// startExpanded decides whether the parents open on first render.
export const startExpanded = groups => patternCount(groups) <= COLLAPSE_ABOVE;

const plural = (n, one, many) => `${fmtNum(n)} ${n === 1 ? one : many}`;

export function headingText(groups) {
  return `${plural(patternCount(groups), 'pattern', 'patterns')} across ${plural(groups.length, 'tool', 'tools')}`;
}

// nextSort is the sort after a header activation: a new column sorts
// descending, the current one flips direction.
export function nextSort(cur, key) {
  if (cur && cur.key === key) return { key, dir: cur.dir === 'desc' ? 'asc' : 'desc' };
  return { key, dir: 'desc' };
}

export function contextCostTip(r) {
  return `${fmtNum(r.context_tokens)} tokens re-read across ${fmtNum(r.reread_turns)} turns · ` +
    `write ${fmtMoney(r.context_write_usd)} + read ${fmtMoney(r.context_read_usd)}`;
}

export function errorTip(r) {
  return r.error_pct == null
    ? 'No result recorded for these calls.'
    : `${fmtNum(r.error_count)} of ${fmtNum(r.results)} results errored`;
}

export function fmtDuration(ms) {
  if (ms == null) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60_000).toFixed(1)} min`;
}
