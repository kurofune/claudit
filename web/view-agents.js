// Agents observability view — "what are my agents doing right now?"
//
// One trace you can audit. The same /_claudit/api/agents payload is shown
// through three LENSES that share ONE selection and ONE persistent detail
// drawer (master-detail, like every serious trace viewer):
//   • Mission Control (#agents/control)   — running agents pinned up top with
//     live ticking timers + current tool, over a reverse-chronological event
//     feed (tool calls / spawns / completions). The second-monitor watch view.
//   • Inspector       (#agents/inspector) — a session→agent tree on the left,
//     the selected agent's step + tool log beside it; every step/tool row
//     selectable.
//   • Flow graph      (#agents/flow)      — main→sub-agent node graph, nodes
//     pulsing while they run.
// Clicking ANY row / card / node / tree item in any lens sets the shared
// selection and repaints the drawer on the right with the full audit payload
// of that agent / step / tool: input, output, status, reasoning, tokens,
// cost, model, duration. Nothing is a dead end.
//
// Liveness is in-place, not a page reload: while this view is active it
// registers an SSE live handler (web/sse.js) that refetches and re-renders
// the active lens on each generation bump, preserving scroll/selection/
// open panels; a 1s ticker advances the elapsed timers between bumps.
//
// This is the SWAPPABLE DOM layer — every number/shape/ref comes from the
// pure, unit-tested helpers in agents-logic.js.

import { fetchAgents } from './api.js';
import { fmtMoney, fmtNum, fmtCompact, escHtml } from './format.js';
import { sessionListSkeleton } from './skeleton.js';
import { setLiveHandler } from './sse.js';
import {
  flattenSession, agentElapsedMs, formatElapsed, graphStats,
  agentLabel, buildEventFeed, buildFlowLayout, parseTime,
  refKey, defaultRef, resolveRef, buildDrawerPayload, agentTokens, baseName,
} from './agents-logic.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

const SHELL = `
  <header class="view-head"><h1>${labelIcon('agents')}Agents</h1></header>

  <details class="guide">
    <summary>Watching agents work</summary>
    <div class="body">
      <p>Three ways to watch the same live data — every Claude Code session expanded into its <em>agent tree</em> (the main agent plus every sub-agent it spawned) — sharing one <strong>detail drawer</strong> on the right. Click any row, card, or node in any lens to inspect exactly what that agent, turn, or tool did.</p>
      <ul>
        <li><strong>Mission Control</strong> pins the agents running <em>right now</em> at the top — ticking timer, current tool — over a live feed of every tool call, spawn, and completion as it happens. Best for keeping an eye on a run.</li>
        <li><strong>Inspector</strong> is the drill-down: pick any agent from the tree and read its step-by-step tool log; click a turn for its reasoning, or a tool for the exact input it sent and the output it got back (✓/✗).</li>
        <li><strong>Flow graph</strong> shows the shape — who spawned whom — with each node pulsing while it works.</li>
        <li><strong>Detail drawer.</strong> Whatever you click fills the right-hand panel: input, output, status, reasoning, tokens, cost, model, duration. Empty sections stay put (collapsed) so the layout never jumps.</li>
        <li><strong>Live.</strong> On an active session this updates in place every couple of seconds — no page reload, your scroll, selection, and open panels stay put. The same <code>?since</code>/<code>?until</code>/<code>?project</code> filters scope this tab too.</li>
      </ul>
    </div>
  </details>

  <nav class="subtabs" aria-label="Agent view lenses">
    <a class="subtab is-active" href="#agents/control"   data-subtab="control">Mission Control</a>
    <a class="subtab"           href="#agents/inspector" data-subtab="inspector">Inspector</a>
    <a class="subtab"           href="#agents/flow"       data-subtab="flow">Flow graph</a>
  </nav>

  <div class="agents-body">
    <div class="agents-lens">
      <div class="subview is-active" data-subview="control"></div>
      <div class="subview" data-subview="inspector"></div>
      <div class="subview" data-subview="flow"></div>
    </div>
    <aside class="agents-drawer" data-drawer aria-label="Selection detail"></aside>
  </div>

  <div id="agents-empty" class="empty-note" hidden>No agents in this window. Try widening <code>--since</code>/<code>--until</code>, or open a session that spawned sub-agents.</div>
`;

const SUBS = ['control', 'inspector', 'flow'];

