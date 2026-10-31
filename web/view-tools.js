// Tools view — the by-tool table and the per-tool (tool, pattern)
// drill-down, both on the same seven columns: calls, error rate, median
// and p95 duration, context added and context cost. Row building and
// sorting live in view-tools-logic.js.

import { fetchTools } from './api.js';
import { fmtNum, fmtMoney, fmtPct1, escHtml, truncate } from './format.js';
import { buildRows, wireViewFilters, rowPassesFilter, sortRows } from './table.js';
import { tableBodySkeleton, skeletonResetIfPending } from './skeleton.js';
import { toolRows, drillSections, contextCostTip, fmtDuration, DEFAULT_SORT } from './view-tools-logic.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;
const HEAD = `<header class="view-head"><h1>${labelIcon('tools')}Tools</h1></header>`;
const EMPTY = 'No tool calls in this window';
const COLS = 7;

function headCells(firstKey, firstLabel, firstTip) {
  return `<tr>
    <th data-key="${firstKey}" title="${firstTip}">${firstLabel}</th>
    <th data-key="calls" class="num" title="Times the tool was invoked.">Calls</th>
    <th data-key="error_pct" class="num" title="Errored results as a share of calls whose result was recorded. Calls with no recorded result are left out.">Error %</th>
    <th data-key="median_ms" class="num" title="Median time from the end of the calling turn to the tool's result.">Median</th>
    <th data-key="p95_ms" class="num" title="95th-percentile time from the end of the calling turn to the tool's result.">p95</th>
    <th data-key="context_tokens" class="num" title="Cache tokens the tool's results added to context.">Context added</th>
    <th data-key="context_cost_usd" class="num" title="What those tokens cost: the cache write, plus every later turn that re-read them before the session ended or compacted.">Context cost</th>
  </tr>`;
}

function statCells(r, firstHTML) {
  const errTip = r.error_pct == null
    ? 'No result recorded for these calls.'
    : `${fmtNum(r.error_count)} of ${fmtNum(r.results)} results errored`;
  return [
    [firstHTML, false],
    [fmtNum(r.calls), true],
    [r.error_pct == null ? '—' : fmtPct1(r.error_pct), true, null, errTip],
    [fmtDuration(r.median_ms), true],
    [fmtDuration(r.p95_ms), true],
    [fmtNum(r.context_tokens), true],
    [escHtml(fmtMoney(r.context_cost_usd)), true, null, contextCostTip(r)],
  ];
}

const SHELL = `
  ${HEAD}
  <div class="controls view-filter" role="search">
    <label>Filter rows: <input class="vf-text" type="search" placeholder="tool, command, path…"></label>
    <label>Min context cost: <input class="vf-cost" type="number" value="0" step="0.01" min="0"></label>
  </div>
  <div class="small">Context cost is what a tool's results cost after they returned: the cache write that put them into context, plus each later turn that re-read them. Each call counts once, so the column sums to the attributed context cost, never more.</div>

  <nav class="subtabs" aria-label="Tools sections">
    <a class="subtab is-active" href="#tools/by-tool" data-subtab="by-tool">By tool</a>
    <a class="subtab"           href="#tools/drill"   data-subtab="drill">Drill-down</a>
  </nav>

  <div class="subview is-active" data-subview="by-tool">
    <table data-table="tool">
      <thead>${headCells('tool', 'Tool', 'Tool name (Read, Bash, Edit, Grep, Agent, …).')}</thead>
      <tbody></tbody>
    </table>
  </div>

  <div class="subview" data-subview="drill">
    <div class="small">Each section is one tool's (tool, argument-pattern) rows — e.g. Bash + <code>git status</code>. Open one to dig in.</div>
    <div id="drill-container"></div>
  </div>
`;

function paintToolsSkeletons(container) {
  const tb = container.querySelector('[data-table="tool"] tbody');
  if (tb) tb.innerHTML = tableBodySkeleton(COLS, 6);
  const drill = container.querySelector('#drill-container');
  if (drill) {
    drill.innerHTML = `<div class="skel-list">
      <span class="skeleton skel-card"></span>
      <span class="skeleton skel-card"></span>
      <span class="skeleton skel-card"></span>
    </div>`;
  }
}

