// Shared graph/model pure helpers for the Agents tab: session flattening,
// ref (de)serialization and resolution, token folding, elapsed/format math,
// and the pane-width clamps. Every other agents-*-logic module builds on
// these. Split out of agents-logic.js (which now re-exports everything as a
// facade); unit-tested under `node --test` in jstest/.

// originClass partitions a session's entrypoint into 'sdk' vs 'interactive'.
// Headless/SDK runs report "sdk-cli" (or other "sdk*" origins); everything
// else — interactive "cli", an unknown, or a missing value — is
// 'interactive'. Defaulting unknown to interactive keeps the SDK set a
// conservative "definitely headless" subset, never a catch-all.
export function originClass(ep) {
  return typeof ep === 'string' && ep.toLowerCase().startsWith('sdk')
    ? 'sdk'
    : 'interactive';
}

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

// currentToolKind returns the normalized ToolKind ("exec"/"read"/…) of an
// agent's most recent tool call — the last tool of the last step that has any,
// mirroring the backend's lastToolName but yielding the kind so a running-agent
// card can reuse the feed's badge + color. '' when there's no tool or no kind.
export function currentToolKind(agent) {
  const steps = (agent && agent.steps) || [];
  for (let i = steps.length - 1; i >= 0; i--) {
    const tools = (steps[i] && steps[i].tools) || [];
    if (tools.length) return (tools[tools.length - 1] || {}).kind || '';
  }
  return '';
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