// View-local state. lastGraph is the most recent payload; the live handler
// and lens switches both re-render against it without a refetch. selectedRef
// is the ONE selection shared across every lens — a refKey string
// (agent "sid#ai" · step "sid#ai.si" · tool "sid#ai.si:ti"), persisted across
// refetch so a live update doesn't reset what the user was inspecting.
let painted = false;
let navPainted = false;
let lastGraph = null;
let activeSub = 'control';
let tickerId = null;
let selectedRef = null;

const colorSlot = i => ((i % 5) + 1);

export function reset() {
  painted = false;
  navPainted = false;
  lastGraph = null;
  selectedRef = null;
}

// paintNav resolves the sidebar metric before the tab is first opened.
export async function paintNav() {
  if (navPainted || painted) return;
  let graph;
  try { graph = await fetchAgents(); } catch { return; }
  lastGraph = graph;
  updateNavMetric(graphStats(graph));
  navPainted = true;
}

export async function paint(route) {
  const container = document.getElementById('view-agents');
  if (!container) return;
  activeSub = wantedSub(route && route.sub);

  // First paint builds the shell + a skeleton; later paints (lens swap)
  // reuse it. Register the live updater + start the timer ticker once.
  if (!painted) {
    container.innerHTML = SHELL;
    wireSelection(container);
    const sv = container.querySelector('.subview[data-subview="control"]');
    if (sv) sv.innerHTML = sessionListSkeleton(3);
    setLiveHandler(liveUpdate);
    startTicker();
  }

  // Keep the live handler ours even on a lens switch (paint re-runs).
  setLiveHandler(liveUpdate);

  if (lastGraph == null) {
    try {
      lastGraph = await fetchAgents();
    } catch (err) {
      container.innerHTML = `<header class="view-head"><h1>${labelIcon('agents')}Agents</h1></header>
        <div class="warning-card" role="alert"><strong class="danger">Failed to load agents:</strong> ${escHtml(err.message)}</div>`;
      painted = false;
      return;
    }
  }

  if (!container.querySelector('.subtabs')) {
    container.innerHTML = SHELL;
    wireSelection(container);
  }
  activateSub(container, activeSub);
  renderActive(container);
  updateNavMetric(graphStats(lastGraph));
  painted = true;
  navPainted = true;
}

function wantedSub(sub) {
  return SUBS.includes(sub) ? sub : 'control';
}

// activateSub toggles the active lens tab + subview, like view-tokens.js.
function activateSub(container, sub) {
  container.querySelectorAll('.subtab[data-subtab]').forEach(t =>
    t.classList.toggle('is-active', t.dataset.subtab === sub));
  container.querySelectorAll('.subview').forEach(s =>
    s.classList.toggle('is-active', s.dataset.subview === sub));
}

// renderActive draws the active lens from lastGraph and repaints the shared
// drawer. preserve=true keeps scroll positions / open tool rows across a live
// re-render.
function renderActive(container, preserve = false) {
  const sessions = (lastGraph && lastGraph.sessions) || [];
  ensureSelection(lastGraph);
  const empty = container.querySelector('#agents-empty');
  if (empty) empty.hidden = sessions.length > 0;

  const host = container.querySelector(`.subview[data-subview="${activeSub}"]`);
  if (!host) return;

  const memo = preserve ? captureState(host) : null;
  if (activeSub === 'control') host.innerHTML = renderControl(sessions);
  else if (activeSub === 'inspector') host.innerHTML = renderInspector(sessions);
  else if (activeSub === 'flow') host.innerHTML = renderFlow(sessions);
  if (memo) restoreState(host, memo);
  renderDrawer(container);
  tickTimers(container);
}

// ── shared selection ──────────────────────────────────────────────────────

// ensureSelection pins a default (the root agent) whenever nothing is
// selected or the current selection no longer resolves against the latest
// graph — so the drawer is never empty and a vanished agent can't strand it.
function ensureSelection(graph) {
  if (selectedRef && resolveRef(graph, selectedRef)) return;
  const d = defaultRef(graph);
  selectedRef = d ? refKey(d) : null;
}

// wireSelection installs the delegated click/keyboard handlers once per shell
// build. Any element carrying data-ref in the lens selects it; the drawer's
// copy button copies the full session id. Delegation survives lens re-renders
// because it's bound to the stable .agents-lens / .agents-drawer wrappers.
function wireSelection(container) {
  const lens = container.querySelector('.agents-lens');
  if (lens) {
    lens.addEventListener('click', e => {
      const el = e.target.closest('[data-ref]');
      if (el) select(container, el.dataset.ref);
    });
    lens.addEventListener('keydown', e => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      const el = e.target.closest('[data-ref]');
      if (el) { e.preventDefault(); select(container, el.dataset.ref); }
    });
  }
  const drawer = container.querySelector('.agents-drawer');
  if (drawer) {
    drawer.addEventListener('click', e => {
      const btn = e.target.closest('[data-copy]');
      if (btn) copyText(btn.dataset.copy, btn);
    });
  }
}

