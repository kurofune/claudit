// Agents observability view — the "what are the agents doing" tab.
// Renders each session as a block: a grid of agent cards (the main
// agent plus every sub-agent it spawned) over a swimlane/Gantt SVG that
// places those agents on a shared timeline. Liveness is free — the SSE
// generation-bump reload (web/sse.js) re-runs paint() on a ~2s cadence,
// so running agents advance their elapsed timer and a freshly-spawned
// sub-agent shows up on the next tick without a manual refresh.
//
// This file is the SWAPPABLE layer: every number comes from the
// /_claudit/api/agents JSON contract via the pure helpers in
// agents-logic.js. A redesign rewrites this file against the same
// payload + math and touches nothing else.

import { fetchAgents } from './api.js';
import { fmtMoney, fmtNum, escHtml } from './format.js';
import { chartViewboxWidth } from './charts.js';
import { sessionListSkeleton } from './skeleton.js';
import {
  flattenSession, packLanes, laneCount, makeTimeScale, agentBar,
  agentElapsedMs, formatElapsed, graphStats,
} from './agents-logic.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

const SHELL = `
  <header class="view-head"><h1>${labelIcon('agents')}Agents</h1></header>

  <details class="guide">
    <summary>Watching agents work</summary>
    <div class="body">
      <p>Each block below is one Claude Code session expanded into its <em>agent tree</em>: the main agent plus every sub-agent it spawned (an <code>Agent</code>/<code>Task</code> call). The cards summarize each agent; the swimlane underneath lays them out on a shared timeline so you can see what ran in parallel and for how long.</p>
      <ul>
        <li><strong>A <span class="ag-pill ag-running">running</span> pill</strong> means the agent acted within the last minute — on a live session you'll see it advance every couple of seconds (the dashboard silently refetches on new data). <strong><span class="ag-pill ag-done">done</span></strong> agents have gone quiet.</li>
        <li><strong>Current tool.</strong> A running agent shows the last tool it invoked — a quick read on what it's doing right now.</li>
        <li><strong>The swimlane</strong> packs non-overlapping agents onto the same row; agents that ran at the same time get their own rows. Hover a bar for its type, runtime, and cost. The ticks inside a bar mark each assistant turn.</li>
        <li><strong>Filters apply.</strong> The same <code>?since</code>/<code>?until</code>/<code>?project</code> URL filters that scope every other tab scope this one too.</li>
      </ul>
    </div>
  </details>

  <div id="agents-container" class="agents-container"></div>
  <div id="agents-empty" class="empty-note" hidden>No agents in this window. Try widening <code>--since</code>/<code>--until</code>, or open a session that spawned sub-agents.</div>
`;

let painted = false;
let navPainted = false;

// paintNav fetches /agents just to derive the sidebar metric (agent
// count · live count). Called at startup so the pill resolves before
// the user clicks the tab; full paint() reuses the cached endpoint.
export async function paintNav() {
  if (navPainted || painted) return;
  let graph;
  try { graph = await fetchAgents(); } catch { return; }
  updateNavMetric(graphStats(graph));
  navPainted = true;
}

export async function paint() {
  const container = document.getElementById('view-agents');
  if (!container) return;

  // Re-render on every paint (not guarded by `painted`): the SSE reload
  // re-runs this so a live session's cards/swimlane stay current. The
  // guard only gates the one-time shell + skeleton swap.
  if (!painted) {
    container.innerHTML = SHELL;
    const listEl = container.querySelector('#agents-container');
    if (listEl) listEl.innerHTML = sessionListSkeleton(4);
  }

  let graph;
  try {
    graph = await fetchAgents();
  } catch (err) {
    container.innerHTML = `<header class="view-head"><h1>${labelIcon('agents')}Agents</h1></header>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load agents:</strong> ${escHtml(err.message)}</div>`;
    painted = false;
    return;
  }

  // Ensure the shell exists even if this is the first paint after an error.
  if (!container.querySelector('#agents-container')) {
    container.innerHTML = SHELL;
  }

  renderGraph(container, graph);
  updateNavMetric(graphStats(graph));
  painted = true;
  navPainted = true;
}

export function reset() {
  painted = false;
  navPainted = false;
}

