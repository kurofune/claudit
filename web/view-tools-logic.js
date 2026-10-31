// Pure row building and sorting for the Tools view (view-tools.js), kept
// DOM-free so jstest/view-tools-logic.test.js can import it.
//
// Rows arrive as render.ToolRow (/_claudit/api/tools). toolRows adds the
// sort keys the table headers name: error_pct, median_ms and p95_ms are
// null — shown as "—" and sorted last — when no result joined or no call
// was timed, so an unmeasured tool never reads as 0% errors or 0 ms.

import { fmtNum, fmtMoney } from './format.js';
import { sortRows } from './table.js';

export const DEFAULT_SORT = Object.freeze({ key: 'context_cost_usd', dir: 'desc' });

export function toolRows(rows) {
  return (rows || []).map(r => ({
    ...r,
    error_pct: r.results > 0 ? r.error_rate : null,
    median_ms: r.timed_calls > 0 ? r.duration_median_ms : null,
    p95_ms: r.timed_calls > 0 ? r.duration_p95_ms : null,
    // The shared min-cost filter (table.js rowPassesFilter) reads CostUSD.
    CostUSD: r.context_cost_usd,
  }));
}

// drillSections pairs each tool, in the by-tool order, with its pattern
// rows sorted by the default sort. Tools without patterns are omitted.
export function drillSections(tools, byToolDetail) {
  const detail = byToolDetail || {};
  const out = [];
  for (const t of tools) {
    const rows = detail[t.tool];
    if (!rows || rows.length === 0) continue;
    out.push({ tool: t.tool, summary: t, rows: sortRows(toolRows(rows), DEFAULT_SORT.key, DEFAULT_SORT.dir) });
  }
  return out;
}

export function contextCostTip(r) {
  return `${fmtNum(r.context_tokens)} tokens re-read across ${fmtNum(r.reread_turns)} turns · ` +
    `write ${fmtMoney(r.context_write_usd)} + read ${fmtMoney(r.context_read_usd)}`;
}

export function fmtDuration(ms) {
  if (ms == null) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${(ms / 60_000).toFixed(1)} min`;
}