// select sets the shared selection and repaints. The inspector's step log is
// agent-dependent, so switching selection there re-renders the lens (cheap,
// and preserves open rows via captureState); the other lenses just restyle
// the highlight in place and repaint the drawer.
function select(container, ref) {
  if (!ref) return;
  selectedRef = ref;
  if (activeSub === 'inspector') {
    renderActive(container, true);
  } else {
    updateHighlights(container);
    renderDrawer(container);
  }
}

// updateHighlights toggles .is-selected on lens elements without a re-render:
// the exact selected ref, plus the containing agent's card/node (agent-keyed
// elements) so selecting a tool also lights up its agent.
function updateHighlights(container) {
  const sel = resolveRef(lastGraph, selectedRef);
  const agentKey = sel ? refKey({ sessionId: sel.session.session_id, agentIndex: sel.agentIndex }) : null;
  container.querySelectorAll('.agents-lens [data-ref]').forEach(el => {
    const r = el.dataset.ref;
    el.classList.toggle('is-selected', r === selectedRef || r === agentKey);
  });
}

function copyText(text, btn) {
  if (!text || !navigator.clipboard) return;
  navigator.clipboard.writeText(text).then(() => {
    btn.classList.add('is-copied');
    setTimeout(() => btn.classList.remove('is-copied'), 1200);
  }).catch(() => {});
}

// ── live update + timer ticker ──────────────────────────────────────────

// liveUpdate is the SSE in-place handler: refetch, swap lastGraph, re-render
// the active lens preserving the user's place. Errors are swallowed by the
// SSE layer so a transient fetch failure doesn't tear down the stream.
async function liveUpdate() {
  const container = document.getElementById('view-agents');
  if (!container || !painted) return;
  let graph;
  try { graph = await fetchAgents(); } catch { return; }
  lastGraph = graph;
  renderActive(container, true);
  updateNavMetric(graphStats(graph));
}

// startTicker advances every on-screen elapsed timer once a second so a
// running agent's clock moves smoothly between the ~2s data refetches.
function startTicker() {
  if (tickerId != null) return;
  tickerId = setInterval(() => {
    const container = document.getElementById('view-agents');
    if (container && container.classList.contains('is-active')) tickTimers(container);
  }, 1000);
}

// tickTimers rewrites the text of every [data-elapsed] node from its start/
// end/running data-attributes against the current wall clock — no refetch.
function tickTimers(root) {
  const now = Date.now();
  root.querySelectorAll('[data-elapsed]').forEach(el => {
    const start = Number(el.dataset.start);
    if (!Number.isFinite(start)) return;
    const running = el.dataset.running === '1';
    const end = Number(el.dataset.end);
    const ms = running ? Math.max(0, now - start) : Math.max(0, (Number.isFinite(end) ? end : start) - start);
    el.textContent = formatElapsed(ms);
  });
}

// elapsedSpan renders a live-updating elapsed timer for one agent.
function elapsedSpan(agent, extraCls = '') {
  const start = parseTime(agent && agent.started_at);
  const end = parseTime(agent && agent.ended_at);
  const running = agent && agent.status === 'running';
  const ms = agentElapsedMs(agent, Date.now());
  return `<span class="${extraCls}" data-elapsed data-start="${Number.isFinite(start) ? start : ''}" data-end="${Number.isFinite(end) ? end : ''}" data-running="${running ? '1' : '0'}">${escHtml(formatElapsed(ms))}</span>`;
}

// ── kind → color/icon lens (presentational; pure styling) ──────────────────

// toolFamily buckets a tool name (or the synthetic 'agent'/'step' kinds) into
// a color family so the same kind reads identically in every lens and the
// drawer. Unknown tools fall to 'other'.
function toolFamily(name) {
  const n = String(name || '').toLowerCase();
  if (n === 'agent') return 'agent';
  if (n === 'step') return 'step';
  if (n === 'task') return 'task';
  if (n === 'read') return 'read';
  if (n === 'edit' || n === 'write' || n === 'multiedit' || n === 'notebookedit') return 'edit';
  if (n === 'bash') return 'bash';
  if (n === 'grep' || n === 'glob') return 'search';
  if (n === 'webfetch' || n === 'websearch' || n.startsWith('web')) return 'web';
  return 'other';
}

