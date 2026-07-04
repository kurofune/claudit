// Shared state + helpers for the Agents tab, split out of view-agents.js.
//
// Every lens module (agents-feed/tree/timeline/drawer/filter) imports from
// HERE (and from the pure agents-logic modules) — never from the core
// view-agents.js, which imports the lens entry points. That keeps the module
// graph acyclic (the static-report bundler rejects import cycles).
//
// State variables are exported `let` live bindings: importers READ them by
// name exactly as before the split, but ES modules forbid assigning to an
// imported binding — so each cross-module-assigned variable carries a tiny
// setter the owning writers call instead.

import { escHtml } from './format.js';
import { clampConvSidebarWidth, originClass } from './agents-logic.js';

export const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

// originBadgeHTML tags a session whose origin is headless (claude -p / Agent
// SDK) with an "SDK" pill. Interactive runs get nothing — absence of the badge
// is itself the signal, keeping a long session list quiet. originClass folds
// any "sdk*" entrypoint to 'sdk'; everything else is interactive.
export function originBadgeHTML(session) {
  return originClass(session && session.entrypoint) === 'sdk'
    ? `<span class="s-entry s-entry-sdk" title="Headless run (claude -p / Agent SDK)">SDK</span>`
    : '';
}

// isServeMode is true when a live claudit server is backing the page. The
// static HTML report inlines its data into window.__claudit_static_data and
// has no disk to read at view time, so the drawer's "show full" affordance
// (which fetches untruncated tool I/O from disk) is serve-only.
export function isServeMode() {
  return !(typeof window !== 'undefined' && window.__claudit_static_data);
}

// View-local state. lastGraph is the most recent payload; the live handler
// and lens switches both re-render against it without a refetch. selectedRef
// is the ONE selection shared across every lens — a refKey string
// (agent "sid#ai" · step "sid#ai.si" · tool "sid#ai.si:ti"), persisted across
// refetch so a live update doesn't reset what the user was inspecting.
export let lastGraph = null;
export function setLastGraph(g) { lastGraph = g; }
export let activeSub = 'feed';
export function setActiveSub(sub) { activeSub = sub; }
export let selectedRef = null;
export function setSelectedRef(ref) { selectedRef = ref; }

// jumpFlashRef is the ref a cross-lens jump just landed on; the Timeline render
// stamps an `is-jumped` class on its segment/row so a one-shot CSS pulse draws
// the eye to the spot. State-driven (not an imperative class-add) so it survives
// the re-renders a smooth scroll-into-view triggers; cleared after the pulse.
export let jumpFlashRef = null;
export function setJumpFlashRef(ref) { jumpFlashRef = ref; }

// fullCache keys loaded-full tool I/O by tool_use id → { input?, output? }.
// When the user clicks "show full", the untruncated content is cached here and
// fed into buildDrawerPayload on EVERY drawer paint, so a live SSE re-render
// keeps the expanded content sticky instead of reverting to the snippet.
export let fullCache = {};
export function setFullCache(c) { fullCache = c; }

// fullTurnCache is the turn-level twin: keyed by turn uuid → { thinking?,
// text? }. Same sticky mechanism, feeding buildDrawerPayload's fullByTurn
// param so expanded Reasoning/Message survive live repaints.
export let fullTurnCache = {};
export function setFullTurnCache(c) { fullTurnCache = c; }

// Timeline lens state. prevTimelineKeys tracks which agent rows existed on the
// last render so a genuinely NEW agent fades in (and the rest don't re-animate
// on every live re-render). timelinePinned[sid]=true means the user scrolled a
// session's Gantt back into history, so live updates must NOT yank it to the
// "now" edge — the #1 live-trace UX trap; the "● now" button clears it.
export let prevTimelineKeys = new Set();
export function setPrevTimelineKeys(s) { prevTimelineKeys = s; }
export const timelinePinned = new Map();

