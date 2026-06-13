// Pure, DOM-free logic for the Agents observability tab: lane packing
// for the swimlane, time→x scaling, agent-bar geometry, and the
// status/elapsed derivations the cards display, all computed over the
// /_claudit/api/agents JSON payload.
//
// This is the swappable-UI insurance: view-agents.js does only DOM/SVG
// and pulls every number from here, so a redesign rewrites the view
// against the same contract without touching tested math. Unit-tested
// under `node --test` in jstest/agents-logic.test.js, mirroring how
// sessions-logic.js holds the Sessions view's pure helpers.

// parseTime coerces a payload timestamp to epoch milliseconds. Numbers
// pass through (tests use them); ISO-8601 strings parse via Date.parse;
// anything unparseable is NaN so callers can filter it out.
export function parseTime(ts) {
  if (typeof ts === 'number') return ts;
  if (typeof ts !== 'string') return NaN;
  const t = Date.parse(ts);
  return Number.isNaN(t) ? NaN : t;
}

// agentSpan returns an agent node's { start, end } in epoch ms.
export function agentSpan(a) {
  return { start: parseTime(a && a.started_at), end: parseTime(a && a.ended_at) };
}

// flattenSession returns a session's agents in display order: the main
// agent first, then its sub-agents (already start-ordered by the
// backend). Tolerates a missing main or children so a half-built /
// null payload doesn't throw.
export function flattenSession(session) {
  if (!session) return [];
  const out = [];
  if (session.main) out.push(session.main);
  if (Array.isArray(session.children)) out.push(...session.children);
  return out;
}

// spawnTargetIndex maps a spawn rollup's agent_ref (the parent tool_use id) to
// the flattenSession index of the sub-agent it launched — the child whose
// parent_tool_use_id matches. Returns null when nothing matches or the ref is
// empty, so the caller can fall back to a non-navigable rollup badge. The
// index lines up with flattenSession order (main=0, children 1..), which every
// other lens keys selection off, so the result drops straight into a refKey.
export function spawnTargetIndex(session, agentRef) {
  if (!session || !agentRef) return null;
  const flat = flattenSession(session);
  for (let i = 0; i < flat.length; i++) {
    if (flat[i] && flat[i].parent_tool_use_id === agentRef) return i;
  }
  return null;
}

// conversationSegments groups the main agent's step timeline by originating
// user prompt for the Conversation lens. Each segment is one prompt marker
// (uuid/text/timestamp from session.prompts) plus the contiguous slice of
// main.steps it produced, bounded by the next marker's first_step_index. The
// slice's first absolute index is firstStepIndex, so the renderer can address
// each step with the SAME refKey the other lenses use (main = agentIndex 0).
// Returns [] when the main agent has no steps; a session with steps but no
// markers degrades to one prompt-less segment over all of them.
export function conversationSegments(session) {
  const main = session && session.main;
  const steps = (main && main.steps) || [];
  if (steps.length === 0) return [];
  const markers = ((session && session.prompts) || [])
    .filter(m => m && Number.isInteger(m.first_step_index));
  if (markers.length === 0) {
    return [{ uuid: '', text: '', timestamp: '', firstStepIndex: 0, steps: steps.slice() }];
  }
  const out = [];
  for (let i = 0; i < markers.length; i++) {
    const start = markers[i].first_step_index;
    const end = i + 1 < markers.length ? markers[i + 1].first_step_index : steps.length;
    out.push({
      uuid: markers[i].uuid || '',
      text: markers[i].text || '',
      timestamp: markers[i].timestamp || '',
      firstStepIndex: start,
      steps: steps.slice(start, end),
    });
  }
  return out;
}

// conversationReplies distills a segment down to the assistant's spoken turns —
// the steps that actually produced text — for the text-only Conversation lens.
// Tool-only steps (no prose) are dropped so the thread reads as a dialogue, not
// a trace. Each reply keeps its absolute stepIndex (firstStepIndex + local k) so
// the rendered bubble carries the SAME refKey the other lenses use and a click
// still opens the full turn (tools, reasoning) in the shared drawer.
export function conversationReplies(seg) {
  const steps = (seg && seg.steps) || [];
  const base = (seg && seg.firstStepIndex) || 0;
  const out = [];
  for (let k = 0; k < steps.length; k++) {
    const st = steps[k] || {};
    if (!(st.text || '').trim()) continue;
    out.push({
      stepIndex: base + k,
      text: st.text,
      timestamp: st.timestamp || '',
      model: st.model || '',
      cost_usd: st.cost_usd || 0,
    });
  }
  return out;
}

// conversationSessionList summarizes each session for the Conversation lens's
// session picker: one entry per session that has a real main agent, in the
// ORIGINAL input order. `index` is the session's position in the unfiltered
// input (so it stays a stable color slot even when earlier sessions are
// dropped). promptCount counts only segments tied to a real user prompt (a
// non-empty uuid); replyCount sums the assistant's spoken replies across every
// segment. Null/main-less sessions are excluded; a null/empty input → [].
export function conversationSessionList(sessions) {
  const list = sessions || [];
  const out = [];
  list.forEach((session, index) => {
    if (!session || !session.main) return;
    const segments = conversationSegments(session);
    let promptCount = 0;
    let replyCount = 0;
    for (const seg of segments) {
      if (seg.uuid) promptCount++;
      replyCount += conversationReplies(seg).length;
    }
    out.push({
      sessionId: session.session_id || '',
      cwd: session.cwd || '',
      index,
      promptCount,
      replyCount,
    });
  });
  return out;
}

