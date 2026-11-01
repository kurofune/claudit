// @ts-check
// Graph-model helpers the session header reads its one-session graph
// with: timestamp parsing, session flattening, agent labels and refKey
// parsing.

/** @import { AgentSession, AgentNode } from './api-types.js' */

/**
 * parseRefKey's output: a ref plus the resolved depth.
 * @typedef {Object} ParsedRef
 * @property {string} sessionId
 * @property {number} agentIndex
 * @property {number|null} stepIndex
 * @property {number|null} toolIndex
 * @property {'agent'|'step'|'tool'} type
 */

// parseTime coerces a payload timestamp to epoch milliseconds. Numbers
// pass through (tests use them); ISO-8601 strings parse via Date.parse;
// anything unparseable is NaN so callers can filter it out.
/** @param {*} ts @returns {number} */
export function parseTime(ts) {
  if (typeof ts === 'number') return ts;
  if (typeof ts !== 'string') return NaN;
  const t = Date.parse(ts);
  return Number.isNaN(t) ? NaN : t;
}

// flattenSession returns a session's agents in display order: the main
// agent first, then its sub-agents (already start-ordered by the
// backend). Tolerates a missing main or children so a half-built /
// null payload doesn't throw.
/** @param {AgentSession|null|undefined} session @returns {AgentNode[]} */
export function flattenSession(session) {
  if (!session) return [];
  const out = [];
  if (session.main) out.push(session.main);
  if (Array.isArray(session.children)) out.push(...session.children);
  return out;
}

// agentLabel is the short human name for an agent: "main" for the session
// agent, otherwise its sub-agent type ("Explore", "general-purpose", …),
// falling back to "subagent" when the type is missing.
/** @param {AgentNode|null|undefined} a @returns {string} */
export function agentLabel(a) {
  if (!a) return '';
  return a.kind === 'main' ? 'main' : (a.agent_type || 'subagent');
}

// parseRefKey reads a detectSignals ref — "<sessionId>#<agentIndex>",
// "…#<agentIndex>.<stepIndex>" or "…#<agentIndex>.<stepIndex>:<toolIndex>" —
// back into { sessionId, agentIndex, stepIndex, toolIndex, type }. Session ids are UUIDs
// (no '#'), so the LAST '#' splits sessionId from the index tail.
// stepIndex/toolIndex are numbers or null; malformed input → null.
/** @param {string|null|undefined} key @returns {ParsedRef|null} */
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
  let stepIndex = null, toolIndex = null;
  /** @type {'agent'|'step'|'tool'} */
  let type = 'agent';
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
/** @param {string} s @param {string} sep @returns {[string, string|null]} */
function splitOnce(s, sep) {
  const i = s.indexOf(sep);
  return i === -1 ? [s, null] : [s.slice(0, i), s.slice(i + 1)];
}

// toInt parses a non-negative integer string, or null if not numeric.
/** @param {string} s @returns {number|null} */
function toInt(s) {
  if (!/^\d+$/.test(s)) return null;
  return Number(s);
}