function renderGraph(container, graph) {
  const list = container.querySelector('#agents-container');
  const empty = container.querySelector('#agents-empty');
  if (!list) return;
  const sessions = (graph && graph.sessions) || [];
  if (sessions.length === 0) {
    list.innerHTML = '';
    if (empty) empty.hidden = false;
    return;
  }
  if (empty) empty.hidden = true;
  const nowMs = Date.now();
  // Newest sessions first so an in-progress run sits at the top.
  const ordered = sessions.slice().sort((a, b) => parseTs(b.started_at) - parseTs(a.started_at));
  list.innerHTML = ordered.map((s, i) => sessionBlockHTML(s, (i % 5) + 1, nowMs)).join('');
}

// sessionBlockHTML renders one session: a header summarizing the tree,
// the agent-card grid, and the swimlane SVG.
function sessionBlockHTML(session, colorSlot, nowMs) {
  const agents = flattenSession(session);
  const sid = escHtml(session.session_id || '');
  const cwd = session.cwd || '';
  const running = agents.filter(a => a && a.status === 'running').length;
  const liveChip = running > 0
    ? `<span class="ag-pill ag-running">${running} live</span>`
    : '';
  const countLabel = `${agents.length} agent${agents.length === 1 ? '' : 's'}`;
  const cards = agents.map(a => agentCardHTML(a, nowMs)).join('');
  const swim = swimlaneHTML(session, agents, nowMs);

  return `<section class="agent-session" data-session="${sid}">
    <header class="as-head">
      <span class="as-id" data-c="${colorSlot}" title="${sid}">${sid}</span>
      <span class="as-cwd" title="${escHtml(cwd)}">${cwd === '' ? '&mdash;' : escHtml(cwd)}</span>
      <span class="as-stats">
        <span class="as-count">${countLabel}</span>
        ${liveChip}
        <span class="as-cost">${escHtml(fmtMoney(session.cost_usd || 0))}</span>
      </span>
      <span class="as-time">${escHtml(formatTimeRange(session.started_at, session.ended_at))}</span>
    </header>
    <div class="agent-cards">${cards}</div>
    ${swim}
  </section>`;
}

// agentCardHTML renders one agent (main or sub-agent) as a compact card.
function agentCardHTML(agent, nowMs) {
  if (!agent) return '';
  const isMain = agent.kind === 'main';
  const kindLabel = isMain
    ? 'main'
    : escHtml(agent.agent_type || 'subagent');
  const running = agent.status === 'running';
  const statusCls = running ? 'ag-running' : 'ag-done';
  const cur = running && agent.current_tool
    ? ` <span class="ac-cur" title="current tool">· ${escHtml(agent.current_tool)}</span>`
    : '';
  const statusPill = `<span class="ag-pill ${statusCls}">${running ? 'running' : 'done'}${cur}</span>`;
  const desc = !isMain && agent.description
    ? `<div class="ac-desc" title="${escHtml(agent.description)}">${escHtml(agent.description)}</div>`
    : '';
  const steps = (agent.steps || []).length;
  const elapsed = formatElapsed(agentElapsedMs(agent, nowMs));

  return `<div class="agent-card ${isMain ? 'ac-main' : 'ac-sub'} ${running ? 'is-running' : ''}">
    <div class="ac-head">
      <span class="ac-kind">${kindLabel}</span>
      ${statusPill}
    </div>
    ${desc}
    <div class="ac-meta">
      <span title="assistant turns">${fmtNum(steps)} step${steps === 1 ? '' : 's'}</span>
      <span title="elapsed">${escHtml(elapsed)}</span>
      <span class="ac-cost" title="cost">${escHtml(fmtMoney(agent.cost_usd || 0))}</span>
    </div>
  </div>`;
}

