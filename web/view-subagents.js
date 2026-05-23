// Subagents view — paints three subviews:
//   1. Main vs sidechain — stacked bar + small comparison table.
//   2. By subagent type — per-type roll-up table.
//   3. Top invocations — every individual subagent run.
//
// Sidebar metric is the sidechain share of total spend; uses /overview
// for total cost (the /subagents endpoint carries only the sidechain
// half).

import { fetchSubagents, fetchOverview } from './api.js';
import { fmtNum, fmtMoney, fmtPct, fmtDate, escHtml, truncate, pct } from './format.js';
import { buildRows, wireGlobalFilters } from './table.js';
import { tableBodySkeleton, stackedSkeleton } from './skeleton.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

const SHELL = `
  <header class="view-head"><h1>${labelIcon('subagents')}Subagents</h1></header>

  <details class="guide">
    <summary>Main thread vs sidechain — when delegation pays off</summary>
    <div class="body">
      <p>The <strong>main</strong> thread is your direct conversation. <strong>Sidechain</strong> = work delegated to subagents (the Agent tool, custom agents in <code>~/.claude/agents</code>). Sidechains keep main context lean, but every invocation starts with a cold cache.</p>
      <ul>
        <li><strong>Sidechain cost &gt; main cost</strong> → likely over-delegating. A subagent that does &lt; ~2 minutes of real work usually costs more in cold-cache tax than the main-context bytes it saves.</li>
        <li><strong>One subagent type dominating sidechain</strong> → that agent is the highest-value optimization target. Trim its system prompt and tool list — smaller starting context means a smaller cold-cache tax on every invocation.</li>
        <li><strong>Top invocations</strong> surfaces single subagent runs that took unusually long — often a sign the agent's task was poorly scoped, or the agent kept thrashing instead of converging.</li>
      </ul>
    </div>
  </details>

  <nav class="subtabs" aria-label="Subagents sections">
    <a class="subtab is-active" href="#subagents/mainside"    data-subtab="mainside">Main vs sidechain</a>
    <a class="subtab"           href="#subagents/by-type"     data-subtab="by-type">By subagent type</a>
    <a class="subtab"           href="#subagents/invocations" data-subtab="invocations">Top invocations</a>
  </nav>

  <div class="subview is-active" data-subview="mainside">
    <div class="stacked" id="stacked-main"></div>
    <table data-table="mainside">
      <thead><tr>
        <th title="main = your direct conversation. sidechain = work done in subagents (Agent tool, custom agents).">Bucket</th>
        <th class="num" title="Dollar cost of this bucket.">Cost</th>
        <th class="num" title="Share of total cost in this window.">%</th>
        <th class="num" title="Assistant turns in this bucket.">Turns</th>
        <th class="num" title="Tokens served from cache in this bucket. Sidechain cache reads are usually small — every invocation starts cold.">Cache read</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>

  <div class="subview" data-subview="by-type">
    <table data-table="subagent">
      <thead><tr>
        <th data-key="Type" title="Subagent type (the agent name from ~/.claude/agents or the built-in Agent tool).">Subagent</th>
        <th data-key="CostUSD" class="num" title="Total dollar cost across all invocations of this subagent type.">Cost</th>
        <th data-key="PctSide" class="num" title="Share of sidechain cost (not total cost). Use to spot which subagent type dominates your delegation.">% of sidechain</th>
        <th data-key="Turns" class="num" title="Total assistant turns across all invocations of this subagent type.">Turns</th>
        <th data-key="OutputTokens" class="num" title="Tokens the model generated across those turns.">Output</th>
        <th data-key="CacheReadTokens" class="num" title="Tokens served from cache (only possible within a single invocation, after the first turn).">Cache read</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>

  <div class="subview" data-subview="invocations">
    <div class="small">Each row is one subagent run (one <code>agent-&lt;id&gt;.jsonl</code> file). Use the global filter above to narrow by subagent type, description, or project.</div>
    <table data-table="invocation">
      <thead><tr>
        <th data-key="SubagentType" title="Subagent type for this invocation.">Subagent</th>
        <th data-key="Description" title="The description argument passed when this subagent was invoked.">Description</th>
        <th data-key="CostUSD" class="num" title="Dollar cost of this single invocation. Outliers here often signal a poorly-scoped delegation.">Cost</th>
        <th data-key="Turns" class="num" title="Assistant turns in this invocation.">Turns</th>
        <th data-key="Project" title="Working directory the parent session was running in.">Project</th>
        <th data-key="First" title="Timestamp this subagent invocation started.">Started</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>
`;