// kindBadge is the small colored monogram that marks a kind: a glyph for the
// agent/step pseudo-kinds, else the tool's initial.
function kindBadge(kind) {
  const fam = toolFamily(kind);
  const glyph = kind === 'agent' ? '◆' : kind === 'step' ? '✦' : (String(kind || '?')[0] || '?').toUpperCase();
  return `<span class="kind-badge kind-${fam}" aria-hidden="true">${escHtml(glyph)}</span>`;
}

// ── shared detail drawer ────────────────────────────────────────────────────

function renderDrawer(container) {
  const drawer = container.querySelector('.agents-drawer');
  if (!drawer) return;
  drawer.innerHTML = drawerHTML(buildDrawerPayload(lastGraph, selectedRef));
}

function drawerHTML(p) {
  if (!p) return `<div class="dr-empty-state">Select an agent, turn, or tool to inspect it here.</div>`;

  const typeLabel = p.type === 'tool' ? 'tool' : p.type === 'step' ? 'turn' : (p.agentKind === 'main' ? 'main agent' : 'sub-agent');
  const desc = p.description ? `<p class="dr-desc">${escHtml(p.description)}</p>` : '';

  // Compact metric chips — only the ones that apply to this kind.
  const metrics = [
    drMetric('cost', p.cost_usd ? fmtMoney(p.cost_usd) : ''),
    drMetric('dur', p.durationMs ? formatElapsed(p.durationMs) : ''),
    drMetric('model', p.model ? shortModel(p.model) : ''),
    drMetric('tokens', p.tokens && p.tokens.total ? `${fmtCompact(p.tokens.total)}` : ''),
    drMetric('steps', p.type === 'agent' && p.stepCount ? fmtNum(p.stepCount) : ''),
  ].filter(Boolean).join('');

  // The same audit skeleton every time so the layout never jumps; empty
  // sections collapse to a dim header rather than disappearing.
  const sections = [
    drSection('Reasoning', p.thinking, true),
    drSection('Narration', p.text, true),
    drSection('Input', p.input, true),
    drSection('Output', p.output, true),
    p.type === 'agent' ? drTokens(p.tokens) : '',
  ].join('');

  return `<div class="dr">
    <div class="dr-head">
      ${kindBadge(p.kind)}
      <span class="dr-title" title="${escHtml(p.title)}">${escHtml(p.title)}</span>
      <span class="dr-type">${escHtml(typeLabel)}</span>
      ${statusPill(p.status)}
    </div>
    <div class="dr-project" title="${escHtml(p.cwd)}">${labelIcon('overview')}<span class="dr-proj-name">${escHtml(p.project || '—')}</span></div>
    <button type="button" class="dr-sid" data-copy="${escHtml(p.sessionId)}" title="Copy session id&#10;${escHtml(p.sessionId)}">
      <span class="dr-sid-id">${escHtml(p.sessionId || '—')}</span>
      <span class="dr-sid-copy">copy</span>
    </button>
    <div class="dr-agentline"><span class="dr-agent">${escHtml(p.agentLabel)}</span>${p.detail ? ` <span class="dr-detail">${escHtml(p.detail)}</span>` : ''}</div>
    ${desc}
    ${metrics ? `<div class="dr-metrics">${metrics}</div>` : ''}
    ${sections}
  </div>`;
}

function drMetric(label, value) {
  return value ? `<span class="dr-metric"><span class="dr-m-k">${escHtml(label)}</span><span class="dr-m-v">${escHtml(value)}</span></span>` : '';
}

function statusPill(status) {
  if (status === 'running') return `<span class="ag-pill ag-running">running</span>`;
  if (status === 'done') return `<span class="ag-pill ag-done">done</span>`;
  if (status === 'ok') return `<span class="ag-pill ag-ok">✓ ok</span>`;
  if (status === 'error') return `<span class="ag-pill ag-err">✗ error</span>`;
  return '';
}

function drSection(label, content, pre) {
  const empty = content == null || content === '';
  if (empty) return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">${escHtml(label)} <span class="dr-none">—</span></h4></section>`;
  const body = pre
    ? `<pre class="dr-pre">${escHtml(content)}</pre>`
    : `<div class="dr-text">${escHtml(content)}</div>`;
  return `<section class="dr-sec"><h4 class="dr-sec-h">${escHtml(label)}</h4>${body}</section>`;
}

