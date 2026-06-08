// Agents observability view — "what are my agents doing right now?"
//
// One trace you can audit. The same /_claudit/api/agents payload is shown
// through three LENSES that share ONE selection and ONE persistent detail
// drawer (master-detail, like every serious trace viewer):
//   • Feed  (#agents/feed) — running agents pinned up top with live ticking
//     timers + current tool, over a reverse-chronological event feed (tool
//     calls / spawns / completions). The second-monitor watch view.
//   • Tree  (#agents/tree) — a session→agent tree on the left, the selected
//     agent's step + tool log beside it; every step/tool row selectable.
//   • Timeline (#agents/timeline) — a horizontal Gantt on a real time axis:
//     one row per agent (indented by spawn depth), bar = lifetime, overlap =
//     concurrency. Live runs grow at the right edge ("now"). Bars click → drawer.
// The sub-tab nav is a LENS SWITCH: picking a lens swaps ONLY the left pane;
// the shared selection and the detail drawer on the right carry over unchanged.
// Clicking ANY row / card / node / tree item in any lens sets the shared
// selection and repaints the drawer with the full audit payload of that
// agent / step / tool: input, output, status, reasoning, tokens, cost, model,
// duration. Nothing is a dead end.
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
  agentLabel, buildEventFeed, buildTimeline, parseTime,
  refKey, defaultRef, resolveRef, buildDrawerPayload, agentTokens, baseName,
  looksTruncated,
} from './agents-logic.js';
import { fetchAgentToolFull } from './api.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

// isServeMode is true when a live claudit server is backing the page. The
// static HTML report inlines its data into window.__claudit_static_data and
// has no disk to read at view time, so the drawer's "show full" affordance
// (which fetches untruncated tool I/O from disk) is serve-only.
function isServeMode() {
  return !(typeof window !== 'undefined' && window.__claudit_static_data);
}

const SHELL = `
  <header class="view-head"><h1>${labelIcon('agents')}Agents</h1></header>

  <details class="guide">
    <summary>Watching agents work</summary>
    <div class="body">
      <p>Three <em>lenses</em> on the same live data — every Claude Code session expanded into its <em>agent tree</em> (the main agent plus every sub-agent it spawned) — over one shared <strong>selection</strong> and one <strong>detail drawer</strong> on the right. Switching lens swaps only the left pane; what you've selected and the drawer stay put. Click any row, card, or node in any lens to inspect exactly what that agent, turn, or tool did.</p>
      <ul>
        <li><strong>Feed</strong> pins the agents running <em>right now</em> at the top — ticking timer, current tool — over a live feed of every tool call, spawn, and completion as it happens. Best for keeping an eye on a run.</li>
        <li><strong>Tree</strong> is the drill-down: pick any agent from the tree and read its step-by-step tool log; click a turn for its reasoning, or a tool for the exact input it sent and the output it got back (✓/✗).</li>
        <li><strong>Timeline</strong> is the Gantt: one row per agent on a real time axis, indented by who spawned whom, bar width = how long it ran, and overlapping bars = agents that ran at the same time. A live run grows at the right edge ("now"); scroll back into history and a <em>● now</em> button brings you back.</li>
        <li><strong>Detail drawer.</strong> Whatever you click fills the right-hand panel: input, output, status, reasoning, tokens, cost, model, duration. Empty sections stay put (collapsed) so the layout never jumps, and the panel survives lens switches.</li>
        <li><strong>Live.</strong> On an active session this updates in place every couple of seconds — no page reload, your scroll, selection, and open panels stay put. The same <code>?since</code>/<code>?until</code>/<code>?project</code> filters scope this tab too.</li>
      </ul>
    </div>
  </details>

  <nav class="subtabs" aria-label="Agent view lenses">
    <a class="subtab is-active" href="#agents/feed" data-subtab="feed">Feed</a>
    <a class="subtab"           href="#agents/tree" data-subtab="tree">Tree</a>
    <a class="subtab"           href="#agents/timeline" data-subtab="timeline">Timeline</a>
  </nav>

  <div class="agents-body">
    <div class="agents-lens">
      <div class="subview is-active" data-subview="feed"></div>
      <div class="subview" data-subview="tree"></div>
      <div class="subview" data-subview="timeline"></div>
    </div>
    <aside class="agents-drawer" data-drawer aria-label="Selection detail"></aside>
  </div>

  <div id="agents-empty" class="empty-note" hidden>No agents in this window. Try widening <code>--since</code>/<code>--until</code>, or open a session that spawned sub-agents.</div>
`;

