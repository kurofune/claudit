// @ts-check
// Timeline (Gantt) math for the Agents tab: lane packing, time→x scaling,
// bar/segment geometry, playhead phase/stats, zoom clamps, and the
// session-picker rollups. Pure geometry — no DOM, no Date.now() (callers
// pass nowMs). Split out of agents-logic.js (re-exported by the facade).

import {
  agentLabel, agentSpan, agentTokens, flattenSession,
  parseRefKey, parseTime, refKey,
} from './agents-model.js';
import { conversationSegments } from './agents-conversation-logic.js';

/** @import { AgentGraph, AgentSession, AgentNode, AgentStep } from './api-types.js' */

/**
 * The linear time→x map makeTimeScale builds.
 * @typedef {Object} TimeScale
 * @property {(ts: *) => number} x
 * @property {number} startMs
 * @property {number} endMs
 * @property {number} width
 * @property {number} span
 * @property {number} minBlock
 */

/**
 * Shared options for the segment builders (stepSegments / toolSegments /
 * idleSegments). `scale` is required in practice — optional here only so the
 * `opts = {}` default-parameter idiom typechecks.
 * @typedef {Object} SegmentOpts
 * @property {TimeScale} [scale]
 * @property {number} [chartX]
 * @property {string} [sessionId]
 * @property {number} [agentIndex]
 * @property {number} [effEnd]
 * @property {number|null} [until]
 */

/**
 * The knobs buildTimeline / timelineAtTime / buildFlowLayout accept. All
 * optional — zero config yields the documented defaults.
 * @typedef {Object} TimelineOpts
 * @property {number} [hostW]
 * @property {number} [rowH]
 * @property {number} [axisH]
 * @property {number} [labelW]
 * @property {number} [pad]
 * @property {number} [nowMs]
 * @property {number} [minBlock]
 * @property {number} [minPxPerMs]
 * @property {number} [indent]
 * @property {number} [tickCount]
 * @property {TimelineExpansion} [expanded]
 */

/**
 * Progressive-disclosure state for the Timeline waterfall: which agent rows
 * are expanded to their per-turn spans (`rows`, keyed by the row key
 * `${sessionId}#${flattenIndex}`) and which turns are further expanded to
 * their per-tool sub-spans (`turns`, keyed by the step refKey
 * `${sessionId}#${agentIndex}.${stepIndex}`). Absent/empty sets mean
 * everything is collapsed — the default.
 * @typedef {Object} TimelineExpansion
 * @property {Set<string>} [rows]
 * @property {Set<string>} [turns]
 */

// timelineSessionList summarizes each session for the Timeline lens's session
// picker — the sibling of conversationSessionList. One entry per session that
// has a real main agent, in ORIGINAL input order (index = unfiltered position,
// a stable color slot). Each entry carries { sessionId, cwd, entrypoint, index }
// plus the whole-session rollups the picker rows triage on, by reusing
// sessionStats. entrypoint feeds the SDK badge, matching conversationSessionList.
// Null/main-less sessions are excluded; a null/empty input → [].
/** @param {AgentSession[]|null|undefined} sessions @param {number} [nowMs] */
export function timelineSessionList(sessions, nowMs = Date.now()) {
  const list = sessions || [];
  const out = [];
  list.forEach((session, index) => {
    if (!session || !session.main) return;
    out.push({
      sessionId: session.session_id || '',
      cwd: session.cwd || '',
      entrypoint: session.entrypoint || '',
      index,
      ...sessionStats(session, nowMs),
    });
  });
  return out;
}

// pickTimelineSid resolves which single session the Timeline lens plots, given
// the user's explicit pick (timelineSid), the shared drawer selection
// (selectedRef), and the current sessions. Precedence: an explicit timelineSid
// wins while that session is still in the window; else the session of
// selectedRef (so selecting a turn in another lens scopes the Gantt here too);
// else the first plottable session; else null when nothing is plottable. The
// timelineSid/selectedRef fallbacks guard against a chosen session aging out of
// the live window — it quietly drops back to the first.
/**
 * @param {AgentSession[]|null|undefined} sessions
 * @param {string|null|undefined} timelineSid
 * @param {string|null|undefined} selectedRef
 * @returns {string|null}
 */
export function pickTimelineSid(sessions, timelineSid, selectedRef) {
  const list = timelineSessionList(sessions);
  if (list.length === 0) return null;
  const present = sid => list.some(e => e.sessionId === sid);
  if (timelineSid && present(timelineSid)) return timelineSid;
  const ref = parseRefKey(selectedRef);
  if (ref && present(ref.sessionId)) return ref.sessionId;
  return list[0].sessionId;
}

// packLanes assigns each agent to a swimlane via greedy interval
// packing: agents whose [start, end] spans don't overlap can share a
// lane, so a session's main agent (spanning everything) takes lane 0
// and disjoint sub-agents reuse lanes beneath it. Returns a new array
// of { agent, lane, start, end } in start order; agents with an
// unparseable start are dropped. End is treated as inclusive of the
// boundary, so two agents that merely touch (one ends exactly when the
// next starts) can share a lane.
/**
 * @param {AgentNode[]|null|undefined} agents
 * @returns {{agent: AgentNode, lane: number, start: number, end: number}[]}
 */
export function packLanes(agents) {
  const items = (agents || [])
    .map(a => ({ agent: a, ...agentSpan(a) }))
    .filter(it => !Number.isNaN(it.start));
  // Use the start as a stable end when end is missing/NaN — a zero-width
  // span still occupies its instant.
  for (const it of items) {
    if (Number.isNaN(it.end) || it.end < it.start) it.end = it.start;
  }
  items.sort((a, b) => a.start - b.start || a.end - b.end);

  const laneEnds = []; // laneEnds[i] = end time currently occupying lane i
  const out = [];
  for (const it of items) {
    let lane = -1;
    for (let i = 0; i < laneEnds.length; i++) {
      if (laneEnds[i] <= it.start) { lane = i; break; }
    }
    if (lane === -1) {
      lane = laneEnds.length;
      laneEnds.push(it.end);
    } else {
      laneEnds[lane] = it.end;
    }
    out.push({ agent: it.agent, lane, start: it.start, end: it.end });
  }
  return out;
}

// laneCount returns how many lanes a packed layout occupies (max lane
// index + 1), or 0 when empty — the swimlane's row count.
/** @param {{lane: number}[]|null|undefined} packed @returns {number} */
export function laneCount(packed) {
  let max = -1;
  for (const p of packed || []) if (p.lane > max) max = p.lane;
  return max + 1;
}

// makeTimeScale builds a linear map from timestamps in [startMs, endMs]
// onto x-pixels in [0, width]. x() clamps out-of-range timestamps to the
// viewport edges and collapses a zero/inverted span to 0 (avoiding a
// divide-by-zero). minBlock is the floor agentBar applies to bar widths
// so a sub-second agent stays visible/clickable; it's carried on the
// returned scale for agentBar to read.
/**
 * @param {{startMs: number, endMs: number, width: number, minBlock?: number}} opts
 * @returns {TimeScale}
 */
