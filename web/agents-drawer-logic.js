// Drawer payload logic: the flat audit record the shared detail drawer
// renders for a selection, plus its row/spawn sub-builders and the
// truncation probe. Split out of agents-logic.js (re-exported by the
// facade).

import {
  agentLabel, agentSpan, agentTokens, baseName,
  refKey, resolveRef, spawnTargetIndex,
} from './agents-model.js';

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
//
// `fullByTurn` (optional) mirrors `fullByTool` for the turn-level snippets:
// keyed by turn uuid (the same value exposed as payload `turnUuid`); each
// value is `{ thinking?, text? }`. A string field overrides the truncated
// snippet for a STEP ref and for any TOOL ref sharing that uuid (a tool
// inherits its parent turn's thinking/text). A field absent from the entry
// keeps its snippet; ignored when null/undefined.
export function buildDrawerPayload(graph, ref, fullByTool = null, fullByTurn = null) {
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

  // Turn-level fields come from the step (for a tool, its parent step). A
  // previously loaded-full thinking/text (keyed by the step's uuid) overrides
  // the snippet, mirroring the fullByTool mechanism above.
  const fullTurn = (step && step.uuid && fullByTurn) ? fullByTurn[step.uuid] : null;
  const thinking = typeof (fullTurn && fullTurn.thinking) === 'string'
    ? fullTurn.thinking : step ? (step.thinking || '') : '';
  const text = typeof (fullTurn && fullTurn.text) === 'string'
    ? fullTurn.text : step ? (step.text || '') : '';
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
    // turnUuid lets the drawer's "show full" action fetch the untruncated
    // thinking/text for the turn. A tool inherits its parent step's uuid, the
    // same way it inherits thinking/text; '' for agent refs or when absent.
    turnUuid: step ? (step.uuid || '') : '',
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
