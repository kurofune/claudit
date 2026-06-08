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

// agentLabel is the short human name for an agent: "main" for the session
// agent, otherwise its sub-agent type ("Explore", "general-purpose", …),
// falling back to "subagent" when the type is missing.
export function agentLabel(a) {
  if (!a) return '';
  return a.kind === 'main' ? 'main' : (a.agent_type || 'subagent');
}

// buildEventFeed flattens the whole graph into a single reverse-chronological
// stream of discrete events — the "tail -f" the Mission Control view renders.
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
            tool: tool.name || '', detail: tool.detail || '',
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
export function buildDrawerPayload(graph, ref) {
  const r = resolveRef(graph, ref);
  if (!r) return null;
  const { type, session, agent, agentIndex, step, stepIndex, tool, toolIndex } = r;

  // Title/status vary by level.
  let title = '', status = '';
  if (type === 'agent') { title = agentLabel(agent); status = agent.status || ''; }
  else if (type === 'step') { title = `Turn ${stepIndex + 1}`; status = ''; }
  else { title = tool.name || ''; status = tool.status || ''; }

  // Turn-level fields come from the step (for a tool, its parent step).
  const thinking = step ? (step.thinking || '') : '';
  const text = step ? (step.text || '') : '';
  const model = step ? (step.model || '') : '';

  // Tokens/cost/duration roll up to the agent for an agent ref, else to
  // the step (tool inherits the step's).
  const tokens = type === 'agent'
    ? agentTokens(agent)
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
    kind: type === 'tool' ? (tool.name || '') : (type === 'step' ? 'step' : 'agent'),
    status,
    detail: type === 'tool' ? (tool.detail || '') : '',
    input: type === 'tool' ? (tool.input || '') : '',
    output: type === 'tool' ? (tool.output || '') : '',
    thinking, text, model,
    tokens, cost_usd, durationMs,
    stepCount: (agent.steps || []).length,
  };
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