export function makeTimeScale({ startMs, endMs, width, minBlock = 2 }) {
  const span = endMs - startMs;
  const x = ts => {
    if (!(span > 0)) return 0;
    const t = parseTime(ts);
    if (Number.isNaN(t)) return 0;
    const clamped = Math.max(startMs, Math.min(endMs, t));
    return ((clamped - startMs) / span) * width;
  };
  return { x, startMs, endMs, width, span, minBlock };
}

// agentBar computes the { x, width, lane } pixel rect for one packed
// agent against a scale. Width is floored to the scale's minBlock so a
// near-instant agent is still a visible block rather than a hairline.
/**
 * @param {{start: number, end: number, lane?: number}} item
 * @param {TimeScale} scale
 * @returns {{x: number, width: number, lane?: number}}
 */
export function agentBar(item, scale) {
  const x = scale.x(item.start);
  const raw = scale.x(item.end) - x;
  return { x, width: Math.max(scale.minBlock, raw), lane: item.lane };
}

// stepSegments computes the per-turn sub-segments that tile an agent's bar:
// one rect per timed step, placed on the shared time scale. A step's timestamp
// is its start; its duration_ms (the wall-clock gap to the next step) is its
// width, and the final step (duration_ms 0) stretches to the agent's effEnd so
// the segments tile [firstStep … end]. `until` caps segment ends at the
// playhead T (default: no cap, i.e. effEnd) — a step starting after the cap is
// dropped and a step straddling it is truncated. A segment is tinted 'error'
// when any tool in its step failed. x already includes chartX and each segment
// carries the step's refKey, so a click selects that turn in the drawer.
// durationMs is the segment's VISUAL span (end - start after any playhead clamp),
// not step.duration_ms — so the last step (raw duration 0, stretched to effEnd)
// and a truncated straddler both report the wall-clock the rendered width depicts,
// which is what an inline label reads off (see fitSegmentLabel).
/** @param {AgentNode|null|undefined} agent @param {SegmentOpts} [opts] */
export function stepSegments(agent, opts = {}) {
  const { scale, chartX = 0, sessionId = '', agentIndex = 0, effEnd, until } = opts;
  const steps = (agent && agent.steps) || [];
  const cap = until == null ? effEnd : until;
  const segs = [];
  for (let k = 0; k < steps.length; k++) {
    const step = steps[k];
    const start = parseTime(step && step.timestamp);
    if (Number.isNaN(start)) continue;
    if (cap != null && start > cap) continue;
    const dur = (step && step.duration_ms) || 0;
    let end = dur > 0 ? start + dur : effEnd;
    if (Number.isNaN(end) || end < start) end = start;
    if (cap != null && end > cap) end = cap;
    const bar = agentBar({ start, end }, scale);
    const hasErr = ((step && step.tools) || []).some(t => t && t.status === 'error');
    segs.push({
      x: chartX + bar.x,
      w: bar.width,
      stepIndex: k,
      refKey: refKey({ sessionId, agentIndex, stepIndex: k }),
      status: hasErr ? 'error' : '',
      cost_usd: (step && step.cost_usd) || 0,
      durationMs: end - start,
      kind: 'step',
    });
  }
  return segs;
}

// toolSegments is stepSegments' finer-grained sibling: where a step's tools
// carry real wall-clock (ended_at, from the backend tool_use→tool_result join),
// it splits that step into one sub-span per tool; where they don't (older data,
// or a pure-thinking turn), it falls back to the step's single turn segment so
// the row never looks empty. Same opts and segment shape as stepSegments — tool
// segments add a `toolIndex` and a tool refKey so a click opens that exact tool.
//
// Tools tile contiguously from the step's start, ordered by ended_at: tool i
// spans [previous tool's end (or step start), its own ended_at]. Because the
// tool_use side only stamps the whole turn, this is the honest reading of serial
// tool execution — the gaps between successive result timestamps ARE each tool's
// wall-clock (an 8s Bash is wide; an instant Read that ran right after is a
// sliver). Tools share their parent step's cost (the finest cost the wire gives
// — tools aren't individually priced), which keeps the cost-heat ramp alive;
// the render suppresses per-tool cost in labels so it never implies otherwise.
/** @param {AgentNode|null|undefined} agent @param {SegmentOpts} [opts] */
export function toolSegments(agent, opts = {}) {
  const { scale, chartX = 0, sessionId = '', agentIndex = 0, effEnd, until } = opts;
  const steps = (agent && agent.steps) || [];
  const cap = until == null ? effEnd : until;
  const segs = [];
  for (let k = 0; k < steps.length; k++) {
    const step = steps[k];
    const stepStart = parseTime(step && step.timestamp);
    if (Number.isNaN(stepStart)) continue;
    if (cap != null && stepStart > cap) continue;
    const dur = (step && step.duration_ms) || 0;
    let stepEnd = dur > 0 ? stepStart + dur : effEnd;
    if (Number.isNaN(stepEnd) || stepEnd < stepStart) stepEnd = stepStart;
    const tools = (step && step.tools) || [];
    const timed = [];
    for (let i = 0; i < tools.length; i++) {
      const end = parseTime(tools[i] && tools[i].ended_at);
      if (!Number.isNaN(end)) timed.push({ toolIndex: i, tool: tools[i], end });
    }
    const stepCost = (step && step.cost_usd) || 0;
    if (timed.length === 0) {
      // No tool timing → one turn segment, byte-identical to stepSegments.
      let end = stepEnd;
      if (cap != null && end > cap) end = cap;
      const bar = agentBar({ start: stepStart, end }, scale);
      const hasErr = tools.some(t => t && t.status === 'error');
      segs.push({
        x: chartX + bar.x, w: bar.width, stepIndex: k,
        refKey: refKey({ sessionId, agentIndex, stepIndex: k }),
        status: hasErr ? 'error' : '', cost_usd: stepCost, durationMs: end - stepStart,
        kind: 'step',
      });
      continue;
    }
    timed.sort((a, b) => a.end - b.end);
    let prev = stepStart;
    for (const { toolIndex, tool, end: rawEnd } of timed) {
      const start = prev;
      if (cap != null && start >= cap) break; // starts at/after the playhead → not begun yet, drop the rest
      let end = rawEnd;
      if (end < start) end = start;       // never run backwards
      if (end > stepEnd) end = stepEnd;    // a tool can't outlast its turn
      if (cap != null && end > cap) end = cap;
      const bar = agentBar({ start, end }, scale);
      segs.push({
        x: chartX + bar.x, w: bar.width, stepIndex: k, toolIndex,
        refKey: refKey({ sessionId, agentIndex, stepIndex: k, toolIndex }),
        status: tool && tool.status === 'error' ? 'error' : '',
        cost_usd: stepCost, durationMs: end - start,
        kind: (tool && tool.kind) || 'other',
      });
      prev = end;
    }
  }
  return segs;
}