// Timeline scrubber state. playheadT is the instant the Gantt is rendered "as
// of": null means LIVE (the playhead follows now and auto-advances on each
// refetch); a number pauses it at that absolute epoch-ms, so the bars/counts
// recompute from events ≤ T (a pure seek, never an incremental replay).
export let playheadT = null;
export function setPlayheadT(t) { playheadT = t; }
// Timeline zoom state. tlMinPxPerMs is the time-axis density (px per ms) the
// Gantt renders at; null ⇒ buildTimeline's default (the un-zoomed overview).
// Scroll-wheel over the chart multiplies it (cursor-anchored), double-click
// resets to fit-to-width. Like playheadT it's a single module var scoped to the
// one plotted session and reset whenever the plotted session changes. The
// time WINDOW [startMs,endMs] is untouched by zoom — only pixel density — so
// zoom and scrub compose cleanly (the axis never reflows).
export let tlMinPxPerMs = null;
export function setTlMinPxPerMs(v) { tlMinPxPerMs = v; }
// Which single session the Timeline lens plots. null ⇒ resolve via
// pickTimelineSid (selected turn's session, else the first); a sid pins that
// session until the user picks another or it ages out of the window. Kept
// separate from selectedRef so inspecting a tool sub-span (which sets
// selectedRef) never changes which session is plotted.
export let timelineSid = null;
export function setTimelineSid(sid) { timelineSid = sid; }

// Trace filter (Phase 2). filterSpec is the live filter over the loaded graph;
// when specActive, filterTrace gives the Set of matching refKeys and every lens
// dims the rest. filterBarBuilt guards the one-time bar build (kind chips depend
// on the graph) so a live re-render never wipes the user's typing or focus.
// hitIndex steps the selection through ordered matches via the ‹ › buttons.
export let filterSpec = { text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: 0 };
export function setFilterSpec(spec) { filterSpec = spec; }
export let filterBarBuilt = false;
export function setFilterBarBuilt(b) { filterBarBuilt = b; }
export let hitIndex = -1;
export function setHitIndex(i) { hitIndex = i; }
// Cached match Set from the last applyFilter (null when not filtering). The
// Timeline's scrub repaint rebuilds ~19k segment nodes per animation frame and
// re-dims them from THIS cache — a class-only pass — instead of re-running the
// full-graph filterTrace walk every frame.
export let filterMatchSet = null;
export function setFilterMatchSet(s) { filterMatchSet = s; }

// Conversation lens: the session list on the left is drag-resizable. Width is
// clamped (clampConvSidebarWidth) and persisted to localStorage so it survives
// reloads, live re-renders, and lens switches. Read once, lazily, on first use.
const CONV_SIDEBAR_KEY = 'claudit.agents.convSidebarW';
let convSidebarW = null;
export function convSidebarWidth() {
  if (convSidebarW == null) {
    let stored = null;
    try { stored = localStorage.getItem(CONV_SIDEBAR_KEY); } catch { /* private mode */ }
    convSidebarW = clampConvSidebarWidth(stored);
  }
  return convSidebarW;
}
export function setConvSidebarWidth(px) {
  convSidebarW = clampConvSidebarWidth(px);
  try { localStorage.setItem(CONV_SIDEBAR_KEY, String(convSidebarW)); } catch { /* private mode */ }
  return convSidebarW;
}

export const colorSlot = i => ((i % 5) + 1);

// Tree lens: which agent nodes have their step/tool log rendered. The graph can
// hold thousands of agents (and hundreds of thousands of tools), and a collapsed
// <details> still keeps its children in the DOM — so agent bodies are LAZY: only
// expanded agents (this set) render their log; others are bare summaries. Filled
// on expand (onTreeToggle / ensureAgentExpanded), cleared on collapse, and read
// by renderInspector so a live re-render reproduces exactly the open bodies.
export const openAgentBodies = new Set();

// Tree lens paging: the window can hold thousands of sessions, and a <details>
// keeps its children in the DOM even when collapsed — rendering every session
// eagerly (and re-rendering them on every live tick) is what made the tree
// janky. So the tree renders only the newest TREE_PAGE sessions, with a "show
// more" control revealing the next page. Reset on a fresh load / window change.
export const TREE_PAGE = 40;
export let treeLimit = TREE_PAGE;
export function setTreeLimit(n) { treeLimit = n; }

// coreHooks carries the core's select() to lens modules that need to jump
// the shared selection (the filter bar's ‹ › stepper). Lens modules cannot
// import the core directly — the core imports THEM, and an import cycle
// breaks the static report's blob-URL bundler — so the core registers the
// function here at module init and callers invoke coreHooks.select(...).
export const coreHooks = { select: null };