// swimlaneHTML builds the Gantt SVG for a session: one row per packed
// lane, a bar per agent positioned by agents-logic geometry, with step
// ticks inside each bar and a few clock ticks along the bottom. Returns
// '' when there's nothing measurable to draw (single instant / no spans).
function swimlaneHTML(session, agents, nowMs) {
  const packed = packLanes(agents);
  if (packed.length === 0) return '';

  // Window spans every agent; running agents extend to now so their bar
  // grows on each refetch. Fall back to the session's own span.
  let startMs = Infinity, endMs = -Infinity;
  for (const p of packed) {
    if (p.start < startMs) startMs = p.start;
    const end = p.agent && p.agent.status === 'running' ? Math.max(p.end, nowMs) : p.end;
    if (end > endMs) endMs = end;
  }
  if (!(endMs > startMs)) {
    // Degenerate (all instantaneous): give the lane a nominal 1s so bars
    // render at minBlock rather than collapsing.
    endMs = startMs + 1000;
  }

  const lanes = laneCount(packed);
  const W = chartViewboxWidth();
  const padX = 8, padTop = 6, padBottom = 22;
  const laneH = 22, barH = 14;
  const H = padTop + lanes * laneH + padBottom;
  const scale = makeTimeScale({ startMs, endMs, width: W - padX * 2, minBlock: 3 });

  let bars = '';
  for (const item of packed) {
    const agent = item.agent;
    const running = agent && agent.status === 'running';
    // Running agents draw to "now" so the bar tracks live progress.
    const drawItem = running ? { ...item, end: Math.max(item.end, nowMs) } : item;
    const bar = agentBar(drawItem, scale);
    const x = padX + bar.x;
    const y = padTop + item.lane * laneH + (laneH - barH) / 2;
    const isMain = agent && agent.kind === 'main';
    const cls = `ag-bar ${isMain ? 'ag-bar-main' : 'ag-bar-sub'} ${running ? 'is-running' : ''}`;
    const label = isMain ? 'main' : (agent && agent.agent_type) || 'subagent';
    const elapsed = formatElapsed(agentElapsedMs(agent, nowMs));
    const tip = `${label}${agent && agent.description ? ' — ' + agent.description : ''} · ${elapsed} · ${fmtMoney(agent && agent.cost_usd || 0)}`;

    // Step ticks: a thin mark per assistant turn so the bar shows the
    // agent's cadence, not just its span.
    let ticks = '';
    for (const step of (agent && agent.steps) || []) {
      const sx = padX + scale.x(parseTs(step.timestamp));
      if (sx < x || sx > x + bar.width) continue;
      ticks += `<line class="ag-tick" x1="${sx.toFixed(1)}" y1="${(y + 1).toFixed(1)}" x2="${sx.toFixed(1)}" y2="${(y + barH - 1).toFixed(1)}"/>`;
    }

    // Inline label clipped to the bar when it's wide enough to read.
    const labelEl = bar.width > 42
      ? `<text class="ag-bar-label" x="${(x + 5).toFixed(1)}" y="${(y + barH - 4).toFixed(1)}">${escHtml(label)}</text>`
      : '';

    bars += `<g class="ag-bar-g">
      <rect class="${cls}" x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${bar.width.toFixed(1)}" height="${barH}" rx="3">
        <title>${escHtml(tip)}</title>
      </rect>
      ${ticks}
      ${labelEl}
    </g>`;
  }

  // Bottom clock axis — ~5 evenly spaced ticks across the window.
  let axis = '';
  const N = 5;
  for (let i = 0; i < N; i++) {
    const t = startMs + (i / (N - 1)) * (endMs - startMs);
    const ax = padX + scale.x(t);
    const anchor = i === 0 ? 'start' : i === N - 1 ? 'end' : 'middle';
    axis += `<text class="ag-axis-tick" x="${ax.toFixed(1)}" y="${(H - 6).toFixed(1)}" text-anchor="${anchor}">${escHtml(formatClock(t))}</text>`;
  }

  return `<div class="agent-swimlane">
    <svg viewBox="0 0 ${W} ${H}" width="100%" height="${H}" preserveAspectRatio="xMidYMid meet" role="img" aria-label="Agent timeline">
      ${bars}
      ${axis}
    </svg>
  </div>`;
}

function updateNavMetric(stats) {
  const el = document.getElementById('nav-metric-agents');
  if (!el) return;
  if (!stats || stats.agents === 0) {
    el.textContent = '—';
    el.removeAttribute('title');
    return;
  }
  el.textContent = stats.running > 0
    ? `${fmtNum(stats.agents)} · ${fmtNum(stats.running)} live`
    : `${fmtNum(stats.agents)} agents`;
  el.title = `${stats.agents} agents across ${stats.sessions} session${stats.sessions === 1 ? '' : 's'}${stats.running > 0 ? `; ${stats.running} running now` : ''}`;
}

// ── small time formatters (local; the swimlane needs clock labels the
// shared format.js helpers don't cover) ────────────────────────────
function parseTs(ts) {
  if (typeof ts === 'number') return ts;
  const t = Date.parse(ts);
  return Number.isNaN(t) ? 0 : t;
}

function formatClock(ms) {
  const d = new Date(ms);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour12: false });
}

function formatTime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  const pad = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function formatTimeRange(startTs, endTs) {
  const left = formatTime(startTs);
  const right = formatTime(endTs);
  if (!left && !right) return '';
  return `${left} → ${right}`;
}