// turnTimeBuckets decomposes one assistant turn into the honest 3-bucket split
// "model generate/think · tool exec · wait/idle" (Phase 1b, on Phase 0a's
// gen_ms). genMs is the streamed-generation span (step.gen_ms); toolMs is the
// sum of the serial tool-exec spans read off the SAME ended_at-chain toolSegments
// tiles bars from — tools ordered by ended_at, each spanning from the previous
// tool's end (or the turn start) to its own end, clamped inside the turn; idleMs
// is whatever wall-clock is left (duration_ms − gen − tool), floored at 0.
// Pre-0a fallback: when gen_ms is missing/0 we can't separate generation from
// wait, so idleMs stays 0 — no attribution beats mislabelling generation as idle.
/** @param {AgentStep|null|undefined} step @returns {{genMs: number, toolMs: number, idleMs: number}} */
export function turnTimeBuckets(step) {
  const genMs = Math.max(0, (step && step.gen_ms) || 0);
  const durMs = Math.max(0, (step && step.duration_ms) || 0);
  const stepStart = parseTime(step && step.timestamp);
  let toolMs = 0;
  if (!Number.isNaN(stepStart)) {
    const stepEnd = stepStart + durMs;
    const ends = ((step && step.tools) || [])
      .map(t => parseTime(t && t.ended_at))
      .filter(e => !Number.isNaN(e))
      .sort((a, b) => a - b);
    let prev = stepStart;
    for (let end of ends) {
      const start = prev;
      if (end < start) end = start;       // never run backwards
      if (end > stepEnd) end = stepEnd;    // a tool can't outlast its turn
      toolMs += end - start;
      prev = end;
    }
  }
  const idleMs = genMs > 0 ? Math.max(0, durMs - genMs - toolMs) : 0;
  return { genMs, toolMs, idleMs };
}

// idleSegments draws the wait/idle gap as a distinct span at the trailing edge of
// each turn — the "tool-end → next turn" dead air where the agent was neither
// generating nor running a tool, so "where did the time go" is visible in the
// Gantt. Width is turnTimeBuckets' idleMs; the span sits flush against the turn's
// end (== the next turn's start) so it reads as the lull before the next turn.
// Honest-only: a pre-0a turn (no gen_ms) yields idleMs 0 → no span is drawn.
// Same opts/playhead-cap contract as toolSegments; refKey points at the step (no
// toolIndex) so a click selects that turn. Kept separate from toolSegments so the
// idle rail renders behind/around the tool bars without disturbing their tiling.
/** @param {AgentNode|null|undefined} agent @param {SegmentOpts} [opts] */
export function idleSegments(agent, opts = {}) {
  const { scale, chartX = 0, sessionId = '', agentIndex = 0, effEnd, until } = opts;
  const steps = (agent && agent.steps) || [];
  const cap = until == null ? effEnd : until;
  const segs = [];
  for (let k = 0; k < steps.length; k++) {
    const step = steps[k];
    const { idleMs } = turnTimeBuckets(step);
    if (!(idleMs > 0)) continue;
    // "tool-end → next turn": only a turn that ran a timed tool has a tool-end to
    // anchor the gap, and only then does toolSegments leave the trailing region
    // uncovered (an untimed turn draws one full-width segment, occluding the gap).
    const tools = (step && step.tools) || [];
    if (!tools.some(t => !Number.isNaN(parseTime(t && t.ended_at)))) continue;
    const stepStart = parseTime(step && step.timestamp);
    if (Number.isNaN(stepStart)) continue;
    const dur = (step && step.duration_ms) || 0;
    let stepEnd = dur > 0 ? stepStart + dur : effEnd;
    if (Number.isNaN(stepEnd) || stepEnd < stepStart) continue;
    let start = stepEnd - idleMs;
    if (start < stepStart) start = stepStart;
    let end = stepEnd;
    if (cap != null) {
      if (start >= cap) continue;   // idle not yet reached at the playhead → drop
      if (end > cap) end = cap;
    }
    if (end <= start) continue;
    const bar = agentBar({ start, end }, scale);
    segs.push({
      x: chartX + bar.x, w: bar.width, stepIndex: k,
      refKey: refKey({ sessionId, agentIndex, stepIndex: k }),
      kind: 'idle', durationMs: end - start,
    });
  }
  return segs;
}

// fitSegmentLabel picks the richest inline label that fits inside a timeline
// segment `width` px wide: "<dur> · <cost>" when both fit, else just "<dur>"
// when that fits, else '' (the segment is too narrow → stay tooltip-only). The
// caller pre-formats durText (via formatElapsed) and costText (via fmtMoney);
// costText '' means "no cost worth showing", so only the duration is considered.
// charW is the approximate px per glyph at the segment font size and padX the
// breathing room reserved on each side — both tunable so the view can match its
// actual CSS without this module touching the DOM.
/**
 * @param {number} width
 * @param {string} durText
 * @param {string} costText
 * @param {{charW?: number, padX?: number}} [opts]
 * @returns {string}
 */
export function fitSegmentLabel(width, durText, costText, opts = {}) {
  const { charW = 6, padX = 4 } = opts;
  const avail = width - padX * 2;
  const fits = s => s.length * charW <= avail;
  if (costText) {
    const both = `${durText} · ${costText}`;
    if (fits(both)) return both;
  }
  if (durText && fits(durText)) return durText;
  return '';
}

// costHeat maps a segment's cost to a 0..1 intensity RELATIVE to maxCost — the
// cost-heat ramp the Timeline tints segments with so an expensive turn stands
// out against its session's other turns. 0 is coolest (a free turn, or a session
// with no costed turns at all); 1 is the most expensive turn in view. The gamma
// shapes the ramp: gamma > 1 holds cheap turns cool so only the genuinely
// expensive ones light up (cost is long-tailed, so a linear ramp would wash
// everything mid-bright). The caller feeds the result to an inline CSS var.
/** @param {number} cost @param {number} maxCost @param {{gamma?: number}} [opts] @returns {number} */
export function costHeat(cost, maxCost, opts = {}) {
  const { gamma = 2 } = opts;
  const c = cost > 0 ? cost : 0;
  const max = maxCost > 0 ? maxCost : 0;
  if (max <= 0 || c <= 0) return 0;
  const frac = Math.min(1, c / max);
  return frac ** gamma;
}

// KIND_FAMILIES is the set of palette families the Timeline colors segments by —
// the normalized ToolKind enum plus the 'agent'/'step' pseudo-kinds. It mirrors
// the `.kind-*` CSS classes (each setting --kc) reused from the Feed/Tree badges
// 1:1, so a kind reads identically in every lens. Anything outside it is
// uncategorized → 'other'.
const KIND_FAMILIES = new Set([
  'read', 'web', 'exec', 'edit', 'skill', 'mcp', 'command', 'todo', 'other', 'step', 'agent',
]);

// KIND_ORDER is the canonical legend order: the loud/common tool kinds first, the
// procedural ones and the 'step' (turn) fallback last — so the scrubber legend
// keeps a stable order rather than reshuffling with each session's kind mix.
const KIND_ORDER = ['read', 'edit', 'exec', 'web', 'skill', 'mcp', 'command', 'todo', 'step', 'other'];