// startTimelineResize drives the Timeline sidebar's drag-to-resize. It reuses
// the Conversation sidebar's width state (convSidebarW + clamp + persistence) —
// the two lenses are never visible at once, so one shared width is DRY and
// keeps both rails the same size.
export function startTimelineResize(container, startX) {
  const sidebar = container.querySelector('.tl-sidebar');
  if (!sidebar) return;
  const startW = sidebar.getBoundingClientRect().width;
  document.body.style.cursor = 'col-resize';
  document.body.style.userSelect = 'none';
  const onMove = e => {
    const w = clampConvSidebarWidth(startW + (e.clientX - startX));
    convSidebarW = w;
    sidebar.style.width = `${w}px`;
  };
  const onUp = () => {
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup', onUp);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    setConvSidebarWidth(convSidebarW);
  };
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup', onUp);
}

// startConvResize drives the Conversation sidebar's drag-to-resize. It sizes the
// live .conv-sidebar element directly during the drag (no re-render — keeps it
// smooth) and persists the clamped width on release, so it survives reloads,
// lens switches, and live re-renders (which read it back via convSidebarWidth).
export function startConvResize(container, startX) {
  const sidebar = container.querySelector('.conv-sidebar');
  if (!sidebar) return;
  const startW = sidebar.getBoundingClientRect().width;
  document.body.style.cursor = 'col-resize';
  document.body.style.userSelect = 'none';
  const onMove = e => {
    const w = clampConvSidebarWidth(startW + (e.clientX - startX));
    convSidebarW = w;
    sidebar.style.width = `${w}px`;
  };
  const onUp = () => {
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup', onUp);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    setConvSidebarWidth(convSidebarW);
  };
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup', onUp);
}

