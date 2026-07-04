// @ts-check
// Feed-lens derivations: the reverse-chronological event feed and the
// running-agent live rows. Split out of agents-logic.js (re-exported by
// the facade).

import {
  agentElapsedMs, agentLabel, agentTokens, currentToolKind,
  flattenSession, parseTime,
} from './agents-model.js';

/** @import { AgentGraph } from './api-types.js' */

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
/**
 * @param {AgentGraph|null|undefined} graph
 * @param {{limit?: number}} [opts]
 * @returns {any[]} newest-first events (tool | spawn | done)
 */
export function buildEventFeed(graph, { limit = 200 } = {}) {
  const sessions = (graph && graph.sessions) || [];
  const events = [];
  for (const s of sessions) {
    const sid = (s && s.session_id) || '';
    const cwd = (s && s.cwd) || '';
    // Session origin, stamped on every event so a Feed row can mark headless
    // (SDK) runs without re-deriving it per row.
    const entrypoint = (s && s.entrypoint) || '';
    flattenSession(s).forEach((a, idx) => {
      if (!a) return;
      const label = agentLabel(a);
      // A sub-agent's birth is an event in its own right.
      if (a.kind !== 'main') {
        const st = parseTime(a.started_at);
        if (!Number.isNaN(st)) {
          events.push({
            kind: 'spawn', t: st, sessionId: sid, cwd, entrypoint, agentIndex: idx,
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
            kind: 'tool', t, sessionId: sid, cwd, entrypoint, agentIndex: idx, agentLabel: label,
            tool: tool.name || '', toolKind: tool.kind || '', detail: tool.detail || '',
            input: tool.input || '', status: tool.status || '',
            output: tool.output || '',
            stepIndex, toolIndex,
            cost_usd: step.cost_usd || 0, durationMs: step.duration_ms || 0,
            tokens: agentTokens(step).total,
          });
        });
      });
      // Only finished agents get a done event — a running agent hasn't
      // finished, so emitting one would lie about its state.
      if (a.status === 'done') {
        const et = parseTime(a.ended_at);
        if (!Number.isNaN(et)) {
          events.push({
            kind: 'done', t: et, sessionId: sid, cwd, entrypoint, agentIndex: idx,
            agentLabel: label, steps: (a.steps || []).length, cost_usd: a.cost_usd || 0,
            tokens: agentTokens(a).total,
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

// buildLiveFeed collects every currently-running agent across the graph into
// the descriptors the Feed lens pins as sticky "live rows" at the top of the
// feed (replacing the old full-width Active-now band). Pure + DOM-free so it's
// unit-testable; the view turns each descriptor into a one-line row.
// Sorted longest-running first — the agent that's been going longest (often the
// one worth watching) sits at the top. Each descriptor carries the agent's
// flatten index (0=main, 1.. children) so the row colors/links consistently
// with the rest of the tab, plus the started_at/status the live timer ticks on.
/** @param {AgentGraph|null|undefined} graph @param {number} [nowMs] */
export function buildLiveFeed(graph, nowMs = Date.now()) {
  const sessions = (graph && graph.sessions) || [];
  const live = [];
  for (const s of sessions) {
    const sid = (s && s.session_id) || '';
    const cwd = (s && s.cwd) || '';
    flattenSession(s).forEach((a, idx) => {
      if (!a || a.status !== 'running') return;
      live.push({
        sessionId: sid, cwd, agentIndex: idx,
        agentLabel: agentLabel(a), kind: a.kind || '',
        description: a.description || '',
        currentTool: a.current_tool || '', currentToolKind: currentToolKind(a),
        startedAt: parseTime(a.started_at), status: 'running',
        elapsedMs: agentElapsedMs(a, nowMs),
        cost_usd: a.cost_usd || 0, tokens: agentTokens(a).total,
        steps: (a.steps || []).length,
      });
    });
  }
  live.sort((x, y) => y.elapsedMs - x.elapsedMs);
  return live;
}