function drTokens(t) {
  if (!t || !t.total) return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">Tokens <span class="dr-none">—</span></h4></section>`;
  const cell = (k, v) => `<div class="dr-tok"><span class="dr-tok-k">${k}</span><span class="dr-tok-v">${escHtml(fmtNum(v))}</span></div>`;
  return `<section class="dr-sec"><h4 class="dr-sec-h">Tokens <span class="dr-sec-sum">${escHtml(fmtCompact(t.total))} total</span></h4>
    <div class="dr-toks">${cell('input', t.input)}${cell('output', t.output)}${cell('cache write', t.cacheWrite)}${cell('cache read', t.cacheRead)}</div>
  </section>`;
}

// ── Mission Control ───────────────────────────────────────────────────────

function renderControl(sessions) {
  // Flatten to (session, agent, index) tuples so we can pull the live ones.
  const all = [];
  sessions.forEach(s => flattenSession(s).forEach((a, i) => all.push({ s, a, i })));
  const live = all.filter(x => x.a && x.a.status === 'running');

  const activeHTML = live.length === 0
    ? `<div class="ac-idle">No agents running right now. The feed below is the recent history; it'll come alive the moment one starts.</div>`
    : live
      .sort((x, y) => agentElapsedMs(y.a, Date.now()) - agentElapsedMs(x.a, Date.now()))
      .map(x => activeCardHTML(x.s, x.a, x.i)).join('');

  const feed = buildEventFeed(lastGraph, { limit: 250 });
  const feedHTML = feed.length === 0
    ? `<div class="ac-idle">No activity yet.</div>`
    : feed.map(feedRowHTML).join('');

  return `
    <section class="mc-active">
      <div class="mc-section-head"><span class="mc-dot-live"></span>Active now <span class="mc-count">${live.length}</span></div>
      <div class="mc-active-grid">${activeHTML}</div>
    </section>
    <section class="mc-feed">
      <div class="mc-section-head">Live feed <span class="mc-count">${feed.length}</span></div>
      <div class="agent-feed" tabindex="0">${feedHTML}</div>
    </section>`;
}

function activeCardHTML(session, agent, idx) {
  const sid = (session && session.session_id) || '';
  const ref = refKey({ sessionId: sid, agentIndex: idx });
  const c = colorSlot(idx);
  const sel = ref === selectedRef ? ' is-selected' : '';
  const label = agentLabel(agent);
  const tool = agent.current_tool
    ? `<div class="acard-tool"><span class="acard-tool-name">${escHtml(agent.current_tool)}</span></div>`
    : `<div class="acard-tool acard-tool-idle">working…</div>`;
  const desc = agent.kind !== 'main' && agent.description
    ? `<div class="acard-desc" title="${escHtml(agent.description)}">${escHtml(agent.description)}</div>` : '';
  const steps = (agent.steps || []).length;
  const tokens = agentTokens(agent).total;
  return `<article class="acard is-running${sel}" data-c="${c}" data-ref="${escHtml(ref)}" tabindex="0" role="button">
    <div class="acard-head">
      <span class="acard-pulse"></span>
      <span class="acard-label">${escHtml(label)}</span>
      <span class="acard-proj" title="${escHtml(session.cwd || '')}">${escHtml(baseName(session.cwd))}</span>
      ${elapsedSpan(agent, 'acard-elapsed')}
    </div>
    ${desc}
    ${tool}
    <div class="acard-meta">
      <span>${fmtNum(steps)} step${steps === 1 ? '' : 's'}</span>
      ${tokens ? `<span>${fmtCompact(tokens)} tok</span>` : ''}
      <span class="acard-cost">${escHtml(fmtMoney(agent.cost_usd || 0))}</span>
    </div>
  </article>`;
}