// segKindColor normalizes a segment's kind to its `.kind-*` palette family: a
// known kind passes through, anything unknown/missing falls to 'other'. The
// Timeline renders `kind-${segKindColor(seg.kind)}` so hue carries the kind while
// cost-heat demotes to a secondary (saturation) channel and error-red still wins.
// DOM-free twin of view-agents' kindFamily (which also drives the glyph monograms).
/** @param {string|undefined} kind @returns {string} */
export function segKindColor(kind) {
  return KIND_FAMILIES.has(kind) ? kind : 'other';
}

// pctOfAgent returns a segment's share of its agent's total runtime as a rounded
// integer percent (0..100) — the "(xx% of agent)" clause the richer tooltip adds.
// A zero/negative part is 0; a zero/invalid whole returns null so the caller drops
// the clause rather than dividing by zero.
/** @param {number} partMs @param {number} wholeMs @returns {number|null} */
export function pctOfAgent(partMs, wholeMs) {
  if (!(wholeMs > 0)) return null;
  if (!(partMs > 0)) return 0;
  return Math.min(100, Math.round((partMs / wholeMs) * 100));
}

// segTooltip assembles the Timeline segment's hover string from already-formatted
// pieces (mirroring fitSegmentLabel's "caller pre-formats" contract, so this stays
// DOM- and format-free): `head · dur (pct% of agent) · cost · tokens`. Empty pieces
// are dropped; a null pct drops the parenthetical; an error appends a trailing
// marker. Richer hover, NOT richer in-bar labels — bars are a poor text channel.
/**
 * @param {{head?: string, durText?: string, pct?: number|null, costText?: string,
 *   tokensText?: string, isError?: boolean}} [parts]
 * @returns {string}
 */
export function segTooltip({ head = '', durText = '', pct = null, costText = '', tokensText = '', isError = false } = {}) {
  const parts = [];
  if (head) parts.push(head);
  if (durText) parts.push(pct != null ? `${durText} (${pct}% of agent)` : durText);
  if (costText) parts.push(costText);
  if (tokensText) parts.push(tokensText);
  let s = parts.join(' · ');
  if (isError) s += s ? ' · error' : 'error';
  return s;
}

// timelineKinds lists the distinct segment kinds present in a session, in
// canonical legend order, for the scrubber's kind legend. It mirrors toolSegments'
// timed-vs-untimed decision exactly: a step with ≥1 tool carrying ended_at
// contributes each timed tool's kind; a step with none falls back to the 'step'
// (turn) color — so the legend names exactly the colors the Gantt actually draws.
/** @param {AgentSession|null|undefined} session @returns {string[]} */
export function timelineKinds(session) {
  const seen = new Set();
  for (const a of flattenSession(session)) {
    for (const step of (a.steps || [])) {
      const tools = (step && step.tools) || [];
      const timed = tools.filter(t => t && !Number.isNaN(parseTime(t.ended_at)));
      if (timed.length === 0) seen.add('step');
      else for (const t of timed) seen.add(segKindColor(t.kind));
    }
  }
  return KIND_ORDER.filter(k => seen.has(k));
}

// criticalSpans flags the standout spans the Timeline outlines so the eye lands
// on "where time/money went" without reading every bar: per SESSION the single
// longest-duration segment and the single highest-cost turn across all agents,
// and per AGENT the same two within each row. Returns refKeys only (DOM-free) —
// the view places a subtle ▲/outline like the existing .tl-err-pip. Ties resolve
// to the first occurrence (stable across refetches). A segment with no positive
// duration contributes no longest mark; one with no positive cost no whale mark,
// so a row can carry a whale without a longest (and vice versa). Input is
// buildTimeline's rows (each { key, segments:[{ refKey, durationMs, cost_usd }] }).
/**
 * @param {{key: string, segments: {refKey: string, durationMs: number, cost_usd: number}[]}[]|null|undefined} rows
 * @returns {{session: {longestRef?: string, whaleRef?: string},
 *   agents: Object<string, {longestRef?: string, whaleRef?: string}>}}
 */
export function criticalSpans(rows) {
  const list = Array.isArray(rows) ? rows : [];
  const agents = /** @type {Object<string, {longestRef?: string, whaleRef?: string}>} */ ({});
  let allLongest = null, allWhale = null;
  for (const row of list) {
    if (!row) continue;
    const segs = row.segments || [];
    let longest = null, whale = null;
    for (const s of segs) {
      if (!s) continue;
      if (s.durationMs > 0 && (!longest || s.durationMs > longest.durationMs)) longest = s;
      if (s.cost_usd > 0 && (!whale || s.cost_usd > whale.cost_usd)) whale = s;
    }
    const entry = /** @type {{longestRef?: string, whaleRef?: string}} */ ({});
    if (longest) entry.longestRef = longest.refKey;
    if (whale) entry.whaleRef = whale.refKey;
    if (longest || whale) agents[row.key] = entry;
    if (longest && (!allLongest || longest.durationMs > allLongest.durationMs)) allLongest = longest;
    if (whale && (!allWhale || whale.cost_usd > allWhale.cost_usd)) allWhale = whale;
  }
  const session = /** @type {{longestRef?: string, whaleRef?: string}} */ ({});
  if (allLongest) session.longestRef = allLongest.refKey;
  if (allWhale) session.whaleRef = allWhale.refKey;
  return { session, agents };
}

// promptBands segments the Timeline waterfall by user prompt: one labeled
// vertical band per PromptMarker, spanning from the prompt's first turn to the
// next prompt's first turn (the last band runs to the chart's end). Pure
// geometry over an existing time scale — x/w land on the same axis as the
// bars, already including chartX, and the scale's own clamping keeps every
// band inside the chart bounds.
/**
 * @param {AgentSession|null|undefined} session
 * @param {{scale?: TimeScale, chartX?: number}} [opts]
 * @returns {{uuid: string, text: string, x: number, w: number, firstStepIndex: number}[]}
 */
export function promptBands(session, opts = {}) {
  const { scale, chartX = 0 } = opts;
  const prompts = (session && session.prompts) || [];
  if (!scale || prompts.length === 0) return [];
  const steps = (session && session.main && session.main.steps) || [];
  const items = [];
  for (const p of prompts) {
    if (!p) continue;
    const idx = p.first_step_index;
    const step = steps[idx];
    let start = parseTime(step && step.timestamp);
    if (Number.isNaN(start)) start = parseTime(p.timestamp);
    if (Number.isNaN(start)) continue;
    items.push({ uuid: p.uuid || '', text: p.text || '', firstStepIndex: idx, start });
  }
  items.sort((a, b) => a.start - b.start);
  return items.map((it, i) => {
    const end = i + 1 < items.length ? items[i + 1].start : scale.endMs;
    const x = chartX + scale.x(it.start);
    const w = Math.max(0, chartX + scale.x(end) - x);
    return { uuid: it.uuid, text: it.text, x, w, firstStepIndex: it.firstStepIndex };
  });
}

