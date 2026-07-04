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
import { fmtMoney, fmtNum, fmtCompact, fmtPct1, escHtml } from './format.js';
import { sessionListSkeleton } from './skeleton.js';
import { setLiveHandler } from './sse.js';
import {
  flattenSession, agentLabel, graphStats, parseTime, refKey, parseRefKey,
  resolveRef, defaultRef, baseName, formatElapsed,
  conversationSegments, conversationReplies, conversationSessionList,
  clampTreeWidth, clampDrawerWidth, orderTreeSessions, treeFollowMode,
  toolMix, percentiles, durationHistogram, costPareto, errorRates,
  contextSeries, binSeries, groupBy, detectSignals,
} from './agents-logic.js';
import {
  lastGraph, setLastGraph, activeSub, setActiveSub,
  selectedRef, setSelectedRef, setJumpFlashRef,
  setFullCache, setFullTurnCache, setPrevTimelineKeys, timelinePinned,
  setPlayheadT, setTlMinPxPerMs, setTimelineSid,
  setFilterSpec, setFilterBarBuilt, setHitIndex,
  openAgentBodies, TREE_PAGE, treeLimit, setTreeLimit, coreHooks,
  labelIcon, originBadgeHTML, isServeMode, colorSlot, convSidebarWidth,
  startConvResize, startTimelineResize, kindFamily, kindBadge,
  captureState, restoreState, clockTime, shortId,
} from './agents-shared.js';
import { renderControl } from './agents-feed.js';
import {
  renderInspector, onTreeToggle, ensureAgentExpanded,
} from './agents-tree.js';
import {
  renderTimeline, currentTimelineSession, syncTimelineScroll,
  onTimelineScroll, jumpToNow, onScrub, onTimelineWheel, onTimelineZoomReset,
  onTimelineDisclose, onNarrativeNav,
} from './agents-timeline.js';
import { renderDrawer, loadFull, loadFullTurn } from './agents-drawer.js';
import {
  lensHasFilter, ensureFilterBar, applyFilter,
  applyBucketFilter, applyCostFilter, applyErrorFilter,
  onFilterInput, onFilterClick,
} from './agents-filter.js';

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
    <a class="subtab"           href="#agents/insights" data-subtab="insights">Insights</a>
  </nav>

  <div class="trace-filter" data-trace-filter role="search" aria-label="Filter this trace"></div>

  <div class="agents-body">
    <div class="agents-lens">
      <div class="subview is-active" data-subview="feed"></div>
      <div class="subview" data-subview="tree"></div>
      <div class="subview" data-subview="timeline"></div>
      <div class="subview" data-subview="conversation"></div>
      <div class="subview" data-subview="insights"></div>
    </div>
    <div class="agents-resize" data-agents-resize role="separator" aria-orientation="vertical" aria-label="Resize the detail panel"></div>
    <aside class="agents-drawer" data-drawer aria-label="Selection detail"></aside>
  </div>

  <div id="agents-empty" class="empty-note" hidden>No agents in this window. Try widening <code>--since</code>/<code>--until</code>, or open a session that spawned sub-agents.</div>