function feedRowHTML(e) {
  const c = colorSlot(e.agentIndex);
  const time = clockTime(e.t);
  const ref = e.kind === 'tool'
    ? refKey({ sessionId: e.sessionId, agentIndex: e.agentIndex, stepIndex: e.stepIndex, toolIndex: e.toolIndex })
    : refKey({ sessionId: e.sessionId, agentIndex: e.agentIndex });
  const sel = ref === selectedRef ? ' is-selected' : '';
  let glyph = '<span class="fe-glyph fe-arrow">→</span>';
  let body;
  let metric = '';
  if (e.kind === 'spawn') {
    glyph = '<span class="fe-glyph fe-spawn">↳</span>';
    body = `<span class="fe-verb">spawn</span> <span class="fe-strong">${escHtml(e.agentLabel)}</span>${e.description ? ` <span class="fe-dim">${escHtml(e.description)}</span>` : ''}`;
  } else if (e.kind === 'done') {
    glyph = '<span class="fe-glyph fe-done">✓</span>';
    body = `<span class="fe-verb">done</span> <span class="fe-dim">${fmtNum(e.steps)} step${e.steps === 1 ? '' : 's'}</span>`;
    metric = feMetric(e.cost_usd, 0);
  } else {
    if (e.status === 'error') glyph = '<span class="fe-glyph fe-err">✗</span>';
    else if (e.status === 'ok') glyph = '<span class="fe-glyph fe-ok">✓</span>';
    const arg = e.input || e.detail;
    body = `<span class="fe-tool">${escHtml(e.tool)}</span>${arg ? ` <span class="fe-arg" title="${escHtml(e.input || e.detail)}">${escHtml(clip(arg, 72))}</span>` : ''}`;
    metric = feMetric(e.cost_usd, e.durationMs);
  }
  return `<div class="fe-row fe-${e.kind}${sel}" data-c="${c}" data-ref="${escHtml(ref)}" tabindex="0" role="button">
    <span class="fe-time">${escHtml(time)}</span>
    <span class="fe-agent" title="${escHtml(e.agentLabel)}">${escHtml(clip(e.agentLabel, 14))}</span>
    ${glyph}
    <span class="fe-body">${body}</span>
    ${metric}
  </div>`;
}

// feMetric is the compact per-row cost·duration chip — the feed doubles as a
// spend/latency heat-map. Renders nothing when both are zero.
function feMetric(cost, ms) {
  const parts = [];
  if (cost) parts.push(fmtMoney(cost));
  if (ms) parts.push(formatElapsed(ms));
  return parts.length ? `<span class="fe-metric">${escHtml(parts.join(' · '))}</span>` : '';
}

// ── Inspector ─────────────────────────────────────────────────────────────

function renderInspector(sessions) {
  const sel = resolveRef(lastGraph, selectedRef);
  const tree = sessions.map((s, si) => inspectorSessionHTML(s, si, sel)).join('');
  const detail = sel
    ? inspectorLogHTML(sel.session, sel.agent, sel.agentIndex)
    : `<div class="ac-idle">Pick an agent on the left.</div>`;
  return `<div class="insp">
    <div class="insp-tree" role="tablist" aria-label="Agents">${tree}</div>
    <div class="insp-log">${detail}</div>
  </div>`;
}

function inspectorSessionHTML(session, si, sel) {
  const sid = session.session_id || '';
  const c = colorSlot(si);
  const agents = flattenSession(session);
  const rows = agents.map((a, i) => {
    const ref = refKey({ sessionId: sid, agentIndex: i });
    const running = a && a.status === 'running';
    const isSel = (sel && sel.session.session_id === sid && sel.agentIndex === i) ? ' is-selected' : '';
    const steps = (a.steps || []).length;
    return `<button type="button" class="insp-agent${isSel}" data-ref="${escHtml(ref)}" data-c="${colorSlot(i)}">
      <span class="insp-dot ${running ? 'is-running' : 'is-done'}"></span>
      <span class="insp-name">${escHtml(agentLabel(a))}</span>
      <span class="insp-sub">${fmtNum(steps)} · ${elapsedSpan(a)}</span>
    </button>`;
  }).join('');
  return `<div class="insp-sess">
    <div class="insp-sess-head" data-c="${c}" title="${escHtml(session.cwd || '')}">
      <span class="insp-sess-proj">${escHtml(baseName(session.cwd) || '—')}</span>
      <span class="insp-sess-sid" title="${escHtml(sid)}">${escHtml(shortId(sid))}</span>
    </div>
    ${rows}
  </div>`;
}

function inspectorLogHTML(session, agent, agentIndex) {
  const sid = session.session_id || '';
  const running = agent.status === 'running';
  const tokens = agentTokens(agent).total;
  const agentRef = refKey({ sessionId: sid, agentIndex });
  const desc = agent.kind !== 'main' && agent.description
    ? `<p class="insp-d-desc">${escHtml(agent.description)}</p>` : '';
  const steps = (agent.steps || []);
  const stepHTML = steps.length === 0
    ? `<div class="ac-idle">No assistant turns recorded.</div>`
    : steps.map((st, i) => inspectorStepHTML(st, i, steps.length, sid, agentIndex)).join('');
  return `<div class="insp-d">
    <div class="insp-d-head${agentRef === selectedRef ? ' is-selected' : ''}" data-ref="${escHtml(agentRef)}" tabindex="0" role="button">
      <span class="insp-dot ${running ? 'is-running' : 'is-done'}"></span>
      <span class="insp-d-name">${escHtml(agentLabel(agent))}</span>
      <span class="ag-pill ${running ? 'ag-running' : 'ag-done'}">${running ? 'running' : 'done'}</span>
      <span class="insp-d-spacer"></span>
      <span class="insp-d-stat">${fmtNum(steps.length)} steps</span>
      ${tokens ? `<span class="insp-d-stat">${fmtCompact(tokens)} tok</span>` : ''}
      <span class="insp-d-stat">${elapsedSpan(agent)}</span>
      <span class="insp-d-stat insp-d-cost">${escHtml(fmtMoney(agent.cost_usd || 0))}</span>
    </div>
    ${desc}
    <div class="insp-steps">${stepHTML}</div>
  </div>`;
}