function drillHTML(sections) {
  if (sections.length === 0) return `<div class="small empty-state">${EMPTY}</div>`;
  return sections.map((s, i) => `<details class="tool-drill" data-drill="${i}">
      <summary>${escHtml(s.tool)} <span class="small">— ${fmtNum(s.rows.length)} pattern(s) · ${escHtml(fmtMoney(s.summary.context_cost_usd))} context cost</span></summary>
      <table data-table="drill-${i}">
        <thead>${headCells('pattern', 'Pattern', 'The argument pattern: the command for Bash, the file extension for Read, the host for WebFetch, …')}</thead>
        <tbody></tbody>
      </table>
    </details>`).join('');
}

function activateSubview(container, sub) {
  const subs = container.querySelectorAll('.subtab[data-subtab]');
  if (!subs.length) return;
  const wanted = sub && container.querySelector(`.subtab[data-subtab="${sub}"]`)
    ? sub
    : subs[0].dataset.subtab;
  subs.forEach(t => t.classList.toggle('is-active', t.dataset.subtab === wanted));
  container.querySelectorAll('.subview').forEach(s =>
    s.classList.toggle('is-active', s.dataset.subview === wanted));
}

function updateNavMetric(rows) {
  const navEl = document.getElementById('nav-metric-tools');
  if (!navEl) return;
  const top = rows[0];
  navEl.textContent = top ? `${truncate(top.tool, 14)} · ${fmtMoney(top.context_cost_usd)}` : '—';
}

function errorReason(err) {
  if (err && err.status) return `HTTP ${err.status}${err.statusText ? ' ' + err.statusText : ''}`;
  return (err && err.message) || 'network error';
}

let painted = false;
let navPainted = false;

// paintNav fetches /tools just to derive the sidebar metric (the tool
// with the highest context cost). Called from app.js at startup; full
// paint() reuses the same endpoint via the server's render cache.
export async function paintNav() {
  if (navPainted || painted) return;
  let tools;
  try { tools = await fetchTools(); } catch { return; }
  updateNavMetric(sortRows(toolRows(tools.by_tool), DEFAULT_SORT.key, DEFAULT_SORT.dir));
  navPainted = true;
}

export async function paint(route) {
  const container = document.getElementById('view-tools');
  if (!container) return;

  if (painted) {
    activateSubview(container, route && route.sub);
    return;
  }

  container.innerHTML = SHELL;
  activateSubview(container, route && route.sub);
  paintToolsSkeletons(container);

  let tools;
  try {
    tools = await fetchTools();
  } catch (err) {
    container.innerHTML = `${HEAD}
      <div class="warning-card" role="alert"><strong class="danger">Failed to load tools:</strong> ${escHtml(errorReason(err))}
        <button type="button" class="retry" data-retry>Retry</button></div>`;
    const retry = container.querySelector('[data-retry]');
    if (retry) retry.addEventListener('click', () => { paint(route); });
    skeletonResetIfPending('nav-metric-tools');
    return;
  }

  const rows = sortRows(toolRows(tools.by_tool), DEFAULT_SORT.key, DEFAULT_SORT.dir);
  const sort = { sortKey: DEFAULT_SORT.key, sortDir: DEFAULT_SORT.dir };

  buildRows(container.querySelector('[data-table="tool"]'), rows,
    r => statCells(r, escHtml(r.tool)), { ...sort, emptyText: EMPTY });

  const sections = drillSections(rows, tools.by_tool_detail);
  const drillEl = container.querySelector('#drill-container');
  drillEl.innerHTML = drillHTML(sections);
  sections.forEach((s, i) => {
    buildRows(drillEl.querySelector(`[data-table="drill-${i}"]`), s.rows,
      r => statCells(r, `<code>${escHtml(r.pattern)}</code>`), sort);
  });

  // The tables re-filter themselves; a drill section with no matching
  // pattern is hidden so the filter narrows the drill-down list too.
  wireViewFilters(container, ({ q, minCost }) => {
    sections.forEach((s, i) => {
      const el = drillEl.querySelector(`[data-drill="${i}"]`);
      if (el) el.hidden = !s.rows.some(r => rowPassesFilter(r, q, minCost));
    });
  });

  updateNavMetric(rows);
  painted = true;
  navPainted = true;
}

export function reset() { painted = false; navPainted = false; }