// narrativeStrip rolls a session up to the per-prompt outline the Timeline
// renders above the Gantt: one row per prompt segment (conversationSegments'
// slicing, so segment boundaries are exactly the Conversation lens's —
// prompt i's turns are main.steps[first_step_index_i, first_step_index_i+1);
// a session with steps but no markers degrades to one orphan (uuid '') row).
//
// Time window: a segment starts at its first turn's timestamp (falling back
// to the prompt marker's own) and ends where the next segment starts; the
// last segment ends at the main agent's ended_at, or at the injected nowMs
// while the main agent is still running (no Date.now() here — callers pass it).
//
// INCLUSION RULE — a sub-agent belongs WHOLLY to the segment that spawned it:
// each agent's spawn chain (timelineRowOrder's spawn metadata, i.e. the
// parent_tool_use_id links item 14 re-parents rows by) is walked up to the
// main agent, and the main-agent stepIndex where the chain anchors picks the
// segment — nested sub-agents therefore attribute to their ROOT spawning
// prompt. turnCount/toolCount/costUsd sum the segment's main-agent steps plus
// EVERY step of its attributed sub-agents (whole-agent, even where a
// sub-agent outlives the segment window — its work answers that prompt).
// An agent whose spawn can't be resolved falls back to time containment: the
// segment whose [startMs, next startMs) window holds its started_at (clamped
// to the first segment when earlier, the last when later/unparseable).
//
// OUTCOME RULE — a chip is ✗ (ok:false) iff the agent's error_count > 0:
// error_count is the backend's whole-agent rollup of failed tools, the same
// source the Gantt gutter's err-pip reads, so the strip and the waterfall
// can never disagree; a last-step-status heuristic would also miss mid-run
// failures the agent recovered from, which the audit still wants surfaced.
/**
 * @param {AgentSession|null|undefined} session
 * @param {{nowMs?: number}} [opts]
 * @returns {{uuid: string, text: string, firstStepIndex: number, startMs: number,
 *   endMs: number, durationMs: number, turnCount: number, toolCount: number,
 *   costUsd: number, agents: {agentIndex: number, label: string, ok: boolean}[]}[]}
 */
export function narrativeStrip(session, opts = {}) {
  const segments = conversationSegments(session);
  if (segments.length === 0) return [];
  const main = session && session.main;
  // Each segment starts at its first step's timestamp (falling back to the
  // prompt marker's own timestamp) and ends where the next one starts; the
  // last ends at the main agent's end.
  const starts = segments.map(seg => {
    const t = parseTime(seg.steps[0] && seg.steps[0].timestamp);
    return Number.isNaN(t) ? parseTime(seg.timestamp) : t;
  });
  const { nowMs = 0 } = opts;
  const mainEnd = main && main.status === 'running'
    ? nowMs
    : parseTime(main && main.ended_at);

  // Sub-agent attribution: walk each agent's spawn chain (timelineRowOrder's
  // spawn metadata) up to the main agent; the main-agent stepIndex where the
  // chain anchors picks the segment. The whole agent belongs to that segment.
  const agents = flattenSession(session);
  const spawnByAgent = new Map(timelineRowOrder(session).map(e => [e.agentIndex, e.spawn]));
  const segOfStep = stepIndex => {
    for (let i = segments.length - 1; i >= 0; i--) {
      if (stepIndex >= segments[i].firstStepIndex) return i;
    }
    return 0; // a pre-first-marker turn clamps into the first segment
  };
  const subsOf = segments.map(() => /** @type {number[]} */ ([]));
  agents.forEach((a, ai) => {
    if (!a || a.kind === 'main') return;
    // Walk to the root: main (agentIndex 0) anchors the segment.
    let loc = spawnByAgent.get(ai) || null;
    const seen = new Set([ai]);
    while (loc && loc.agentIndex !== 0 && !seen.has(loc.agentIndex)) {
      seen.add(loc.agentIndex);
      loc = spawnByAgent.get(loc.agentIndex) || null;
    }
    if (loc && loc.agentIndex === 0) { subsOf[segOfStep(loc.stepIndex)].push(ai); return; }
    // Unresolved spawn (no/dangling parent_tool_use_id, or a cycle): fall back
    // to time containment — the segment whose window holds the agent's start.
    // A start before the first window clamps to segment 0; a NaN/late start
    // lands in the last (findIndex misses → segments.length - 1).
    const start = parseTime(a.started_at);
    let idx = segments.length - 1;
    for (let i = 0; i < segments.length; i++) {
      const hi = i + 1 < segments.length ? starts[i + 1] : Infinity;
      if (start < hi) { idx = i; break; }
    }
    subsOf[idx].push(ai);
  });

  return segments.map((seg, i) => {
    const startMs = starts[i];
    const endMs = i + 1 < segments.length ? starts[i + 1] : mainEnd;
    let turnCount = seg.steps.length, toolCount = 0, costUsd = 0;
    for (const step of seg.steps) {
      toolCount += ((step && step.tools) || []).length;
      costUsd += (step && step.cost_usd) || 0;
    }
    const chips = subsOf[i].map(ai => {
      const a = agents[ai];
      for (const step of ((a && a.steps) || [])) {
        turnCount++;
        toolCount += ((step && step.tools) || []).length;
        costUsd += (step && step.cost_usd) || 0;
      }
      return { agentIndex: ai, label: agentLabel(a), ok: !((a && a.error_count) > 0) };
    });
    return {
      uuid: seg.uuid,
      text: seg.text,
      firstStepIndex: seg.firstStepIndex,
      startMs, endMs,
      durationMs: Math.max(0, endMs - startMs),
      turnCount,
      toolCount,
      costUsd,
      agents: chips,
    };
  });
}

// timelineBounds returns the [startMs, endMs] time window a Gantt timeline
// should span over a set of agents. Each agent's effective end is "now" if
// it's still running, else its parseable ended_at, else (NaN end) its own
// start — so a malformed/instant agent collapses to a zero-width point
// rather than poisoning the window. startMs is the earliest parseable
// start; endMs is the latest of all starts and effective ends, clamped so
// endMs >= startMs. Null when no agent has a parseable start.
/**
 * @param {AgentNode[]|null|undefined} agents
 * @param {number} nowMs
 * @returns {{startMs: number, endMs: number}|null}
 */
export function timelineBounds(agents, nowMs) {
  let startMs = Infinity;
  let endMs = -Infinity;
  for (const a of agents || []) {
    const start = parseTime(a && a.started_at);
    if (Number.isNaN(start)) continue;
    if (start < startMs) startMs = start;
    if (start > endMs) endMs = start;
    let effEnd;
    if (a && a.status === 'running') {
      effEnd = nowMs;
    } else {
      const end = parseTime(a && a.ended_at);
      effEnd = Number.isNaN(end) ? start : end;
    }
    if (effEnd > endMs) endMs = effEnd;
  }
  if (startMs === Infinity) return null;
  if (endMs < startMs) endMs = startMs;
  return { startMs, endMs };
}