const SUBS = ['feed', 'tree', 'timeline'];

// View-local state. lastGraph is the most recent payload; the live handler
// and lens switches both re-render against it without a refetch. selectedRef
// is the ONE selection shared across every lens — a refKey string
// (agent "sid#ai" · step "sid#ai.si" · tool "sid#ai.si:ti"), persisted across
// refetch so a live update doesn't reset what the user was inspecting.
let painted = false;
let navPainted = false;
let lastGraph = null;
let activeSub = 'feed';
let tickerId = null;
let selectedRef = null;

// Timeline lens state. prevTimelineKeys tracks which agent rows existed on the
// last render so a genuinely NEW agent fades in (and the rest don't re-animate
// on every live re-render). timelinePinned[sid]=true means the user scrolled a
// session's Gantt back into history, so live updates must NOT yank it to the
// "now" edge — the #1 live-trace UX trap; the "● now" button clears it.
let prevTimelineKeys = new Set();
const timelinePinned = new Map();
let liveScheduled = false;

const colorSlot = i => ((i % 5) + 1);

export function reset() {
  painted = false;
  navPainted = false;
  lastGraph = null;
  selectedRef = null;
  prevTimelineKeys = new Set();
  timelinePinned.clear();
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
    const sv = container.querySelector('.subview[data-subview="feed"]');
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
  // A lens switch (paint re-runs on the same already-built shell) swaps only
  // the left pane — the shared selection + drawer carry over untouched, so we
  // skip the drawer repaint. The first paint (and live updates) repaint it.
  const lensSwitch = painted;
  activateSub(container, activeSub);
  renderActive(container, false, !lensSwitch);
  updateNavMetric(graphStats(lastGraph));
  painted = true;
  navPainted = true;
}

function wantedSub(sub) {
  return SUBS.includes(sub) ? sub : 'feed';
}

// activateSub toggles the active lens tab + subview, like view-tokens.js.
function activateSub(container, sub) {
  container.querySelectorAll('.subtab[data-subtab]').forEach(t =>
    t.classList.toggle('is-active', t.dataset.subtab === sub));
  container.querySelectorAll('.subview').forEach(s =>
    s.classList.toggle('is-active', s.dataset.subview === sub));
}

// renderActive draws the active lens from lastGraph. preserve=true keeps
// scroll positions / open tool rows across a live re-render. paintDrawer=true
// (the default) repaints the shared drawer; a pure lens switch passes false so
// the drawer — already showing the unchanged selection — is left intact.
function renderActive(container, preserve = false, paintDrawer = true) {
  const sessions = (lastGraph && lastGraph.sessions) || [];
  ensureSelection(lastGraph);
  const empty = container.querySelector('#agents-empty');
  if (empty) empty.hidden = sessions.length > 0;

  const host = container.querySelector(`.subview[data-subview="${activeSub}"]`);
  if (!host) return;

  const memo = preserve ? captureState(host) : null;
  if (activeSub === 'feed') host.innerHTML = renderControl(sessions);
  else if (activeSub === 'tree') host.innerHTML = renderInspector(sessions);
  else if (activeSub === 'timeline') host.innerHTML = renderTimeline(sessions);
  if (memo) restoreState(host, memo);
  // The Gantt's horizontal scroll (live-edge follow + restored history offset)
  // can only be set on real DOM, so it runs after innerHTML is in place.
  if (activeSub === 'timeline') syncTimelineScroll(container);
  if (paintDrawer) renderDrawer(container);
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
      // The Timeline's "● now" button is not a selection — it re-pins the Gantt
      // to the live edge; handle it before the data-ref selection delegate.
      const jump = e.target.closest('[data-tljump]');
      if (jump) { jumpToNow(container, jump.dataset.tljump); return; }
      const el = e.target.closest('[data-ref]');
      if (el) select(container, el.dataset.ref);
    });
    lens.addEventListener('keydown', e => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      const el = e.target.closest('[data-ref]');
      if (el) { e.preventDefault(); select(container, el.dataset.ref); }
    });
    // Scroll doesn't bubble, so catch it in the capture phase: when the user
    // drags a session's Gantt away from the right edge we "pin" it so a live
    // update won't yank them back to now; returning to the edge un-pins.
    lens.addEventListener('scroll', e => onTimelineScroll(e), true);
  }
  const drawer = container.querySelector('.agents-drawer');
  if (drawer) {
    drawer.addEventListener('click', e => {
      const copy = e.target.closest('[data-copy]');
      if (copy) { copyText(copy.dataset.copy, copy); return; }
      const full = e.target.closest('[data-loadfull]');
      if (full) loadFull(full);
    });
  }
}

