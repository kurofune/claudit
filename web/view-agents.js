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
  agentLabel, buildEventFeed, parseTime,
  refKey, defaultRef, resolveRef, buildDrawerPayload, agentTokens, baseName,
  looksTruncated, timelineAtTime, playheadBounds, playheadStats,
  filterTrace, specActive, parseRefKey, detectRetries, spawnTargetIndex,
  conversationSegments,
  conversationReplies,
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
    <a class="subtab"           href="#agents/conversation" data-subtab="conversation">Conversation</a>
  </nav>

  <div class="trace-filter" data-trace-filter role="search" aria-label="Filter this trace"></div>

  <div class="agents-body">
    <div class="agents-lens">
      <div class="subview is-active" data-subview="feed"></div>
      <div class="subview" data-subview="tree"></div>
      <div class="subview" data-subview="timeline"></div>
      <div class="subview" data-subview="conversation"></div>
    </div>
    <aside class="agents-drawer" data-drawer aria-label="Selection detail"></aside>
  </div>

  <div id="agents-empty" class="empty-note" hidden>No agents in this window. Try widening <code>--since</code>/<code>--until</code>, or open a session that spawned sub-agents.</div>
`;

const SUBS = ['feed', 'tree', 'timeline', 'conversation'];

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

// fullCache keys loaded-full tool I/O by tool_use id → { input?, output? }.
// When the user clicks "show full", the untruncated content is cached here and
// fed into buildDrawerPayload on EVERY drawer paint, so a live SSE re-render
// keeps the expanded content sticky instead of reverting to the snippet.
let fullCache = {};

// Timeline lens state. prevTimelineKeys tracks which agent rows existed on the
// last render so a genuinely NEW agent fades in (and the rest don't re-animate
// on every live re-render). timelinePinned[sid]=true means the user scrolled a
// session's Gantt back into history, so live updates must NOT yank it to the
// "now" edge — the #1 live-trace UX trap; the "● now" button clears it.
let prevTimelineKeys = new Set();
const timelinePinned = new Map();
let liveScheduled = false;

// Timeline scrubber state. playheadT is the instant the Gantt is rendered "as
// of": null means LIVE (the playhead follows now and auto-advances on each
// refetch); a number pauses it at that absolute epoch-ms, so the bars/counts
// recompute from events ≤ T (a pure seek, never an incremental replay).
let playheadT = null;
let scrubRaf = 0;

// Trace filter (Phase 2). filterSpec is the live filter over the loaded graph;
// when specActive, filterTrace gives the Set of matching refKeys and every lens
// dims the rest. filterBarBuilt guards the one-time bar build (kind chips depend
// on the graph) so a live re-render never wipes the user's typing or focus.
// hitIndex steps the selection through ordered matches via the ‹ › buttons.
let filterSpec = { text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: 0 };
let filterBarBuilt = false;
let hitIndex = -1;
let currentHits = [];

const colorSlot = i => ((i % 5) + 1);

export function reset() {
  painted = false;
  navPainted = false;
  lastGraph = null;
  selectedRef = null;
  fullCache = {};
  prevTimelineKeys = new Set();
  timelinePinned.clear();
  playheadT = null;
  filterSpec = { text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: 0 };
  filterBarBuilt = false;
  hitIndex = -1;
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
  ensureFilterBar(container);
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
  else if (activeSub === 'conversation') host.innerHTML = renderConversation(sessions);
  if (memo) restoreState(host, memo);
  // The Gantt's horizontal scroll (live-edge follow + restored history offset)
  // can only be set on real DOM, so it runs after innerHTML is in place.
  if (activeSub === 'timeline') syncTimelineScroll(container);
  if (paintDrawer) renderDrawer(container);
  applyFilter(container);
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
      // The scrubber's "● live" toggle resumes live playhead-follow.
      const live = e.target.closest('[data-tllive]');
      if (live) { setLive(container); return; }
      const el = e.target.closest('[data-ref]');
      if (el) select(container, el.dataset.ref);
    });
    // The playhead scrubber fires `input` as it's dragged — recompute the
    // Gantt "as of" the new instant T (rAF-coalesced so the drag stays smooth).
    lens.addEventListener('input', e => {
      const range = e.target.closest('[data-tlrange]');
      if (range) onScrub(container, range);
    });
    // The Conversation lens's session picker switches which conversation shows
    // by re-pointing the shared selection at the chosen session's main agent.
    lens.addEventListener('change', e => {
      const pick = e.target.closest('[data-conv-pick]');
      if (pick) pickConversation(container, pick.value);
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
      if (full) { loadFull(full); return; }
      // A data-ref inside the drawer (the retry "attempt N of M" link) jumps the
      // shared selection — same delegate the lenses use.
      const el = e.target.closest('[data-ref]');
      if (el) select(container, el.dataset.ref);
    });
  }
  const bar = container.querySelector('[data-trace-filter]');
  if (bar) {
    bar.addEventListener('input', e => onFilterInput(container, e));
    bar.addEventListener('click', e => onFilterClick(container, e));
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

// pickConversation switches the Conversation lens to another session by pointing
// the shared selection at that session's main agent (agentIndex 0), then doing a
// full re-render so the lens shows the new dialogue and the drawer follows. No
// scroll preserve — a new conversation opens at its top, as you'd expect.
function pickConversation(container, sessionId) {
  if (!sessionId) return;
  selectedRef = refKey({ sessionId, agentIndex: 0 });
  renderActive(container, false);
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

// ── trace filter (Phase 2) ──────────────────────────────────────────────────
// One bar above the lenses turns the trace from something you read into
// something you interrogate: free text + kind chips + an errors toggle + slow/
// expensive thresholds. Matching (filterTrace) is pure and runs against the
// already-loaded graph, so it works in the offline static report with no
// round-trip. Non-matching rows dim across ALL lenses via a post-render DOM
// pass (no lens renderer needs to know about the filter); ‹ › step the shared
// selection through the matches.

const FILTER_KIND_ORDER = ['agent', 'edit', 'exec', 'read', 'web', 'skill', 'mcp', 'command', 'todo', 'other'];

// presentKinds lists the kinds that actually occur in the graph, in canonical
// order — the chip row only offers kinds the user can act on.
function presentKinds(graph) {
  const seen = new Set();
  for (const s of (graph && graph.sessions) || []) {
    for (const a of flattenSession(s)) {
      for (const st of a.steps || []) {
        for (const t of st.tools || []) if (t && t.kind) seen.add(t.kind);
      }
    }
  }
  return FILTER_KIND_ORDER.filter(k => seen.has(k));
}

// ensureFilterBar fills the (persistent) bar exactly once. Kind chips reflect
// the loaded graph; rebuilding on every live update would wipe the user's
// focus/typing, so this is strictly idempotent.
function ensureFilterBar(container) {
  if (filterBarBuilt) return;
  const bar = container.querySelector('[data-trace-filter]');
  if (!bar) return;
  const chips = presentKinds(lastGraph).map(k =>
    `<button type="button" class="tf-chip kind-${kindFamily(k)}" data-tf-kind="${k}" aria-pressed="false" title="Show only ${escHtml(k)} calls">${kindBadge(k)}<span class="tf-chip-label">${escHtml(k)}</span></button>`).join('');
  bar.innerHTML = `
    <input type="search" class="tf-text" data-tf-text placeholder="filter trace — name, path, input, reasoning…" aria-label="Filter trace by text">
    <div class="tf-chips" role="group" aria-label="Tool kinds">${chips}</div>
    <button type="button" class="tf-toggle" data-tf-errors aria-pressed="false" title="Only tool calls that errored">✗ errors</button>
    <label class="tf-num" title="Turns at least this slow"><span aria-hidden="true">⏱</span> ≥<input type="number" data-tf-dur min="0" step="500" placeholder="0"><span class="tf-unit">ms</span></label>
    <label class="tf-num" title="Turns or agents at least this costly"><span aria-hidden="true">$</span> ≥<input type="number" data-tf-cost min="0" step="0.01" placeholder="0"></label>
    <div class="tf-result" data-tf-result hidden>
      <span class="tf-count" data-tf-count></span>
      <button type="button" class="tf-nav" data-tf-prev title="Previous match" aria-label="Previous match">‹</button>
      <button type="button" class="tf-nav" data-tf-next title="Next match" aria-label="Next match">›</button>
      <button type="button" class="tf-clear" data-tf-clear title="Clear the filter">clear</button>
    </div>`;
  filterBarBuilt = true;
}

// readSpec pulls the current spec out of the bar's controls.
function readSpec(container) {
  const bar = container.querySelector('[data-trace-filter]');
  if (!bar) return filterSpec;
  const val = sel => { const el = bar.querySelector(sel); return el ? el.value : ''; };
  const pressed = sel => { const el = bar.querySelector(sel); return !!el && el.getAttribute('aria-pressed') === 'true'; };
  return {
    text: val('[data-tf-text]'),
    kinds: [...bar.querySelectorAll('[data-tf-kind][aria-pressed="true"]')].map(b => b.dataset.tfKind),
    errorsOnly: pressed('[data-tf-errors]'),
    minDurationMs: Number(val('[data-tf-dur]')) || 0,
    minCostUSD: Number(val('[data-tf-cost]')) || 0,
  };
}

// applyFilter recomputes the match set and dims every non-matching lens row.
// Called after each (re)render so live updates and lens switches keep dimming.
function applyFilter(container) {
  const active = specActive(filterSpec);
  const matchSet = active ? filterTrace(lastGraph, filterSpec) : null;
  const lens = container.querySelector('.agents-lens');
  if (lens) {
    lens.classList.toggle('is-filtering', active);
    lens.querySelectorAll('[data-ref]').forEach(el => {
      el.classList.toggle('is-dimmed', active && !(matchSet && matchSet.has(el.dataset.ref)));
    });
  }
  currentHits = matchSet ? matchHits(matchSet) : [];
  if (hitIndex >= currentHits.length) hitIndex = -1;
  updateFilterResult(container);
}

// matchHits is the ordered list of "deepest" matched refs — a matched ref with
// no matched descendant — which is what ‹ › steps through. Sorted in reading
// order (session order, then agent/step/tool index).
function matchHits(matchSet) {
  const refs = [...matchSet];
  const order = new Map(((lastGraph && lastGraph.sessions) || []).map((s, i) => [(s && s.session_id) || '', i]));
  const leaves = refs.filter(r => !refs.some(o => o !== r && (o.startsWith(r + '.') || o.startsWith(r + ':'))));
  const rank = r => {
    const p = parseRefKey(r) || {};
    return [order.has(p.sessionId) ? order.get(p.sessionId) : 1e9, p.agentIndex ?? 1e9, p.stepIndex ?? -1, p.toolIndex ?? -1];
  };
  return leaves.sort((a, b) => {
    const ra = rank(a), rb = rank(b);
    for (let i = 0; i < ra.length; i++) if (ra[i] !== rb[i]) return ra[i] - rb[i];
    return 0;
  });
}

// updateFilterResult refreshes the "N matches" readout and prev/next state.
function updateFilterResult(container) {
  const bar = container.querySelector('[data-trace-filter]');
  if (!bar) return;
  const result = bar.querySelector('[data-tf-result]');
  const countEl = bar.querySelector('[data-tf-count]');
  if (!specActive(filterSpec)) { if (result) result.hidden = true; return; }
  if (result) result.hidden = false;
  const n = currentHits.length;
  if (countEl) {
    countEl.textContent = hitIndex >= 0 && n ? `${hitIndex + 1} / ${n} matches` : `${n} match${n === 1 ? '' : 'es'}`;
    countEl.classList.toggle('is-empty', n === 0);
  }
  bar.querySelectorAll('[data-tf-prev],[data-tf-next]').forEach(b => { b.disabled = n === 0; });
}

// stepHit advances the shared selection to the next/prev match and scrolls it
// into view in the current lens (the drawer still updates even if the active
// lens has no row for that ref — e.g. a tool hit while on the Timeline).
function stepHit(container, dir) {
  if (!currentHits.length) return;
  hitIndex = (hitIndex + dir + currentHits.length) % currentHits.length;
  const ref = currentHits[hitIndex];
  select(container, ref);
  const el = container.querySelector(`.agents-lens [data-ref="${ref}"]`);
  if (el && el.scrollIntoView) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  updateFilterResult(container);
}

// clearFilter resets the controls and removes all dimming.
function clearFilter(container) {
  const bar = container.querySelector('[data-trace-filter]');
  if (bar) {
    bar.querySelectorAll('[data-tf-text],[data-tf-dur],[data-tf-cost]').forEach(i => { i.value = ''; });
    bar.querySelectorAll('[data-tf-kind],[data-tf-errors]').forEach(b => b.setAttribute('aria-pressed', 'false'));
  }
  filterSpec = { text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: 0 };
  hitIndex = -1;
  applyFilter(container);
}

// onFilterInput/onFilterClick are the bar's delegated handlers (wired once).
function onFilterInput(container, e) {
  if (!e.target.closest('[data-tf-text],[data-tf-dur],[data-tf-cost]')) return;
  filterSpec = readSpec(container);
  hitIndex = -1;
  applyFilter(container);
}

function onFilterClick(container, e) {
  const toggle = e.target.closest('[data-tf-kind],[data-tf-errors]');
  if (toggle) {
    toggle.setAttribute('aria-pressed', toggle.getAttribute('aria-pressed') === 'true' ? 'false' : 'true');
    filterSpec = readSpec(container);
    hitIndex = -1;
    applyFilter(container);
    return;
  }
  if (e.target.closest('[data-tf-prev]')) { stepHit(container, -1); return; }
  if (e.target.closest('[data-tf-next]')) { stepHit(container, +1); return; }
  if (e.target.closest('[data-tf-clear]')) { clearFilter(container); }
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

// KIND_GLYPH is the monogram for each normalized ToolKind (Phase 1), plus the
// 'agent'/'step' pseudo-kinds. Picked to be visually distinct — the old
// "first letter" rule collided edit/exec → "E". The loud kinds (agent/edit/
// exec) get iconic glyphs; the rest get a clear monogram.
const KIND_GLYPH = {
  agent: '◆', edit: '✎', exec: '❯', read: 'R', web: 'W',
  skill: 'S', mcp: 'M', command: '/', todo: '☑', other: '•', step: '✦',
};

// kindFamily maps a normalized ToolKind (or an 'agent'/'step' pseudo-kind) to
// its CSS color-family class so the same kind reads identically in every lens
// and the drawer. The enum values are the class names 1:1; anything unknown
// (e.g. legacy data with no kind) falls to 'other'.
function kindFamily(kind) {
  return Object.prototype.hasOwnProperty.call(KIND_GLYPH, kind) ? kind : 'other';
}

// kindBadge is the small colored monogram that marks a kind. Takes a normalized
// ToolKind ("exec"/"read"/…) or a pseudo-kind ("agent"/"step") — NOT a raw tool
// name (that mapping now lives in the backend's aggregate.ToolKind).
function kindBadge(kind) {
  const fam = kindFamily(kind);
  return `<span class="kind-badge kind-${fam}" aria-hidden="true">${escHtml(KIND_GLYPH[fam])}</span>`;
}

// ── shared detail drawer ────────────────────────────────────────────────────

function renderDrawer(container) {
  const drawer = container.querySelector('.agents-drawer');
  if (!drawer) return;
  drawer.innerHTML = drawerHTML(buildDrawerPayload(lastGraph, selectedRef, fullCache), retryInfoFor(selectedRef));
}

// retryInfoFor reports whether the selected tool is a retry of an earlier
// errored call of the same (kind,name,detail) — { attempt, total, ofRefKey } —
// or null. `total` is the chain length (max attempt sharing the same first
// call); ofRefKey is the full refKey of attempt 1 so the drawer can link back.
// Pure lookup over detectRetries for the selected tool's agent.
function retryInfoFor(ref) {
  const parsed = parseRefKey(ref);
  if (!parsed || parsed.type !== 'tool') return null;
  const r = resolveRef(lastGraph, ref);
  if (!r || r.type !== 'tool') return null;
  const retries = detectRetries(r.agent);
  const entry = retries.get(`${parsed.stepIndex}:${parsed.toolIndex}`);
  if (!entry) return null;
  let total = entry.attempt;
  for (const v of retries.values()) {
    if (v.ofRef === entry.ofRef && v.attempt > total) total = v.attempt;
  }
  return { attempt: entry.attempt, total, ofRefKey: `${r.session.session_id || ''}#${r.agentIndex}.${entry.ofRef}` };
}