function paintSubagentsSkeletons(container) {
  const stacked = container.querySelector('#stacked-main');
  if (stacked) stacked.innerHTML = stackedSkeleton();
  const tables = {
    mainside: 5,
    subagent: 6,
    invocation: 6,
  };
  for (const [name, cols] of Object.entries(tables)) {
    const tb = container.querySelector(`[data-table="${name}"] tbody`);
    if (tb) tb.innerHTML = tableBodySkeleton(cols, name === 'mainside' ? 2 : 6);
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

// paintNav fetches /subagents + /overview to derive the sidebar metric
// (sidechain cost · share of total). Called from app.js at startup;
// full paint() reuses both endpoints via the server's render cache.
export async function paintNav() {
  if (navPainted || painted) return;
  let subagents, overview;
  try {
    [subagents, overview] = await Promise.all([fetchSubagents(), fetchOverview()]);
  } catch { return; }
  const side = subagents.sidechain || { cost: 0 };
  const sideCost = side.cost || 0;
  const totalCost = (overview.totals && overview.totals.CostUSD) || 0;
  const navEl = document.getElementById('nav-metric-subagents');
  if (navEl) {
    navEl.textContent = totalCost > 0
      ? `${fmtMoney(sideCost)} · ${fmtPct(sideCost, totalCost)}`
      : '—';
  }
  navPainted = true;
}

export async function paint(route) {
  const container = document.getElementById('view-subagents');
  if (!container) return;

  if (painted) {
    activateSubview(container, route.sub);
    return;
  }

  container.innerHTML = SHELL;
  paintSubagentsSkeletons(container);

  let subagents, overview;
  try {
    [subagents, overview] = await Promise.all([
      fetchSubagents(),
      fetchOverview(),
    ]);
  } catch (err) {
    container.innerHTML = `<header class="view-head"><h1>${labelIcon('subagents')}Subagents</h1></header>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load subagents data:</strong> ${escHtml(err.message)}</div>`;
    return;
  }

  const bySub = subagents.by_subagent || [];
  const invs = subagents.agent_invocations || [];
  const main = subagents.main || { cost: 0, turns: 0, tokens: {} };
  const side = subagents.sidechain || { cost: 0, turns: 0, tokens: {} };
  const totalCost = (overview.totals && overview.totals.CostUSD) || 0;
  const sideCost = side.cost || 0;
  const totalMainSide = main.cost + side.cost;

  // Stacked bar — main vs sidechain.
  const stacked = container.querySelector('#stacked-main');
  if (stacked && totalMainSide > 0) {
    const mp = 100 * main.cost / totalMainSide;
    const sp = 100 - mp;
    stacked.innerHTML = `
      <div class="bucket bucket-main" style="width:${mp}%" title="main: ${escHtml(fmtMoney(main.cost))}">main · ${mp.toFixed(1)}%</div>
      <div class="bucket bucket-side" style="width:${sp}%" title="sidechain: ${escHtml(fmtMoney(side.cost))}">sidechain · ${sp.toFixed(1)}%</div>`;
  }

  // Mainside table — no sort (the rows are fixed: main, sidechain).
  const mainsideBody = container.querySelector('[data-table="mainside"] tbody');
  if (mainsideBody) {
    const buckets = [
      ['main', main.cost, main.turns, (main.tokens || {}).CacheReadTokens || 0],
      ['sidechain', side.cost, side.turns, (side.tokens || {}).CacheReadTokens || 0],
    ];
    mainsideBody.innerHTML = buckets.map(([n, c, t, cr]) =>
      `<tr data-cost="${c}"><td>${n}</td><td class="num">${escHtml(fmtMoney(c))}</td><td class="num">${fmtPct(c, totalMainSide)}</td><td class="num">${fmtNum(t)}</td><td class="num">${fmtNum(cr)}</td></tr>`
    ).join('');
  }

  buildRows(container.querySelector('[data-table="subagent"]'), bySub, r => [
    [escHtml(r.Type), false],
    [escHtml(fmtMoney(r.CostUSD)), true, r.CostUSD],
    [pct(r.CostUSD, sideCost), true],
    [fmtNum(r.Turns), true],
    [fmtNum(r.OutputTokens), true],
    [fmtNum(r.CacheReadTokens), true],
  ]);

  buildRows(container.querySelector('[data-table="invocation"]'), invs, r => [
    [escHtml(r.SubagentType || '(unknown)'), false],
    [`<span title="${escHtml(r.Description)}">${escHtml(truncate(r.Description, 80))}</span>`, false],
    [escHtml(fmtMoney(r.CostUSD)), true, r.CostUSD],
    [fmtNum(r.Turns), true],
    [`<span class="truncate path" title="${escHtml(r.Project)}">${escHtml(truncate(r.Project, 80))}</span>`, false],
    [fmtDate(r.First), false],
  ]);

  wireGlobalFilters();
  activateSubview(container, route.sub);

  const navEl = document.getElementById('nav-metric-subagents');
  if (navEl) {
    navEl.textContent = totalCost > 0
      ? `${fmtMoney(sideCost)} · ${fmtPct(sideCost, totalCost)}`
      : '—';
  }

  painted = true;
  navPainted = true;
}

export function reset() { painted = false; navPainted = false; }
