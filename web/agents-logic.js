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