// dimNodes toggles is-dimmed on every [data-ref] under root against a match Set
// (null = not filtering → clears dimming). Class-only, no filterTrace recompute,
// so the Timeline's per-frame scrub repaint can re-dim its fresh nodes cheaply.
export function dimNodes(root, matchSet) {
  root.querySelectorAll('[data-ref]').forEach(el => {
    el.classList.toggle('is-dimmed', !!matchSet && !matchSet.has(el.dataset.ref));
  });
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
export function kindFamily(kind) {
  return Object.prototype.hasOwnProperty.call(KIND_GLYPH, kind) ? kind : 'other';
}

// kindBadge is the small colored monogram that marks a kind. Takes a normalized
// ToolKind ("exec"/"read"/…) or a pseudo-kind ("agent"/"step") — NOT a raw tool
// name (that mapping now lives in the backend's aggregate.ToolKind).
export function kindBadge(kind) {
  const fam = kindFamily(kind);
  return `<span class="kind-badge kind-${fam}" aria-hidden="true">${escHtml(KIND_GLYPH[fam])}</span>`;
}

// cssEsc escapes a value for use inside a querySelector attribute match.
export const cssEsc = s => (typeof CSS !== 'undefined' && CSS.escape) ? CSS.escape(s) : String(s).replace(/["\\]/g, '\\$&');

// ── preserve scroll / open-rows across a live re-render ────────────────────

export function captureState(host) {
  const m = { scrolls: {}, tlScroll: {}, nodes: {} };
  host.querySelectorAll('.agent-feed, .itree, .timeline-lens, .conv, .conv-sidebar, .tl-sidebar').forEach(el => {
    m.scrolls[scrollKey(el)] = el.scrollTop;
  });
  // Per-session horizontal Gantt offset, so a live update that rebuilds the SVG
  // doesn't reset a user who scrolled into history (syncTimelineScroll then
  // overrides only the sessions still following the live edge).
  host.querySelectorAll('.timeline-scroll[data-tlscroll]').forEach(sc => {
    m.tlScroll[sc.dataset.tlscroll] = sc.scrollLeft;
  });
  // Tree lens session groups default open and the user can collapse them, so
  // snapshot EXACT open-state (must also be re-closed). Agent nodes need no
  // snapshot — openAgentBodies drives their open state and lazy body, so
  // renderInspector already reproduces them on a re-render.
  host.querySelectorAll('details.itree-sess[data-skey]').forEach(d => { m.nodes[`s:${d.dataset.skey}`] = d.open; });
  // Tree anchor: when scrolled into the list, pin a specific row across the
  // re-render so it stays put even if the session order shifts around it. The
  // plain scrollTop restore alone would keep the SAME pixel offset over now-
  // different content → a visible jump.
  if (activeSub === 'tree') {
    const itree = host.querySelector('.itree');
    if (itree && itree.scrollTop > 0) m.treeAnchor = captureTreeAnchor(itree);
  }
  return m;
}

// captureTreeAnchor picks the row to hold steady across a tree re-render and
// records its id plus its current offset from the .itree scroll container's
// top. Prefers the selected row when it's on screen (that's what the user is
// reading); otherwise the topmost row that starts within the viewport. Returns
// null when no stable-id row is visible. Anchor ids are namespaced 'ref:'
// (a step/tool/agent row) or 'skey:' (a session group).
function captureTreeAnchor(itree) {
  const cTop = itree.getBoundingClientRect().top;
  const cBottom = cTop + itree.clientHeight;
  const offsetOf = el => el.getBoundingClientRect().top - cTop;
  const idOf = el => el.dataset.ref ? `ref:${el.dataset.ref}` : (el.dataset.skey ? `skey:${el.dataset.skey}` : null);
  const visible = el => { const t = el.getBoundingClientRect().top; return t < cBottom && el.getBoundingClientRect().bottom > cTop; };

  const sel = itree.querySelector('[data-ref].is-selected, .is-selected[data-ref]');
  if (sel && visible(sel)) {
    const id = idOf(sel);
    if (id) return { id, offset: offsetOf(sel) };
  }
  // Topmost row that begins at/below the container top — the first thing whose
  // top edge the user can see. Session <details> are tall, so we lean on the
  // row elements (data-ref) and fall back to the session summary's data-skey.
  const rows = itree.querySelectorAll('.itree-agent-row[data-ref], .insp-step-head[data-ref], .tr[data-ref], details.itree-sess[data-skey]');
  for (const el of rows) {
    const top = el.getBoundingClientRect().top;
    if (top >= cTop - 1 && top < cBottom) {
      const id = idOf(el);
      if (id) return { id, offset: top - cTop };
    }
  }
  return null;
}

export function restoreState(host, m) {
  host.querySelectorAll('.agent-feed, .itree, .timeline-lens, .conv, .conv-sidebar, .tl-sidebar').forEach(el => {
    const v = m.scrolls[scrollKey(el)];
    if (v != null) el.scrollTop = v;
  });
  host.querySelectorAll('.timeline-scroll[data-tlscroll]').forEach(sc => {
    const v = m.tlScroll[sc.dataset.tlscroll];
    if (v != null) sc.scrollLeft = v;
  });
  if (m.nodes) {
    host.querySelectorAll('details.itree-sess[data-skey]').forEach(d => {
      const v = m.nodes[`s:${d.dataset.skey}`]; if (v != null) d.open = v;
    });
  }
  // Re-pin the anchored row last: the scrollTop restore above lands us roughly
  // right, then we nudge so the anchor sits at exactly its old offset — even if
  // the order (and thus what's above it) changed. Falls through to the plain
  // restore when the row vanished or none was captured.
  if (m.treeAnchor && activeSub === 'tree') {
    const itree = host.querySelector('.itree');
    if (itree) applyTreeAnchor(itree, m.treeAnchor);
  }
}

// applyTreeAnchor adjusts the .itree scrollTop so the anchored row sits at its
// captured offset again. The delta is (current offset − saved offset): a no-op
// when nothing above it moved, a correction when the order shifted.
function applyTreeAnchor(itree, anchor) {
  const sel = anchor.id.startsWith('ref:')
    ? `[data-ref="${cssEsc(anchor.id.slice(4))}"]`
    : `details.itree-sess[data-skey="${cssEsc(anchor.id.slice(5))}"]`;
  const el = itree.querySelector(sel);
  if (!el) return;
  const cTop = itree.getBoundingClientRect().top;
  const newOffset = el.getBoundingClientRect().top - cTop;
  itree.scrollTop += newOffset - anchor.offset;
}

const scrollKey = el => el.className.split(' ').find(c => /feed|itree|timeline-lens|conv/.test(c)) || el.className;

export function clockTime(ms) {
  const d = new Date(ms);
  return isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour12: false });
}

export function shortId(id) {
  const s = String(id || '');
  return s.length > 8 ? s.slice(0, 8) : s;
}

export function shortModel(m) {
  return String(m || '').replace(/^claude-/, '').replace(/-\d{8}$/, '');
}

export function clip(s, n) {
  const str = String(s == null ? '' : s);
  return str.length > n ? str.slice(0, n - 1) + '…' : str;
}
