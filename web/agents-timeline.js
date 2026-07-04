// ── Timeline (Gantt) lens ───────────────────────────────────────────────────
//
// Split out of view-agents.js: the per-session Gantt, its scrubber/playhead,
// scroll-pinning, and wheel zoom. The core wires delegation and calls the
// exported entry points (renderTimeline, syncTimelineScroll, the on* event
// handlers, jumpToNow, currentTimelineSession).

import { fmtMoney, fmtNum, fmtCompact, escHtml } from './format.js';
import {
  flattenSession, agentTokens, agentElapsedMs, formatElapsed, baseName,
  refKey, parseRefKey, resolveRef, sessionStats, timelineSessionList, pickTimelineSid,
  playheadBounds, playheadStats, timelineAtTime, buildTimeline, timelineKinds,
  makeTimeScale, promptBands,
  fitSegmentLabel, costHeat, segKindColor, pctOfAgent, segTooltip,
  criticalSpans, detectSignals, signalPipsByAgent,
  zoomClampPxPerMs, zoomAnchorScrollLeft, TL_MAX_PX_PER_MS,
} from './agents-logic.js';
import {
  lastGraph, selectedRef, timelineSid, playheadT, setPlayheadT,
  tlMinPxPerMs, setTlMinPxPerMs, prevTimelineKeys, setPrevTimelineKeys,
  timelinePinned, jumpFlashRef, filterMatchSet, dimNodes,
  captureState, restoreState, convSidebarWidth,
  colorSlot, originBadgeHTML, isServeMode, kindFamily, cssEsc,
  clockTime, shortId, clip,
} from './agents-shared.js';

let scrubRaf = 0;
let zoomRaf = 0;
// The pending wheel gesture, coalesced into one rAF repaint: the scroll element,
// the cursor's x within its viewport, and the summed deltaY since the last frame.
let zoomPending = null;

// Progressive-disclosure state (Phase-3 item 14). Module-level so it survives
// repaints, scrub frames, and SSE ticks: `rows` holds expanded agent-row keys
// (`sid#ai` — collapsed rows draw one bar, no segments), `turns` holds expanded
// step refKeys (`sid#ai.si` — their tool sub-spans disclose). Fed to
// buildTimeline/timelineAtTime as opts.expanded; local to the Timeline lens
// (no other lens collapses), so it lives here rather than agents-shared.js.
const tlExpanded = { rows: new Set(), turns: new Set() };

// onTimelineDisclose toggles one disclosure key — an agent row key expands/
// collapses that row's turn band; a step refKey expands/collapses that turn's
// tool sub-spans — then repaints only the plotted session at the current
// playhead (same cheap path a scrub frame uses). Collapsing a row keeps its
// turn expansions so re-expanding restores the same view.
export function onTimelineDisclose(container, key) {
  const p = parseRefKey(key);
  if (!p) return;
  const set = p.stepIndex != null ? tlExpanded.turns : tlExpanded.rows;
  if (set.has(key)) set.delete(key); else set.add(key);
  const nowMs = Date.now();
  const chosen = currentTimelineSession();
  const bounds = chosen ? playheadBounds({ sessions: [chosen.session] }, nowMs) : null;
  repaintTimelineSessions(container, playheadAt(bounds, nowMs));
}

// ensureDisclosedFor force-expands whatever encloses `ref` (its agent row, and
// its turn when the ref is tool-deep) so a cross-lens jump — a Signals row, a
// retry link — always lands on a RENDERED segment even though rows collapse by
// default. Called with the pending jump's ref at render time.
function ensureDisclosedFor(ref) {
  const p = parseRefKey(ref);
  if (!p || p.stepIndex == null) return;
  tlExpanded.rows.add(`${p.sessionId}#${p.agentIndex}`);
  if (p.toolIndex != null) tlExpanded.turns.add(`${p.sessionId}#${p.agentIndex}.${p.stepIndex}`);
}

// renderTimeline draws the Gantt lens: a scrubber bar on top, then one
// horizontal Gantt per session (a real time axis, one row per agent, bar =
// lifetime, overlap = concurrency). Everything is rendered "as of" the playhead
// instant T (live → now); geometry comes from the pure, unit-tested
// timelineAtTime. The scrubber is a separate sibling from the .timeline-sessions
// container so a scrub re-renders ONLY the sessions, leaving the range input
// (mid-drag) untouched.
export function renderTimeline(sessions) {
  const list = timelineSessionList(sessions);
  if (list.length === 0) return `<div class="ac-idle">No agents to plot.</div>`;
  const chosen = currentTimelineSession();
  // chosen is always non-null here (list is non-empty ⇒ pickTimelineSid resolves
  // a sid); guard anyway so a future caller can't NPE.
  const view = chosen ? { sessions: [chosen.session] } : { sessions: [] };
  const curSid = chosen ? chosen.session.session_id || '' : '';
  const nowMs = Date.now();
  // Scrubber window + counts are scoped to the ONE plotted session, not the
  // whole graph — the playhead spans just this trace, and ▶/✓/○ count only its
  // agents. This is also what removes the all-sessions repaint per scrub frame.
  const bounds = playheadBounds(view, nowMs);
  const live = playheadT == null;
  const T = playheadAt(bounds, nowMs);
  // The kind legend names the colors the Gantt actually draws for THIS session.
  const kinds = chosen ? timelineKinds(chosen.session) : [];
  const scrubber = bounds ? scrubberHTML(bounds, T, live, playheadStats(view, T), kinds) : '';
  const w = convSidebarWidth();
  return `<div class="timeline-layout">
    <div class="tl-sidebar" style="width:${w}px" role="tablist" aria-label="Sessions">${timelineSidebarHTML(list, curSid)}</div>
    <div class="tl-resize" data-tl-resize role="separator" aria-orientation="vertical" aria-label="Resize the session list"></div>
    <div class="timeline-lens">${scrubber}<div class="timeline-sessions">${renderTimelineSessions(view.sessions, nowMs, T, chosen ? chosen.index : 0)}</div></div>
  </div>`;
}