function inspectorStepHTML(step, i, total, sid, agentIndex) {
  const time = clockTime(parseTime(step.timestamp));
  const ref = refKey({ sessionId: sid, agentIndex, stepIndex: i });
  const sel = ref === selectedRef ? ' is-selected' : '';
  const tools = (step.tools || []);
  const toolHTML = tools.map((t, j) => toolRowHTML(t, sid, agentIndex, i, j)).join('');
  const model = step.model ? `<span class="insp-step-model">${escHtml(shortModel(step.model))}</span>` : '';
  const reasoned = (step.thinking || step.text)
    ? `<span class="insp-step-reason" title="this turn has reasoning — click to read it">✦ reasoned</span>` : '';
  return `<div class="insp-step">
    <div class="insp-step-head${sel}" data-ref="${escHtml(ref)}" tabindex="0" role="button">
      <span class="insp-step-n">${i + 1}/${total}</span>
      <span class="insp-step-time">${escHtml(time)}</span>
      ${model}
      ${reasoned}
      ${step.cost_usd ? `<span class="insp-step-cost">${escHtml(fmtMoney(step.cost_usd))}</span>` : ''}
    </div>
    ${tools.length ? `<div class="insp-tools">${toolHTML}</div>` : ''}
  </div>`;
}

function toolRowHTML(tool, sid, agentIndex, stepIndex, toolIndex) {
  const name = tool.name || '';
  const ref = refKey({ sessionId: sid, agentIndex, stepIndex, toolIndex });
  const sel = ref === selectedRef ? ' is-selected' : '';
  const detail = tool.detail ? `<span class="tr-detail">${escHtml(tool.detail)}</span>` : '';
  const status = tool.status === 'error'
    ? '<span class="tr-status tr-err" title="errored">✗</span>'
    : tool.status === 'ok' ? '<span class="tr-status tr-ok" title="ok">✓</span>' : '';
  const hasBody = (tool.input && tool.input !== '') || (tool.output && tool.output !== '');
  const tkey = `${name}:${tool.detail || ''}:${(tool.input || '').slice(0, 24)}`;
  if (!hasBody) {
    return `<div class="tr${sel}" data-ref="${escHtml(ref)}" tabindex="0" role="button"><span class="tr-row">${kindBadge(name)}<span class="tr-name">${escHtml(name)}</span>${detail}${status}</span></div>`;
  }
  const input = tool.input ? `<div class="tr-io"><span class="tr-io-k">in</span><pre>${escHtml(tool.input)}</pre></div>` : '';
  const output = tool.output ? `<div class="tr-io tr-io-out${tool.status === 'error' ? ' is-err' : ''}"><span class="tr-io-k">out</span><pre>${escHtml(tool.output)}</pre></div>` : '';
  return `<details class="tr tr-exp${sel}" data-tkey="${escHtml(tkey)}" data-ref="${escHtml(ref)}">
    <summary class="tr-row"><span class="tr-caret">▸</span>${kindBadge(name)}<span class="tr-name">${escHtml(name)}</span>${detail}${status}</summary>
    <div class="tr-body">${input}${output}</div>
  </details>`;
}

// ── Flow graph ────────────────────────────────────────────────────────────

function renderFlow(sessions) {
  if (sessions.length === 0) return `<div class="ac-idle">No agents to graph.</div>`;
  const width = flowWidth();
  // Payload is already newest-first (the backend orders by last activity), so
  // an in-progress run sits on top without a client-side re-sort.
  const sel = resolveRef(lastGraph, selectedRef);
  const selAgentKey = sel ? refKey({ sessionId: sel.session.session_id, agentIndex: sel.agentIndex }) : null;
  const graphs = sessions.map((s, si) => flowSessionHTML(s, si, width, selAgentKey)).join('');
  return `<div class="flow-graphs">${graphs}</div>`;
}