// agentPhaseAt classifies what phase an agent is in as of instant T, purely
// from its own timestamps. 'pending' before it starts (or with an unparseable
// start), 'active' while it runs, 'done' once it has ended. A running agent
// (no end yet) is active for any T at or after its start. Boundaries are
// inclusive: T === start is active, T === end is done.
/** @param {AgentNode|null|undefined} agent @param {number} T @returns {'pending'|'active'|'done'} */
export function agentPhaseAt(agent, T) {
  const start = parseTime(agent && agent.started_at);
  if (Number.isNaN(start) || start > T) return 'pending';
  if (agent && agent.status === 'running') return 'active';
  let end = parseTime(agent && agent.ended_at);
  if (Number.isNaN(end) || end < start) end = start;
  return T >= end ? 'done' : 'active';
}

// playheadBounds returns the global scrub range [startMs, endMs] over every
// agent of every session — the window the playhead slider spans. Flattens all
// sessions into one agent array and delegates to timelineBounds (so a running
// agent extends endMs to nowMs). Null for a null/empty/agent-less graph.
/** @param {AgentGraph|null|undefined} graph @param {number} nowMs */
export function playheadBounds(graph, nowMs) {
  const sessions = (graph && graph.sessions) || [];
  const allAgents = sessions.flatMap(flattenSession);
  return timelineBounds(allAgents, nowMs);
}

// playheadStats counts how many agents across every session are pending /
// active / done as of instant T, classifying each via agentPhaseAt. Zeros for
// a null/empty graph. Before the earliest start everything is pending; after
// the latest end everything is done.
/** @param {AgentGraph|null|undefined} graph @param {number} T @returns {{pending: number, active: number, done: number}} */
export function playheadStats(graph, T) {
  const out = { pending: 0, active: 0, done: 0 };
  const sessions = (graph && graph.sessions) || [];
  for (const s of sessions) {
    for (const a of flattenSession(s)) {
      out[agentPhaseAt(a, T)]++;
    }
  }
  return out;
}

// sessionStats rolls one session up to the numbers the Timeline's per-session
// summary strip shows, all from already-present data.
/** @param {AgentSession|null|undefined} session @param {number} [nowMs] */
export function sessionStats(session, nowMs = Date.now()) {
  const agents = flattenSession(session);
  let turnCount = 0, toolCount = 0, toolErrors = 0, stepCost = 0, tokenCount = 0;
  for (const a of agents) {
    tokenCount += agentTokens(a).total; // input+output+cache, the drawer's "X total"
    for (const step of (a.steps || [])) {
      turnCount++;
      stepCost += step.cost_usd || 0;
      for (const t of (step.tools || [])) {
        toolCount++;
        if (t && t.status === 'error') toolErrors++;
      }
    }
  }
  const start = parseTime(session && session.started_at);
  const end = parseTime(session && session.ended_at);
  const until = Number.isNaN(end) ? nowMs : end;
  const durationMs = Number.isNaN(start) ? 0 : Math.max(0, until - start);
  // session.error_count / cost_usd are backend totals for the whole session;
  // prefer them when present, else fall back to the per-step/tool hand walk.
  const errorCount = Number.isFinite(session && session.error_count) ? session.error_count : toolErrors;
  const cost_usd = Number.isFinite(session && session.cost_usd) ? session.cost_usd : stepCost;
  return { durationMs, turnCount, toolCount, errorCount, agentCount: agents.length, tokenCount, cost_usd };
}

// timelineRowOrder computes the DISPLAY order of a session's timeline rows as
// a depth-first spawn tree: main first, then each sub-agent nested immediately
// under the agent whose Agent tool_use spawned it (resolved by matching the
// child's parent_tool_use_id against every tool's id), recursively — a
// sub-agent that itself spawned gets its own children beneath it at
// depth+1. Children of one parent order by their spawning (stepIndex,
// toolIndex); unresolvable/missing parent links fall back to the old flat
// model (depth 1 under main, appended after the resolved children in flatten
// order). agentIndex is ALWAYS the flattenSession index — the stable identity
// every refKey in the tab hangs off — only order/depth/spawn are computed.
// spawn is the spawning tool's location {agentIndex, stepIndex, toolIndex}
// (null for main/unresolved), the connector metadata the view draws elbows from.
/**
 * @param {AgentSession|null|undefined} session
 * @returns {{agentIndex: number, depth: number,
 *   spawn: {agentIndex: number, stepIndex: number, toolIndex: number}|null}[]}
 */
export function timelineRowOrder(session) {
  const agents = flattenSession(session);
  // One pass building tool_use id → location; the Agent-kind tools among these
  // are the spawn calls a child's parent_tool_use_id points back at.
  const toolLoc = new Map();
  agents.forEach((a, ai) => {
    ((a && a.steps) || []).forEach((step, si) => {
      ((step && step.tools) || []).forEach((tool, ti) => {
        if (tool && tool.id && !toolLoc.has(tool.id)) {
          toolLoc.set(tool.id, { agentIndex: ai, stepIndex: si, toolIndex: ti });
        }
      });
    });
  });
  // Resolve each non-main agent's spawn location, bucket resolved children
  // under their parent agent (ordered by spawning stepIndex/toolIndex, then
  // flatten order for stability), and emit depth-first from the roots.
  const spawnOf = agents.map((a, i) => {
    const isMain = a && a.kind === 'main';
    if (isMain || !a || !a.parent_tool_use_id) return null;
    const loc = toolLoc.get(a.parent_tool_use_id);
    return (loc && loc.agentIndex !== i) ? loc : null; // a self-spawn is garbage → unresolved
  });
  const childrenOf = new Map(); // parent agentIndex → [child agentIndex…]
  spawnOf.forEach((loc, i) => {
    if (!loc) return;
    if (!childrenOf.has(loc.agentIndex)) childrenOf.set(loc.agentIndex, []);
    childrenOf.get(loc.agentIndex).push(i);
  });
  for (const kids of childrenOf.values()) {
    kids.sort((x, y) =>
      spawnOf[x].stepIndex - spawnOf[y].stepIndex ||
      spawnOf[x].toolIndex - spawnOf[y].toolIndex ||
      x - y);
  }
  const out = [];
  const visited = new Set();
  const emit = (i, depth) => {
    if (visited.has(i)) return; // cycle guard — each agent appears exactly once
    visited.add(i);
    out.push({ agentIndex: i, depth, spawn: spawnOf[i] });
    for (const kid of childrenOf.get(i) || []) emit(kid, depth + 1);
  };
  // Roots: main first, then (after its resolved subtree) every unresolved
  // sub-agent at the old flat depth 1, in flatten order — including agents
  // orphaned by a spawn cycle, so nothing is ever dropped.
  agents.forEach((a, i) => { if (a && a.kind === 'main') emit(i, 0); });
  agents.forEach((a, i) => {
    if (visited.has(i)) return;
    if (!spawnOf[i]) { emit(i, 1); return; }
    // Resolved but unreachable (its parent chain never reached a root — a
    // cycle): break the link and fall back to the flat model.
    spawnOf[i] = null;
    emit(i, 1);
  });
  return out;
}

