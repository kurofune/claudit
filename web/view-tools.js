// Tools view — paints the by-tool table (with hbar list) and the
// per-tool drill-down (e.g. for Bash: "git status" / 1242 / $89.40).
// Drill rows are render-once + capped at 50 per tool, matching the
// legacy SSR'd output.

import { fetchTools, fetchTrends, fetchCost } from './api.js';
import { fmtNum, fmtMoney, escHtml, truncate, pct } from './format.js';
import { trendSpark } from './charts.js';
import {
  buildRows, withTrend, injectTrendColumn, wireGlobalFilters,
} from './table.js';
import { hbarListSkeleton, tableBodySkeleton } from './skeleton.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

function toolBarsHTML(rows, totalCost) {
  return rows.map(r => {
    const pctW = totalCost > 0 ? (100 * r.CostUSD / totalCost).toFixed(1) : '0.0';
    return `<div class="hbar">
      <div class="fill" style="width:${pctW}%"></div>
      <div class="lbl" title="${escHtml(r.Name)}">${escHtml(r.Name)}</div>
      <div class="val">${escHtml(fmtMoney(r.CostUSD))} · ${fmtNum(r.Count)} calls</div>
    </div>`;
  }).join('');
}

function drillHTML(tools, byToolDetail) {
  if (!tools || tools.length === 0) {
    return `<div class="small empty-state">No tool-detail data — no tool received more than one distinct invocation pattern.</div>`;
  }
  return tools.map(tool => {
    const rows = byToolDetail[tool.Name] || [];
    const max = tool.CostUSD || 1;
    const tbody = rows.slice(0, 50).map(r => `<tr data-cost="${r.CostUSD}">
      <td><code>${escHtml(r.Detail)}</code></td>
      <td class="num">${fmtNum(r.Count)}</td>
      <td class="num">${fmtNum(r.TurnCount)}</td>
      <td class="num heat" style="--heat:${Math.min(100,100*r.CostUSD/max).toFixed(1)}%">${escHtml(fmtMoney(r.CostUSD))}</td>
      <td class="num">${pct(r.CostUSD, tool.CostUSD)}</td>
      <td class="num">${fmtNum(r.OutputTokens)}</td>
    </tr>`).join('');
    const more = rows.length > 50
      ? `<tr><td colspan="6" class="small"><em>${rows.length - 50} more rows hidden — use the JSON output (<code>--json</code>) for the full list.</em></td></tr>`
      : '';
    return `<details class="tool-drill" data-table="drill-${escHtml(tool.Name)}">
      <summary>${escHtml(tool.Name)} <span class="small">— ${escHtml(fmtMoney(tool.CostUSD))} across ${fmtNum(tool.Count)} call(s) · ${rows.length} pattern(s)</span></summary>
      <table>
        <thead><tr>
          <th data-key="Detail">Pattern</th>
          <th data-key="Count" class="num">Calls</th>
          <th data-key="TurnCount" class="num">Turns</th>
          <th data-key="CostUSD" class="num">Cost</th>
          <th data-key="Pct" class="num">% of ${escHtml(tool.Name)}</th>
          <th data-key="OutputTokens" class="num">Output tokens</th>
        </tr></thead>
        <tbody>${tbody}${more}</tbody>
      </table>
    </details>`;
  }).join('');
}

const SHELL = `
  <header class="view-head"><h1>${labelIcon('tools')}Tools</h1></header>

  <details class="guide">
    <summary>What "tool cost" means</summary>
    <div class="body">
      <p>Cost attributed to a tool is the cost of the <em>assistant turns that called it</em> — the model has to read the tool's output back into context, so a tool that returns large results inflates the next turn's input cost.</p>
      <ul>
        <li><strong>High Read or Grep cost</strong> → likely reading huge files or grepping with broad patterns. Open the <strong>Drill-down</strong> tab to see the (tool, args) breakdown and look for arguments hitting big files.</li>
        <li><strong>High Bash cost</strong> → often <code>tail</code>, <code>npm test</code>, log streaming, or other commands whose stdout streams large output back into context. Consider redirecting to a file and reading a slice.</li>
        <li><strong>High Edit cost</strong> → not the edit itself; usually the file was large enough that subsequent reads got expensive.</li>
      </ul>
    </div>
  </details>

  <nav class="subtabs" aria-label="Tools sections">
    <a class="subtab is-active" href="#tools/by-tool" data-subtab="by-tool">By tool</a>
    <a class="subtab"           href="#tools/drill"   data-subtab="drill">Drill-down</a>
  </nav>

  <div class="subview is-active" data-subview="by-tool">
    <div class="hbar-list" id="tools-bars"></div>
    <table data-table="tool">
      <thead><tr>
        <th data-key="Name" title="Tool name (Read, Bash, Edit, Grep, Agent, …).">Tool</th>
        <th data-key="Count" class="num" title="Number of times this tool was invoked across all sessions.">Calls</th>
        <th data-key="TurnCount" class="num" title="Assistant turns that called this tool.">Turns</th>
        <th data-key="CostUSD" class="num" title="Cost of the assistant turns that called this tool. Large tool output inflates the next turn's input cost.">Cost</th>
        <th data-key="Pct" class="num" title="Share of total cost in this window.">%</th>
        <th data-key="OutputTokens" class="num" title="Tokens the model generated on those turns (does not include the tool's own output, which is input on the next turn).">Output tokens</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>

  <div class="subview" data-subview="drill">
    <div class="small">Each section is a (tool, argument-pattern) breakdown — e.g. Bash + <code>git status</code>. Open one to dig in.</div>
    <div id="drill-container"></div>
  </div>
`;