function flowSessionHTML(session, si, width, selAgentKey) {
  const layout = buildFlowLayout(session, { width });
  const sid = session.session_id || '';
  const edges = layout.edges.map(ed =>
    `<line class="flow-edge${ed.running ? ' is-running' : ''}" x1="${ed.x1.toFixed(1)}" y1="${ed.y1.toFixed(1)}" x2="${ed.x2.toFixed(1)}" y2="${ed.y2.toFixed(1)}"/>`).join('');
  const nodes = layout.nodes.map(n => flowNodeHTML(n, selAgentKey)).join('');
  return `<div class="flow-sess">
    <div class="flow-sess-head" data-c="${colorSlot(si)}" title="${escHtml(session.cwd || '')}">
      <span class="flow-sess-proj">${escHtml(baseName(session.cwd) || '—')}</span>
      <span class="flow-sess-sid" title="${escHtml(sid)}">${escHtml(shortId(sid))}</span>
    </div>
    <svg class="flow-svg" viewBox="0 0 ${layout.width} ${layout.height}" width="100%" height="${layout.height}" role="img" aria-label="Agent flow graph">
      <g class="flow-edges">${edges}</g>
      <g class="flow-nodes">${nodes}</g>
    </svg>
  </div>`;
}

function flowNodeHTML(n, selAgentKey) {
  // buildFlowLayout keys nodes `${sid}#${flattenIndex}` — exactly an agent ref.
  const running = n.status === 'running';
  const sel = n.key === selAgentKey ? ' is-selected' : '';
  const cls = `flow-node ${n.kind === 'main' ? 'fn-main' : 'fn-sub'}${running ? ' is-running' : ''}${sel}`;
  const sub = running ? 'running' : `${fmtNum(n.steps)} · ${fmtMoney(n.cost_usd || 0)}`;
  return `<g class="${cls}" data-ref="${escHtml(n.key)}" transform="translate(${n.x.toFixed(1)},${n.y.toFixed(1)})" tabindex="0" role="button">
    <rect class="fn-box" width="${n.w}" height="${n.h}" rx="8"/>
    ${running ? `<circle class="fn-pulse" cx="12" cy="12" r="4"/>` : ''}
    <text class="fn-label" x="${n.w / 2}" y="20" text-anchor="middle">${escHtml(clip(n.label, 16))}</text>
    <text class="fn-sub" x="${n.w / 2}" y="36" text-anchor="middle">${escHtml(sub)}</text>
  </g>`;
}

function flowWidth() {
  const host = document.querySelector('#view-agents .subview[data-subview="flow"]');
  const w = host ? host.clientWidth : 0;
  // The shared drawer is outside the lens now, so the flow lens gets its full
  // width; clamp so a lone graph stays readable and a wide one stays bounded.
  const graphW = (w > 0 ? w : 760) - 24;
  return Math.max(420, Math.min(1100, graphW));
}

// ── preserve scroll / open-rows across a live re-render ────────────────────

function captureState(host) {
  const m = { scrolls: {}, openTools: [] };
  host.querySelectorAll('.agent-feed, .insp-tree, .insp-log, .flow-graphs').forEach(el => {
    m.scrolls[scrollKey(el)] = el.scrollTop;
  });
  host.querySelectorAll('details.tr[open]').forEach(d => m.openTools.push(d.dataset.tkey));
  return m;
}

function restoreState(host, m) {
  host.querySelectorAll('.agent-feed, .insp-tree, .insp-log, .flow-graphs').forEach(el => {
    const v = m.scrolls[scrollKey(el)];
    if (v != null) el.scrollTop = v;
  });
  if (m.openTools.length) {
    const want = new Set(m.openTools);
    host.querySelectorAll('details.tr').forEach(d => { if (want.has(d.dataset.tkey)) d.open = true; });
  }
}

const scrollKey = el => el.className.split(' ').find(c => /feed|tree|log|graphs/.test(c)) || el.className;

// ── nav metric + small formatters ────────────────────────────────────────

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

function clockTime(ms) {
  const d = new Date(ms);
  return isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour12: false });
}

function shortId(id) {
  const s = String(id || '');
  return s.length > 8 ? s.slice(0, 8) : s;
}

function shortModel(m) {
  return String(m || '').replace(/^claude-/, '').replace(/-\d{8}$/, '');
}

function clip(s, n) {
  const str = String(s == null ? '' : s);
  return str.length > n ? str.slice(0, n - 1) + '…' : str;
}
