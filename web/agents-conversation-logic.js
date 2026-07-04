// @ts-check
// Conversation-lens derivations: the main agent's step timeline sliced by
// user prompt into segments/replies, plus the session-picker summary list.
// Split out of agents-logic.js (re-exported by the facade).

/** @import { AgentSession, AgentStep } from './api-types.js' */

/**
 * One prompt-bounded slice of the main agent's step timeline.
 * @typedef {Object} ConversationSegment
 * @property {string} uuid
 * @property {string} text
 * @property {string} timestamp
 * @property {number} firstStepIndex
 * @property {AgentStep[]} steps
 */

// conversationSegments groups the main agent's step timeline by originating
// user prompt for the Conversation lens. Each segment is one prompt marker
// (uuid/text/timestamp from session.prompts) plus the contiguous slice of
// main.steps it produced, bounded by the next marker's first_step_index. The
// slice's first absolute index is firstStepIndex, so the renderer can address
// each step with the SAME refKey the other lenses use (main = agentIndex 0).
// Returns [] when the main agent has no steps; a session with steps but no
// markers degrades to one prompt-less segment over all of them.
/** @param {AgentSession|null|undefined} session @returns {ConversationSegment[]} */
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
/**
 * @param {ConversationSegment|null|undefined} seg
 * @returns {{stepIndex: number, text: string, timestamp: string, model: string, cost_usd: number}[]}
 */
export function conversationReplies(seg) {
  const steps = (seg && seg.steps) || [];
  const base = (seg && seg.firstStepIndex) || 0;
  const out = [];
  for (let k = 0; k < steps.length; k++) {
    const st = steps[k] || /** @type {Partial<AgentStep>} */ ({});
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
/**
 * @param {AgentSession[]|null|undefined} sessions
 * @returns {{sessionId: string, cwd: string, entrypoint: string, index: number,
 *   promptCount: number, replyCount: number}[]}
 */
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
      entrypoint: session.entrypoint || '',
      index,
      promptCount,
      replyCount,
    });
  });
  return out;
}