// select sets the shared selection and repaints. The Tree lens's step log is
// agent-dependent, so switching selection there re-renders the lens (cheap,
// and preserves open rows via captureState); the other lenses just restyle
// the highlight in place and repaint the drawer.
function select(container, ref) {
  if (!ref) return;
  selectedRef = ref;
  if (activeSub === 'tree') {
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

// liveUpdate is the SSE in-place handler. It RENDER-BATCHES: a burst of
// generation bumps (e.g. a fan-out spawning many sub-agents at once) coalesces
// into a single refetch + re-render ~100ms later, so the timeline doesn't
// thrash. Errors are swallowed by the SSE layer so a transient fetch failure
// doesn't tear down the stream.
function liveUpdate() {
  if (liveScheduled) return;
  liveScheduled = true;
  setTimeout(flushLiveUpdate, 100);
}

async function flushLiveUpdate() {
  liveScheduled = false;
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
    drIOSection('Input', p.input, 'input', p),
    drIOSection('Output', p.output, 'output', p),
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

// drIOSection renders a tool's Input/Output as a <pre>, with a "show full"
// affordance when the snippet was truncated (looksTruncated). In serve mode
// that's a button that loads the untruncated content from disk; in static
// mode there's no disk, so it degrades to a clear "snippet only" label rather
// than a dead button.
function drIOSection(label, content, field, p) {
  const empty = content == null || content === '';
  if (empty) {
    return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">${escHtml(label)} <span class="dr-none">—</span></h4></section>`;
  }
  let affordance = '';
  if (p.type === 'tool' && p.toolId && looksTruncated(content)) {
    affordance = isServeMode()
      ? `<button type="button" class="dr-full-btn" data-loadfull="${escHtml(field)}" data-session="${escHtml(p.sessionId)}" data-tool="${escHtml(p.toolId)}">show full</button>`
      : `<span class="dr-full-note" title="Run claudit serve to load the full content">snippet only</span>`;
  }
  return `<section class="dr-sec"><h4 class="dr-sec-h">${escHtml(label)}${affordance}</h4><pre class="dr-pre">${escHtml(content)}</pre></section>`;
}

// loadFull handles a "show full" click: fetch the untruncated tool I/O from
// the server and swap it into the section's <pre>. The button is removed once
// the full content is in (there's nothing more to load); a failure re-enables
// it so the user can retry.
async function loadFull(btn) {
  const sec = btn.closest('.dr-sec');
  const pre = sec && sec.querySelector('.dr-pre');
  const { session, tool, loadfull: field } = btn.dataset;
  if (!pre || !session || !tool) return;
  btn.disabled = true;
  btn.textContent = 'loading…';
  try {
    const d = await fetchAgentToolFull(session, tool);
    pre.textContent = (field === 'output' ? d.output : d.input) || '';
    btn.remove();
  } catch {
    btn.textContent = 'failed — retry';
    btn.disabled = false;
  }
}

function drTokens(t) {
  if (!t || !t.total) return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">Tokens <span class="dr-none">—</span></h4></section>`;
  const cell = (k, v) => `<div class="dr-tok"><span class="dr-tok-k">${k}</span><span class="dr-tok-v">${escHtml(fmtNum(v))}</span></div>`;
  return `<section class="dr-sec"><h4 class="dr-sec-h">Tokens <span class="dr-sec-sum">${escHtml(fmtCompact(t.total))} total</span></h4>
    <div class="dr-toks">${cell('input', t.input)}${cell('output', t.output)}${cell('cache write', t.cacheWrite)}${cell('cache read', t.cacheRead)}</div>
  </section>`;
}

// ── Feed lens (formerly Mission Control) ────────────────────────────────────

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

// ── Tree lens (formerly Inspector) ──────────────────────────────────────────

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

// ── Timeline (Gantt) lens ───────────────────────────────────────────────────

// renderTimeline draws one horizontal Gantt per session: a real time axis with
// one row per agent, bar = lifetime, overlap = concurrency. Geometry comes from
// the pure, unit-tested buildTimeline; this layer only emits SVG + wires the
// live-edge scroll behavior. prevTimelineKeys → an agent that's new this render
// fades in (the rest don't re-animate).
function renderTimeline(sessions) {
  if (sessions.length === 0) return `<div class="ac-idle">No agents to plot.</div>`;
  const hostW = timelineHostW();
  const nowMs = Date.now();
  const sel = resolveRef(lastGraph, selectedRef);
  const selAgentKey = sel ? refKey({ sessionId: sel.session.session_id, agentIndex: sel.agentIndex }) : null;
  const seen = prevTimelineKeys;
  const next = new Set();
  const html = sessions.map((s, si) => timelineSessionHTML(s, si, hostW, nowMs, selAgentKey, seen, next)).join('');
  prevTimelineKeys = next;
  return `<div class="timeline-lens">${html}</div>`;
}

function timelineSessionHTML(session, si, hostW, nowMs, selAgentKey, seen, next) {
  const sid = session.session_id || '';
  const tl = buildTimeline(session, { hostW, nowMs });
  const running = tl.rows.some(r => r.running);
  // The rows start at y = axisH (buildTimeline), so the first row's top is the
  // axis baseline — tick labels sit above it, gridlines run from it down.
  const axisY = tl.rows.length ? tl.rows[0].y : 20;

  const ticks = tl.ticks.map(tk =>
    `<g class="tl-tick"><line x1="${tk.x.toFixed(1)}" y1="${axisY}" x2="${tk.x.toFixed(1)}" y2="${tl.height}"/><text x="${(tk.x + 3).toFixed(1)}" y="${(axisY - 7).toFixed(1)}">${escHtml(clockTime(tk.t))}</text></g>`).join('');
  const nowLine = running && tl.nowX != null
    ? `<line class="tl-now" x1="${tl.nowX.toFixed(1)}" y1="0" x2="${tl.nowX.toFixed(1)}" y2="${tl.height}"/>` : '';
  const rows = tl.rows.map(r => timelineRowHTML(r, tl, selAgentKey, seen, next)).join('');

  return `<div class="timeline-sess">
    <div class="timeline-sess-head" data-c="${colorSlot(si)}" title="${escHtml(session.cwd || '')}">
      <span class="timeline-sess-proj">${escHtml(baseName(session.cwd) || '—')}</span>
      <span class="timeline-sess-sid" title="${escHtml(sid)}">${escHtml(shortId(sid))}</span>
      <button type="button" class="timeline-jump" data-tljump="${escHtml(sid)}" hidden>● now</button>
    </div>
    <div class="timeline-scroll" data-tlscroll="${escHtml(sid)}">
      <svg class="timeline-svg" viewBox="0 0 ${tl.contentW} ${tl.height}" width="${tl.contentW}" height="${tl.height}" role="img" aria-label="Agent timeline">
        <g class="tl-grid">${ticks}</g>
        <g class="tl-rows">${rows}</g>
        ${nowLine}
      </svg>
    </div>
  </div>`;
}

function timelineRowHTML(r, tl, selAgentKey, seen, next) {
  next.add(r.key);
  const isNew = !seen.has(r.key);
  const sel = r.key === selAgentKey ? ' is-selected' : '';
  const barH = Math.max(6, r.h - 10);
  const barY = r.y + Math.round((r.h - barH) / 2);
  const cx = (r.x + r.w).toFixed(1);
  const cy = (barY + barH / 2).toFixed(1);
  const cls = `tl-row ${r.kind === 'main' ? 'tl-main' : 'tl-sub'}${r.running ? ' is-running' : ''}${sel}${isNew ? ' is-new' : ''}`;
  const meta = r.running ? 'running' : `${fmtNum(r.steps)} · ${fmtMoney(r.cost_usd || 0)}`;
  return `<g class="${cls}" data-ref="${escHtml(r.key)}" tabindex="0" role="button">
    <rect class="tl-rowbg" x="0" y="${r.y}" width="${tl.contentW}" height="${r.h}"/>
    <text class="tl-label" x="${r.labelX}" y="${(r.y + r.h / 2 + 4).toFixed(1)}">${escHtml(clip(r.label, 16))}</text>
    <rect class="tl-bar" x="${r.x.toFixed(1)}" y="${barY}" width="${r.w.toFixed(1)}" height="${barH}" rx="3">
      <title>${escHtml(r.label)} — ${escHtml(meta)}</title>
    </rect>
    ${r.running ? `<circle class="tl-pulse" cx="${cx}" cy="${cy}" r="3.5"/>` : ''}
  </g>`;
}

function timelineHostW() {
  const host = document.querySelector('#view-agents .subview[data-subview="timeline"]');
  const w = host ? host.clientWidth : 0;
  // Leave room for the session card's padding/border; clamp so the fit-width
  // floor stays readable when the lens is narrow.
  return Math.max(420, (w > 0 ? w : 760) - 28);
}

// syncTimelineScroll applies the live-edge follow after a (re)render: a session
// with a running agent and horizontal overflow auto-scrolls to "now" UNLESS the
// user pinned it by scrolling into history, in which case the "● now" button is
// revealed instead. Runs on real DOM (scroll offsets need layout).
function syncTimelineScroll(container) {
  container.querySelectorAll('.timeline-scroll[data-tlscroll]').forEach(sc => {
    const sid = sc.dataset.tlscroll;
    const sess = sc.closest('.timeline-sess');
    const overflow = sc.scrollWidth - sc.clientWidth > 2;
    const running = !!(sess && sess.querySelector('.tl-row.is-running'));
    const pinned = timelinePinned.get(sid) === true;
    if (overflow && running && !pinned) sc.scrollLeft = sc.scrollWidth;
    const jump = sess && sess.querySelector('[data-tljump]');
    if (jump) jump.hidden = !(overflow && running && pinned);
  });
}

// onTimelineScroll pins/un-pins a session as the user drags its Gantt: scrolled
// off the right edge → pinned (live updates won't yank it); back at the edge →
// un-pinned (resumes following). Toggles the "● now" button to match.
function onTimelineScroll(e) {
  const sc = e.target.closest && e.target.closest('.timeline-scroll[data-tlscroll]');
  if (!sc) return;
  const sid = sc.dataset.tlscroll;
  const overflow = sc.scrollWidth - sc.clientWidth > 2;
  const atEdge = sc.scrollLeft + sc.clientWidth >= sc.scrollWidth - 2;
  timelinePinned.set(sid, overflow && !atEdge);
  const sess = sc.closest('.timeline-sess');
  const running = !!(sess && sess.querySelector('.tl-row.is-running'));
  const jump = sess && sess.querySelector('[data-tljump]');
  if (jump) jump.hidden = !(overflow && running && !atEdge);
}

// jumpToNow re-pins a session to the live edge and snaps its Gantt there.
function jumpToNow(container, sid) {
  const sc = container.querySelector(`.timeline-scroll[data-tlscroll="${sid}"]`);
  if (!sc) return;
  timelinePinned.set(sid, false);
  sc.scrollLeft = sc.scrollWidth;
  const sess = sc.closest('.timeline-sess');
  const jump = sess && sess.querySelector('[data-tljump]');
  if (jump) jump.hidden = true;
}

// ── preserve scroll / open-rows across a live re-render ────────────────────

function captureState(host) {
  const m = { scrolls: {}, openTools: [], tlScroll: {} };
  host.querySelectorAll('.agent-feed, .insp-tree, .insp-log, .timeline-lens').forEach(el => {
    m.scrolls[scrollKey(el)] = el.scrollTop;
  });
  // Per-session horizontal Gantt offset, so a live update that rebuilds the SVG
  // doesn't reset a user who scrolled into history (syncTimelineScroll then
  // overrides only the sessions still following the live edge).
  host.querySelectorAll('.timeline-scroll[data-tlscroll]').forEach(sc => {
    m.tlScroll[sc.dataset.tlscroll] = sc.scrollLeft;
  });
  host.querySelectorAll('details.tr[open]').forEach(d => m.openTools.push(d.dataset.tkey));
  return m;
}

function restoreState(host, m) {
  host.querySelectorAll('.agent-feed, .insp-tree, .insp-log, .timeline-lens').forEach(el => {
    const v = m.scrolls[scrollKey(el)];
    if (v != null) el.scrollTop = v;
  });
  host.querySelectorAll('.timeline-scroll[data-tlscroll]').forEach(sc => {
    const v = m.tlScroll[sc.dataset.tlscroll];
    if (v != null) sc.scrollLeft = v;
  });
  if (m.openTools.length) {
    const want = new Set(m.openTools);
    host.querySelectorAll('details.tr').forEach(d => { if (want.has(d.dataset.tkey)) d.open = true; });
  }
}

const scrollKey = el => el.className.split(' ').find(c => /feed|tree|log|timeline-lens/.test(c)) || el.className;

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