function paintToolsSkeletons(container) {
  const bars = container.querySelector('#tools-bars');
  if (bars) bars.innerHTML = hbarListSkeleton(7);
  const tb = container.querySelector('[data-table="tool"] tbody');
  if (tb) tb.innerHTML = tableBodySkeleton(6, 6);
  const drill = container.querySelector('#drill-container');
  if (drill) {
    drill.innerHTML = `<div class="skel-list">
      <span class="skeleton skel-card"></span>
      <span class="skeleton skel-card"></span>
      <span class="skeleton skel-card"></span>
    </div>`;
  }
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

let painted = false;
let navPainted = false;

// paintNav fetches /tools just to derive the sidebar metric (top tool
// name + cost). Called from app.js at startup; full paint() reuses
// the same endpoint via the server's render cache.
export async function paintNav() {
  if (navPainted || painted) return;
  let tools;
  try { tools = await fetchTools(); } catch { return; }
  const byTool = tools.by_tool || [];
  const topTool = byTool[0];
  const navEl = document.getElementById('nav-metric-tools');
  if (navEl) navEl.textContent = topTool
    ? `${truncate(topTool.Name, 14)} · ${fmtMoney(topTool.CostUSD)}`
    : '—';
  navPainted = true;
}

export async function paint(route) {
  const container = document.getElementById('view-tools');
  if (!container) return;

  if (painted) {
    activateSubview(container, route.sub);
    return;
  }

  container.innerHTML = SHELL;
  paintToolsSkeletons(container);

  // Tools needs a totalCost figure for the "%" column. The /tools
  // endpoint doesn't include the overall cost; sourcing from /cost
  // keeps the API surface focused (each section returns its own
  // slice). Fetched in parallel with /tools to avoid serializing.
  let tools, cost;
  try {
    [tools, cost] = await Promise.all([fetchTools(), fetchCost()]);
  } catch (err) {
    container.innerHTML = `<header class="view-head"><h1>${labelIcon('tools')}Tools</h1></header>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load tools data:</strong> ${escHtml(err.message)}</div>`;
    return;
  }

  const byTool = tools.by_tool || [];
  const detail = tools.by_tool_detail || {};
  // total_cost_usd ships from Go (aggregate.Totals().CostUSD); the
  // /cost fetch above is the source for the "%" column denominator.
  const totalCost = cost.total_cost_usd || 0;

  // hbar list — SSR-equivalent client render.
  const barsEl = container.querySelector('#tools-bars');
  if (barsEl) barsEl.innerHTML = toolBarsHTML(byTool, totalCost);

  // Trend column gated on period.
  let toolTrends = null, period = '';
  try {
    toolTrends = await fetchTrends('tool');
    period = (toolTrends && toolTrends.period) || '';
  } catch { /* graceful */ }

  const toolTable = container.querySelector('[data-table="tool"]');
  if (period) injectTrendColumn(toolTable, 'OutputTokens', period);

  buildRows(toolTable, byTool, r => withTrend([
    [escHtml(r.Name), false],
    [fmtNum(r.Count), true],
    [fmtNum(r.TurnCount), true],
    [escHtml(fmtMoney(r.CostUSD)), true, r.CostUSD],
    [pct(r.CostUSD, totalCost), true],
    [fmtNum(r.OutputTokens), true],
  ], 5, toolTrends && toolTrends.series ? toolTrends.series[r.Name] : null, period, trendSpark));

  // Drill-down: render-once, capped at 50 rows per tool. Matches the
  // legacy SSR shape — the global filter just hides rows via CSS class.
  const drillEl = container.querySelector('#drill-container');
  if (drillEl) {
    const toolsWithDetail = byTool.filter(t => detail[t.Name] && detail[t.Name].length);
    drillEl.innerHTML = drillHTML(toolsWithDetail, detail);
  }

  wireGlobalFilters();
  activateSubview(container, route.sub);

  const topTool = byTool[0];
  const navEl = document.getElementById('nav-metric-tools');
  if (navEl) navEl.textContent = topTool
    ? `${truncate(topTool.Name, 14)} · ${fmtMoney(topTool.CostUSD)}`
    : '—';

  painted = true;
  navPainted = true;
}

export function reset() { painted = false; navPainted = false; }