// buildTimeline computes a per-session horizontal trace-waterfall layout: one
// row per agent in timelineRowOrder's depth-first spawn-tree order (main at
// depth 0, each sub-agent nested under its spawning turn at parent depth + 1),
// each bar placed on a real time axis spanning the session's lifetime, plus
// axis ticks and a "now" line x. Pure geometry — no DOM and no Date.now(); the
// caller passes nowMs so a running agent's bar (and the now-line) advance on
// refetch. The chart floors to the host width but widens (→ horizontal scroll)
// for long sessions via minPxPerMs.
//
// Progressive disclosure (opts.expanded): by default every row is COLLAPSED —
// one bar, no per-turn/per-tool segments, height rowH — so a busy session
// renders O(agents) elements, not O(tools). A row whose key is in
// expanded.rows doubles to h = 2·rowH: the bar keeps the top laneH band and a
// per-turn segment band opens beneath it (segY = y + laneH). A turn whose step
// refKey is in expanded.turns additionally emits its tool sub-spans right
// after its turn span (drawn on top of it).
//
// Rows REORDER by spawn tree but each keeps the key/rowIndex flattenSession
// assigns (`${sid}#${flattenIndex}`) — the identity every refKey/selection in
// the tab hangs off; an agent with an unparseable start just gets a left-edge
// sliver bar rather than being dropped. Returns the empty layout when the
// session has no agents or no parseable times.
/** @param {AgentSession|null|undefined} session @param {TimelineOpts} [opts] */
export function buildTimeline(session, opts = {}) {
  const {
    hostW = 800, rowH = 24, axisH = 20, labelW = 130, pad = 8,
    nowMs = 0, minBlock = 3, minPxPerMs = 0.0012, indent = 12, tickCount = 5,
  } = opts;
  const agents = flattenSession(session);
  const sid = (session && session.session_id) || '';

  const bounds = timelineBounds(agents, nowMs);
  if (agents.length === 0 || bounds === null) {
    return {
      sessionId: sid, startMs: NaN, endMs: NaN, span: 0,
      chartX: labelW, chartW: 0, contentW: hostW, width: hostW,
      height: axisH + pad, nowX: null, rows: [], ticks: [],
    };
  }

  const { startMs, endMs } = bounds;
  const span = endMs - startMs;
  const chartHostW = Math.max(minBlock, hostW - labelW - pad);
  const chartW = Math.max(chartHostW, Math.ceil(span * minPxPerMs));
  const scale = makeTimeScale({ startMs, endMs, width: chartW, minBlock });
  const chartX = labelW;

  const order = timelineRowOrder(session);
  const expRows = (opts.expanded && opts.expanded.rows) || null;
  const expTurns = (opts.expanded && opts.expanded.turns) || null;
  let nextY = axisH;
  const rows = order.map(entry => {
    const i = entry.agentIndex;
    const a = agents[i];
    const start = parseTime(a.started_at);
    const effEnd = a.status === 'running'
      ? nowMs
      : (Number.isNaN(parseTime(a.ended_at)) ? start : parseTime(a.ended_at));
    const bar = agentBar({ start, end: effEnd }, scale);
    const d = entry.depth;
    const key = `${sid}#${i}`;
    const expanded = !!(expRows && expRows.has(key));
    const h = expanded ? rowH * 2 : rowH;
    const y = nextY;
    nextY += h;
    const segOpts = { scale, chartX, sessionId: sid, agentIndex: i, effEnd };
    return {
      key, rowIndex: i, depth: d, spawn: entry.spawn,
      label: agentLabel(a), kind: a.kind || '', status: a.status || '',
      running: a.status === 'running',
      cost_usd: a.cost_usd || 0, steps: (a.steps || []).length,
      x: chartX + bar.x, w: bar.width,
      y, h, laneH: rowH, expanded,
      segY: expanded ? y + rowH : null,
      labelX: pad + d * indent,
      segments: expanded ? disclosedSegments(a, segOpts, expTurns) : [],
      idleSegments: expanded ? idleSegments(a, segOpts) : [],
    };
  });

  const nowInRange = nowMs >= startMs && nowMs <= endMs;
  const nowX = nowInRange ? chartX + scale.x(nowMs) : null;

  let ticks;
  if (span <= 0) {
    ticks = [{ x: chartX, t: startMs }];
  } else {
    ticks = [];
    for (let k = 0; k < tickCount; k++) {
      const t = startMs + span * k / (tickCount - 1);
      ticks.push({ x: chartX + scale.x(t), t });
    }
  }

  const contentW = chartX + chartW + pad;
  const height = nextY + pad; // rows are variable-height (expanded = 2·rowH)

  return {
    sessionId: sid, startMs, endMs, span,
    chartX, chartW, chartHostW, contentW, width: contentW, height,
    nowX, rows, ticks,
  };
}

// disclosedSegments builds an EXPANDED row's segment list: one turn span per
// timed step (stepSegments), and — for each turn whose step refKey is in the
// `turns` expansion set — that turn's per-tool sub-spans appended immediately
// after its turn span, so the tools draw on top of it (SVG paint order) while
// the turn span keeps the trailing gen/idle region visible (and clickable as
// the collapse target). Same opts/playhead-cap contract as the segment
// builders it composes.
/**
 * @param {AgentNode|null|undefined} agent
 * @param {SegmentOpts} opts
 * @param {Set<string>|null} turns
 */
function disclosedSegments(agent, opts, turns) {
  const turnSegs = stepSegments(agent, opts);
  if (!turns || turns.size === 0 || !turnSegs.some(s => turns.has(s.refKey))) {
    return turnSegs;
  }
  const toolSegs = toolSegments(agent, opts).filter(s => s.toolIndex != null);
  const out = [];
  for (const seg of turnSegs) {
    out.push(seg);
    if (turns.has(seg.refKey)) {
      for (const t of toolSegs) if (t.stepIndex === seg.stepIndex) out.push(t);
    }
  }
  return out;
}

// TL_MAX_PX_PER_MS caps how far the Timeline can zoom in: enough px-per-ms that
// even a sub-second tool call stays a legible, clickable sliver, without letting
// the chart blow up to an unscrollable width. The floor is computed per session
// (fit-to-width); this is the shared ceiling, tuned by eye.
export const TL_MAX_PX_PER_MS = 0.12;

// zoomClampPxPerMs bounds a requested time-axis density (px per ms) to the
// Timeline's usable zoom range. The FLOOR is fit-to-width — chartHostW / span,
// the density at which the chart exactly fills the host (zooming out past it
// would only add empty scroll). The CEILING is TL_MAX_PX_PER_MS. A short
// session whose fit-density already exceeds the ceiling can't zoom at all, so
// floor wins and every request collapses to it. A zero/negative span (a
// zero-length session) has no meaningful axis → returns the ceiling; a
// non-positive/NaN request returns the floor (the safe overview density).
/**
 * @param {number} pxPerMs
 * @param {{span?: number, chartHostW?: number, maxPxPerMs?: number}} [opts]
 * @returns {number}
 */
export function zoomClampPxPerMs(pxPerMs, opts = {}) {
  const { span, chartHostW, maxPxPerMs = TL_MAX_PX_PER_MS } = opts;
  const floor = span > 0 ? chartHostW / span : maxPxPerMs;
  const lo = floor;
  const hi = Math.max(floor, maxPxPerMs);
  if (!(pxPerMs > 0)) return lo;
  return Math.max(lo, Math.min(hi, pxPerMs));
}