// timelineSessionList summarizes each session for the Timeline lens's session
// picker — the sibling of conversationSessionList. One entry per session that
// has a real main agent, in ORIGINAL input order (index = unfiltered position,
// a stable color slot). Each entry carries { sessionId, cwd, index } plus the
// whole-session rollups the picker rows triage on, by reusing sessionStats.
// Null/main-less sessions are excluded; a null/empty input → [].
export function timelineSessionList(sessions, nowMs = Date.now()) {
  const list = sessions || [];
  const out = [];
  list.forEach((session, index) => {
    if (!session || !session.main) return;
    out.push({
      sessionId: session.session_id || '',
      cwd: session.cwd || '',
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
export function pickTimelineSid(sessions, timelineSid, selectedRef) {
  const list = timelineSessionList(sessions);
  if (list.length === 0) return null;
  const present = sid => list.some(e => e.sessionId === sid);
  if (timelineSid && present(timelineSid)) return timelineSid;
  const ref = parseRefKey(selectedRef);
  if (ref && present(ref.sessionId)) return ref.sessionId;
  return list[0].sessionId;
}

// clampConvSidebarWidth keeps the Conversation lens's resizable session
// sidebar within sane bounds: a finite px is clamped to [MIN, MAX] and rounded
// to a whole pixel; anything non-finite (NaN, undefined, null, Infinity, a
// non-numeric string) falls back to DEFAULT.
export function clampConvSidebarWidth(px) {
  const MIN = 160, MAX = 560, DEFAULT = 240;
  const n = typeof px === 'number' ? px : (typeof px === 'string' ? Number(px) : NaN);
  if (!Number.isFinite(n)) return DEFAULT;
  return Math.round(Math.max(MIN, Math.min(MAX, n)));
}

// clampTreeWidth bounds the Tree lens's left rail: it is a fixed-width column
// (the detail pane takes the remaining width) that the user drags to resize.
// Same contract as clampConvSidebarWidth — finite px clamped to [MIN, MAX] and
// rounded; non-finite falls back to DEFAULT.
export function clampTreeWidth(px) {
  const MIN = 220, MAX = 680, DEFAULT = 320;
  const n = typeof px === 'number' ? px : (typeof px === 'string' ? Number(px) : NaN);
  if (!Number.isFinite(n)) return DEFAULT;
  return Math.round(Math.max(MIN, Math.min(MAX, n)));
}

// clampDrawerWidth bounds the detail drawer on the Feed/Timeline lenses: there
// the drawer is the fixed-width RIGHT column (the lens flexes to fill the rest)
// and the handle between them resizes it. Same contract as the rail clamps —
// finite px clamped to [MIN, MAX] and rounded; non-finite falls back to DEFAULT.
export function clampDrawerWidth(px) {
  const MIN = 280, MAX = 640, DEFAULT = 360;
  const n = typeof px === 'number' ? px : (typeof px === 'string' ? Number(px) : NaN);
  if (!Number.isFinite(n)) return DEFAULT;
  return Math.round(Math.max(MIN, Math.min(MAX, n)));
}

// orderTreeSessions reorders the natural (newest-first) session list to a
// frozen display order, so a live tick can't reshuffle rows under a user who's
// mid-read. `frozenOrderIds` null ⇒ no freeze, return the list as-is. Otherwise
// emit the sessions whose ids appear in frozenOrderIds first (in that order),
// then append any session not in the frozen list in its natural order — that's
// where sessions that arrived since the freeze land. Frozen ids with no current
// session are skipped.
export function orderTreeSessions(sessions, frozenOrderIds) {
  if (!frozenOrderIds) return sessions;
  const byId = new Map((sessions || []).map(s => [s.session_id || '', s]));
  const seen = new Set();
  const ordered = [];
  for (const id of frozenOrderIds) {
    const s = byId.get(id);
    if (s && !seen.has(id)) { ordered.push(s); seen.add(id); }
  }
  for (const s of sessions || []) {
    const id = s.session_id || '';
    if (!seen.has(id)) { ordered.push(s); seen.add(id); }
  }
  return ordered;
}

// treeFollowMode decides whether the tree should follow the live newest-first
// order ('follow') or hold a frozen order ('frozen') so a live re-render doesn't
// yank rows around while the user reads. It follows when the user is at the top
// of the list, has never scrolled, or has been idle for at least idleMs since
// their last scroll; otherwise it stays frozen.
export function treeFollowMode(lastScrollAtMs, nowMs, atTop, idleMs = 15000) {
  if (atTop) return 'follow';
  if (lastScrollAtMs == null) return 'follow';
  return (nowMs - lastScrollAtMs >= idleMs) ? 'follow' : 'frozen';
}

// packLanes assigns each agent to a swimlane via greedy interval
// packing: agents whose [start, end] spans don't overlap can share a
// lane, so a session's main agent (spanning everything) takes lane 0
// and disjoint sub-agents reuse lanes beneath it. Returns a new array
// of { agent, lane, start, end } in start order; agents with an
// unparseable start are dropped. End is treated as inclusive of the
// boundary, so two agents that merely touch (one ends exactly when the
// next starts) can share a lane.
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
export function segKindColor(kind) {
  return KIND_FAMILIES.has(kind) ? kind : 'other';
}

// pctOfAgent returns a segment's share of its agent's total runtime as a rounded
// integer percent (0..100) — the "(xx% of agent)" clause the richer tooltip adds.
// A zero/negative part is 0; a zero/invalid whole returns null so the caller drops
// the clause rather than dividing by zero.
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
export function criticalSpans(rows) {
  const list = Array.isArray(rows) ? rows : [];
  const agents = {};
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
    const entry = {};
    if (longest) entry.longestRef = longest.refKey;
    if (whale) entry.whaleRef = whale.refKey;
    if (longest || whale) agents[row.key] = entry;
    if (longest && (!allLongest || longest.durationMs > allLongest.durationMs)) allLongest = longest;
    if (whale && (!allWhale || whale.cost_usd > allWhale.cost_usd)) allWhale = whale;
  }
  const session = {};
  if (allLongest) session.longestRef = allLongest.refKey;
  if (allWhale) session.whaleRef = allWhale.refKey;
  return { session, agents };
}

// toolMix aggregates an array of agent nodes (already scoped by the caller —
// whole graph, one session, or one agent) into per-kind tool totals for the
// Insights "Tool mix" panel: count · wall-clock · cost, grouped by ToolKind.
// Pure and scope-agnostic. Returns [] for a null/empty input.
export function toolMix(agents) {
  if (!Array.isArray(agents)) return [];
  const byKind = new Map();
  for (const a of agents) {
    for (const step of (a && a.steps) || []) {
      const tools = (step && step.tools) || [];
      // Cost lives only at the turn level, so spread it across the turn's tools
      // by call-share — the honest split that sums back to the step cost. A
      // tool-less turn (pure text) contributes no per-kind cost, by design.
      const stepCalls = tools.reduce((n, t) => n + ((t && t.count) || (t ? 1 : 0)), 0);
      const stepCost = (step && step.cost_usd) || 0;
      for (const t of tools) {
        if (!t) continue;
        const kind = t.kind || 'other';
        const calls = t.count || 1;
        const row = byKind.get(kind) || { kind, count: 0, durationMs: 0, costUSD: 0 };
        row.count += calls;
        const start = parseTime(t.started_at);
        const end = parseTime(t.ended_at);
        if (!Number.isNaN(start) && !Number.isNaN(end) && end > start) row.durationMs += end - start;
        if (stepCalls > 0) row.costUSD += stepCost * (calls / stepCalls);
        byKind.set(kind, row);
      }
    }
  }
  return [...byKind.values()].sort((a, b) =>
    b.costUSD - a.costUSD || b.count - a.count || a.kind.localeCompare(b.kind));
}

// percentiles returns one value per requested point.
export function percentiles(values, ps) {
  const points = Array.isArray(ps) ? ps : [];
  const nums = (Array.isArray(values) ? values : [])
    .filter(v => typeof v === 'number' && !Number.isNaN(v))
    .sort((a, b) => a - b);
  if (nums.length === 0) return points.map(() => NaN);
  return points.map(p => {
    const rank = (Math.max(0, Math.min(100, p)) / 100) * (nums.length - 1);
    const lo = Math.floor(rank), hi = Math.ceil(rank);
    if (lo === hi) return nums[lo];
    return nums[lo] + (rank - lo) * (nums[hi] - nums[lo]);
  });
}

// DURATION_EDGES are the default upper bounds (ms) for the latency histogram —
// a log-ish progression tuned for tool wall-clock (sub-100ms reads up to
// multi-minute execs). Each edge opens a [prev, edge) bucket; a trailing
// [lastEdge, ∞) bucket catches the long tail.
export const DURATION_EDGES = [100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000];

// durationHistogram buckets per-tool wall-clock.
export function durationHistogram(agents, opts = {}) {
  const edges = (Array.isArray(opts.edges) && opts.edges.length)
    ? opts.edges.slice().sort((a, b) => a - b)
    : DURATION_EDGES;
  const buckets = [];
  let lo = 0;
  for (const e of edges) { buckets.push({ lo, hi: e, count: 0, kinds: [] }); lo = e; }
  buckets.push({ lo, hi: Infinity, count: 0, kinds: [] });
  const values = [];
  for (const a of (Array.isArray(agents) ? agents : [])) {
    for (const step of (a && a.steps) || []) {
      for (const t of (step && step.tools) || []) {
        if (!t) continue;
        const start = parseTime(t.started_at), end = parseTime(t.ended_at);
        if (Number.isNaN(start) || Number.isNaN(end) || end <= start) continue;
        const d = end - start;
        const b = buckets.find(bk => d < bk.hi);
        b.count += 1;
        const kind = t.kind || 'other';
        if (!b.kinds.includes(kind)) b.kinds.push(kind);
        values.push(d);
      }
    }
  }
  return { buckets, values };
}

// costPareto ranks an agent array's costed turns (steps with positive cost_usd)
// worst-first for the Insights "Cost Pareto" panel and measures how concentrated
// the spend is. Cost lives only at the turn level (same convention toolMix uses),
// so a turn is the honest unit. Pure and scope-agnostic — the caller passes the
// already-scoped agents (whole graph, one session, or one agent). Returns:
//   { total, count, rows, headline }
//   total      — summed cost over every costed turn
//   count      — how many costed turns there are
//   rows       — top-N turns (opts.topN, default 10), each
//                { rank (1-based), cost, agentLabel, share, cumShare }; share is
//                the turn's fraction of total, cumShare the running cumulative
//                fraction over the FULL ranking (so the curve is honest even
//                though rows are truncated)
//   headline   — the concentration callout, null when there are no costed turns:
//                { turnsPct, turnCount, spendShare, thresholdCost } — the top
//                `turnsPct`% of turns (rounded up, ≥1) account for spendShare of
//                spend; thresholdCost is the cheapest turn still inside that slice
//                (the minCostUSD cut that isolates the whales).
export function costPareto(agents, opts = {}) {
  const topN = opts.topN != null ? opts.topN : 10;
  const headlinePct = opts.headlinePct != null ? opts.headlinePct : 10;
  const turns = [];
  let total = 0;
  for (const a of (Array.isArray(agents) ? agents : [])) {
    for (const step of (a && a.steps) || []) {
      const cost = (step && step.cost_usd) || 0;
      if (!(cost > 0)) continue;
      turns.push({ cost, agentLabel: agentLabel(a) });
      total += cost;
    }
  }
  turns.sort((x, y) => y.cost - x.cost);
  let cumCost = 0;
  const rows = turns.map((t, i) => {
    cumCost += t.cost;
    return {
      rank: i + 1, cost: t.cost, agentLabel: t.agentLabel,
      share: t.cost / total, cumShare: cumCost / total,
    };
  });
  // Headline: the top `headlinePct`% of turns (rounded UP, at least one) and the
  // share of spend they concentrate. thresholdCost is the cheapest turn still
  // inside that slice — the minCostUSD cut that isolates the whales.
  let headline = null;
  if (rows.length) {
    const turnCount = Math.max(1, Math.ceil(rows.length * headlinePct / 100));
    const edge = rows[turnCount - 1];
    headline = { turnsPct: headlinePct, turnCount, spendShare: edge.cumShare, thresholdCost: edge.cost };
  }
  return { total, count: turns.length, rows: rows.slice(0, topN), headline };
}

// agentElapsedMs returns how long an agent has been active. A running
// agent counts up to nowMs (so a card's timer advances on each ~2s
// refetch); a done agent reports its fixed (end - start). Never negative.
export function agentElapsedMs(agent, nowMs = Date.now()) {
  const { start, end } = agentSpan(agent);
  if (Number.isNaN(start)) return 0;
  const until = agent && agent.status === 'running' ? nowMs : end;
  if (Number.isNaN(until)) return 0;
  return Math.max(0, until - start);
}

// formatElapsed renders a millisecond duration as a compact human
// string: "5s", "1m 5s", "2m", "1h", "1h 5m". Tuned for agent runtimes
// (seconds to hours) rather than the sub-second tool gaps the Sessions
// view formats.
export function formatElapsed(ms) {
  if (!ms || ms <= 0) return '0s';
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) {
    const s = sec % 60;
    return s ? `${m}m ${s}s` : `${m}m`;
  }
  const h = Math.floor(m / 60);
  const mm = m % 60;
  return mm ? `${h}h ${mm}m` : `${h}h`;
}

// graphStats walks the whole graph for the sidebar metric: how many
// sessions, agents (main + sub), and currently-running agents. Tolerates
// a null graph / missing children.
export function graphStats(graph) {
  let agents = 0, running = 0;
  const sessions = (graph && graph.sessions) || [];
  for (const s of sessions) {
    for (const a of flattenSession(s)) {
      agents++;
      if (a && a.status === 'running') running++;
    }
  }
  return { sessions: sessions.length, agents, running };
}

// agentLabel is the short human name for an agent: "main" for the session
// agent, otherwise its sub-agent type ("Explore", "general-purpose", …),
// falling back to "subagent" when the type is missing.
export function agentLabel(a) {
  if (!a) return '';
  return a.kind === 'main' ? 'main' : (a.agent_type || 'subagent');
}

// buildEventFeed flattens the whole graph into a single reverse-chronological
// stream of discrete events — the "tail -f" the Feed lens renders.
// Three kinds:
//   tool  — an agent invoked a tool (carries name/detail/input/status, the
//           stepIndex/toolIndex that locate it for selection, and the parent
//           step's cost_usd/durationMs for a per-row cost·duration metric)
//   spawn — a sub-agent began (carries its description)
//   done  — an agent finished (only for status==="done"; carries step count)
// Each event keeps its sessionId and agentIndex (0=main, 1.. children in
// flatten order) so the view can color and group consistently with the rest
// of the tab. Events are emitted in a deterministic order, then stably sorted
// newest-first; `limit` caps the result (0 = unlimited). DOM-free + pure so
// it's unit-testable and a refetch can diff against the previous feed.
export function buildEventFeed(graph, { limit = 200 } = {}) {
  const sessions = (graph && graph.sessions) || [];
  const events = [];
  for (const s of sessions) {
    const sid = (s && s.session_id) || '';
    flattenSession(s).forEach((a, idx) => {
      if (!a) return;
      const label = agentLabel(a);
      // A sub-agent's birth is an event in its own right.
      if (a.kind !== 'main') {
        const st = parseTime(a.started_at);
        if (!Number.isNaN(st)) {
          events.push({
            kind: 'spawn', t: st, sessionId: sid, agentIndex: idx,
            agentLabel: label, description: a.description || '',
          });
        }
      }
      (a.steps || []).forEach((step, stepIndex) => {
        const t = parseTime(step && step.timestamp);
        if (Number.isNaN(t)) return;
        (step.tools || []).forEach((tool, toolIndex) => {
          if (!tool) return;
          events.push({
            kind: 'tool', t, sessionId: sid, agentIndex: idx, agentLabel: label,
            tool: tool.name || '', toolKind: tool.kind || '', detail: tool.detail || '',
            input: tool.input || '', status: tool.status || '',
            output: tool.output || '',
            stepIndex, toolIndex,
            cost_usd: step.cost_usd || 0, durationMs: step.duration_ms || 0,
          });
        });
      });
      // Only finished agents get a done event — a running agent hasn't
      // finished, so emitting one would lie about its state.
      if (a.status === 'done') {
        const et = parseTime(a.ended_at);
        if (!Number.isNaN(et)) {
          events.push({
            kind: 'done', t: et, sessionId: sid, agentIndex: idx,
            agentLabel: label, steps: (a.steps || []).length, cost_usd: a.cost_usd || 0,
          });
        }
      }
    });
  }
  // Stable descending sort: Array.sort is stable in modern engines, so
  // equal-timestamp events keep the deterministic emit order above.
  events.sort((x, y) => y.t - x.t);
  return limit > 0 ? events.slice(0, limit) : events;
}

// timelineBounds returns the [startMs, endMs] time window a Gantt timeline
// should span over a set of agents. Each agent's effective end is "now" if
// it's still running, else its parseable ended_at, else (NaN end) its own
// start — so a malformed/instant agent collapses to a zero-width point
// rather than poisoning the window. startMs is the earliest parseable
// start; endMs is the latest of all starts and effective ends, clamped so
// endMs >= startMs. Null when no agent has a parseable start.
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
export function playheadBounds(graph, nowMs) {
  const sessions = (graph && graph.sessions) || [];
  const allAgents = sessions.flatMap(flattenSession);
  return timelineBounds(allAgents, nowMs);
}

// playheadStats counts how many agents across every session are pending /
// active / done as of instant T, classifying each via agentPhaseAt. Zeros for
// a null/empty graph. Before the earliest start everything is pending; after
// the latest end everything is done.
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

// buildTimeline computes a per-session horizontal Gantt layout: one row per
// agent (main first at depth 0, each sub-agent at depth 1), each bar placed on
// a real time axis spanning the session's lifetime, plus axis ticks and a
// "now" line x. Pure geometry — no DOM and no Date.now(); the caller passes
// nowMs so a running agent's bar (and the now-line) advance on refetch. The
// chart floors to the host width but widens (→ horizontal scroll) for long
// sessions via minPxPerMs. Rows stay 1:1 with flattenSession order/index so a
// row's refKey index aligns with the rest of the tab; an agent with an
// unparseable start just gets a left-edge sliver bar rather than being dropped.
// Returns the empty layout when the session has no agents or no parseable times.
export function buildTimeline(session, opts = {}) {
  const {
    hostW = 800, rowH = 24, axisH = 20, labelW = 130, pad = 8,
    nowMs = 0, minBlock = 3, minPxPerMs = 0.0012, indent = 12, tickCount = 5,
  } = opts;
  const agents = flattenSession(session);
  const sid = (session && session.session_id) || '';
  const depth = a => (a && a.kind === 'main' ? 0 : 1);

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

  const rows = agents.map((a, i) => {
    const start = parseTime(a.started_at);
    const effEnd = a.status === 'running'
      ? nowMs
      : (Number.isNaN(parseTime(a.ended_at)) ? start : parseTime(a.ended_at));
    const bar = agentBar({ start, end: effEnd }, scale);
    const d = depth(a);
    return {
      key: `${sid}#${i}`, rowIndex: i, depth: d,
      label: agentLabel(a), kind: a.kind || '', status: a.status || '',
      running: a.status === 'running',
      cost_usd: a.cost_usd || 0, steps: (a.steps || []).length,
      x: chartX + bar.x, w: bar.width,
      y: axisH + i * rowH, h: rowH,
      labelX: pad + d * indent,
      segments: toolSegments(a, { scale, chartX, sessionId: sid, agentIndex: i, effEnd }),
      idleSegments: idleSegments(a, { scale, chartX, sessionId: sid, agentIndex: i, effEnd }),
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
  const height = axisH + agents.length * rowH + pad;

  return {
    sessionId: sid, startMs, endMs, span,
    chartX, chartW, chartHostW, contentW, width: contentW, height,
    nowX, rows, ticks,
  };
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
// Rows stay 1:1 with flattenSession order so selection indices still line up.
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
  const rows = agents.map((a, i) => {
    const start = parseTime(a.started_at);
    const phase = agentPhaseAt(a, T);
    if (phase === 'pending') {
      return { ...base.rows[i], w: 0, phase: 'pending', running: false, segments: [], idleSegments: [] };
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
      agentIndex: i, effEnd, until: clampEnd,
    };
    const segments = toolSegments(a, segOpts);
    return {
      ...base.rows[i], w: bar.width, phase, running: phase === 'active',
      segments, idleSegments: idleSegments(a, segOpts),
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

// baseName returns the last path segment of a filesystem path (the
// project folder name for a session cwd). Trailing slashes are ignored;
// a null/empty/segmentless input yields ''.
export function baseName(path) {
  if (!path) return '';
  const parts = String(path).split('/').filter(Boolean);
  return parts.length ? parts[parts.length - 1] : '';
}

// agentTokens sums an agent's token tuple into a flat, 0-safe shape.
// Reads the Go-marshalled field names (Tokens has no json tags), folding
// the 5m + 1h cache-creation buckets into a single cacheWrite.
export function agentTokens(agent) {
  const t = (agent && agent.tokens) || {};
  const input = t.InputTokens || 0;
  const output = t.OutputTokens || 0;
  const cacheWrite = (t.CacheCreate5mTokens || 0) + (t.CacheCreate1hTokens || 0);
  const cacheRead = t.CacheReadTokens || 0;
  return { input, output, cacheWrite, cacheRead, total: input + output + cacheWrite + cacheRead };
}

// refKey serializes a selection ref to a stable string. Forms:
//   agent  "<sessionId>#<agentIndex>"
//   step   "<sessionId>#<agentIndex>.<stepIndex>"
//   tool   "<sessionId>#<agentIndex>.<stepIndex>:<toolIndex>"
// A tool form always nests a step, so a toolIndex without a stepIndex is
// dangling — it degrades to the agent form. Garbage (no sessionId /
// non-numeric agentIndex) yields ''.
export function refKey(ref) {
  if (!ref || !ref.sessionId || typeof ref.agentIndex !== 'number') return '';
  let key = `${ref.sessionId}#${ref.agentIndex}`;
  if (ref.stepIndex == null) return key; // no step → drop any dangling tool
  key += `.${ref.stepIndex}`;
  if (ref.toolIndex != null) key += `:${ref.toolIndex}`;
  return key;
}

// parseRefKey is refKey's inverse: it recovers { sessionId, agentIndex,
// stepIndex, toolIndex, type } from a key string. Session ids are UUIDs
// (no '#'), so the LAST '#' splits sessionId from the index tail.
// stepIndex/toolIndex are numbers or null; malformed input → null.
export function parseRefKey(key) {
  if (typeof key !== 'string' || !key) return null;
  const hash = key.lastIndexOf('#');
  if (hash < 1) return null;
  const sessionId = key.slice(0, hash);
  const tail = key.slice(hash + 1);
  // tail = "<agent>" | "<agent>.<step>" | "<agent>.<step>:<tool>"
  const [agentPart, rest] = splitOnce(tail, '.');
  const agentIndex = toInt(agentPart);
  if (agentIndex == null) return null;
  let stepIndex = null, toolIndex = null, type = 'agent';
  if (rest != null) {
    const [stepPart, toolPart] = splitOnce(rest, ':');
    stepIndex = toInt(stepPart);
    if (stepIndex == null) return null;
    type = 'step';
    if (toolPart != null) {
      toolIndex = toInt(toolPart);
      if (toolIndex == null) return null;
      type = 'tool';
    }
  }
  return { sessionId, agentIndex, stepIndex, toolIndex, type };
}

// splitOnce splits at the first occurrence of sep, returning [head, tail]
// where tail is null when sep is absent (so '' after sep is distinguished).
function splitOnce(s, sep) {
  const i = s.indexOf(sep);
  return i === -1 ? [s, null] : [s.slice(0, i), s.slice(i + 1)];
}

// toInt parses a non-negative integer string, or null if not numeric.
function toInt(s) {
  if (!/^\d+$/.test(s)) return null;
  return Number(s);
}

// deepestRefs reduces a collection of matched refKeys (a Set or array) to its
// leaves: every ref with no strictly-deeper matched descendant in the input.
// It is the O(n) replacement for the O(n²) leaf scan
//   arr.filter(r => !arr.some(o => o !== r &&
//     (o.startsWith(r + '.') || o.startsWith(r + ':'))))
// Rather than scan pairwise (which also mis-flags agent-index prefixes like
// "s#1" vs "s#12.0"), it derives each ref's ancestor refKeys via parseRefKey /
// refKey and marks any ancestor that is itself in the set as a non-leaf:
//   tool s#a.t:u → ancestors step s#a.t and agent s#a
//   step s#a.t   → ancestor agent s#a
//   agent s#a    → no ancestors
// Input order is preserved; duplicates collapse (input is treated as a set).
export function deepestRefs(refs) {
  const set = refs instanceof Set ? refs : new Set(refs);
  const nonLeaf = new Set();
  for (const ref of set) {
    const p = parseRefKey(ref);
    if (!p) continue;
    const ancestors = [];
    if (p.type === 'tool') {
      ancestors.push(refKey({ sessionId: p.sessionId, agentIndex: p.agentIndex, stepIndex: p.stepIndex }));
      ancestors.push(refKey({ sessionId: p.sessionId, agentIndex: p.agentIndex }));
    } else if (p.type === 'step') {
      ancestors.push(refKey({ sessionId: p.sessionId, agentIndex: p.agentIndex }));
    }
    for (const anc of ancestors) {
      if (set.has(anc)) nonLeaf.add(anc);
    }
  }
  return [...set].filter(r => !nonLeaf.has(r));
}

// defaultRef is the root selection: the first session (array order) that
// has at least one agent, pinned to its main agent (agentIndex 0). Skips
// leading empty sessions; null when no session has any agent.
export function defaultRef(graph) {
  const sessions = (graph && graph.sessions) || [];
  for (const s of sessions) {
    if (flattenSession(s).length >= 1) {
      return { sessionId: (s && s.session_id) || '', agentIndex: 0 };
    }
  }
  return null;
}

// resolveRef looks up a (possibly stale) selection ref against the graph,
// accepting either a ref object or a refKey string. Returns the resolved
// session/agent/step/tool plus a degraded `type`: a step or tool whose
// index is out of range silently drops to the nearest valid level (so a
// stale tool ref becomes a 'step' or 'agent' rather than throwing). Null
// when the session or agent can't be found — the caller falls back to
// defaultRef.
export function resolveRef(graph, ref) {
  const r = typeof ref === 'string' ? parseRefKey(ref) : ref;
  if (!r || !r.sessionId) return null;
  const sessions = (graph && graph.sessions) || [];
  const session = sessions.find(s => s && s.session_id === r.sessionId);
  if (!session) return null;
  const agent = flattenSession(session)[r.agentIndex];
  if (!agent) return null;

  const step = (r.stepIndex != null && agent.steps) ? (agent.steps[r.stepIndex] || null) : null;
  const tool = (step && r.toolIndex != null && step.tools) ? (step.tools[r.toolIndex] || null) : null;
  const type = tool ? 'tool' : step ? 'step' : 'agent';

  return {
    type, session,
    agent, agentIndex: r.agentIndex,
    step, stepIndex: step ? r.stepIndex : null,
    tool, toolIndex: tool ? r.toolIndex : null,
  };
}

// buildDrawerPayload assembles the full audit object the detail drawer
// renders for a selection. Resolves the (possibly stale) ref, then emits
// a flat record where EVERY field is always present — empty string / 0 /
// null for the levels that don't apply — so the drawer can show-but-
// collapse empty sections without conditionals. For a tool the turn's
// reasoning (thinking/text/model/cost/duration) is inherited from its
// PARENT step. Null when the ref can't resolve.
//
// `fullByTool` (optional) keys loaded-full I/O by tool id (the same value
// exposed as payload `toolId`); each value is `{ input?, output? }`. For a
// TOOL ref with a matching entry, a string `input`/`output` overrides the
// truncated snippet so the drawer keeps expanded content sticky across live
// re-renders; a field absent from the entry keeps its snippet. Ignored for
// non-tool refs and when null/undefined (the default — existing callers).
export function buildDrawerPayload(graph, ref, fullByTool = null) {
  const r = resolveRef(graph, ref);
  if (!r) return null;
  const { type, session, agent, agentIndex, step, stepIndex, tool, toolIndex } = r;

  // A tool's snippet I/O can be overridden by a previously loaded-full
  // entry so the drawer keeps expanded content sticky across re-renders.
  const full = (type === 'tool' && fullByTool) ? fullByTool[tool.id] : null;
  const toolInput = typeof (full && full.input) === 'string' ? full.input : (tool && tool.input) || '';
  const toolOutput = typeof (full && full.output) === 'string' ? full.output : (tool && tool.output) || '';

  // Title/status vary by level.
  let title = '', status = '';
  if (type === 'agent') { title = agentLabel(agent); status = agent.status || ''; }
  else if (type === 'step') { title = `Turn ${stepIndex + 1}`; status = ''; }
  else { title = tool.name || ''; status = tool.status || ''; }

  // Turn-level fields come from the step (for a tool, its parent step).
  const thinking = step ? (step.thinking || '') : '';
  const text = step ? (step.text || '') : '';
  const model = step ? (step.model || '') : '';

  // Tokens roll up to the agent for an agent ref and to the turn for a step
  // ref (agentTokens just reads a `.tokens` tuple, so it works on a step too).
  // A tool ref inherits its parent turn's tokens — usage is a turn-level
  // total, the same way cost/model/duration are inherited from the step.
  const tokens = type === 'agent'
    ? agentTokens(agent)
    : step
      ? agentTokens(step)
      : { input: 0, output: 0, cacheWrite: 0, cacheRead: 0, total: 0 };
  const cost_usd = type === 'agent' ? (agent.cost_usd || 0) : (step ? (step.cost_usd || 0) : 0);
  let durationMs;
  if (type === 'agent') {
    const { start, end } = agentSpan(agent);
    durationMs = Number.isNaN(end - start) ? 0 : Math.max(0, end - start);
  } else {
    durationMs = step ? (step.duration_ms || 0) : 0;
  }

  return {
    type,
    refKey: refKey({ sessionId: session.session_id, agentIndex, stepIndex, toolIndex }),
    project: baseName(session.cwd),
    cwd: session.cwd || '',
    sessionId: session.session_id || '',
    agentLabel: agentLabel(agent),
    agentKind: agent.kind || '',
    description: agent.kind !== 'main' ? (agent.description || '') : '',
    title,
    // kind is the normalized ToolKind enum (Phase 1) for tools, driving the
    // colored kind badge; 'step'/'agent' are pseudo-kinds for turns/agents.
    // The raw tool name lives in `title`, not here.
    kind: type === 'tool' ? (tool.kind || 'other') : (type === 'step' ? 'step' : 'agent'),
    status,
    detail: type === 'tool' ? (tool.detail || '') : '',
    // toolId lets the drawer's "show full" action fetch the untruncated I/O
    // back from disk (serve mode). Only a tool ref has one.
    toolId: type === 'tool' ? (tool.id || '') : '',
    input: type === 'tool' ? toolInput : '',
    output: type === 'tool' ? toolOutput : '',
    // spawned: the sub-agent rollup on an Agent tool_use, enriched with a
    // childRef — the navigable refKey of the sub-agent it launched — so the
    // drawer can link straight to it. null for non-tool refs, plain tools, and
    // spawns whose sub-agent isn't in the snapshot (childRef '').
    spawned: spawnDrawerInfo(type, tool, session),
    // tools: the turn's tool calls as navigable rows, so a turn drawer lists
    // every tool (and never renders as an empty slab). Empty for non-steps.
    tools: stepToolRows(type, step, session, agentIndex, stepIndex),
    thinking, text, model,
    tokens, cost_usd, durationMs,
    stepCount: (agent.steps || []).length,
  };
}

// stepToolRows builds the navigable tool-call rows for a step (turn) ref: one
// per tool_use, each carrying the refKey of the exact tool so the drawer can
// click through to its Input/Output. Empty for any non-step ref.
function stepToolRows(type, step, session, agentIndex, stepIndex) {
  if (type !== 'step' || !step || !Array.isArray(step.tools)) return [];
  return step.tools.map((tu, i) => ({
    name: tu.name || '',
    kind: tu.kind || 'other',
    detail: tu.detail || '',
    status: tu.status || '',
    refKey: refKey({ sessionId: session.session_id, agentIndex, stepIndex, toolIndex: i }),
  }));
}

// spawnDrawerInfo builds the drawer's spawn rollup for a tool ref: the
// backend SpawnRollup plus childRef, the navigable refKey of the sub-agent it
// launched (resolved via spawnTargetIndex). Returns null unless this is a tool
// ref that actually spawned a sub-agent.
function spawnDrawerInfo(type, tool, session) {
  if (type !== 'tool' || !tool || !tool.spawned) return null;
  const idx = spawnTargetIndex(session, tool.spawned.agent_ref);
  const childRef = idx == null ? '' : refKey({ sessionId: session.session_id, agentIndex: idx });
  return { ...tool.spawned, childRef };
}

// looksTruncated reports whether a bounded snippet was cut short — the parse
// layer appends a "…" (U+2026) marker when it truncates a tool input/output
// to its rune cap. The drawer uses this to decide whether to offer a "show
// full" toggle, so it doesn't promise more content than exists.
export function looksTruncated(s) {
  return typeof s === 'string' && s.endsWith('…');
}

// nodeOf builds one flow-graph node rect from an agent payload.
function nodeOf(a, key, kind, x, y, w, h) {
  return {
    key, kind, label: agentLabel(a),
    status: (a && a.status) || '', description: (a && a.description) || '',
    cost_usd: (a && a.cost_usd) || 0, steps: ((a && a.steps) || []).length,
    x, y, w, h,
  };
}

// ── trace filter (Phase 2) ──────────────────────────────────────────────────

// specActive reports whether a filter spec constrains anything — true iff at
// least one dimension is set. The UI uses it to decide whether to dim rows at
// all (an inactive spec means "show everything", not "match nothing").
export function specActive(spec) {
  const s = spec || {};
  return !!(
    (typeof s.text === 'string' && s.text.trim()) ||
    (Array.isArray(s.kinds) && s.kinds.length) ||
    s.errorsOnly ||
    (s.minDurationMs > 0) ||
    (s.minCostUSD > 0) ||
    (typeof s.agentType === 'string' && s.agentType)
  );
}

// filterTrace returns the Set of refKeys (the "sid#ai" / "sid#ai.si" /
// "sid#ai.si:ti" scheme) matching a filter spec, so the Agents lenses can dim
// the rest. Empty Set when the spec is inactive.
//
// Dimensions AND together. Matching runs bottom-up so a parent ref is included
// whenever any descendant matches (the tree stays navigable). kinds/errorsOnly
// are tool-level *gates*: when either is active, only a matching tool (and its
// ancestors) can be included — a step or agent can't match on its own. The
// other dimensions (text/duration/cost/agentType) can also match a step (e.g. a
// slow step with no tools) or an agent (e.g. an expensive sub-agent) directly.
export function filterTrace(graph, spec) {
  const out = new Set();
  if (!specActive(spec)) return out;

  const s = spec || {};
  const text = (typeof s.text === 'string' ? s.text.trim() : '').toLowerCase();
  const kinds = Array.isArray(s.kinds) ? s.kinds : [];
  const errorsOnly = !!s.errorsOnly;
  const minDurationMs = s.minDurationMs > 0 ? s.minDurationMs : 0;
  const minCostUSD = s.minCostUSD > 0 ? s.minCostUSD : 0;
  const agentType = typeof s.agentType === 'string' ? s.agentType : '';

  const pText = fields => !text || fields.some(f => String(f || '').toLowerCase().includes(text));
  const pDur = step => !minDurationMs || (step.duration_ms || 0) >= minDurationMs;
  const pCostStep = step => !minCostUSD || (step.cost_usd || 0) >= minCostUSD;
  const pCostAgent = agent => !minCostUSD || (agent.cost_usd || 0) >= minCostUSD;
  const pType = agent => !agentType || agent.agent_type === agentType;
  const toolGated = kinds.length > 0 || errorsOnly;

  const sessions = (graph && graph.sessions) || [];
  for (const session of sessions) {
    const sid = (session && session.session_id) || '';
    flattenSession(session).forEach((agent, ai) => {
      let agentMatched = false;
      (agent.steps || []).forEach((step, si) => {
        let stepMatched = false;
        (step.tools || []).forEach((tool, ti) => {
          if (!tool) return;
          const toolMatch =
            pText([tool.name, tool.detail, tool.input, tool.output, step.thinking, step.text, agent.description, agent.agent_type]) &&
            (!kinds.length || kinds.includes(tool.kind)) &&
            (!errorsOnly || tool.status === 'error') &&
            pDur(step) && pCostStep(step) && pType(agent);
          if (toolMatch) {
            out.add(`${sid}#${ai}.${si}:${ti}`);
            stepMatched = true;
          }
        });
        if (!stepMatched && !toolGated) {
          stepMatched = pText([step.thinking, step.text, agent.description, agent.agent_type]) &&
            pDur(step) && pCostStep(step) && pType(agent);
        }
        if (stepMatched) {
          out.add(`${sid}#${ai}.${si}`);
          agentMatched = true;
        }
      });
      if (!agentMatched && !toolGated && !minDurationMs) {
        agentMatched = pText([agent.description, agent.agent_type]) && pCostAgent(agent) && pType(agent);
      }
      if (agentMatched) out.add(`${sid}#${ai}`);
    });
  }
  return out;
}

// detectRetries finds repeated tool calls where an earlier attempt errored,
// returning a Map from each retry's agent-relative coord ("si:ti") to
// { attempt, ofRef } — attempt is the 1-based ordinal of the call within its
// group, ofRef the coord of the group's first call. Tool calls are grouped by
// (kind, name, detail) and walked in step-then-tool order (steps are already
// chronological). A call is a retry only when some earlier call in its group
// had status "error", so a clean repeat (ok → ok) is never flagged. Coords are
// the suffix of the full tool refKey, composable via `${sid}#${ai}.${coord}`.
export function detectRetries(agent) {
  const out = new Map();
  const groups = new Map(); // groupKey -> { firstRef, count, sawError }
  const steps = (agent && agent.steps) || [];
  steps.forEach((step, si) => {
    (step.tools || []).forEach((tool, ti) => {
      if (!tool) return;
      const coord = `${si}:${ti}`;
      const groupKey = `${tool.kind || ''}\u0000${tool.name || ''}\u0000${tool.detail || ''}`;
      let g = groups.get(groupKey);
      if (!g) {
        g = { firstRef: coord, count: 0, sawError: false };
        groups.set(groupKey, g);
      }
      g.count += 1;
      if (g.sawError) out.set(coord, { attempt: g.count, ofRef: g.firstRef });
      if (tool.status === 'error') g.sawError = true;
    });
  });
  return out;
}
