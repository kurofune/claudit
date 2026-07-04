// Trace-filter logic (Phase 2): the spec-activity probe and the bottom-up
// refKey matcher every lens dims against. Split out of agents-logic.js
// (re-exported by the facade).

import { flattenSession } from './agents-model.js';

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