// zoomAnchorScrollLeft keeps the timestamp currently under the pointer pinned in
// place as the chart's pixel width changes under a zoom. Given the cursor's x
// within the scroll viewport, the current scrollLeft, the viewport width, and
// the old/new chart content widths, it returns the scrollLeft that puts the same
// FRACTIONAL content position back under the cursor — clamped to [0, maxScroll]
// so the result never over- or under-scrolls. A degenerate old width yields 0.
/**
 * @param {{cursorX?: number, scrollLeft?: number, viewportW?: number,
 *   oldChartW?: number, newChartW?: number}} [opts]
 * @returns {number}
 */
export function zoomAnchorScrollLeft(opts = {}) {
  const { cursorX = 0, scrollLeft = 0, viewportW = 0, oldChartW = 0, newChartW = 0 } = opts;
  if (!(oldChartW > 0)) return 0;
  const frac = (scrollLeft + cursorX) / oldChartW;
  const want = frac * newChartW - cursorX;
  const maxScroll = Math.max(0, newChartW - viewportW);
  return Math.max(0, Math.min(maxScroll, want));
}

// timelineAtTime is buildTimeline scrubbed to an instant T: it reuses the same
// time window/scale (so the axis and ticks never reflow as the playhead moves)
// but recomputes each agent bar as of T — a not-yet-started agent collapses to
// width 0, an in-flight agent's bar grows only up to the playhead, and a
// finished agent keeps its real bar. Adds `playheadX` (the playhead line's x,
// null when the session hasn't begun at T) and `atMs` (the T it was built for).
// Rows keep buildTimeline's spawn-tree order and flattenSession-pinned keys, so
// selection indices still line up; the same opts.expanded disclosure applies —
// scrubbing a collapsed row keeps it collapsed (no segments), an expanded one
// gets its turn/tool spans clamped to T.
/** @param {AgentSession|null|undefined} session @param {number} T @param {TimelineOpts} [opts] */
export function timelineAtTime(session, T, opts = {}) {
  const base = buildTimeline(session, opts);
  if (base.rows.length === 0) {
    return { ...base, rows: [], playheadX: null, atMs: T };
  }
  const { minBlock = 3 } = opts;
  const scale = makeTimeScale({
    startMs: base.startMs, endMs: base.endMs, width: base.chartW, minBlock,
  });
  const agents = flattenSession(session);
  const nowMs = opts.nowMs || 0;
  const expTurns = (opts.expanded && opts.expanded.turns) || null;
  const rows = base.rows.map(row => {
    const a = agents[row.rowIndex];
    const start = parseTime(a.started_at);
    const phase = agentPhaseAt(a, T);
    if (phase === 'pending') {
      return { ...row, w: 0, phase: 'pending', running: false, segments: [], idleSegments: [] };
    }
    let clampEnd;
    if (phase === 'active') {
      clampEnd = T;
    } else {
      const end = parseTime(a.ended_at);
      clampEnd = (Number.isNaN(end) || end < start) ? start : end;
    }
    const bar = agentBar({ start, end: clampEnd, lane: 0 }, scale);
    const effEnd = a.status === 'running'
      ? nowMs
      : (Number.isNaN(parseTime(a.ended_at)) ? start : parseTime(a.ended_at));
    const segOpts = {
      scale, chartX: base.chartX, sessionId: base.sessionId,
      agentIndex: row.rowIndex, effEnd, until: clampEnd,
    };
    return {
      ...row, w: bar.width, phase, running: phase === 'active',
      segments: row.expanded ? disclosedSegments(a, segOpts, expTurns) : [],
      idleSegments: row.expanded ? idleSegments(a, segOpts) : [],
    };
  });
  const playheadX = T < base.startMs ? null : base.chartX + scale.x(T);
  return { ...base, rows, playheadX, atMs: T };
}

// buildFlowLayout computes node + edge geometry for the Flow graph view: the
// main agent as a node centered at the top, its sub-agents in a centered grid
// beneath it, and one edge from main down to each child. Pure geometry over a
// given pixel width — the view renders the returned rects/lines as SVG.
// Nodes are keyed `${sessionId}#${flattenIndex}` (0=main) so selection and
// coloring stay stable across refetches. Tolerates a missing main / children.
/**
 * @param {AgentSession|null|undefined} session
 * @param {{width?: number, nodeW?: number, nodeH?: number, padding?: number,
 *   gapX?: number, gapY?: number}} [opts]
 */
export function buildFlowLayout(session, opts = {}) {
  const {
    width = 800, nodeW = 120, nodeH = 48,
    padding = 16, gapX = 24, gapY = 56,
  } = opts;
  const sid = (session && session.session_id) || '';
  const main = session && session.main;
  const children = session && Array.isArray(session.children) ? session.children : [];

  const nodes = [];
  const edges = [];

  let mainNode = null;
  if (main) {
    mainNode = nodeOf(main, `${sid}#0`, 'main', (width - nodeW) / 2, padding, nodeW, nodeH);
    nodes.push(mainNode);
  }

  // Children wrap into centered rows so a session with many sub-agents stays
  // inside the viewport instead of overflowing to the right.
  const usable = Math.max(nodeW, width - padding * 2);
  const perRow = Math.max(1, Math.floor((usable + gapX) / (nodeW + gapX)));
  const rowTop = padding + nodeH + gapY;
  children.forEach((c, i) => {
    const row = Math.floor(i / perRow);
    const col = i % perRow;
    const inRow = Math.min(perRow, children.length - row * perRow);
    const rowW = inRow * nodeW + (inRow - 1) * gapX;
    const rowX = (width - rowW) / 2;
    const x = rowX + col * (nodeW + gapX);
    const y = rowTop + row * (nodeH + gapY);
    nodes.push(nodeOf(c, `${sid}#${i + 1}`, 'subagent', x, y, nodeW, nodeH));
    if (mainNode) {
      edges.push({
        x1: mainNode.x + mainNode.w / 2, y1: mainNode.y + mainNode.h,
        x2: x + nodeW / 2, y2: y,
        running: c && c.status === 'running',
      });
    }
  });

  const rows = Math.ceil(children.length / perRow) || 0;
  const height = children.length === 0
    ? padding + nodeH + padding
    : rowTop + rows * nodeH + (rows - 1) * gapY + padding;

  return { width, height, nodes, edges };
}

// nodeOf builds one flow-graph node rect from an agent payload.
/**
 * @param {AgentNode|null|undefined} a
 * @param {string} key
 * @param {string} kind
 * @param {number} x @param {number} y @param {number} w @param {number} h
 */
function nodeOf(a, key, kind, x, y, w, h) {
  return {
    key, kind, label: agentLabel(a),
    status: (a && a.status) || '', description: (a && a.description) || '',
    cost_usd: (a && a.cost_usd) || 0, steps: ((a && a.steps) || []).length,
    x, y, w, h,
  };
}