function drawerHTML(p, retry = null) {
  if (!p) return `<div class="dr-empty-state">Select an agent, turn, or tool to inspect it here.</div>`;

  // A retry chain: this tool repeats an earlier call that errored. Link back to
  // the first attempt so the whole chain is one click apart.
  const retryRow = retry
    ? `<button type="button" class="dr-retry" data-ref="${escHtml(retry.ofRefKey)}" title="Jump to the first attempt">
         <span class="dr-retry-icon" aria-hidden="true">↻</span> attempt ${retry.attempt} of ${retry.total}
       </button>`
    : '';

  const typeLabel = p.type === 'tool' ? 'tool' : p.type === 'step' ? 'turn' : (p.agentKind === 'main' ? 'main agent' : 'sub-agent');
  const desc = p.description ? `<p class="dr-desc">${escHtml(p.description)}</p>` : '';

  // An Agent call links straight to the sub-agent it launched, with that
  // sub-agent's rolled-up cost/errors — one decision's blast radius, one click
  // away. Only a navigable child (in this window) gets a link.
  const spawnRow = (p.spawned && p.spawned.childRef)
    ? `<button type="button" class="dr-spawn" data-ref="${escHtml(p.spawned.childRef)}" title="Jump to the sub-agent this call launched">
         <span class="dr-spawn-icon" aria-hidden="true">↳</span>
         <span class="dr-spawn-label">sub-agent</span>
         <span class="dr-spawn-stats">+${escHtml(fmtMoney(p.spawned.cost_usd || 0))}${p.spawned.error_count ? ` · ${fmtNum(p.spawned.error_count)} ${p.spawned.error_count === 1 ? 'error' : 'errors'}` : ''}</span>
       </button>`
    : '';

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
    ${retryRow}
    ${spawnRow}
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
    const fullText = (field === 'output' ? d.output : d.input) || '';
    // Cache by tool_use id so the next drawer paint (e.g. a live SSE tick)
    // re-applies the full content instead of reverting to the snippet.
    fullCache[tool] = { ...(fullCache[tool] || {}), [field]: fullText };
    pre.textContent = fullText;
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
    body = `${kindBadge(e.toolKind)}<span class="fe-tool kind-${kindFamily(e.toolKind)}">${escHtml(e.tool)}</span>${arg ? ` <span class="fe-arg" title="${escHtml(e.input || e.detail)}">${escHtml(clip(arg, 72))}</span>` : ''}`;
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
    const errs = (a && a.error_count) || 0;
    const errBadge = errs > 0
      ? `<span class="insp-err" title="${errs} ${errs === 1 ? 'error' : 'errors'}">✗${fmtNum(errs)}</span>` : '';
    return `<button type="button" class="insp-agent${isSel}" data-ref="${escHtml(ref)}" data-c="${colorSlot(i)}">
      <span class="insp-dot ${running ? 'is-running' : 'is-done'}"></span>
      <span class="insp-name">${escHtml(agentLabel(a))}</span>
      <span class="insp-sub">${fmtNum(steps)} · ${elapsedSpan(a)}</span>
      ${errBadge}
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
    : steps.map((st, i) => inspectorStepHTML(st, i, steps.length, sid, agentIndex, session)).join('');
  return `<div class="insp-d">
    <div class="insp-d-head${agentRef === selectedRef ? ' is-selected' : ''}" data-ref="${escHtml(agentRef)}" tabindex="0" role="button">
      <span class="insp-dot ${running ? 'is-running' : 'is-done'}"></span>
      <span class="insp-d-name">${escHtml(agentLabel(agent))}</span>
      <span class="ag-pill ${running ? 'ag-running' : 'ag-done'}">${running ? 'running' : 'done'}</span>
      <span class="insp-d-spacer"></span>
      <span class="insp-d-stat">${fmtNum(steps.length)} steps</span>
      ${agent.error_count ? `<span class="insp-d-stat insp-d-err" title="tool calls that errored">✗ ${fmtNum(agent.error_count)} ${agent.error_count === 1 ? 'error' : 'errors'}</span>` : ''}
      ${tokens ? `<span class="insp-d-stat">${fmtCompact(tokens)} tok</span>` : ''}
      <span class="insp-d-stat">${elapsedSpan(agent)}</span>
      <span class="insp-d-stat insp-d-cost">${escHtml(fmtMoney(agent.cost_usd || 0))}</span>
    </div>
    ${desc}
    <div class="insp-steps">${stepHTML}</div>
  </div>`;
}

function inspectorStepHTML(step, i, total, sid, agentIndex, session) {
  const time = clockTime(parseTime(step.timestamp));
  const ref = refKey({ sessionId: sid, agentIndex, stepIndex: i });
  const sel = ref === selectedRef ? ' is-selected' : '';
  const tools = (step.tools || []);
  const toolHTML = tools.map((t, j) => toolRowHTML(t, sid, agentIndex, i, j, session)).join('');
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

function toolRowHTML(tool, sid, agentIndex, stepIndex, toolIndex, session) {
  const name = tool.name || '';
  const ref = refKey({ sessionId: sid, agentIndex, stepIndex, toolIndex });
  const sel = ref === selectedRef ? ' is-selected' : '';
  const detail = tool.detail ? `<span class="tr-detail">${escHtml(tool.detail)}</span>` : '';
  const status = tool.status === 'error'
    ? '<span class="tr-status tr-err" title="errored">✗</span>'
    : tool.status === 'ok' ? '<span class="tr-status tr-ok" title="ok">✓</span>' : '';
  // A spawning Agent call shows its sub-agent's cost inline (the blast radius
  // of one decision), with the full clickable rollup nested directly beneath.
  const costBadge = tool.spawned
    ? `<span class="tr-spawn-cost" title="cost of the sub-agent this call launched">+${escHtml(fmtMoney(tool.spawned.cost_usd || 0))}</span>` : '';
  const spawnRow = tool.spawned ? spawnRowHTML(tool, session) : '';
  const hasBody = (tool.input && tool.input !== '') || (tool.output && tool.output !== '');
  const tkey = `${name}:${tool.detail || ''}:${(tool.input || '').slice(0, 24)}`;
  if (!hasBody) {
    return `<div class="tr${sel}" data-ref="${escHtml(ref)}" tabindex="0" role="button"><span class="tr-row">${kindBadge(tool.kind)}<span class="tr-name">${escHtml(name)}</span>${detail}${status}${costBadge}</span></div>${spawnRow}`;
  }
  const input = tool.input ? `<div class="tr-io"><span class="tr-io-k">in</span><pre>${escHtml(tool.input)}</pre></div>` : '';
  const output = tool.output ? `<div class="tr-io tr-io-out${tool.status === 'error' ? ' is-err' : ''}"><span class="tr-io-k">out</span><pre>${escHtml(tool.output)}</pre></div>` : '';
  return `<details class="tr tr-exp${sel}" data-tkey="${escHtml(tkey)}" data-ref="${escHtml(ref)}">
    <summary class="tr-row"><span class="tr-caret">▸</span>${kindBadge(tool.kind)}<span class="tr-name">${escHtml(name)}</span>${detail}${status}${costBadge}</summary>
    <div class="tr-body">${input}${output}</div>
  </details>${spawnRow}`;
}

// spawnRowHTML renders the nested sub-agent affordance under a spawning Agent
// call: the child's label plus the rolled-up "+$X · N tools · M errors across
// sub-agent". When the sub-agent is in this window it's a button that jumps the
// shared selection to it (data-ref = the child's agent refKey); otherwise it's
// a static badge. This is the Tree lens's nesting — each sub-agent shown under
// the exact step/tool that launched it.
function spawnRowHTML(tool, session) {
  const sp = tool.spawned;
  if (!sp || !session) return '';
  const idx = spawnTargetIndex(session, sp.agent_ref);
  const child = idx == null ? null : flattenSession(session)[idx];
  const parts = [`+${fmtMoney(sp.cost_usd || 0)}`];
  if (child) {
    const n = (child.steps || []).reduce((sum, st) => sum + (st.tools || []).length, 0);
    parts.push(`${fmtNum(n)} ${n === 1 ? 'tool' : 'tools'}`);
  }
  const errs = sp.error_count || 0;
  if (errs) parts.push(`${fmtNum(errs)} ${errs === 1 ? 'error' : 'errors'}`);
  const stats = `${parts.join(' · ')} across sub-agent`;
  const label = child ? agentLabel(child) : 'sub-agent';
  if (idx == null) {
    return `<div class="tr-spawn" title="This sub-agent isn't in the current window">
      <span class="tr-spawn-arrow" aria-hidden="true">↳</span>
      <span class="tr-spawn-label">${escHtml(label)}</span>
      <span class="tr-spawn-stats">${escHtml(stats)}</span>
    </div>`;
  }
  const childRef = refKey({ sessionId: session.session_id || '', agentIndex: idx });
  const selCls = childRef === selectedRef ? ' is-selected' : '';
  return `<button type="button" class="tr-spawn is-link${selCls}" data-ref="${escHtml(childRef)}" title="Jump to this sub-agent">
    <span class="tr-spawn-arrow" aria-hidden="true">↳</span>
    <span class="tr-spawn-label">${escHtml(label)}</span>
    <span class="tr-spawn-stats">${escHtml(stats)}</span>
  </button>`;
}

// ── Conversation lens ────────────────────────────────────────────────────────
//
// "What did I ask, and how did it respond?" — the SELECTED session's main agent
// read as a chat thread: each user-prompt bubble followed by the assistant's
// spoken replies (conversationSegments slices main.steps by prompt marker;
// conversationReplies keeps only the steps that produced text). Tool calls are
// deliberately absent — this lens is the dialogue, not the trace; the tools and
// reasoning are a click away in the shared drawer (each bubble carries the SAME
// refKey the other lenses use, main = agentIndex 0) and in the Tree/Timeline
// lenses. Sub-agents are out of frame too — this is the human↔main-agent talk.
//
// ONE session at a time, keyed off the shared selection: a "conversation"
// stacking thousands of sessions is neither readable nor renderable (full
// inline prose across every session is megabytes of DOM rebuilt on each live
// tick). Selecting any turn/agent in another lens, then switching here, scopes
// the thread to that session; the sticky header names which one.
function renderConversation(sessions) {
  const withMain = sessions.filter(s => s && s.main);
  if (withMain.length === 0) {
    return `<div class="ac-idle">No conversation in this window.</div>`;
  }
  const sel = resolveRef(lastGraph, selectedRef);
  const session = (sel && sel.session && sel.session.main) ? sel.session : withMain[0];
  const si = sessions.findIndex(s => s && s.session_id === session.session_id);
  return `<div class="conv">${conversationSessionHTML(session, si < 0 ? 0 : si, withMain)}</div>`;
}

// conversationSessionHTML renders the chosen session's header + dialogue. When
// more than one session is in the window the header is a <select> picker (the
// in-lens way to switch which conversation you're reading — selection elsewhere
// still scopes it too); with a single session it's just the project + short id.
function conversationSessionHTML(session, si, candidates) {
  const sid = session.session_id || '';
  const c = colorSlot(si);
  const segs = conversationSegments(session);
  const body = segs.length === 0
    ? `<div class="ac-idle">No assistant turns recorded.</div>`
    : segs.map(seg => conversationSegmentHTML(seg, sid, session)).join('');
  const head = (candidates && candidates.length > 1)
    ? `<select class="conv-sess-pick" data-conv-pick aria-label="Choose which conversation to read">
        ${candidates.map(cd => {
          const cid = cd.session_id || '';
          const label = `${baseName(cd.cwd) || '—'} · ${shortId(cid)}`;
          return `<option value="${escHtml(cid)}"${cid === sid ? ' selected' : ''}>${escHtml(label)}</option>`;
        }).join('')}
      </select>
      <span class="conv-sess-count">${candidates.length} sessions</span>`
    : `<span class="conv-sess-proj">${escHtml(baseName(session.cwd) || '—')}</span>
       <span class="conv-sess-sid" title="${escHtml(sid)}">${escHtml(shortId(sid))}</span>`;
  return `<section class="conv-sess">
    <div class="conv-sess-head" data-c="${c}" title="${escHtml(session.cwd || '')}">
      ${head}
    </div>
    ${body}
  </section>`;
}

// conversationSegmentHTML renders one prompt + the assistant's spoken replies.
// Each reply bubble carries its absolute step index's refKey (main = agentIndex
// 0) so a click opens that turn — tools and reasoning included — in the shared
// drawer. An orphan segment (empty uuid) renders no prompt bubble, just the
// replies; a segment whose every step was tool-only renders no reply bubbles.
function conversationSegmentHTML(seg, sid, session) {
  const bubble = seg.uuid
    ? `<div class="conv-prompt">
        <span class="conv-prompt-icon" aria-hidden="true">${labelIcon('agents')}</span>
        <div class="conv-prompt-text">${escHtml(seg.text || '')}</div>
        <span class="conv-prompt-time">${escHtml(clockTime(parseTime(seg.timestamp)))}</span>
      </div>`
    : '';
  const replies = conversationReplies(seg)
    .map(r => conversationReplyHTML(r, sid))
    .join('');
  return `${bubble}<div class="conv-turns">${replies}</div>`;
}

// conversationReplyHTML renders one assistant reply bubble: aligned left (the
// agent side), the spoken text, and a footer with the time and — claudit being
// spend-aware — the turn's cost. It is a selectable affordance (data-ref), so a
// click pulls the full turn into the shared drawer.
function conversationReplyHTML(reply, sid) {
  const ref = refKey({ sessionId: sid, agentIndex: 0, stepIndex: reply.stepIndex });
  const sel = ref === selectedRef ? ' is-selected' : '';
  const cost = reply.cost_usd
    ? `<span class="conv-reply-cost">${escHtml(fmtMoney(reply.cost_usd))}</span>` : '';
  return `<div class="conv-reply${sel}" data-ref="${escHtml(ref)}" tabindex="0" role="button">
    <div class="conv-reply-text">${escHtml(reply.text)}</div>
    <div class="conv-reply-foot">
      <span class="conv-reply-time">${escHtml(clockTime(parseTime(reply.timestamp)))}</span>
      ${cost}
    </div>
  </div>`;
}

// ── Timeline (Gantt) lens ───────────────────────────────────────────────────

// renderTimeline draws the Gantt lens: a scrubber bar on top, then one
// horizontal Gantt per session (a real time axis, one row per agent, bar =
// lifetime, overlap = concurrency). Everything is rendered "as of" the playhead
// instant T (live → now); geometry comes from the pure, unit-tested
// timelineAtTime. The scrubber is a separate sibling from the .timeline-sessions
// container so a scrub re-renders ONLY the sessions, leaving the range input
// (mid-drag) untouched.
function renderTimeline(sessions) {
  if (sessions.length === 0) return `<div class="ac-idle">No agents to plot.</div>`;
  const nowMs = Date.now();
  const bounds = playheadBounds(lastGraph, nowMs);
  const live = playheadT == null;
  const T = playheadAt(bounds, nowMs);
  const scrubber = bounds ? scrubberHTML(bounds, T, live, playheadStats(lastGraph, T)) : '';
  return `<div class="timeline-lens">${scrubber}<div class="timeline-sessions">${renderTimelineSessions(sessions, nowMs, T)}</div></div>`;
}

// playheadAt resolves the instant the Gantt renders at: the paused T, or — when
// live — the right edge of the global window (now), falling back to nowMs when
// there are no parseable agent times yet.
function playheadAt(bounds, nowMs) {
  if (playheadT != null) return playheadT;
  return bounds ? bounds.endMs : nowMs;
}

// renderTimelineSessions emits just the per-session Gantts (the scrubber is
// rendered/updated separately). prevTimelineKeys → a genuinely NEW agent fades
// in; phase changes don't, because every agent always has a row (a pending one
// is simply zero-width), so scrubbing never adds/removes keys.
function renderTimelineSessions(sessions, nowMs, T) {
  const hostW = timelineHostW();
  const sel = resolveRef(lastGraph, selectedRef);
  const selAgentKey = sel ? refKey({ sessionId: sel.session.session_id, agentIndex: sel.agentIndex }) : null;
  const seen = prevTimelineKeys;
  const next = new Set();
  const html = sessions.map((s, si) => timelineSessionHTML(s, si, hostW, nowMs, T, selAgentKey, seen, next)).join('');
  prevTimelineKeys = next;
  return html;
}

// scrubberHTML is the sticky control strip: a "● live" toggle, a range input
// spanning the whole trace window, and a clock + active/done/pending readout —
// all "as of" the playhead T.
function scrubberHTML(bounds, T, live, stats) {
  const span = bounds.endMs - bounds.startMs;
  const val = Math.max(0, Math.min(span, T - bounds.startMs));
  return `<div class="tl-scrubber">
    <button type="button" class="tl-live${live ? ' is-live' : ''}" data-tllive title="Resume live — the playhead follows now">● live</button>
    <input class="tl-range" type="range" min="0" max="${span}" step="any" value="${val}"
      data-tlrange data-tlstart="${bounds.startMs}" aria-label="Scrub the timeline to a point in time"/>
    <span class="tl-clock" data-tlclock>${escHtml(clockTime(T))}</span>
    <span class="tl-counts" data-tlcounts>${countsHTML(stats)}</span>
  </div>`;
}

function countsHTML(s) {
  return `<span class="tlc tlc-active" title="agents active at the playhead">▶ ${fmtNum(s.active)}</span>` +
    `<span class="tlc tlc-done" title="agents finished by the playhead">✓ ${fmtNum(s.done)}</span>` +
    `<span class="tlc tlc-pending" title="agents not yet started at the playhead">○ ${fmtNum(s.pending)}</span>`;
}

function timelineSessionHTML(session, si, hostW, nowMs, T, selAgentKey, seen, next) {
  const sid = session.session_id || '';
  const tl = timelineAtTime(session, T, { hostW, nowMs });
  // The rows start at y = axisH (buildTimeline), so the first row's top is the
  // axis baseline — tick labels sit above it, gridlines run from it down.
  const axisY = tl.rows.length ? tl.rows[0].y : 20;

  // The agent-label column (x: 0 → chartX) is frozen as its own SVG; the chart
  // (ticks/bars/playhead) lives in a separate horizontally-scrolling SVG whose
  // viewBox starts at chartX, so panning the chart never carries the labels off.
  const gutterW = tl.chartX;
  const chartW = tl.contentW - tl.chartX;

  const ticks = tl.ticks.map(tk =>
    `<g class="tl-tick"><line x1="${tk.x.toFixed(1)}" y1="${axisY}" x2="${tk.x.toFixed(1)}" y2="${tl.height}"/><text x="${(tk.x + 3).toFixed(1)}" y="${(axisY - 7).toFixed(1)}">${escHtml(clockTime(tk.t))}</text></g>`).join('');
  // The playhead replaces the old now-line: one vertical line at T. Hidden when
  // T precedes the session (playheadX null) or the session finished before T
  // (T past its end → the line would just pin to the right edge, redundant).
  const showHead = tl.playheadX != null && T <= tl.endMs;
  const playhead = showHead
    ? `<line class="tl-playhead" x1="${tl.playheadX.toFixed(1)}" y1="0" x2="${tl.playheadX.toFixed(1)}" y2="${tl.height}"/>` : '';
  const agents = flattenSession(session);
  const parts = tl.rows.map(r => timelineRowHTML(r, tl, selAgentKey, seen, next, agents[r.rowIndex]));
  const gutterRows = parts.map(p => p.gutter).join('');
  const chartRows = parts.map(p => p.chart).join('');

  return `<div class="timeline-sess">
    <div class="timeline-sess-head" data-c="${colorSlot(si)}" title="${escHtml(session.cwd || '')}">
      <span class="timeline-sess-proj">${escHtml(baseName(session.cwd) || '—')}</span>
      <span class="timeline-sess-sid" title="${escHtml(sid)}">${escHtml(shortId(sid))}</span>
      <button type="button" class="timeline-jump" data-tljump="${escHtml(sid)}" hidden>● now</button>
    </div>
    <div class="timeline-body">
      <svg class="timeline-gutter" viewBox="0 0 ${gutterW} ${tl.height}" width="${gutterW}" height="${tl.height}" role="img" aria-label="Agent labels">
        <g class="tl-rows">${gutterRows}</g>
      </svg>
      <div class="timeline-scroll" data-tlscroll="${escHtml(sid)}">
        <svg class="timeline-svg" viewBox="${tl.chartX} 0 ${chartW} ${tl.height}" width="${chartW}" height="${tl.height}" role="img" aria-label="Agent timeline">
          <g class="tl-grid">${ticks}</g>
          <g class="tl-rows">${chartRows}</g>
          ${playhead}
        </svg>
      </div>
    </div>
  </div>`;
}

// timelineRowHTML splits one agent row into two synchronized <g>s: a `gutter`
// piece (frozen label column) and a `chart` piece (the scrolling bar). Both
// carry the same data-ref so a click in either selects the row, and both get the
// is-selected / is-new / tl-pending classes so state shows on both sides of the
// freeze line. A row's phase comes from the playhead: 'active' draws the pulse
// at the (clamped) bar end, 'pending' ghosts the label (the bar is zero-width).
function timelineRowHTML(r, tl, selAgentKey, seen, next, agent) {
  next.add(r.key);
  const isNew = !seen.has(r.key);
  const sel = r.key === selAgentKey ? ' is-selected' : '';
  const pending = r.phase === 'pending' ? ' tl-pending' : '';
  const barH = Math.max(6, r.h - 10);
  const barY = r.y + Math.round((r.h - barH) / 2);
  const cx = (r.x + r.w).toFixed(1);
  const cy = (barY + barH / 2).toFixed(1);
  // A red pip flags an agent that hit ≥1 tool error — but not while pending (it
  // hasn't run at the playhead T yet, so it can't have errored). It lives in the
  // frozen gutter (right edge of the label column), not on the bar: a live Gantt
  // auto-scrolls to "now", which would carry a bar-start pip off-screen.
  const errs = (agent && agent.error_count) || 0;
  const errPip = errs > 0 && r.phase !== 'pending'
    ? `<circle class="tl-err-pip" cx="${(tl.chartX - 7).toFixed(1)}" cy="${(r.y + r.h / 2).toFixed(1)}" r="3.5"><title>${errs} ${errs === 1 ? 'error' : 'errors'}</title></circle>`
    : '';
  const cls = `tl-row ${r.kind === 'main' ? 'tl-main' : 'tl-sub'}${r.running ? ' is-running' : ''}${sel}${isNew ? ' is-new' : ''}${pending}`;
  const meta = r.phase === 'pending' ? 'not started yet'
    : r.running ? 'running' : `${fmtNum(r.steps)} · ${fmtMoney(r.cost_usd || 0)}`;
  const labelY = (r.y + r.h / 2 + 4).toFixed(1);
  const gutter = `<g class="${cls}" data-ref="${escHtml(r.key)}">
    <rect class="tl-rowbg" x="0" y="${r.y}" width="${tl.chartX}" height="${r.h}"/>
    <text class="tl-label" x="${r.labelX}" y="${labelY}">${escHtml(clip(r.label, 16))}</text>
    ${errPip}
    <title>${escHtml(r.label)} — ${escHtml(meta)}</title>
  </g>`;
  const chart = `<g class="${cls}" data-ref="${escHtml(r.key)}" tabindex="0" role="button">
    <rect class="tl-rowbg" x="${tl.chartX}" y="${r.y}" width="${tl.contentW - tl.chartX}" height="${r.h}"/>
    <rect class="tl-bar" x="${r.x.toFixed(1)}" y="${barY}" width="${r.w.toFixed(1)}" height="${barH}" rx="3">
      <title>${escHtml(r.label)} — ${escHtml(meta)}</title>
    </rect>
    ${r.running ? `<circle class="tl-pulse" cx="${cx}" cy="${cy}" r="3.5"/>` : ''}
  </g>`;
  return { gutter, chart };
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

// ── playhead scrubber ─────────────────────────────────────────────────────

// setLive resumes live mode: the playhead follows "now" and auto-advances on
// each refetch. A full re-render rebuilds the scrubber with the thumb at the
// right edge and every bar grown to the present.
function setLive(container) {
  playheadT = null;
  renderActive(container, true);
}

// onScrub handles a drag of the range input: it parks the playhead at the picked
// absolute instant (T = window start + slider value), drops live mode, and
// schedules a single rAF repaint so a fast drag coalesces to one frame.
function onScrub(container, range) {
  const start = Number(range.dataset.tlstart);
  playheadT = start + Number(range.value);
  const liveBtn = container.querySelector('.tl-live');
  if (liveBtn) liveBtn.classList.remove('is-live');
  if (scrubRaf) return;
  scrubRaf = requestAnimationFrame(() => { scrubRaf = 0; renderScrub(container); });
}

// renderScrub repaints ONLY the session Gantts + the clock/counts readout for
// the current playhead T — it deliberately leaves the scrubber's range input
// alone so the in-progress drag isn't interrupted, and preserves each Gantt's
// horizontal scroll so seeking through time never yanks the viewport sideways.
function renderScrub(container) {
  const sessions = (lastGraph && lastGraph.sessions) || [];
  const T = playheadT;
  const lensHost = container.querySelector('.subview[data-subview="timeline"]');
  const host = container.querySelector('.timeline-sessions');
  if (host && lensHost) {
    const memo = captureState(lensHost);
    host.innerHTML = renderTimelineSessions(sessions, Date.now(), T);
    restoreState(lensHost, memo);
  }
  const clock = container.querySelector('[data-tlclock]');
  if (clock) clock.textContent = clockTime(T);
  const counts = container.querySelector('[data-tlcounts]');
  if (counts) counts.innerHTML = countsHTML(playheadStats(lastGraph, T));
}

// ── preserve scroll / open-rows across a live re-render ────────────────────

function captureState(host) {
  const m = { scrolls: {}, openTools: [], tlScroll: {} };
  host.querySelectorAll('.agent-feed, .insp-tree, .insp-log, .timeline-lens, .conv').forEach(el => {
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
  host.querySelectorAll('.agent-feed, .insp-tree, .insp-log, .timeline-lens, .conv').forEach(el => {
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

const scrollKey = el => el.className.split(' ').find(c => /feed|tree|log|timeline-lens|conv/.test(c)) || el.className;

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