// currentTimelineSession resolves the single session the Timeline plots: its
// sid via pickTimelineSid (explicit pick → selected turn's session → first),
// then the matching session object plus its ORIGINAL index (the stable color
// slot). Returns null when nothing is plottable. Shared by renderTimeline and
// renderScrub so both render and scope to the SAME session.
export function currentTimelineSession() {
  const sessions = (lastGraph && lastGraph.sessions) || [];
  const sid = pickTimelineSid(sessions, timelineSid, selectedRef);
  if (!sid) return null;
  const index = sessions.findIndex(s => s && (s.session_id || '') === sid);
  if (index < 0) return null;
  return { session: sessions[index], index };
}

// timelineSidebarHTML renders the left session list: one selectable row per
// plottable session, the in-lens way to switch which Gantt is plotted. The
// active session is highlighted; each row carries data-tl-sess so a click
// re-points timelineSid at it. Rows lead with project/shortId and a few
// sessionStats pills (⏱ duration · ⚠ errors · 💰 cost) so the list triages.
function timelineSidebarHTML(list, curSid) {
  return list.map(e => {
    const sel = e.sessionId === curSid ? ' is-selected' : '';
    const c = colorSlot(e.index);
    const err = e.errorCount > 0
      ? `<span class="tl-sess-item-pill tl-sess-item-err" title="${fmtNum(e.errorCount)} tool error${e.errorCount === 1 ? '' : 's'}">⚠️ ${fmtNum(e.errorCount)}</span>`
      : '';
    return `<button type="button" class="tl-sess-item${sel}" role="tab" aria-selected="${e.sessionId === curSid}" data-tl-sess="${escHtml(e.sessionId)}" data-c="${c}">
      <span class="tl-sess-item-head">
        <span class="tl-sess-item-proj" title="${escHtml(e.cwd || '')}">${escHtml(baseName(e.cwd) || '—')}</span>
        ${originBadgeHTML(e)}
      </span>
      <span class="tl-sess-item-meta">
        <span class="tl-sess-item-sid">${escHtml(shortId(e.sessionId))}</span>
        <span class="tl-sess-item-pills">
          <span class="tl-sess-item-pill" title="session duration">⏱️ ${escHtml(formatElapsed(e.durationMs))}</span>
          ${err}
          <span class="tl-sess-item-pill" title="session cost">💰 ${escHtml(fmtMoney(e.cost_usd))}</span>
        </span>
      </span>
    </button>`;
  }).join('');
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
// Only the chosen session is plotted, so `colorIdx` is its ORIGINAL list index
// (the stable color slot) — passed through to the session-head data-c so the
// card keeps the same hue it had when every session was stacked.
function renderTimelineSessions(sessions, nowMs, T, colorIdx = 0) {
  const hostW = timelineHostW();
  const sel = resolveRef(lastGraph, selectedRef);
  const selAgentKey = sel ? refKey({ sessionId: sel.session.session_id, agentIndex: sel.agentIndex }) : null;
  const seen = prevTimelineKeys;
  const next = new Set();
  const html = sessions.map(s => timelineSessionHTML(s, colorIdx, hostW, nowMs, T, selAgentKey, seen, next)).join('');
  setPrevTimelineKeys(next);
  return html;
}

// scrubberHTML is the sticky control strip: a "● live" toggle, a range input
// spanning the whole trace window, and a clock + active/done/pending readout —
// all "as of" the playhead T.
function scrubberHTML(bounds, T, live, stats, kinds = []) {
  const span = bounds.endMs - bounds.startMs;
  const val = Math.max(0, Math.min(span, T - bounds.startMs));
  return `<div class="tl-scrubber">
    <button type="button" class="tl-live${live ? ' is-live' : ''}" data-tllive title="Resume live — the playhead follows now">● live</button>
    <input class="tl-range" type="range" min="0" max="${span}" step="any" value="${val}"
      data-tlrange data-tlstart="${bounds.startMs}" aria-label="Scrub the timeline to a point in time"/>
    <span class="tl-clock" data-tlclock>${escHtml(clockTime(T))}</span>
    <span class="tl-counts" data-tlcounts>${countsHTML(stats)}</span>
    ${kindLegendHTML(kinds)}
  </div>`;
}

// kindLegendHTML renders the segment-color key for the Timeline: one swatch +
// label per kind the plotted session draws, in canonical order (from
// timelineKinds), reusing the .kind-* palette (the swatch reads var(--kc)). Empty
// string when the session has no segments — no legend, no clutter.
function kindLegendHTML(kinds) {
  if (!kinds || kinds.length === 0) return '';
  const items = kinds.map(k =>
    `<span class="tl-leg-item kind-${kindFamily(k)}" title="${escHtml(k)} segments"><span class="tl-leg-sw" aria-hidden="true"></span>${escHtml(k)}</span>`).join('');
  return `<span class="tl-legend" aria-label="Segment colors by tool kind">${items}</span>`;
}

function countsHTML(s) {
  return `<span class="tlc tlc-active" title="agents active at the playhead">▶ ${fmtNum(s.active)}</span>` +
    `<span class="tlc tlc-done" title="agents finished by the playhead">✓ ${fmtNum(s.done)}</span>` +
    `<span class="tlc tlc-pending" title="agents not yet started at the playhead">○ ${fmtNum(s.pending)}</span>`;
}

// timelineSummaryHTML is the at-a-glance triage strip for a session card: its
// duration, turns, tools, agents, errors, and cost as .tlc pills (mirroring
// countsHTML's idiom). These are whole-session totals, NOT playhead-relative, so
// every scrub frame recomputes the same values — but it must live in the shared
// timelineSessionHTML anyway, because renderScrub rewrites .timeline-sessions
// wholesale and a strip kept out of that path would vanish mid-scrub. Stable
// values → no flicker; the sessionStats walk is the same O(steps) order as the
// timelineAtTime geometry the scrub already runs per session. The errors pill is
// omitted when the session is clean.
function timelineSummaryHTML(s) {
  // Each pill leads with an emoji (the strip is HTML, so emoji render fine) and
  // keeps the full meaning in its title tooltip. The errors pill is omitted when
  // the session is clean.
  const err = s.errorCount > 0
    ? `<span class="tlc tlc-err" title="${fmtNum(s.errorCount)} tool error${s.errorCount === 1 ? '' : 's'} in this session">⚠️ ${fmtNum(s.errorCount)}</span>`
    : '';
  return `<span class="tlc" title="session duration">⏱️ ${escHtml(formatElapsed(s.durationMs))}</span>` +
    `<span class="tlc" title="turns (model steps) across all agents">💬 ${fmtNum(s.turnCount)}</span>` +
    `<span class="tlc" title="tool calls across all agents">🛠️ ${fmtNum(s.toolCount)}</span>` +
    `<span class="tlc" title="agents (main + sub-agents)">🤖 ${fmtNum(s.agentCount)}</span>` +
    err +
    `<span class="tlc" title="total tokens (input + output + cache) across all agents">🎟️ ${escHtml(fmtCompact(s.tokenCount))}</span>` +
    `<span class="tlc tlc-cost" title="session cost">💰 ${escHtml(fmtMoney(s.cost_usd))}</span>`;
}

// Timeline signal pips (Phase 3d): the per-agent anomaly markers drawn in the
// Gantt gutter come from the SAME detectSignals output the Insights → Signals
// panel uses, scoped to the ONE plotted session. detectSignals is pure but not
// free, and renderScrub repaints the sessions every rAF frame of a scrub drag, so
// the result is memoized by session-object identity — a data reload swaps the
// session reference and invalidates it; scrubbing the same session reuses it. nowMs
// keeps the static-safe contract (serve → live clock, static → undefined), the same
// as the Signals panel; pip presence doesn't depend on the playhead T (it's gated
// per row by phase, not by signal data), so a stale nowMs across scrub frames is
// harmless.
const TL_SIG_CAP = 3;
// Row geometry overrides for the single-session Gantt (buildTimeline defaults are
// 24 / 130, tuned for the old multi-session stack). TL_ROW_H drives bar height
// (barH = rowH - 10); TL_LABEL_W is the frozen label column / gutter width, and
// TL_LABEL_CHARS is the matching char budget for the in-SVG label (no native
// ellipsis in <text>, so we clip in JS — keep it in step with TL_LABEL_W).
const TL_ROW_H = 34;
const TL_LABEL_W = 190;
const TL_LABEL_CHARS = 24;
// Taller axis strip when prompt bands are drawn: the top half carries each
// band's prompt snippet, the bottom half the usual tick labels.
const TL_AXIS_PROMPT_H = 38;
// Prompt-band label budget: ~60 chars max, shrunk to what the band width fits
// (≈6px/char at the 9px mono size, 8px padding).
const TL_PBAND_CHARS = 60;
let tlSigCache = null; // { session, byAgent }
function timelineSignalPips(session, sid) {
  if (tlSigCache && tlSigCache.session === session) return tlSigCache.byAgent;
  const nowMs = isServeMode() ? Date.now() : undefined;
  const byAgent = signalPipsByAgent(detectSignals({ sessions: [session] }, { nowMs }), sid, TL_SIG_CAP);
  tlSigCache = { session, byAgent };
  return byAgent;
}

function timelineSessionHTML(session, si, hostW, nowMs, T, selAgentKey, seen, next) {
  const sid = session.session_id || '';
  // A cross-lens jump must land on a rendered node: expand the jump target's
  // row/turn BEFORE building the layout (rows collapse by default).
  if (jumpFlashRef) ensureDisclosedFor(jumpFlashRef);
  // With prompt bands the axis strip doubles as the prompt-label rail: band
  // labels ride the top half, tick labels keep their usual baseline slot.
  const hasPrompts = Array.isArray(session.prompts) && session.prompts.length > 0;
  const axisH = hasPrompts ? TL_AXIS_PROMPT_H : 20;
  // tlMinPxPerMs (null ⇒ default) is the scroll-wheel zoom density; buildTimeline
  // still floors the chart to the host width, so a stale value below fit-to-width
  // simply renders as the overview.
  // We show one session at a time, so there's vertical room: taller rows (TL_ROW_H)
  // give the bars real presence, and a wider label column (TL_LABEL_W) stops agent
  // names like "Djinn review-leak-detector" from clipping to "Djinn review-le…".
  const tl = timelineAtTime(session, T, {
    hostW, nowMs, minPxPerMs: tlMinPxPerMs ?? undefined,
    rowH: TL_ROW_H, labelW: TL_LABEL_W, axisH,
    expanded: tlExpanded,
  });
  // The rows start at y = axisH (buildTimeline), so the first row's top is the
  // axis baseline — tick labels sit above it, gridlines run from it down.
  const axisY = tl.rows.length ? tl.rows[0].y : axisH;

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
  // The cost-heat ramp normalizes per session: the priciest turn anywhere in
  // this card is the hottest, so a click-target turn "stands out" relative to
  // its own trace rather than to some other session's spend.
  let maxSegCost = 0;
  for (const r of tl.rows) for (const s of (r.segments || [])) {
    if (s.cost_usd > maxSegCost) maxSegCost = s.cost_usd;
  }
  // Critical-path marks: the longest span and cost-whale turn, per session and
  // per agent. The session pair gets the loud ▲; the per-agent pair a subtle
  // outline. crit.refs maps a segment refKey → which mark(s) to draw on it.
  const crit = criticalMarks(tl.rows);
  // Per-agent anomaly pips for this session's gutter (Phase 3d), memoized so a
  // scrub drag's per-frame repaint doesn't re-run detectSignals.
  const sigPips = timelineSignalPips(session, sid);
  const parts = tl.rows.map(r => timelineRowHTML(r, tl, selAgentKey, seen, next, agents[r.rowIndex], maxSegCost, crit, sigPips.get(r.rowIndex)));
  const gutterRows = parts.map(p => p.gutter).join('');
  const chartRows = parts.map(p => p.chart).join('');

  // Prompt bands (Phase-3 item 14a): one labeled full-height band per user
  // prompt, spanning that prompt's turns — always visible, whatever the
  // rows' disclosure state. Drawn behind the grid/rows.
  const bandsHTML = hasPrompts && !Number.isNaN(tl.startMs)
    ? promptBandsHTML(session, tl) : '';

  // Spawn connectors (14b): an elbow from the parent row's bar lane down to
  // each re-parented sub-agent row at the child's start x — the visual edge of
  // the spawn tree. Behind the rows so bars/segments stay clickable.
  const rowByAgent = new Map(tl.rows.map(r => [r.rowIndex, r]));
  const elbows = tl.rows.map(r => {
    if (!r.spawn || r.phase === 'pending') return '';
    const p = rowByAgent.get(r.spawn.agentIndex);
    if (!p) return '';
    const laneH = r.laneH || r.h;
    const pLaneH = p.laneH || p.h;
    const x = r.x.toFixed(1);
    const fromY = (p.y + pLaneH - 4).toFixed(1);
    const toY = (r.y + laneH / 2).toFixed(1);
    return `<path class="tl-elbow" d="M ${x} ${fromY} L ${x} ${toY} L ${(r.x + 5).toFixed(1)} ${toY}"/>`;
  }).join('');

  return `<div class="timeline-sess">
    <div class="timeline-sess-head" data-c="${colorSlot(si)}" title="${escHtml(session.cwd || '')}">
      <span class="timeline-sess-proj">${escHtml(baseName(session.cwd) || '—')}</span>
      <span class="timeline-sess-sid" title="${escHtml(sid)}">${escHtml(shortId(sid))}</span>
      <button type="button" class="timeline-jump" data-tljump="${escHtml(sid)}" hidden>● now</button>
    </div>
    <div class="timeline-sess-sum">${timelineSummaryHTML(sessionStats(session, nowMs))}</div>
    <div class="timeline-body">
      <svg class="timeline-gutter" viewBox="0 0 ${gutterW} ${tl.height}" width="${gutterW}" height="${tl.height}" role="img" aria-label="Agent labels">
        <g class="tl-rows">${gutterRows}</g>
      </svg>
      <div class="timeline-scroll" data-tlscroll="${escHtml(sid)}">
        <svg class="timeline-svg" viewBox="${tl.chartX} 0 ${chartW} ${tl.height}" width="${chartW}" height="${tl.height}" role="img" aria-label="Agent timeline">
          ${bandsHTML}
          <g class="tl-grid">${ticks}</g>
          <g class="tl-elbows">${elbows}</g>
          <g class="tl-rows">${chartRows}</g>
          ${playhead}
        </svg>
      </div>
    </div>
  </div>`;
}

// promptBandsHTML renders the prompt-segmentation layer: for each PromptMarker
// a subtle full-height band (alternating tint), a dashed boundary line at its
// first turn, and the prompt snippet as a label in the top axis strip —
// truncated to what the band width fits (never bleeding into the next band),
// with the fuller text in a hover <title>. An orphan marker (uuid '') labels
// as (no prompt). Everything escHtml'd.
function promptBandsHTML(session, tl) {
  const scale = makeTimeScale({ startMs: tl.startMs, endMs: tl.endMs, width: tl.chartW, minBlock: 3 });
  const bands = promptBands(session, { scale, chartX: tl.chartX });
  if (bands.length === 0) return '';
  const inner = bands.map((b, i) => {
    const text = b.text || '(no prompt)';
    const fitChars = Math.floor((b.w - 8) / 6);
    const label = fitChars >= 4 ? clip(text, Math.min(TL_PBAND_CHARS, fitChars)) : '';
    const tip = `Prompt ${i + 1}: ${clip(text, 400)}`;
    const labelEl = label
      ? `<text class="tl-pband-label" x="${(b.x + 4).toFixed(1)}" y="12" pointer-events="none">${escHtml(label)}</text>`
      : '';
    return `<g class="tl-pband${i % 2 ? ' is-alt' : ''}">
      <rect x="${b.x.toFixed(1)}" y="0" width="${b.w.toFixed(1)}" height="${tl.height}"><title>${escHtml(tip)}</title></rect>
      <line x1="${b.x.toFixed(1)}" y1="0" x2="${b.x.toFixed(1)}" y2="${tl.height}"/>
      ${labelEl}
    </g>`;
  }).join('');
  return `<g class="tl-pbands" aria-label="Prompt segments">${inner}</g>`;
}

// timelineRowHTML splits one agent row into two synchronized <g>s: a `gutter`
// piece (frozen label column) and a `chart` piece (the scrolling bar). Both
// carry the same data-ref so a click in either selects the row, and both get the
// is-selected / is-new / tl-pending classes so state shows on both sides of the
// freeze line. A row's phase comes from the playhead: 'active' draws the pulse
// at the (clamped) bar end, 'pending' ghosts the label (the bar is zero-width).
// criticalMarks folds criticalSpans' refKey lists into a refKey→{longest, whale,
// session} map the row renderer queries per segment. Session-level marks (the
// loud ▲) and agent-level marks (a subtle outline) share the map; `session` is
// set only for the across-session standouts. A segment that is both the longest
// span and the cost whale carries both flags.
function criticalMarks(rows) {
  const c = criticalSpans(rows);
  const refs = new Map();
  const flag = (ref, key, isSession) => {
    if (!ref) return;
    const m = refs.get(ref) || { longest: false, whale: false, session: false };
    m[key] = true;
    if (isSession) m.session = true;
    refs.set(ref, m);
  };
  for (const e of Object.values(c.agents)) {
    flag(e.longestRef, 'longest', false);
    flag(e.whaleRef, 'whale', false);
  }
  flag(c.session.longestRef, 'longest', true);
  flag(c.session.whaleRef, 'whale', true);
  return refs;
}

function timelineRowHTML(r, tl, selAgentKey, seen, next, agent, maxSegCost = 0, crit = new Map(), sig = null) {
  next.add(r.key);
  const isNew = !seen.has(r.key);
  const sel = r.key === selAgentKey ? ' is-selected' : '';
  const pending = r.phase === 'pending' ? ' tl-pending' : '';
  // laneH is the per-band height: a collapsed row is one lane (h === laneH), an
  // expanded row stacks a second lane (the turn/tool band at segY) beneath the
  // bar lane, so bar geometry always centers within the TOP lane.
  const laneH = r.laneH || r.h;
  const barH = Math.max(6, laneH - 10);
  const barY = r.y + Math.round((laneH - barH) / 2);
  // Segments live in the disclosure band (expanded rows only); same inset as
  // the bar so turn spans read as a sibling lane.
  const segRectY = r.expanded && r.segY != null ? r.segY + Math.round((laneH - barH) / 2) : barY;
  const cx = (r.x + r.w).toFixed(1);
  const cy = (barY + barH / 2).toFixed(1);
  // A red pip flags an agent that hit ≥1 tool error — but not while pending (it
  // hasn't run at the playhead T yet, so it can't have errored). It lives in the
  // frozen gutter (right edge of the label column), not on the bar: a live Gantt
  // auto-scrolls to "now", which would carry a bar-start pip off-screen.
  const errs = (agent && agent.error_count) || 0;
  const errPip = errs > 0 && r.phase !== 'pending'
    ? `<circle class="tl-err-pip" cx="${(tl.chartX - 7).toFixed(1)}" cy="${(r.y + laneH / 2).toFixed(1)}" r="3.5"><title>${errs} ${errs === 1 ? 'error' : 'errors'}</title></circle>`
    : '';
  // Signal pips (Phase 3d): one small ▲ per detected anomaly on this agent, tier-
  // colored, in the frozen gutter just left of the err-pip (same off-screen-proof
  // rationale). They share the err-pip's pending gate — a not-yet-started row can't
  // have anomalies at T. Each pip carries data-ref = the signal's exact refKey
  // (agent/step/tool), so a click routes through the shared data-ref delegate and
  // lands selection on the anomaly — no bespoke handler. Worst-first (the list is
  // severity-ordered), capped at TL_SIG_CAP with a +N overflow glyph; the full list
  // lives in Insights → Signals.
  const sigPips = (sig && r.phase !== 'pending') ? sig.pips : [];
  const sigOverflow = (sig && r.phase !== 'pending') ? sig.overflow : 0;
  const pipCy = r.y + laneH / 2;
  // Reserve the err-pip's slot (chartX-7) so pips never collide with it; step left.
  const pipBaseX = tl.chartX - 7 - (errs > 0 && r.phase !== 'pending' ? 9 : 0);
  const sigHTML = sigPips.map((p, i) => {
    const px = pipBaseX - i * 9;
    const d = `M ${px.toFixed(1)} ${(pipCy - 4).toFixed(1)} L ${(px - 4).toFixed(1)} ${(pipCy + 3.5).toFixed(1)} L ${(px + 4).toFixed(1)} ${(pipCy + 3.5).toFixed(1)} Z`;
    return `<path class="tl-sig-pip sig-${p.tier}" data-ref="${escHtml(p.ref)}" d="${d}"><title>${escHtml(p.summary)}</title></path>`;
  }).join('');
  const moreHTML = sigOverflow > 0
    ? `<text class="tl-sig-more" data-goto-insights role="button" tabindex="0" x="${(pipBaseX - sigPips.length * 9).toFixed(1)}" y="${(pipCy + 3).toFixed(1)}" text-anchor="end">+${sigOverflow}<title>${sigOverflow} more signal${sigOverflow === 1 ? '' : 's'} — open Insights → Signals</title></text>`
    : '';
  // Segments tile the bar on the time axis at the finest grain the data allows:
  // one sub-span per TOOL where the backend captured tool wall-clock (toolIndex
  // set, data-ref = that tool → a click opens the tool in the drawer), else one
  // per TURN (data-ref = the step). The bar/rowbg under them have no data-ref and
  // fall through to selecting the whole agent. When a row has segments the
  // lifetime bar drops to a faint rail behind them (.has-segs); a row with no
  // timed steps (older data, or a run before its first turn) keeps the solid bar
  // so it never looks empty.
  // Each turn segment encodes time as width (Phase 1), now also cost as fill
  // intensity (a per-session heat ramp) and — on wide-enough segments — its
  // duration (and cost, when there's room) as an inline label. A segment that
  // hit a tool error stays red and skips the heat var so error always wins the
  // fill; the label still rides on top. Narrow segments carry no label and read
  // by width + tint + tooltip alone, so the row never crowds.
  // Segments now live in their own disclosure band beneath the bar lane, so the
  // bar no longer fades behind them (the old .has-segs rail treatment).
  const segs = r.segments || [];
  const segLabelY = (segRectY + barH / 2 + 3).toFixed(1);
  const steps = (agent && agent.steps) || [];
  // The segment's share of its agent's whole runtime, for the "(xx% of agent)"
  // clause — a fixed denominator so every segment in a row reads against the same
  // total. Done agents report end−start; a running one counts to now.
  const agentDurMs = agentElapsedMs(agent, Date.now());
  // Idle/wait gaps (Phase 1b): a hatched span at each turn's trailing edge for
  // the dead air between its last tool and the next turn, drawn BEHIND the tool
  // segments so "where did the time go" reads at a glance. Click selects the
  // turn. Honest-only — pre-0a turns (no gen_ms) emit none.
  const idleHTML = (r.idleSegments || []).map(s => {
    const idur = formatElapsed(s.durationMs);
    const ipct = pctOfAgent(s.durationMs, agentDurMs);
    const itip = segTooltip({ head: 'idle / wait', durText: idur, pct: ipct });
    const isel = s.refKey === selectedRef ? ' is-selected' : '';
    return `<rect class="tl-idle${isel}" data-ref="${escHtml(s.refKey)}" x="${s.x.toFixed(1)}" y="${segRectY}" width="${s.w.toFixed(1)}" height="${barH}" rx="2"><title>${escHtml(itip)}</title></rect>`;
  }).join('');
  // Critical-path overlays (Phase 1c): a non-filled outline over the longest span
  // / cost-whale turn so it never fights the segment's own fill or state stroke.
  // Session standouts also get a ▲ above the bar (rare → loud); per-agent marks
  // are the outline alone. pointer-events:none so the click still hits the rect.
  const critHTML = segs.map(s => {
    const m = crit.get(s.refKey);
    if (!m) return '';
    const cls = `tl-crit${m.longest ? ' is-long' : ''}${m.whale ? ' is-whale' : ''}${m.session ? ' is-session' : ''}`;
    const outline = `<rect class="${cls}" x="${s.x.toFixed(1)}" y="${segRectY}" width="${s.w.toFixed(1)}" height="${barH}" rx="2" pointer-events="none"/>`;
    if (!m.session) return outline;
    const what = [m.longest ? 'longest span' : '', m.whale ? 'cost whale' : ''].filter(Boolean).join(' · ');
    const mark = `<text class="tl-crit-mark${m.whale ? ' is-whale' : ''}" x="${(s.x + s.w / 2).toFixed(1)}" y="${(segRectY - 2).toFixed(1)}" text-anchor="middle" pointer-events="none">▲<title>session ${escHtml(what)}</title></text>`;
    return outline + mark;
  }).join('');
  const segHTML = segs.map(s => {
    const ssel = s.refKey === selectedRef ? ' is-selected' : '';
    const isErr = s.status === 'error';
    const serr = isErr ? ' is-error' : '';
    const durText = formatElapsed(s.durationMs);
    const pct = pctOfAgent(s.durationMs, agentDurMs);
    const step = steps[s.stepIndex] || {};
    const tokens = agentTokens(step).total;
    const tokensText = tokens ? `${fmtCompact(tokens)} tok` : '';
    // Hue comes from the tool kind (or 'step' for a turn segment), reusing the
    // same .kind-* palette as the Feed/Tree badges; cost-heat (--seg-heat) stays
    // a secondary saturation channel and an error overrides the fill to red.
    const kindCls = ` kind-${segKindColor(s.kind)}`;
    // A tool sub-span (toolIndex set) leads with the tool name·detail and shows
    // duration·tokens only — tools aren't individually priced (the heat still
    // reflects the parent turn's cost), so a per-tool cost would mislead. A turn
    // segment leads with "Turn N" and carries cost too. Both gain the %-of-agent
    // and token context — richer hover, while the in-bar label stays minimal.
    const isTool = s.toolIndex != null;
    let tip, costText;
    if (isTool) {
      const tool = (step.tools || [])[s.toolIndex] || {};
      const name = tool.name || 'tool';
      const detail = tool.detail ? ` · ${tool.detail}` : '';
      tip = segTooltip({ head: `${name}${detail}`, durText, pct, tokensText, isError: isErr });
      costText = '';
    } else {
      costText = s.cost_usd ? fmtMoney(s.cost_usd) : '';
      tip = segTooltip({ head: `Turn ${s.stepIndex + 1}`, durText, pct, costText, tokensText, isError: isErr });
    }
    const heat = isErr ? '' : ` style="--seg-heat:${costHeat(s.cost_usd, maxSegCost).toFixed(3)}"`;
    const sjump = s.refKey === jumpFlashRef ? ' is-jumped' : '';
    // Two-level disclosure: a TURN span is the toggle target for its own tool
    // sub-spans (data-tlturn → onTimelineDisclose, which also selects it via
    // data-ref as before); tool sub-spans render inset on top of their turn
    // span so both stay hoverable/clickable.
    const segY2 = isTool ? segRectY + 3 : segRectY;
    const segH2 = isTool ? Math.max(4, barH - 6) : barH;
    const isOpen = !isTool && tlExpanded.turns.has(s.refKey);
    const turnAttr = isTool ? '' : ` data-tlturn="${escHtml(s.refKey)}"`;
    const turnCls = isTool ? ' is-tool' : (isOpen ? ' tl-turn is-open' : ' tl-turn');
    const rect = `<rect class="tl-seg${kindCls}${serr}${ssel}${sjump}${turnCls}" data-ref="${escHtml(s.refKey)}"${turnAttr}${heat} x="${s.x.toFixed(1)}" y="${segY2}" width="${s.w.toFixed(1)}" height="${segH2}" rx="2"><title>${escHtml(isTool ? tip : `${tip} — click to ${isOpen ? 'collapse' : 'expand'} tools`)}</title></rect>`;
    // Inline label: the richest of "dur · cost" / "dur" that fits the segment
    // width; '' when too narrow. pointer-events:none keeps the click on the rect.
    const text = (isTool || isOpen) ? '' : fitSegmentLabel(s.w, durText, costText);
    const label = text
      ? `<text class="tl-seg-label" x="${(s.x + s.w / 2).toFixed(1)}" y="${segLabelY}">${escHtml(text)}</text>`
      : '';
    return rect + label;
  }).join('');
  const rjump = r.key === jumpFlashRef ? ' is-jumped' : '';
  const cls = `tl-row ${r.kind === 'main' ? 'tl-main' : 'tl-sub'}${r.running ? ' is-running' : ''}${sel}${isNew ? ' is-new' : ''}${pending}${rjump}${r.expanded ? ' is-expanded' : ''}`;
  const meta = r.phase === 'pending' ? 'not started yet'
    : r.running ? 'running' : `${fmtNum(r.steps)} · ${fmtMoney(r.cost_usd || 0)}`;
  const labelY = (r.y + laneH / 2 + 4).toFixed(1);
  // Disclosure caret (14c): rows with turns expand to their per-turn band. It
  // sits before the label in the gutter; data-tltoggle routes the click to
  // onTimelineDisclose instead of the data-ref selection delegate.
  const canDisclose = r.steps > 0;
  const disclose = canDisclose
    ? `<text class="tl-disclose" data-tltoggle="${escHtml(r.key)}" role="button" tabindex="0" x="${r.labelX}" y="${labelY}" aria-expanded="${r.expanded ? 'true' : 'false'}">${r.expanded ? '▾' : '▸'}<title>${r.expanded ? 'Collapse' : 'Expand'} ${escHtml(r.label)} to its turns</title></text>`
    : '';
  const labelX = r.labelX + (canDisclose ? 12 : 0);
  const gutter = `<g class="${cls}" data-ref="${escHtml(r.key)}">
    <rect class="tl-rowbg" x="0" y="${r.y}" width="${tl.chartX}" height="${r.h}"/>
    ${disclose}
    <text class="tl-label" x="${labelX}" y="${labelY}">${escHtml(clip(r.label, TL_LABEL_CHARS))}</text>
    ${errPip}
    ${sigHTML}
    ${moreHTML}
    <title>${escHtml(r.label)} — ${escHtml(meta)}</title>
  </g>`;
  const chart = `<g class="${cls}" data-ref="${escHtml(r.key)}" tabindex="0" role="button">
    <rect class="tl-rowbg" x="${tl.chartX}" y="${r.y}" width="${tl.contentW - tl.chartX}" height="${r.h}"/>
    <rect class="tl-bar" x="${r.x.toFixed(1)}" y="${barY}" width="${r.w.toFixed(1)}" height="${barH}" rx="3">
      <title>${escHtml(r.label)} — ${escHtml(meta)}</title>
    </rect>
    ${idleHTML}
    ${segHTML}
    ${critHTML}
    ${r.running ? `<circle class="tl-pulse" cx="${cx}" cy="${cy}" r="3.5"/>` : ''}
  </g>`;
  return { gutter, chart };
}

function timelineHostW() {
  const host = document.querySelector('#view-agents .subview[data-subview="timeline"]');
  const w = host ? host.clientWidth : 0;
  // The Gantt now occupies only the pane to the right of the session sidebar, so
  // discount the sidebar + drag handle (6px) from the available width — else the
  // chart floors too wide and overflows the pane. Leave room for the session
  // card's padding/border; clamp so the fit-width floor stays readable.
  const rail = convSidebarWidth() + 6;
  return Math.max(420, (w > 0 ? w : 760) - rail - 28);
}

// syncTimelineScroll applies the live-edge follow after a (re)render: a session
// with a running agent and horizontal overflow auto-scrolls to "now" UNLESS the
// user pinned it by scrolling into history, in which case the "● now" button is
// revealed instead. Runs on real DOM (scroll offsets need layout).
export function syncTimelineScroll(container) {
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
export function onTimelineScroll(e) {
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
export function jumpToNow(container, sid) {
  const sc = container.querySelector(`.timeline-scroll[data-tlscroll="${sid}"]`);
  if (!sc) return;
  timelinePinned.set(sid, false);
  sc.scrollLeft = sc.scrollWidth;
  const sess = sc.closest('.timeline-sess');
  const jump = sess && sess.querySelector('[data-tljump]');
  if (jump) jump.hidden = true;
}

// ── playhead scrubber ─────────────────────────────────────────────────────

// onScrub handles a drag of the range input: it parks the playhead at the picked
// absolute instant (T = window start + slider value), drops live mode, and
// schedules a single rAF repaint so a fast drag coalesces to one frame.
export function onScrub(container, range) {
  const start = Number(range.dataset.tlstart);
  setPlayheadT(start + Number(range.value));
  const liveBtn = container.querySelector('.tl-live');
  if (liveBtn) liveBtn.classList.remove('is-live');
  if (scrubRaf) return;
  scrubRaf = requestAnimationFrame(() => { scrubRaf = 0; renderScrub(container); });
}

// repaintTimelineSessions rebuilds ONLY the .timeline-sessions inner HTML for a
// given playhead T and returns the single-session view it plotted. It's the
// shared single-session repaint behind both scrubbing and zooming (was inlined
// in renderScrub): resolve the SAME session renderTimeline plotted and repaint
// ONLY it (not every session, every frame — the scrub-perf win), preserving each
// Gantt's horizontal scroll and re-applying the cached filter dimming. It leaves
// the scrubber's range input alone so an in-progress drag isn't interrupted.
function repaintTimelineSessions(container, T) {
  const chosen = currentTimelineSession();
  const view = chosen ? { sessions: [chosen.session] } : { sessions: [] };
  const lensHost = container.querySelector('.subview[data-subview="timeline"]');
  const host = container.querySelector('.timeline-sessions');
  if (host && lensHost) {
    const memo = captureState(lensHost);
    host.innerHTML = renderTimelineSessions(view.sessions, Date.now(), T, chosen ? chosen.index : 0);
    restoreState(lensHost, memo);
    // The repaint replaced the row/segment nodes with fresh, undimmed ones; re-
    // apply the cached filter dimming (class-only, no recompute) so it survives.
    // is-filtering lives on the parent .agents-lens, untouched here.
    if (filterMatchSet) dimNodes(host, filterMatchSet);
  }
  return view;
}

// renderScrub repaints ONLY the session Gantts + the clock/counts readout for
// the current playhead T — preserving each Gantt's horizontal scroll so seeking
// through time never yanks the viewport sideways.
function renderScrub(container) {
  const T = playheadT;
  const view = repaintTimelineSessions(container, T);
  const clock = container.querySelector('[data-tlclock]');
  if (clock) clock.textContent = clockTime(T);
  const counts = container.querySelector('[data-tlcounts]');
  if (counts) counts.innerHTML = countsHTML(playheadStats(view, T));
}

// ── timeline zoom (scroll-wheel over the Gantt) ────────────────────────────

// onTimelineWheel turns a scroll-wheel over the Gantt into a TIME-AXIS zoom.
// Shift+wheel and horizontal-dominant (trackpad) gestures fall through to the
// browser's native horizontal scroll — that's PAN, which the chart already does.
// A plain vertical wheel is captured (preventDefault stops the page scrolling
// under it); deltas accumulate and a single rAF applies the zoom so a fast
// scroll coalesces to one repaint. No-ops gracefully off a Gantt (e.g. the
// static report, which has no .timeline-scroll).
export function onTimelineWheel(container, e) {
  const sc = e.target.closest && e.target.closest('.timeline-scroll[data-tlscroll]');
  if (!sc) return;
  if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return; // → native pan
  e.preventDefault();
  const cursorX = e.clientX - sc.getBoundingClientRect().left;
  if (zoomPending && zoomPending.sc === sc) {
    zoomPending.deltaY += e.deltaY;
    zoomPending.cursorX = cursorX;
  } else {
    zoomPending = { sc, sid: sc.dataset.tlscroll, cursorX, deltaY: e.deltaY };
  }
  if (zoomRaf) return;
  zoomRaf = requestAnimationFrame(() => { zoomRaf = 0; applyTimelineZoom(container); });
}

// applyTimelineZoom consumes the pending wheel gesture: it multiplies the axis
// density by 1.15^(-deltaY/100) (a perceptually-even step), clamps to the usable
// range (floor = fit-to-width, ceiling = TL_MAX_PX_PER_MS), repaints the single
// plotted Gantt at the new density, then sets the scroll offset so the timestamp
// under the cursor stays pinned. The current density is read off the live chart
// width (scrollWidth/span) so the first tick is responsive even from a stale
// below-floor state (the chart renders AT the floor in that case).
function applyTimelineZoom(container) {
  const pending = zoomPending;
  zoomPending = null;
  if (!pending) return;
  const chosen = currentTimelineSession();
  if (!chosen) return;
  const nowMs = Date.now();
  const { span, chartHostW } = buildTimeline(chosen.session, { hostW: timelineHostW(), nowMs });
  if (!(span > 0)) return; // zero-length session → no axis to zoom

  const oldChartW = pending.sc.scrollWidth;
  const viewportW = pending.sc.clientWidth;
  const scrollLeft = pending.sc.scrollLeft;
  const cur = oldChartW > 0 ? oldChartW / span : chartHostW / span;
  const factor = Math.pow(1.15, -pending.deltaY / 100);
  setTlMinPxPerMs(zoomClampPxPerMs(cur * factor, { span, chartHostW, maxPxPerMs: TL_MAX_PX_PER_MS }));

  const bounds = playheadBounds({ sessions: [chosen.session] }, nowMs);
  const T = playheadAt(bounds, nowMs);
  repaintTimelineSessions(container, T);

  // The repaint replaced the scroll node; re-query it and pin the cursor's
  // timestamp by setting scrollLeft from the old/new content widths.
  const newSc = container.querySelector(`.timeline-scroll[data-tlscroll="${cssEsc(pending.sid)}"]`);
  if (newSc) {
    newSc.scrollLeft = zoomAnchorScrollLeft({
      cursorX: pending.cursorX, scrollLeft, viewportW,
      oldChartW, newChartW: newSc.scrollWidth,
    });
  }
}

// onTimelineZoomReset returns the Gantt to the fit-to-width overview: it sets the
// density to the floor (chartHostW/span) so the whole session fits with no
// horizontal scroll — the double-click escape hatch from a deep zoom.
export function onTimelineZoomReset(container) {
  const chosen = currentTimelineSession();
  if (!chosen) return;
  const nowMs = Date.now();
  const { span, chartHostW } = buildTimeline(chosen.session, { hostW: timelineHostW(), nowMs });
  if (!(span > 0)) return;
  setTlMinPxPerMs(chartHostW / span);
  const bounds = playheadBounds({ sessions: [chosen.session] }, nowMs);
  repaintTimelineSessions(container, playheadAt(bounds, nowMs));
}