`;

const SUBS = ['feed', 'tree', 'timeline', 'conversation', 'insights'];

// Core-private view state (the cross-lens shared state lives in
// agents-shared.js).
let painted = false;
let navPainted = false;
let tickerId = null;
// pendingJumpRef carries a ref across a lens-switch navigation (e.g. a Signals
// row click on the drawer-less Insights lens jumping to the Timeline). paint()
// reads it to FORCE a drawer repaint (a plain lens switch skips it) and scroll
// the target into view once the destination lens is in the DOM, then clears it.
let pendingJumpRef = null;
let jumpFlashTimer = null;
let liveScheduled = false;

// Insights lens (Phase 2). insightsScope picks which slice of the graph the
// aggregations cover — 'graph' (every agent in the window), 'session' (the one
// plotted by currentTimelineSession), or 'agent' (the selected agent, via
// selectedRef) — reusing the SAME selection state the other lenses key off so
// the toggle stays in sync with what the user clicked elsewhere. insightsMetric
// is which figure drives the Tool-mix bars (cost / time / calls). Both clamp to
// a safe default when the chosen scope/metric has nothing to show.
let insightsScope = 'graph';
let insightsMetric = 'cost';
// Group-by panel: which dimension buckets the tool rows (kind / model / agent /
// status). Clamps to 'kind' via INS_GROUP_DIMS when an unknown value sneaks in.
let insightsGroupDim = 'kind';
// Which Insights section is showing. Each panel is its own tab under the
// Insights lens (see INS_TABS / renderInsights); clamps to the first tab when
// an unknown key sneaks in. Signals leads since it's the audit headline.
let insightsTab = 'signals';

// The Tree lens is a fixed-width left RAIL (the detail drawer takes the rest of
// the width); the handle between them resizes the rail — the "left panel" the
// user widens/narrows. Clamped + persisted like the conv sidebar so it survives
// reloads, lens switches, and live re-renders.
const TREE_KEY = 'claudit.agents.treeW';
let treeW = null;
function treeWidth() {
  if (treeW == null) {
    let stored = null;
    try { stored = localStorage.getItem(TREE_KEY); } catch { /* private mode */ }
    treeW = clampTreeWidth(stored);
  }
  return treeW;
}
function setTreeWidth(px) {
  treeW = clampTreeWidth(px);
  try { localStorage.setItem(TREE_KEY, String(treeW)); } catch { /* private mode */ }
  return treeW;
}

// On the Feed/Timeline lenses the detail drawer is the opposite of the Tree
// rail: a fixed-width RIGHT column (the lens flexes to fill the rest) that the
// handle between them resizes. Its width is clamped + persisted like the rail
// so it survives reloads, lens switches, and live re-renders.
const DRAWER_KEY = 'claudit.agents.drawerW';
let drawerW = null;
function drawerWidth() {
  if (drawerW == null) {
    let stored = null;
    try { stored = localStorage.getItem(DRAWER_KEY); } catch { /* private mode */ }
    drawerW = clampDrawerWidth(stored);
  }
  return drawerW;
}
function setDrawerWidth(px) {
  drawerW = clampDrawerWidth(px);
  try { localStorage.setItem(DRAWER_KEY, String(drawerW)); } catch { /* private mode */ }
  return drawerW;
}

// Tree "anchor + pause reorder" live-stability state. lastTreeScrollAt is the
// wall-clock of the user's last scroll in the .itree container; frozenTreeOrder
// is the session-id order to hold while they're active (null = follow the live
// newest-first order). treeFollowMode/orderTreeSessions in agents-logic.js turn
// these into a decision; see renderActive's tree branch.
let lastTreeScrollAt = null;
let frozenTreeOrder = null;

export function reset() {
  painted = false;
  navPainted = false;
  setLastGraph(null);
  setSelectedRef(null);
  setFullCache({});
  setFullTurnCache({});
  setPrevTimelineKeys(new Set());
  timelinePinned.clear();
  setPlayheadT(null);
  setTlMinPxPerMs(null);
  setTimelineSid(null);
  setFilterSpec({ text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: 0 });
  setFilterBarBuilt(false);
  setHitIndex(-1);
  openAgentBodies.clear();
  setTreeLimit(TREE_PAGE);
  lastTreeScrollAt = null;
  frozenTreeOrder = null;
}

// paintNav resolves the sidebar metric before the tab is first opened.
export async function paintNav() {
  if (navPainted || painted) return;
  let graph;
  try { graph = await fetchAgents(); } catch { return; }
  setLastGraph(graph);
  updateNavMetric(graphStats(graph));
  navPainted = true;
}

export async function paint(route) {
  const container = document.getElementById('view-agents');
  if (!container) return;
  setActiveSub(wantedSub(route && route.sub));

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
      setLastGraph(await fetchAgents());
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
  // A pending cross-lens jump (e.g. Signals → Timeline) changed the selection on
  // a drawer-less lens. Stamp the jump's ref BEFORE rendering so the destination
  // lens paints its `is-jumped` pulse, force the drawer repaint a plain lens
  // switch skips, then scroll the target into view.
  const jumpRef = pendingJumpRef;
  if (jumpRef != null) armJumpFlash(container, jumpRef);
  renderActive(container, false, !lensSwitch || jumpRef != null);
  if (jumpRef != null) {
    scrollRefIntoView(container, jumpRef);
    pendingJumpRef = null;
  }
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
  // The Conversation lens carries its own left session list and hides the shared
  // detail drawer (the thread IS the detail), so it gets the full body width.
  const body = container.querySelector('.agents-body');
  if (body) {
    body.dataset.lens = sub;
    body.classList.toggle('is-no-drawer', sub === 'conversation' || sub === 'insights');
  }
  const bar = container.querySelector('[data-trace-filter]');
  if (bar) bar.hidden = !lensHasFilter(sub);
  applyBodyLayout(container);
}

// applyBodyLayout sizes the lens|drawer split per lens. The TREE lens is a
// fixed-width navigator RAIL (resizable via the handle) with the detail drawer
// flexing to fill the rest — the wide pane. Feed/Timeline are the opposite: a
// wide lens with the drawer as a fixed side panel (no handle). Conversation
// drops the drawer entirely. Inline so the persisted rail width beats the
// stylesheet default and the right template survives lens switches.
function applyBodyLayout(container) {
  const body = container.querySelector('.agents-body');
  if (!body) return;
  const lens = body.dataset.lens || 'feed';
  if (lens === 'conversation' || lens === 'insights') {
    // No shared drawer: the conversation thread / Insights dashboard reclaims
    // the full width.
    body.style.gridTemplateColumns = 'minmax(0, 1fr)';
    body.style.gridTemplateAreas = '"lens"';
  } else if (lens === 'tree') {
    // Fixed left RAIL (resizable) with the drawer flexing to fill the rest.
    body.style.gridTemplateColumns = `${treeWidth()}px 6px minmax(0, 1fr)`;
    body.style.gridTemplateAreas = '"lens resize drawer"';
  } else {
    // Feed/Timeline: a wide lens with the drawer as a fixed RIGHT column the
    // handle resizes. Running agents now ride as sticky "live rows" inside the
    // feed itself, so the lens and drawer both start at the top edge.
    body.style.gridTemplateColumns = `minmax(0, 1fr) 6px ${drawerWidth()}px`;
    body.style.gridTemplateAreas = '"lens resize drawer"';
  }
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
  else if (activeSub === 'tree') host.innerHTML = renderInspector(treeSessionOrder(host, sessions));
  else if (activeSub === 'timeline') host.innerHTML = renderTimeline(sessions);
  else if (activeSub === 'conversation') host.innerHTML = renderConversation(sessions);
  else if (activeSub === 'insights') host.innerHTML = renderInsights(sessions);
  if (memo) restoreState(host, memo);
  // The Gantt's horizontal scroll (live-edge follow + restored history offset)
  // can only be set on real DOM, so it runs after innerHTML is in place.
  if (activeSub === 'timeline') syncTimelineScroll(container);
  if (paintDrawer) renderDrawer(container);
  applyFilter(container);
  tickTimers(container);
}

// treeSessionOrder picks the session order for a tree (re-)render. While the
// user is reading (scrolled away and recently active) it holds the order frozen
// so a live tick can't reshuffle rows; at the top or once idle it follows the
// live newest-first order, re-capturing that order as the new freeze baseline.
// Reads the OUTGOING .itree (still in `host` before innerHTML is replaced) for
// the current scroll position.
function treeSessionOrder(host, sessions) {
  const itree = host.querySelector('.itree');
  const atTop = !itree || itree.scrollTop <= 0;
  const mode = treeFollowMode(lastTreeScrollAt, Date.now(), atTop);
  if (mode === 'follow') {
    // Keep the freeze baseline current so the instant we flip to 'frozen' we
    // hold the order the user last saw — not a stale one.
    frozenTreeOrder = sessions.map(s => s.session_id || '');
    return sessions;
  }
  return orderTreeSessions(sessions, frozenTreeOrder);
}

// ── shared selection ──────────────────────────────────────────────────────

// ensureSelection pins a default (the root agent) whenever nothing is
// selected or the current selection no longer resolves against the latest
// graph — so the drawer is never empty and a vanished agent can't strand it.
function ensureSelection(graph) {
  if (selectedRef && resolveRef(graph, selectedRef)) return;
  const d = defaultRef(graph);
  setSelectedRef(d ? refKey(d) : null);
}

// wireSelection installs the delegated click/keyboard handlers once per shell
// build. Any element carrying data-ref in the lens selects it. Delegation
// survives lens re-renders because it's bound to the stable .agents-lens /
// .agents-drawer wrappers.
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
      // The Conversation lens's left session list switches which conversation
      // shows by re-pointing the shared selection at that session's main agent.
      const sess = e.target.closest('[data-conv-sess]');
      if (sess) { pickConversation(container, sess.dataset.convSess); return; }
      // The Timeline lens's left session list switches which session's Gantt is
      // plotted (independent of the drawer selection).
      const tlSess = e.target.closest('[data-tl-sess]');
      if (tlSess) { pickTimeline(container, tlSess.dataset.tlSess); return; }
      // A narrative-strip row scrolls the Gantt to its prompt's band and
      // flashes it — pure navigation, never a selection. (Rows are <button>s,
      // so Enter routes here through the native click.)
      const tlNav = e.target.closest('[data-tlnav]');
      if (tlNav) { onNarrativeNav(container, tlNav); return; }
      // The Timeline's disclosure caret expands/collapses an agent row to its
      // turn band — a pure lens repaint, never a selection.
      const tlDisc = e.target.closest('[data-tltoggle]');
      if (tlDisc) { onTimelineDisclose(container, tlDisc.dataset.tltoggle); return; }
      // A turn span both toggles its tool sub-spans AND selects the turn in the
      // drawer (the existing data-ref semantics), so one click discloses + inspects.
      const tlTurn = e.target.closest('[data-tlturn]');
      if (tlTurn) { onTimelineDisclose(container, tlTurn.dataset.tlturn); select(container, tlTurn.dataset.ref); return; }
      // The Insights lens's section tabs switch which panel shows — a pure
      // re-render from lastGraph, no refetch.
      const insTab = e.target.closest('[data-ins-tab]');
      if (insTab) { insightsTab = insTab.dataset.insTab; renderActive(container, false); return; }
      // The Insights lens's scope toggle re-slices the aggregations (graph /
      // session / agent) and the metric toggle re-bars them — both a pure
      // re-render from lastGraph, no refetch.
      const insScope = e.target.closest('[data-ins-scope]');
      if (insScope) { if (!insScope.disabled) { insightsScope = insScope.dataset.insScope; renderActive(container, false); } return; }
      const insMetric = e.target.closest('[data-ins-metric]');
      if (insMetric) { insightsMetric = insMetric.dataset.insMetric; renderActive(container, false); return; }
      // The Group-by panel's dimension toggle re-buckets the tool rows — a pure
      // re-render from lastGraph, like the scope/metric toggles.
      const insGroupDim = e.target.closest('[data-ins-group-dim]');
      if (insGroupDim) { insightsGroupDim = insGroupDim.dataset.insGroupDim; renderActive(container, false); return; }
      // A latency histogram bucket filters the trace to the kinds it holds and
      // jumps to the Timeline — Insights has no dimming of its own, so the filter
      // only "lands" on a filter-bearing lens.
      const insBucket = e.target.closest('[data-ins-bucket]');
      if (insBucket) { applyBucketFilter(container, (insBucket.dataset.insKinds || '').split(',').filter(Boolean)); return; }
      // A Cost-Pareto row/headline filters the trace to turns at/above its dollar
      // threshold and jumps to the Timeline — same land-on-a-filter-lens idiom.
      const insCut = e.target.closest('[data-ins-cost-cut]');
      if (insCut) { applyCostFilter(container, insCut.dataset.insCostCut); return; }
      // An Error-breakdown bar/headline/tool filters the trace to errored tools
      // (optionally one kind) and jumps to the Timeline — same land-on-a-filter-
      // lens idiom. data-ins-kinds is empty for the "all errors" headline.
      const insErr = e.target.closest('[data-ins-err]');
      if (insErr) { applyErrorFilter(container, (insErr.dataset.insKinds || '').split(',').filter(Boolean)); return; }
      // A Group-by row filters the trace where its dimension maps to one: a kind
      // group pins that kind, the 'error' status group turns on errorsOnly — both
      // routing to the Timeline (model/agent/ok rows aren't clickable).
      const insGroup = e.target.closest('[data-ins-group]');
      if (insGroup) {
        if (insGroup.hasAttribute('data-ins-err')) applyErrorFilter(container, []);
        else applyBucketFilter(container, (insGroup.dataset.insKinds || '').split(',').filter(Boolean));
        return;
      }
      // The Tree lens pages its sessions; "show more" reveals the next page.
      if (e.target.closest('[data-tree-more]')) { setTreeLimit(treeLimit + TREE_PAGE); renderActive(container, true); return; }
      // A Signals row lives on the drawer-less Insights lens, so it can't select
      // in place — it jumps to the Timeline at its ref (see gotoSignalRef). The
      // Timeline pip's "+N" overflow glyph routes to the Signals panel itself.
      const sigRow = e.target.closest('.ins-sig-row');
      if (sigRow && sigRow.dataset.ref) { gotoSignalRef(container, sigRow.dataset.ref); return; }
      if (e.target.closest('[data-goto-insights]')) { insightsTab = 'signals'; location.hash = '#agents/insights'; return; }
      const el = e.target.closest('[data-ref]');
      if (el) select(container, el.dataset.ref);
    });
    // The playhead scrubber fires `input` as it's dragged — recompute the
    // Gantt "as of" the new instant T (rAF-coalesced so the drag stays smooth).
    lens.addEventListener('input', e => {
      const range = e.target.closest('[data-tlrange]');
      if (range) onScrub(container, range);
    });
    // Scroll-wheel over a session's Gantt zooms the time axis (cursor-anchored);
    // shift+wheel and horizontal trackpad deltas fall through to native pan.
    // Non-passive so onTimelineWheel can preventDefault the page scroll.
    lens.addEventListener('wheel', e => onTimelineWheel(container, e), { passive: false });
    // Double-click the chart resets the zoom to the fit-to-width overview — the
    // escape hatch after zooming deep into the trace.
    lens.addEventListener('dblclick', e => {
      if (e.target.closest('.timeline-scroll[data-tlscroll]')) onTimelineZoomReset(container);
    });
    // The Conversation lens's session list is drag-resizable: a mousedown on the
    // handle starts a document-level drag that widens/narrows the sidebar live.
    lens.addEventListener('mousedown', e => {
      const handle = e.target.closest('[data-conv-resize]');
      if (handle) { e.preventDefault(); startConvResize(container, e.clientX); return; }
      // The Timeline lens's session list shares the same resizable-sidebar idiom.
      const tlHandle = e.target.closest('[data-tl-resize]');
      if (tlHandle) { e.preventDefault(); startTimelineResize(container, e.clientX); }
    });
    lens.addEventListener('keydown', e => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      const tlDisc = e.target.closest('[data-tltoggle]');
      if (tlDisc) { e.preventDefault(); onTimelineDisclose(container, tlDisc.dataset.tltoggle); return; }
      const bucket = e.target.closest('[data-ins-bucket]');
      if (bucket) { e.preventDefault(); applyBucketFilter(container, (bucket.dataset.insKinds || '').split(',').filter(Boolean)); return; }
      const cut = e.target.closest('[data-ins-cost-cut]');
      if (cut) { e.preventDefault(); applyCostFilter(container, cut.dataset.insCostCut); return; }
      const err = e.target.closest('[data-ins-err]');
      if (err) { e.preventDefault(); applyErrorFilter(container, (err.dataset.insKinds || '').split(',').filter(Boolean)); return; }
      const grp = e.target.closest('[data-ins-group]');
      if (grp) {
        e.preventDefault();
        if (grp.hasAttribute('data-ins-err')) applyErrorFilter(container, []);
        else applyBucketFilter(container, (grp.dataset.insKinds || '').split(',').filter(Boolean));
        return;
      }
      const sigRow = e.target.closest('.ins-sig-row');
      if (sigRow && sigRow.dataset.ref) { e.preventDefault(); gotoSignalRef(container, sigRow.dataset.ref); return; }
      if (e.target.closest('[data-goto-insights]')) { e.preventDefault(); insightsTab = 'signals'; location.hash = '#agents/insights'; return; }
      const el = e.target.closest('[data-ref]');
      if (el) { e.preventDefault(); select(container, el.dataset.ref); }
    });
    // Scroll doesn't bubble, so catch it in the capture phase: when the user
    // drags a session's Gantt away from the right edge we "pin" it so a live
    // update won't yank them back to now; returning to the edge un-pins.
    lens.addEventListener('scroll', e => { onTimelineScroll(e); onTreeScroll(e); }, true);
    // The Tree lens's agent bodies are lazy — fill/clear them as nodes expand or
    // collapse. `toggle` doesn't bubble, so catch it in the capture phase.
    lens.addEventListener('toggle', onTreeToggle, true);
  }
  // The lens|drawer split handle lives in .agents-body, outside the lens, so its
  // drag is wired on the body wrapper (which is stable across lens re-renders).
  const body = container.querySelector('.agents-body');
  if (body) {
    body.addEventListener('mousedown', e => {
      const handle = e.target.closest('[data-agents-resize]');
      if (handle) { e.preventDefault(); startSplitResize(container, e.clientX); }
    });
  }
  const drawer = container.querySelector('.agents-drawer');
  if (drawer) {
    drawer.addEventListener('click', e => {
      const copy = e.target.closest('[data-copy]');
      if (copy) { copyText(copy.dataset.copy, copy); return; }
      const full = e.target.closest('[data-loadfull]');
      if (full) { loadFull(full); return; }
      const fullTurn = e.target.closest('[data-loadfullturn]');
      if (fullTurn) { loadFullTurn(fullTurn); return; }
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

// select sets the shared selection and repaints. Every lens — the unified Tree
// included — restyles the highlight in place and repaints the drawer; no lens
// re-renders on select. (The Tree once re-rendered because its log pane was
// agent-dependent; now the whole tree is always present, so a full re-render
// would only clobber the native <details> toggle the click is also performing.)
function select(container, ref) {
  if (!ref) return;
  setSelectedRef(ref);
  // On the Tree lens, make sure the selected row's agent is expanded (its body
  // is lazy) before highlighting — otherwise a jump into a collapsed agent would
  // light up a row that isn't in the DOM.
  if (activeSub === 'tree') ensureAgentExpanded(container, ref);
  updateHighlights(container);
  renderDrawer(container);
}

// Lens modules can't import select() from the core (the core imports them;
// a cycle would break the static-report bundler), so the shared coreHooks
// registry carries it — see agents-filter.js's stepHit.
coreHooks.select = select;

// pickConversation switches the Conversation lens to another session by pointing
// the shared selection at that session's main agent (agentIndex 0), then doing a
// full re-render so the lens shows the new dialogue and the drawer follows. The
// new thread opens at its top, but the left session LIST keeps its scroll so the
// row you just clicked stays put instead of jumping to the top of the list.
function pickConversation(container, sessionId) {
  if (!sessionId) return;
  setSelectedRef(refKey({ sessionId, agentIndex: 0 }));
  const sidebar = container.querySelector('.conv-sidebar');
  const top = sidebar ? sidebar.scrollTop : 0;
  renderActive(container, false);
  const next = container.querySelector('.conv-sidebar');
  if (next) next.scrollTop = top;
}

// pickTimeline switches which session the Timeline lens plots. It pins
// timelineSid (separate from selectedRef, so the drawer selection is
// untouched) and snaps the playhead back to live — the scrubber window changed
// to this session's span, so a stale paused T would be meaningless. Sidebar
// scrollTop is preserved across the re-render the way pickConversation does.
function pickTimeline(container, sessionId) {
  if (!sessionId) return;
  setTimelineSid(sessionId);
  setPlayheadT(null);
  setTlMinPxPerMs(null);
  const sidebar = container.querySelector('.tl-sidebar');
  const top = sidebar ? sidebar.scrollTop : 0;
  renderActive(container, false);
  const next = container.querySelector('.tl-sidebar');
  if (next) next.scrollTop = top;
}

// gotoSignalRef makes a Signals-panel row click actually "jump to" its anomaly.
// The Insights lens has no drawer (is-no-drawer), so a bare select() there lands
// the selection nowhere visible — the bug this fixes. Instead we point the shared
// selection at the signal's ref, pin the Timeline to the ref's session, and route
// to the Timeline (a drawer-bearing lens with the matching pip), mirroring the
// other Insights jumps (applyBucketFilter et al. route to '#agents/timeline').
// pendingJumpRef tells paint() to force the drawer repaint + scroll on arrival.
function gotoSignalRef(container, ref) {
  if (!ref) return;
  const p = parseRefKey(ref);
  if (p && p.sessionId) { setTimelineSid(p.sessionId); setPlayheadT(null); setTlMinPxPerMs(null); }
  setSelectedRef(ref);
  pendingJumpRef = ref;
  location.hash = '#agents/timeline';
}

// scrollRefIntoView centers the element carrying `ref` in the active lens, if it
// has one (a tool-granular ref may have no own row — the drawer still shows it).
function scrollRefIntoView(container, ref) {
  const el = container.querySelector(`.agents-lens [data-ref="${ref}"]`);
  if (el && el.scrollIntoView) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
}

// armJumpFlash marks `ref` as the just-jumped-to spot so the next render stamps
// an `is-jumped` pulse on it (see timelineRowHTML), then clears that state after
// the pulse — wiping any lingering class without a re-render. Driving the pulse
// from render state (rather than adding the class imperatively) is what makes it
// survive the re-render a smooth scrollRefIntoView triggers: the landing spot is
// often a hair-thin tool segment, and an imperative class gets blown away by
// that follow-up render before it can be seen.
function armJumpFlash(container, ref) {
  setJumpFlashRef(ref);
  clearTimeout(jumpFlashTimer);
  jumpFlashTimer = setTimeout(() => {
    setJumpFlashRef(null);
    container.querySelectorAll('.is-jumped').forEach(el => el.classList.remove('is-jumped'));
  }, 1900);
}

// startSplitResize drives the lens|drawer split handle. Its meaning flips by
// lens: on the Tree lens the LEFT pane is a fixed rail (drag widens the rail,
// shrinks the drawer); on Feed/Timeline the RIGHT pane is the fixed drawer (drag
// LEFT widens the drawer, shrinks the lens). Either way it re-sizes the live
// grid track during the drag (smooth, no re-render) and persists on release.
function startSplitResize(container, startX) {
  const body = container.querySelector('.agents-body');
  const lens = container.querySelector('.agents-lens');
  const drawer = container.querySelector('.agents-drawer');
  if (!body || !lens) return;
  const isTree = (body.dataset.lens || '') === 'tree';
  const hasBand = (body.dataset.lens || '') === 'feed';
  const areas = hasBand ? '"active active active" "lens resize drawer"' : '"lens resize drawer"';
  document.body.style.cursor = 'col-resize';
  document.body.style.userSelect = 'none';
  let onMove;
  if (isTree) {
    const startW = lens.getBoundingClientRect().width;
    onMove = e => {
      treeW = clampTreeWidth(startW + (e.clientX - startX));
      body.style.gridTemplateColumns = `${treeW}px 6px minmax(0, 1fr)`;
    };
  } else {
    const startW = drawer ? drawer.getBoundingClientRect().width : drawerWidth();
    onMove = e => {
      // Drawer is on the right, so dragging the handle LEFT (negative dx) widens it.
      drawerW = clampDrawerWidth(startW - (e.clientX - startX));
      body.style.gridTemplateColumns = `minmax(0, 1fr) 6px ${drawerW}px`;
      body.style.gridTemplateAreas = areas;
    };
  }
  const onUp = () => {
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup', onUp);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    if (isTree) setTreeWidth(treeW);
    else setDrawerWidth(drawerW);
  };
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup', onUp);
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
  setLastGraph(graph);
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
  const list = conversationSessionList(sessions);
  if (list.length === 0) {
    return `<div class="ac-idle">No conversation in this window.</div>`;
  }
  const sel = resolveRef(lastGraph, selectedRef);
  const session = (sel && sel.session && sel.session.main) ? sel.session : sessions[list[0].index];
  const curSid = session.session_id || '';
  const w = convSidebarWidth();
  return `<div class="conv-layout">
    <div class="conv-sidebar" style="width:${w}px" role="tablist" aria-label="Conversations">${conversationSidebarHTML(list, curSid)}</div>
    <div class="conv-resize" data-conv-resize role="separator" aria-orientation="vertical" aria-label="Resize the session list"></div>
    <div class="conv">${conversationSessionHTML(session)}</div>
  </div>`;
}

// conversationSidebarHTML renders the left session list: one selectable row per
// session in the window — the in-lens way to switch which conversation you're
// reading (selecting a turn in another lens scopes it here too). The active
// session is highlighted; each row carries data-conv-sess so a click re-points
// the shared selection at that session's main agent and re-renders the thread.
function conversationSidebarHTML(list, curSid) {
  return list.map(e => {
    const sel = e.sessionId === curSid ? ' is-selected' : '';
    const c = colorSlot(e.index);
    const turns = `${fmtNum(e.replyCount)} ${e.replyCount === 1 ? 'reply' : 'replies'}`;
    return `<button type="button" class="conv-sess-item${sel}" role="tab" aria-selected="${e.sessionId === curSid}" data-conv-sess="${escHtml(e.sessionId)}" data-c="${c}">
      <span class="conv-sess-item-head">
        <span class="conv-sess-item-proj" title="${escHtml(e.cwd || '')}">${escHtml(baseName(e.cwd) || '—')}</span>
        ${originBadgeHTML(e)}
      </span>
      <span class="conv-sess-item-meta">
        <span class="conv-sess-item-sid">${escHtml(shortId(e.sessionId))}</span>
        <span class="conv-sess-item-turns">${turns}</span>
      </span>
    </button>`;
  }).join('');
}

// conversationSessionHTML renders the chosen session's dialogue. No header —
// which session you're reading is named (and switched) in the left sidebar.
function conversationSessionHTML(session) {
  const sid = session.session_id || '';
  const segs = conversationSegments(session);
  const body = segs.length === 0
    ? `<div class="ac-idle">No assistant turns recorded.</div>`
    : segs.map(seg => conversationSegmentHTML(seg, sid, session)).join('');
  return `<section class="conv-sess">${body}</section>`;
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

// ── Insights lens (Phase 2) ─────────────────────────────────────────────────

// INS_METRICS is the Tool-mix bar's metric menu: which figure drives the bar
// width and how each column formats. Order is the column order; the first is the
// default. get() reads a toolMix row; fmt() pre-formats for display (reusing the
// shared formatters so the dashboard speaks the same units as every other lens).
const INS_METRICS = [
  { key: 'cost',  label: 'Cost',  get: r => r.costUSD,    fmt: v => fmtMoney(v) },
  { key: 'time',  label: 'Time',  get: r => r.durationMs, fmt: v => formatElapsed(v) },
  { key: 'count', label: 'Calls', get: r => r.count,      fmt: v => fmtNum(v) },
];

// scopedAgentNode resolves the single agent the 'agent' Insights scope covers —
// the OWNING agent of the shared selection, derived from selectedRef so a tool /
// turn selection still scopes to its agent. Null when nothing is selected or the
// ref no longer resolves (e.g. after a refetch dropped that session).
function scopedAgentNode() {
  const pr = parseRefKey(selectedRef);
  if (!pr) return null;
  const sessions = (lastGraph && lastGraph.sessions) || [];
  const session = sessions.find(s => s && (s.session_id || '') === pr.sessionId);
  if (!session) return null;
  return flattenSession(session)[pr.agentIndex] || null;
}

// insightsScopeAgents resolves the agent list the Insights aggregations run over
// plus the toggle's availability flags and a human label for the active scope.
// The chosen scope CLAMPS to 'graph' when its slice is empty (no plotted session,
// or nothing selected) so the dashboard never renders a blank scope the toggle
// still shows as active.
function insightsScopeAgents() {
  const sessions = (lastGraph && lastGraph.sessions) || [];
  const cur = currentTimelineSession();
  const sessionAgents = cur ? flattenSession(cur.session) : null;
  const agentNode = scopedAgentNode();
  const sessionAvail = !!(sessionAgents && sessionAgents.length);
  const agentAvail = agentNode != null;
  let scope = insightsScope;
  if (scope === 'session' && !sessionAvail) scope = 'graph';
  if (scope === 'agent' && !agentAvail) scope = 'graph';
  if (scope === 'session') {
    return { scope, agents: sessionAgents, sessionAvail, agentAvail,
      label: `${baseName(cur.session.cwd) || '—'} · ${shortId(cur.session.session_id || '')}` };
  }
  if (scope === 'agent') {
    return { scope, agents: [agentNode], sessionAvail, agentAvail, label: agentLabel(agentNode) };
  }
  return { scope, agents: sessions.flatMap(flattenSession), sessionAvail, agentAvail,
    label: `${sessions.length} session${sessions.length === 1 ? '' : 's'}` };
}

// INS_TABS is the Insights section list — each panel is its own tab. Order is
// the tab order; the first (Signals, the audit headline) is the default. `scoped`
// flags whether the panel re-slices by the Graph / Session / Agent toggle —
// Signals is graph-wide (see renderSignalsPanel), so its tab hides the toggle.
// `render` is the panel renderer: scoped panels take the resolved agent list;
// Signals is dispatched specially in renderInsights (it reads lastGraph, not the
// scoped slice).
const INS_TABS = [
  { key: 'signals', label: 'Signals',         scoped: false },
  { key: 'toolmix', label: 'Tool mix',        scoped: true,  render: renderToolMixPanel },
  { key: 'pareto',  label: 'Cost Pareto',     scoped: true,  render: renderParetoPanel },
  { key: 'latency', label: 'Latency',         scoped: true,  render: renderLatencyPanel },
  { key: 'errors',  label: 'Errors',          scoped: true,  render: renderErrorPanel },
  { key: 'tokens',  label: 'Token & context', scoped: true,  render: renderTokenPanel },
  { key: 'groups',  label: 'Group by',        scoped: true,  render: renderGroupPanel },
];

// renderInsights draws the Insights dashboard: a tab strip (one tab per section),
// a scope toggle (graph / session / agent) over the shared selection, and the
// active section's panel. Each panel re-scopes by the toggle EXCEPT Signals,
// which is graph-wide — so its tab hides the toggle. Pure render from lastGraph
// (no scrub / SSE), so it degrades cleanly in the static `claudit report`.
function renderInsights(sessions) {
  const { scope, agents, sessionAvail, agentAvail, label } = insightsScopeAgents();
  const tab = INS_TABS.find(t => t.key === insightsTab) || INS_TABS[0];

  const tabBtn = (t) =>
    `<button type="button" class="ins-tab${t.key === tab.key ? ' is-active' : ''}" data-ins-tab="${t.key}" role="tab" aria-selected="${t.key === tab.key}">${escHtml(t.label)}</button>`;
  const tabNav = `<nav class="ins-tabs" role="tablist" aria-label="Insights sections">${INS_TABS.map(tabBtn).join('')}</nav>`;

  // The scope toggle re-slices every scoped panel; Signals is graph-wide, so it
  // never shows on that tab.
  const scopeBtn = (key, text, avail) =>
    `<button type="button" class="ins-seg-btn${scope === key ? ' is-active' : ''}" data-ins-scope="${key}"${avail ? '' : ' disabled'} aria-pressed="${scope === key}">${escHtml(text)}</button>`;
  const scopeHead = tab.scoped ? `<div class="ins-controls">
      <div class="ins-seg" role="group" aria-label="Insights scope">
        ${scopeBtn('graph', 'Graph', true)}
        ${scopeBtn('session', 'Session', sessionAvail)}
        ${scopeBtn('agent', 'Agent', agentAvail)}
      </div>
      <span class="ins-scope-label" title="${escHtml(label)}">${escHtml(label)}</span>
    </div>` : '';

  let panel;
  if (tab.key === 'signals') {
    // Signals is GRAPH-WIDE by design (scope-independent): detectSignals emits
    // real cross-session refKeys, so the panel surfaces every flagged anomaly in
    // the window and click-through jumps to it wherever it lives; re-scoping it
    // would mean a synthetic sub-graph whose renumbered agent indices break those
    // refs. nowMs is the live clock in serve mode and ABSENT in the static report
    // (where the page can't know the wall-clock), keeping the static-safe
    // contract — idle-stall's trailing-gap check degrades cleanly when omitted.
    const nowMs = isServeMode() ? Date.now() : undefined;
    panel = renderSignalsPanel(detectSignals(lastGraph, { nowMs }));
  } else {
    panel = tab.render(agents);
  }

  return `<div class="ins">${tabNav}${scopeHead}${panel}</div>`;
}

// renderToolMixPanel draws the Tool-mix panel: per-kind count · time · cost as
// horizontal bars, the bar driven by the chosen metric and rows sorted
// worst-first by toolMix. Pure render from the scoped agents (no scrub / SSE).
function renderToolMixPanel(agents) {
  const metric = INS_METRICS.find(m => m.key === insightsMetric) || INS_METRICS[0];
  const metricBtn = (m) =>
    `<button type="button" class="ins-seg-btn${metric.key === m.key ? ' is-active' : ''}" data-ins-metric="${m.key}" aria-pressed="${metric.key === m.key}">${escHtml(m.label)}</button>`;
  const head = `<header class="ins-panel-head">
      <h3 class="ins-panel-title">Tool mix</h3>
      <div class="ins-seg ins-seg-sm" role="group" aria-label="Bar metric">${INS_METRICS.map(metricBtn).join('')}</div>
    </header>`;

  // toolMix returns rows cost-sorted; re-sort worst-first by the ACTIVE metric so
  // the bar column stays monotonic (the longest bar reads first) whichever metric
  // drives it. A copy — toolMix's own ordering is left untouched.
  const mix = toolMix(agents).slice().sort((a, b) => metric.get(b) - metric.get(a));
  if (mix.length === 0) {
    return `<section class="ins-panel">${head}<div class="ins-empty">No tool calls in this scope.</div></section>`;
  }

  const colLabels = `<div class="ins-row ins-row-labels" aria-hidden="true">
      <span class="ins-row-head"></span><span class="ins-bar-wrap"></span>
      <span class="ins-figs">${INS_METRICS.map(m => `<span class="ins-fig${m.key === metric.key ? ' is-primary' : ''}">${escHtml(m.label)}</span>`).join('')}</span>
    </div>`;

  const max = mix.reduce((m, r) => Math.max(m, metric.get(r)), 0) || 1;
  const rows = mix.map(r => {
    const pct = Math.max(2, Math.round((metric.get(r) / max) * 100));
    const fam = kindFamily(r.kind);
    const figs = INS_METRICS.map(m =>
      `<span class="ins-fig${m.key === metric.key ? ' is-primary' : ''}" title="${escHtml(m.label)}">${escHtml(m.fmt(m.get(r)))}</span>`).join('');
    return `<div class="ins-row kind-${fam}">
        <span class="ins-row-head">${kindBadge(r.kind)}<span class="ins-kind-name">${escHtml(r.kind)}</span></span>
        <span class="ins-bar-wrap"><span class="ins-bar" style="width:${pct}%"></span></span>
        <span class="ins-figs">${figs}</span>
      </div>`;
  }).join('');

  return `<section class="ins-panel">
      ${head}
      ${colLabels}
      <div class="ins-rows">${rows}</div>
    </section>`;
}

// renderSignalsPanel draws the Signals panel — the anomaly headline of the
// Agents audit. detectSignals already returns findings worst-first by SEVERITY
// (the cross-kind sort key), so rows render in that order verbatim — NOT
// re-sorted by tier. Each row carries a tier-colored pip (high/med/low — the
// detector's own banding, for pip color only), the self-contained summary
// sentence (rendered as-is — no fmt* re-formatting), and the kind. Every row is
// click-through: data-ref holds the refKey, so the shared data-ref delegate (the
// same one every lens uses) lands selection on that agent/step/tool — no bespoke
// handler. Pure render; in the static report detectSignals ran without nowMs, so
// the list simply omits any trailing idle-stall and degrades cleanly.
const SIG_CAP = 50;
function renderSignalsPanel(signals) {
  const head = `<header class="ins-panel-head"><h3 class="ins-panel-title">Signals</h3></header>`;
  if (!signals || signals.length === 0) {
    return `<section class="ins-panel">${head}<div class="ins-empty ins-sig-clean">No anomalies detected — nothing flagged in this window.</div></section>`;
  }
  // A heavy window can flag thousands of anomalies; rendering them all is a dead
  // DOM. Cap the list at the SIG_CAP worst (the array is already severity-sorted)
  // and disclose the remainder — never silently truncate.
  const shown = signals.slice(0, SIG_CAP);
  const overflow = signals.length - shown.length;
  const rows = shown.map(s => {
    const tier = s.tier === 'high' || s.tier === 'med' ? s.tier : 'low';
    return `<div class="ins-sig-row sig-${tier} is-clickable" role="button" tabindex="0" data-ref="${escHtml(s.ref)}" title="Jump to this ${escHtml(s.kind)}">
        <span class="ins-sig-pip" aria-hidden="true"></span>
        <span class="ins-sig-summary">${escHtml(s.summary)}</span>
        <span class="ins-sig-kind">${escHtml(s.kind)}</span>
      </div>`;
  }).join('');
  const more = overflow > 0
    ? `<div class="ins-sig-more">+ ${fmtNum(overflow)} more, worst-first — showing the top ${SIG_CAP}</div>`
    : '';
  const stats = `<div class="ins-stats"><span class="ins-stat ins-stat-n">${fmtNum(signals.length)} signal${signals.length === 1 ? '' : 's'}</span></div>`;
  return `<section class="ins-panel">
      <div class="ins-panel-head ins-panel-head--split">
        <h3 class="ins-panel-title">Signals</h3>
        ${stats}
      </div>
      <p class="ins-panel-note">Anomalies across the whole graph, worst first — always graph-wide, so the Graph / Session / Agent scope on the other tabs doesn’t apply here. Click any signal to jump to it on the Timeline.</p>
      <div class="ins-rows ins-sig-rows">${rows}</div>
      ${more}
    </section>`;
}

// fmtDur renders a millisecond latency at the resolution tool spans actually
// land in — keeping sub-second values legible (the seconds-floored formatElapsed
// would collapse them all to "0s"). 240→"240ms", 2500→"2.5s", 60000→"1m".
function fmtDur(ms) {
  if (!Number.isFinite(ms)) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const trim = n => (n < 10 ? n.toFixed(1).replace(/\.0$/, '') : String(Math.round(n)));
  if (ms < 60000) return `${trim(ms / 1000)}s`;
  return `${trim(ms / 60000)}m`;
}

// latencyBucketLabel names a histogram bucket from its [lo, hi) edges: the first
// bucket reads "< hi", the open tail "≥ lo", the rest "lo–hi".
function latencyBucketLabel(b) {
  if (b.lo === 0) return `< ${fmtDur(b.hi)}`;
  if (!Number.isFinite(b.hi)) return `≥ ${fmtDur(b.lo)}`;
  return `${fmtDur(b.lo)}–${fmtDur(b.hi)}`;
}

// renderLatencyPanel draws the second Insights panel: a horizontal histogram of
// per-tool wall-clock (one row per duration bucket, bar ∝ count) plus a p50 /
// p95 / max readout. A non-empty bucket is clickable — it filters the trace to
// the tool kinds in that bucket and jumps to the Timeline (the Insights lens
// itself doesn't dim). Pure render from the scoped agents (no scrub / SSE), so
// it degrades cleanly in the static report.
function renderLatencyPanel(agents) {
  const { buckets, values } = durationHistogram(agents);
  const head = `<header class="ins-panel-head"><h3 class="ins-panel-title">Latency</h3></header>`;
  if (values.length === 0) {
    return `<section class="ins-panel">${head}<div class="ins-empty">No timed tool calls in this scope.</div></section>`;
  }

  const [p50, p95, max] = percentiles(values, [50, 95, 100]);
  const stat = (label, v) => `<span class="ins-stat"><span class="ins-stat-k">${label}</span><span class="ins-stat-v">${escHtml(fmtDur(v))}</span></span>`;
  const stats = `<div class="ins-stats">${stat('p50', p50)}${stat('p95', p95)}${stat('max', max)}<span class="ins-stat ins-stat-n">${fmtNum(values.length)} timed</span></div>`;

  const maxCount = buckets.reduce((m, b) => Math.max(m, b.count), 0) || 1;
  const rows = buckets.map(b => {
    const pct = b.count > 0 ? Math.max(3, Math.round((b.count / maxCount) * 100)) : 0;
    const clickable = b.count > 0 && b.kinds.length > 0;
    const attrs = clickable
      ? ` role="button" tabindex="0" data-ins-bucket data-ins-kinds="${escHtml(b.kinds.join(','))}" title="Filter the trace to ${escHtml(b.kinds.join(', '))} and show on the Timeline"`
      : '';
    return `<div class="ins-lat-row${clickable ? ' is-clickable' : ''}"${attrs}>
        <span class="ins-lat-label">${escHtml(latencyBucketLabel(b))}</span>
        <span class="ins-bar-wrap"><span class="ins-bar ins-lat-bar" style="width:${pct}%"></span></span>
        <span class="ins-lat-count">${b.count ? fmtNum(b.count) : ''}</span>
      </div>`;
  }).join('');

  return `<section class="ins-panel">
      <div class="ins-panel-head ins-panel-head--split">
        <h3 class="ins-panel-title">Latency</h3>
        ${stats}
      </div>
      <div class="ins-rows ins-lat-rows">${rows}</div>
    </section>`;
}

// renderParetoPanel draws the third Insights panel: a Cost-Pareto view of where
// the money goes. A headline callout names the concentration ("top N% of turns
// drive X% of spend"), then a ranked list of the priciest turns — each row a
// cost bar with a rising cumulative-share tick (the hand-rolled Pareto line).
// Both the headline and each row are click-through: they set the trace's
// minCostUSD to that turn's cost (the headline uses the decile threshold) and
// jump to the Timeline, dimming everything cheaper — Insights has no dimming of
// its own. Pure render from the scoped agents (no scrub / SSE), so it degrades
// cleanly in the static report.
function renderParetoPanel(agents) {
  const { total, count, rows, headline } = costPareto(agents);
  const head = `<header class="ins-panel-head"><h3 class="ins-panel-title">Cost Pareto</h3></header>`;
  if (count === 0) {
    return `<section class="ins-panel">${head}<div class="ins-empty">No turn cost in this scope.</div></section>`;
  }

  const cut = (v, label) => ` role="button" tabindex="0" data-ins-cost-cut="${v}" title="Filter the trace to turns ≥ ${escHtml(fmtMoney(v))} and show on the Timeline${label ? ` (${escHtml(label)})` : ''}"`;
  const callout = `<button type="button" class="ins-pareto-headline"${cut(headline.thresholdCost, 'isolate the whales')}>
      Top <b>${headline.turnsPct}%</b> of turns (${fmtNum(headline.turnCount)} of ${fmtNum(count)}) drive <b>${escHtml(fmtPct1(headline.spendShare))}</b> of spend
    </button>`;

  const maxCost = rows.reduce((m, r) => Math.max(m, r.cost), 0) || 1;
  const list = rows.map(r => {
    const pct = Math.max(3, Math.round((r.cost / maxCost) * 100));
    return `<div class="ins-pareto-row is-clickable"${cut(r.cost, '')}>
        <span class="ins-pareto-rank">#${r.rank}</span>
        <span class="ins-pareto-who" title="${escHtml(r.agentLabel)}">${escHtml(r.agentLabel)}</span>
        <span class="ins-bar-wrap">
          <span class="ins-bar ins-pareto-bar" style="width:${pct}%"></span>
          <span class="ins-pareto-cum" style="left:${(r.cumShare * 100).toFixed(1)}%" title="cumulative ${escHtml(fmtPct1(r.cumShare))} of spend"></span>
        </span>
        <span class="ins-figs">
          <span class="ins-fig is-primary">${escHtml(fmtMoney(r.cost))}</span>
          <span class="ins-fig ins-pareto-cumfig" title="cumulative share">${escHtml(fmtPct1(r.cumShare))}</span>
        </span>
      </div>`;
  }).join('');

  const stats = `<div class="ins-stats"><span class="ins-stat ins-stat-n">${escHtml(fmtMoney(total))} · ${fmtNum(count)} turns</span></div>`;
  return `<section class="ins-panel">
      <div class="ins-panel-head ins-panel-head--split">
        <h3 class="ins-panel-title">Cost Pareto</h3>
        ${stats}
      </div>
      <p class="ins-panel-note">How concentrated your spend is: the priciest turns ranked first, with a running cumulative-share line. Tells you whether a few “whale” turns drive the cost or it’s spread evenly. Click a row to filter the Timeline to turns at least that expensive.</p>
      ${callout}
      <div class="ins-rows ins-pareto-rows">${list}</div>
    </section>`;
}

// renderErrorPanel draws the fourth Insights panel: where tools fail. A headline
// callout names the overall error rate, then per-ToolKind bars (width ∝ that
// kind's error rate on an absolute 0–100% scale, so a 5% bar reads small) and a
// compact "failing tools" list naming the worst offenders by tool name. Every
// clickable target sets the trace's errorsOnly filter (a kind bar / tool row also
// pins that kind) and jumps to the Timeline, where the dimming Insights can't
// show actually lands. Pure render from the scoped agents (no scrub / SSE), so it
// degrades cleanly in the static report.
function renderErrorPanel(agents) {
  const { total, errors, rate, rows, worst } = errorRates(agents);
  const head = `<header class="ins-panel-head"><h3 class="ins-panel-title">Errors</h3></header>`;
  if (total === 0) {
    return `<section class="ins-panel">${head}<div class="ins-empty">No tool calls in this scope.</div></section>`;
  }
  if (errors === 0) {
    return `<section class="ins-panel">
        <div class="ins-panel-head ins-panel-head--split">
          <h3 class="ins-panel-title">Errors</h3>
          <div class="ins-stats"><span class="ins-stat ins-stat-n">${fmtNum(total)} calls</span></div>
        </div>
        <div class="ins-empty ins-err-clean">No tool errors — all ${fmtNum(total)} calls clean.</div>
      </section>`;
  }

  // The overall callout filters to every errored tool (no kind pin).
  const allErr = ` role="button" tabindex="0" data-ins-err data-ins-kinds="" title="Filter the trace to errored tools and show on the Timeline"`;
  const callout = `<button type="button" class="ins-err-headline"${allErr}>
      <b>${fmtNum(errors)}</b> of <b>${fmtNum(total)}</b> tool calls errored (<b>${escHtml(fmtPct1(rate))}</b>)
    </button>`;

  // Per-kind bars — width is the kind's error rate on an absolute 0–100% scale
  // (clamped to a visible floor), the figs carry the E/T count and the rate.
  const kindRows = rows.map(r => {
    const pct = Math.max(3, Math.round(r.rate * 100));
    const fam = kindFamily(r.kind);
    const cut = ` role="button" tabindex="0" data-ins-err data-ins-kinds="${escHtml(r.kind)}" title="Filter the trace to errored ${escHtml(r.kind)} tools and show on the Timeline"`;
    return `<div class="ins-err-row kind-${fam} is-clickable"${cut}>
        <span class="ins-row-head">${kindBadge(r.kind)}<span class="ins-kind-name">${escHtml(r.kind)}</span></span>
        <span class="ins-bar-wrap"><span class="ins-bar ins-err-bar" style="width:${pct}%"></span></span>
        <span class="ins-figs">
          <span class="ins-fig">${fmtNum(r.errors)}/${fmtNum(r.total)}</span>
          <span class="ins-fig is-primary">${escHtml(fmtPct1(r.rate))}</span>
        </span>
      </div>`;
  }).join('');

  // Failing-tools list — by tool NAME (finer than kind), a compact chip row.
  const worstList = worst.map(w => {
    const cut = ` role="button" tabindex="0" data-ins-err data-ins-kinds="${escHtml(w.kind)}" title="Filter the trace to errored ${escHtml(w.kind)} tools and show on the Timeline"`;
    return `<div class="ins-err-tool is-clickable"${cut}>
        ${kindBadge(w.kind)}
        <span class="ins-err-tool-name" title="${escHtml(w.name)}">${escHtml(w.name)}</span>
        <span class="ins-err-tool-fig"><b>${fmtNum(w.errors)}</b> ${w.errors === 1 ? 'error' : 'errors'} · ${escHtml(fmtPct1(w.rate))}</span>
      </div>`;
  }).join('');
  const worstBlock = worst.length
    ? `<div class="ins-err-worst-head">Failing tools</div><div class="ins-err-worst">${worstList}</div>`
    : '';

  const stats = `<div class="ins-stats"><span class="ins-stat"><span class="ins-stat-k">rate</span><span class="ins-stat-v">${escHtml(fmtPct1(rate))}</span></span><span class="ins-stat ins-stat-n">${fmtNum(total)} calls</span></div>`;
  return `<section class="ins-panel">
      <div class="ins-panel-head ins-panel-head--split">
        <h3 class="ins-panel-title">Errors</h3>
        ${stats}
      </div>
      ${callout}
      <div class="ins-rows ins-err-rows">${kindRows}</div>
      ${worstBlock}
    </section>`;
}

// tokCtxSvg draws the context-growth area sparkline: context per bin over the run
// (bins from binSeries, each carrying the max context it spans), filled from a
// zero baseline with a stroked top line. viewBox units are stretched to the panel
// width (preserveAspectRatio="none"), so the line uses a non-scaling stroke to
// keep an even weight and the chart reads as one continuous curve — the sawtooth
// of accumulation then compaction. A single point draws a flat line. Pure
// geometry from the (already chronological) bins; `peak` scales the y-axis.
function tokCtxSvg(bins, peak) {
  const W = 100, H = 36, pad = 1.5, usable = H - pad * 2;
  const n = bins.length;
  const px = i => (n > 1 ? (i / (n - 1)) * W : W / 2);
  const py = c => H - pad - (peak > 0 ? (c / peak) * usable : 0);
  let line, area;
  if (n === 1) {
    const y = py(bins[0].context).toFixed(2);
    line = `M0,${y} L${W},${y}`;
    area = `M0,${H} L0,${y} L${W},${y} L${W},${H} Z`;
  } else {
    const pts = bins.map((p, i) => `${px(i).toFixed(2)},${py(p.context).toFixed(2)}`);
    line = `M${pts.join(' L')}`;
    area = `M0,${H} L${pts.join(' L')} L${W},${H} Z`;
  }
  return `<svg class="ins-tok-svg ins-tok-ctx-svg" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-hidden="true">
      <path class="ins-tok-ctx-area" d="${area}"/>
      <path class="ins-tok-ctx-line" d="${line}" fill="none" vector-effect="non-scaling-stroke"/>
    </svg>`;
}

// tokTurnsSvg draws the per-turn token bars: one bar per bin, height ∝ that bin's
// total tokens, split into the cache-read share (cheap, bottom) and the fresh
// tokens on top (input + cache-write + output) so the bar strip doubles as a
// per-turn cache-health read. Axis-aligned rects stretch cleanly under
// preserveAspectRatio="none". Pure geometry from the chronological bins.
function tokTurnsSvg(bins) {
  const W = 100, H = 28;
  const maxTotal = bins.reduce((m, p) => Math.max(m, p.total), 0) || 1;
  const n = bins.length;
  const bw = W / n;
  const w = (bw * (n > 60 ? 1 : 0.86)).toFixed(3);
  const bars = bins.map((p, i) => {
    const x = (i * bw).toFixed(3);
    const hTotal = (p.total / maxTotal) * H;
    const hRead = (p.cacheRead / maxTotal) * H;
    const rest = `<rect class="ins-tok-bar-fresh" x="${x}" y="${(H - hTotal).toFixed(2)}" width="${w}" height="${(hTotal - hRead).toFixed(2)}"/>`;
    const read = hRead > 0 ? `<rect class="ins-tok-bar-read" x="${x}" y="${(H - hRead).toFixed(2)}" width="${w}" height="${hRead.toFixed(2)}"/>` : '';
    return rest + read;
  }).join('');
  return `<svg class="ins-tok-svg ins-tok-turns-svg" viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-hidden="true">${bars}</svg>`;
}

// renderTokenPanel draws the fifth Insights panel: token usage and context-window
// growth. A context-growth area sparkline (how the prompt accumulates and resets
// at compaction), a cache-hit readout, and per-turn token bars split by cache-read
// share. Reads contextSeries over the scoped agents. Unlike the other panels this
// one is read-only: the trace filter has no token / context dimension to "land" on
// (it filters by kind / cost / duration / errors), and selecting a single turn
// isn't an Insights idiom — so there's no click-through, just a diagnostic. Pure
// render from the scoped agents (no scrub / SSE), so it degrades cleanly in the
// static `claudit report`.
function renderTokenPanel(agents) {
  const { turns, series, peakContext, cacheHit, totals } = contextSeries(agents);
  const head = `<header class="ins-panel-head"><h3 class="ins-panel-title">Token &amp; context</h3></header>`;
  if (turns === 0) {
    return `<section class="ins-panel">${head}<div class="ins-empty">No token usage in this scope.</div></section>`;
  }

  // Peak prompt size is shown as an absolute figure — not a "% of window" — since
  // the transcript records neither the model's context-window size nor the 1M-beta
  // flag, so any denominator would be a guess (it overstates fill whenever a run's
  // prompts stayed under the 200k tier boundary).
  const stat = (k, v, title) => `<span class="ins-stat"${title ? ` title="${escHtml(title)}"` : ''}><span class="ins-stat-k">${k}</span><span class="ins-stat-v">${escHtml(v)}</span></span>`;
  const stats = `<div class="ins-stats">${stat('cache hit', fmtPct1(cacheHit))}${stat('peak ctx', fmtCompact(peakContext), 'Largest prompt fed to the model in this scope')}<span class="ins-stat ins-stat-n">${fmtNum(turns)} turns</span></div>`;

  // Downsample so a long run stays legible (and cheap to render) — sub-pixel bars
  // and tens of thousands of SVG nodes help no one. binSeries folds nothing away.
  const TOK_MAX_BINS = 120;
  const bins = binSeries(series, TOK_MAX_BINS);
  const binned = bins.length < turns;
  const perBin = binned ? Math.ceil(turns / bins.length) : 1;

  const last = series[series.length - 1].context;
  const ctxFig = `<figure class="ins-tok-fig">
      ${tokCtxSvg(bins, peakContext)}
      <figcaption class="ins-tok-cap">Context over ${fmtNum(turns)} turns · peak <b>${escHtml(fmtCompact(peakContext))}</b> · last ${escHtml(fmtCompact(last))}</figcaption>
    </figure>`;

  // Token totals composition — a four-band stacked bar sharing the Tokens-view
  // color language. Bands follow the canonical per-row token order used by the
  // Tokens view and the drawer (input→warn, output→hot, cache-write→accent,
  // cache-read→accent-2), so the cache-read band IS the visual cache-hit readout.
  const tt = totals.total || 1;
  const band = (cls, val, label) => {
    const pct = (val / tt) * 100;
    return pct > 0 ? `<span class="ins-tok-band ${cls}" style="width:${pct.toFixed(2)}%" title="${escHtml(label)}: ${escHtml(fmtCompact(val))} (${escHtml(fmtPct1(val / tt))})"></span>` : '';
  };
  const comp = `<div class="ins-tok-comp" role="img" aria-label="Token composition">
      ${band('tok-area-input', totals.input, 'input')}${band('tok-area-output', totals.output, 'output')}${band('tok-area-cwrite', totals.cacheWrite, 'cache write')}${band('tok-area-cread', totals.cacheRead, 'cache read')}
    </div>`;
  const leg = (cls, label, val) => `<span class="ins-tok-leg-item"><span class="ins-tok-sw ${cls}"></span>${label} <b>${escHtml(fmtCompact(val))}</b></span>`;
  const legend = `<div class="ins-tok-legend">
      ${leg('tok-area-input', 'input', totals.input)}${leg('tok-area-output', 'output', totals.output)}${leg('tok-area-cwrite', 'cache write', totals.cacheWrite)}${leg('tok-area-cread', 'cache read', totals.cacheRead)}
    </div>`;

  const perLabel = binned ? `per ~${fmtNum(perBin)} turns (binned)` : 'per turn';
  const turnsFig = `<figure class="ins-tok-fig">
      ${tokTurnsSvg(bins)}
      <figcaption class="ins-tok-cap">Tokens ${perLabel} · bottom band is cache read (cheap), top is fresh</figcaption>
    </figure>`;

  return `<section class="ins-panel">
      <div class="ins-panel-head ins-panel-head--split">
        <h3 class="ins-panel-title">Token &amp; context</h3>
        ${stats}
      </div>
      ${ctxFig}
      ${comp}
      ${legend}
      ${turnsFig}
    </section>`;
}

// INS_GROUP_DIMS is the Group-by panel's dimension menu: which field buckets the
// tool rows. Order is the toggle order; the first is the default. Each key is a
// groupBy dimension; label is the toggle text.
const INS_GROUP_DIMS = [
  { key: 'kind',   label: 'Kind' },
  { key: 'model',  label: 'Model' },
  { key: 'agent',  label: 'Agent' },
  { key: 'status', label: 'Status' },
];

// renderGroupPanel draws the sixth Insights panel — the lightweight BubbleUp
// slice. A dimension toggle (kind / model / agent / status) buckets every tool
// row, and the table reports per group: calls, median + total wall-clock, and
// total (call-share-apportioned) cost, sorted worst-first by spend (groupBy's
// order). The bar reads the cost share, so the table doubles as "where the spend
// concentrates" — falling back to call share when the scope carries no cost.
//
// Click-through is partial BY DESIGN: it only lights up where the dimension value
// maps to a precise trace filter. The 'kind' dimension pins kinds:[key] (reusing
// applyBucketFilter); the 'status' dimension's 'error' row turns on errorsOnly
// (reusing applyErrorFilter). 'model' and 'agent' — and the ok/none status rows —
// have no matching trace dimension (the filter is tool-centric: kind / cost /
// duration / errors), so those rows stay read-only diagnostics, like the Token
// panel. Pure render from the scoped agents (no scrub / SSE), so it degrades
// cleanly in the static `claudit report`.
function renderGroupPanel(agents) {
  const dim = INS_GROUP_DIMS.find(d => d.key === insightsGroupDim) || INS_GROUP_DIMS[0];
  const { total, rows } = groupBy(agents, { dimension: dim.key });
  const dimBtn = (d) =>
    `<button type="button" class="ins-seg-btn${dim.key === d.key ? ' is-active' : ''}" data-ins-group-dim="${d.key}" aria-pressed="${dim.key === d.key}">${escHtml(d.label)}</button>`;
  const toggle = `<div class="ins-seg ins-seg-sm" role="group" aria-label="Group-by dimension">${INS_GROUP_DIMS.map(dimBtn).join('')}</div>`;
  if (rows.length === 0) {
    return `<section class="ins-panel">
        <div class="ins-panel-head ins-panel-head--split">
          <h3 class="ins-panel-title">Group by</h3>${toggle}
        </div>
        <div class="ins-empty">No tool calls in this scope.</div>
      </section>`;
  }

  // Bar reads cost share when there's spend to compare, else falls back to call
  // share so the column still ranks something visible.
  const byCost = total.costUSD > 0;
  const barVal = r => (byCost ? r.costUSD : r.count);
  const maxBar = rows.reduce((m, r) => Math.max(m, barVal(r)), 0) || 1;

  const colLabels = `<div class="ins-row ins-grp-row ins-row-labels" aria-hidden="true">
      <span class="ins-row-head"></span><span class="ins-bar-wrap"></span>
      <span class="ins-figs"><span class="ins-fig">calls</span><span class="ins-fig">med</span><span class="ins-fig">time</span><span class="ins-fig is-primary">cost</span></span>
    </div>`;

  const body = rows.map(r => {
    const pct = Math.max(2, Math.round((barVal(r) / maxBar) * 100));
    // Clickability — only where the value maps to a trace filter (see doc above).
    let clickAttr = '', cls = '';
    if (dim.key === 'kind') {
      clickAttr = ` role="button" tabindex="0" data-ins-group data-ins-kinds="${escHtml(r.key)}" title="Filter the trace to ${escHtml(r.key)} tools and show on the Timeline"`;
      cls = ' is-clickable';
    } else if (dim.key === 'status' && r.key === 'error') {
      clickAttr = ` role="button" tabindex="0" data-ins-group data-ins-err title="Filter the trace to errored tools and show on the Timeline"`;
      cls = ' is-clickable';
    }
    const label = dim.key === 'kind'
      ? `${kindBadge(r.key)}<span class="ins-kind-name">${escHtml(r.key)}</span>`
      : `<span class="ins-grp-key" title="${escHtml(r.key)}">${escHtml(r.key)}</span>`;
    const med = Number.isNaN(r.medianMs) ? '—' : fmtDur(r.medianMs);
    const fam = dim.key === 'kind' ? kindFamily(r.key) : 'other';
    return `<div class="ins-row ins-grp-row kind-${fam}${cls}"${clickAttr}>
        <span class="ins-row-head">${label}</span>
        <span class="ins-bar-wrap"><span class="ins-bar ins-grp-bar" style="width:${pct}%"></span></span>
        <span class="ins-figs">
          <span class="ins-fig">${fmtNum(r.count)}</span>
          <span class="ins-fig" title="median tool wall-clock">${escHtml(med)}</span>
          <span class="ins-fig" title="total tool wall-clock">${escHtml(fmtDur(r.durationMs))}</span>
          <span class="ins-fig is-primary">${escHtml(fmtMoney(r.costUSD))}</span>
        </span>
      </div>`;
  }).join('');

  const stats = `<div class="ins-stats"><span class="ins-stat ins-stat-n">${fmtNum(rows.length)} group${rows.length === 1 ? '' : 's'}</span><span class="ins-stat ins-stat-n">${fmtNum(total.count)} calls</span></div>`;
  return `<section class="ins-panel">
      <div class="ins-panel-head ins-panel-head--split">
        <h3 class="ins-panel-title">Group by</h3>
        <div class="ins-grp-head-controls">${stats}${toggle}</div>
      </div>
      ${colLabels}
      <div class="ins-rows ins-grp-rows">${body}</div>
    </section>`;
}

// onTreeScroll stamps the user's last scroll in the tree's .itree container,
// the signal treeFollowMode uses to pause/resume live reordering. A scroll back
// to the very top resets the stamp so we follow again immediately rather than
// waiting out the idle window.
function onTreeScroll(e) {
  const itree = e.target.closest && e.target.closest('.itree');
  if (!itree) return;
  lastTreeScrollAt = itree.scrollTop <= 0 ? null : Date.now();
}

// setLive resumes live mode: the playhead follows "now" and auto-advances on
// each refetch. A full re-render rebuilds the scrubber with the thumb at the
// right edge and every bar grown to the present.
function setLive(container) {
  setPlayheadT(null);
  renderActive(container, true);
}

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
