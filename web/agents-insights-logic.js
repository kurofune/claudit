// @ts-check
// The five per-session anomaly detectors behind the session header's
// "Worth a look" notes (retry storm, cost whale, slow tool, error cascade,
// idle stall), run through detectSignals, with the retry grouping and
// percentile math they share.

import { flattenSession, parseTime } from './agents-model.js';

/** @import { AgentGraph, AgentNode, AgentStep, ToolInvocation } from './api-types.js' */

/**
 * One anomaly finding from detectSignals.
 * @typedef {Object} Signal
 * @property {string} kind      'retry-storm'|'cost-whale'|'slow-tool'|'error-cascade'|'idle-stall'
 * @property {number} severity  [0,1] badness magnitude (cross-kind sort key)
 * @property {string} tier      'high'|'med'|'low'
 * @property {string} ref       agent, turn or call it points at (sid#ai / sid#ai.si / sid#ai.si:ti)
 * @property {string} summary
 * @property {string} reason    the measured value against the threshold that fired
 */

/** @param {number} ms */
const fmtMs = ms => (ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(1)}s`);
/** @param {number} ms */
const fmtMin = ms => `${(ms / 60000).toFixed(1)} min`;
/** @param {number} x */
const pct = x => `${Math.round(x * 100)}%`;
/** @param {string} s @param {number} max */
const clip = (s, max) => (s.length > max ? `${s.slice(0, max - 1)}…` : s);

// percentiles returns one value per requested point.
/** @param {number[]|null|undefined} values @param {number[]|null|undefined} ps @returns {number[]} */
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

// nearestRank is the p-th percentile by nearest rank: the smallest value
// at or above which p% of the values lie, always one of the values.
/** @param {number[]|null|undefined} values @param {number} p @returns {number} */
export function nearestRank(values, p) {
  const nums = (Array.isArray(values) ? values : [])
    .filter(v => typeof v === 'number' && !Number.isNaN(v))
    .sort((a, b) => a - b);
  if (nums.length === 0) return NaN;
  const rank = Math.ceil((Math.max(0, Math.min(100, p)) / 100) * nums.length);
  return nums[Math.max(0, rank - 1)];
}

// retryTarget is what a call was aimed at: its input (the Bash command, the
// file path) when it has one, else its detail. Two Reads of different .go files
// share a detail but are not repeats of each other.
/** @param {Partial<ToolInvocation>} tool */
export function retryTarget(tool) {
  return tool.input || tool.detail || '';
}

// inputIdentity is which input a call ran, for telling repeats apart: the
// whole input's key (input_key) when the payload carries one, so two calls
// whose capped inputs match but whose whole inputs differ are not repeats,
// else retryTarget.
/** @param {Partial<ToolInvocation> & {input_key?: string}} tool */
export function inputIdentity(tool) {
  return tool.input_key ? `k:${tool.input_key}` : `t:${retryTarget(tool)}`;
}

// detectRetries finds repeated tool calls where an earlier attempt errored,
// returning a Map from each retry's agent-relative coord ("si:ti") to
// { attempt, ofRef, errors } — attempt is the 1-based ordinal of the call within
// its group, ofRef the coord of the group's first call, errors how many calls in
// the group up to and including this one had status "error". Tool calls are
// grouped by (kind, name, inputIdentity) and walked in step-then-tool order (steps
// are already chronological). A call is a retry only when some earlier call in
// its group had status "error", so a clean repeat (ok → ok) is never flagged.
// Coords are the suffix of the full tool refKey, composable via
// `${sid}#${ai}.${coord}`.
/**
 * @param {AgentNode|null|undefined} agent
 * @returns {Map<string, {attempt: number, ofRef: string, errors: number}>}
 */
export function detectRetries(agent) {
  const out = new Map();
  const groups = new Map(); // groupKey -> { firstRef, count, errors }
  const steps = (agent && agent.steps) || [];
  steps.forEach((step, si) => {
    (step.tools || []).forEach((tool, ti) => {
      if (!tool) return;
      const coord = `${si}:${ti}`;
      const groupKey = `${tool.kind || ''}\u0000${tool.name || ''}\u0000${inputIdentity(tool)}`;
      let g = groups.get(groupKey);
      if (!g) {
        g = { firstRef: coord, count: 0, errors: 0 };
        groups.set(groupKey, g);
      }
      g.count += 1;
      const sawError = g.errors > 0;
      if (tool.status === 'error') g.errors += 1;
      if (sawError) out.set(coord, { attempt: g.count, ofRef: g.firstRef, errors: g.errors });
    });
  });
  return out;
}

// detectSignals runs the five detectors over every session in graph and
// returns their findings sorted worst-first. Each signal is { kind,
// severity, tier, ref, summary, reason }: severity is a [0,1] "badness"
// magnitude (the cross-kind sort key), tier ('high'|'med'|'low') is the
// detector's own banding, ref locates the agent, turn or call (sid#ai /
// sid#ai.si / sid#ai.si:ti, read back with parseRefKey), summary is a
// self-contained sentence and reason states the measured value against the
// threshold that fired, so a reader can judge it.
/**
 * @param {AgentGraph|null|undefined} graph
 * @param {{whaleShare?: number, whaleMinUSD?: number, retryStormMin?: number, slowToolMin?: number,
 *   slowMultiple?: number, slowP95Multiple?: number, slowFloorMs?: number, errorCascadeMin?: number,
 *   idleStallMs?: number, nowMs?: number}} [opts]
 * @returns {Signal[]}
 */
export function detectSignals(graph, opts = {}) {
  const whaleShare = opts.whaleShare > 0 ? opts.whaleShare : 0.2;
  const whaleMinUSD = opts.whaleMinUSD > 0 ? opts.whaleMinUSD : 0.5;
  const retryStormMin = opts.retryStormMin > 0 ? opts.retryStormMin : 3;
  const slowToolMin = opts.slowToolMin > 0 ? opts.slowToolMin : 5;
  const slowMultiple = opts.slowMultiple > 0 ? opts.slowMultiple : 3;
  const slowP95Multiple = opts.slowP95Multiple > 0 ? opts.slowP95Multiple : 2;
  const slowFloorMs = opts.slowFloorMs > 0 ? opts.slowFloorMs : 60000;
  const errorCascadeMin = opts.errorCascadeMin > 0 ? opts.errorCascadeMin : 3;
  const idleStallMs = opts.idleStallMs > 0 ? opts.idleStallMs : 120000;
  // Static-safe: "now" must come from opts, never Date.now, so a static
  // `claudit report` (which can't know the live clock) simply skips the
  // trailing-gap check instead of inventing a stall.
  const nowMs = (typeof opts.nowMs === 'number' && !Number.isNaN(opts.nowMs)) ? opts.nowMs : null;
  const signals = [];
  for (const session of (graph && graph.sessions) || []) {
    const sid = (session && session.session_id) || '';
    const agents = flattenSession(session);
    signals.push(...retryStormSignals(agents, sid, retryStormMin));
    signals.push(...costWhaleSignals(agents, sid, whaleShare, whaleMinUSD));
    signals.push(...slowToolSignals(agents, sid, { slowToolMin, slowMultiple, slowP95Multiple, slowFloorMs }));
    signals.push(...errorCascadeSignals(agents, sid, errorCascadeMin));
    signals.push(...idleStallSignals(agents, sid, idleStallMs, nowMs));
  }
  // Worst-first. JS sort is stable, so equal-severity signals keep emission order.
  signals.sort((a, b) => b.severity - a.severity);
  return signals;
}

// retryStormSignals: per agent, collapse detectRetries' per-attempt map into one
// signal per group (keyed by the first call's coord), refed at the worst
// (highest-attempt) call so click-through lands on the storm's peak. Emits only
// groups reaching retryStormMin attempts.
/** @param {AgentNode[]} agents @param {string} sid @param {number} retryStormMin @returns {Signal[]} */
function retryStormSignals(agents, sid, retryStormMin) {
  const out = [];
  agents.forEach((a, ai) => {
    const groups = new Map(); // ofRef -> { maxAttempt, worstCoord, errors }
    for (const [coord, { attempt, ofRef, errors }] of detectRetries(a)) {
      const g = groups.get(ofRef);
      if (!g || attempt > g.maxAttempt) groups.set(ofRef, { maxAttempt: attempt, worstCoord: coord, errors });
    }
    for (const { maxAttempt, worstCoord, errors } of groups.values()) {
      if (maxAttempt < retryStormMin) continue;
      const severity = Math.max(0, Math.min(1, (maxAttempt - 1) / 5));
      const tier = maxAttempt >= 5 ? 'high' : maxAttempt >= 4 ? 'med' : 'low';
      const [si, ti] = worstCoord.split(':').map(Number);
      const tool = ((a.steps[si] || /** @type {Partial<AgentStep>} */ ({})).tools || [])[ti]
        || /** @type {Partial<ToolInvocation>} */ ({});
      const name = tool.name || tool.kind || 'tool';
      const summary = tool.detail
        ? `${name} retried ${maxAttempt}× (${tool.detail})`
        : `${name} retried ${maxAttempt}×`;
      const target = retryTarget(tool);
      const of = target ? `${name} \`${clip(target, 80)}\`` : name;
      const reason = `${maxAttempt} attempts of ${of}, ${errors} errored — threshold ${retryStormMin}`;
      out.push({ kind: 'retry-storm', severity, tier, ref: `${sid}#${ai}.${worstCoord}`, summary, reason });
    }
  });
  return out;
}

// slowToolSignals: flag tool calls whose wall-clock is a real outlier within
// the session — at least slowFloorMs long AND either at least slowP95Multiple×
// the session-wide p95 of all tool durations, or at least slowMultiple× the
// median for the tool's own kind (so a slow Bash among fast Reads is caught
// even when it's not in the global top tail). The floor holds for both paths:
// a 30 s Read at 10× its peers is not worth a look. Needs at least slowToolMin
// valid-duration tools for a meaningful percentile; below that the session is
// too small to call anything an outlier. ref is the tool (sid#ai.si:ti).
/**
 * @param {AgentNode[]} agents @param {string} sid
 * @param {{slowToolMin: number, slowMultiple: number, slowP95Multiple: number, slowFloorMs: number}} th
 * @returns {Signal[]}
 */
function slowToolSignals(agents, sid, { slowToolMin, slowMultiple, slowP95Multiple, slowFloorMs }) {
  // Collect every tool with a real wall-clock (NaN-guarded).
  const tools = []; // { ai, si, ti, kind, name, dur }
  agents.forEach((a, ai) => {
    (a.steps || []).forEach((step, si) => {
      (step.tools || []).forEach((t, ti) => {
        if (!t) return;
        const start = parseTime(t.started_at), end = parseTime(t.ended_at);
        if (Number.isNaN(start) || Number.isNaN(end) || end <= start) return;
        tools.push({ ai, si, ti, kind: t.kind || 'other', name: t.name || t.kind || 'tool', dur: end - start });
      });
    });
  });
  if (tools.length < slowToolMin) return [];
  const p95 = percentiles(tools.map(t => t.dur), [95])[0];
  // Per-kind medians for the relative-to-peers path.
  const byKind = new Map();
  for (const t of tools) {
    if (!byKind.has(t.kind)) byKind.set(t.kind, []);
    byKind.get(t.kind).push(t.dur);
  }
  const kindMedian = new Map();
  for (const [kind, durs] of byKind) kindMedian.set(kind, percentiles(durs, [50])[0]);
  const out = [];
  for (const t of tools) {
    if (t.dur < slowFloorMs) continue;
    const km = kindMedian.get(t.kind);
    const overP95 = p95 > 0 && t.dur >= slowP95Multiple * p95;
    const overKind = km > 0 && t.dur >= slowMultiple * km;
    if (!overP95 && !overKind) continue;
    // Severity from how far the p95 ratio runs past its threshold; a
    // kind-median-only flag floors to a small positive value so it still
    // sorts as a real signal.
    let severity = p95 > 0 ? (t.dur / p95 - slowP95Multiple) / slowP95Multiple : 0;
    if (severity <= 0) severity = 0.05;
    severity = Math.max(0, Math.min(1, severity));
    const tier = severity >= 0.5 ? 'high' : severity >= 0.2 ? 'med' : 'low';
    const secs = (t.dur / 1000).toFixed(1);
    const summary = overP95
      ? `${t.name} took ${secs}s — ${(t.dur / p95).toFixed(1)}× the session p95`
      : `${t.name} took ${secs}s — ${(t.dur / km).toFixed(1)}× the typical ${t.kind}`;
    const floor = `at least ${+(slowFloorMs / 1000).toFixed(1)}s`;
    const reason = overP95
      ? `${t.name} ${fmtMs(t.dur)} vs session p95 ${fmtMs(p95)} — threshold ${slowP95Multiple}× the session p95 and ${floor}`
      : `${t.name} ${fmtMs(t.dur)} vs ${t.kind} median ${fmtMs(km)} — threshold ${slowMultiple}× the kind median and ${floor}`;
    out.push({ kind: 'slow-tool', severity, tier, ref: `${sid}#${t.ai}.${t.si}:${t.ti}`, summary, reason });
  }
  return out;
}

// errorCascadeSignals: per agent, walk tools in step-then-tool (chronological)
// order and flag any run of errorCascadeMin-or-more consecutive errored tools as
// one signal, refed at the run's first error so click-through lands where it
// began. A non-error tool breaks the run; runs don't cross agent boundaries.
/** @param {AgentNode[]} agents @param {string} sid @param {number} errorCascadeMin @returns {Signal[]} */
function errorCascadeSignals(agents, sid, errorCascadeMin) {
  const out = [];
  agents.forEach((a, ai) => {
    let run = null; // { startCoord, startName, len }
    const flush = () => {
      if (run && run.len >= errorCascadeMin) {
        const severity = Math.max(0, Math.min(1, (run.len - 1) / 5));
        const tier = run.len >= 5 ? 'high' : run.len >= 4 ? 'med' : 'low';
        const summary = `${run.len} tools errored back-to-back, starting with ${run.startName}`;
        const reason = `${run.len} consecutive tool errors starting with ${run.startName} — threshold ${errorCascadeMin}`;
        out.push({ kind: 'error-cascade', severity, tier, ref: `${sid}#${ai}.${run.startCoord}`, summary, reason });
      }
      run = null;
    };
    (a.steps || []).forEach((step, si) => {
      (step.tools || []).forEach((t, ti) => {
        if (!t) return;
        if (t.status === 'error') {
          if (!run) run = { startCoord: `${si}:${ti}`, startName: t.name || t.kind || 'tool', len: 0 };
          run.len += 1;
        } else {
          flush();
        }
      });
    });
    flush();
  });
  return out;
}

// idleStallSignals: per agent, flag a gap longer than idleStallMs that the agent
// spent idle with work in hand — never a gap it was entitled to.
//   - Between turns: from one step's END (its last tool's ended_at, or its own
//     timestamp) to the next step's START, only when that step called a tool —
//     the agent owed the next turn. A gap after a text-only turn is the user's
//     turn to speak, not a stall. A single long-running tool is NOT a stall,
//     since the gap is measured from when it finished.
//   - Trailing (only when nowMs is provided): from the final step's end to
//     "now", only when the agent shows unfinished work — its last step has a
//     tool with no joined result, or its status is "running". A finished agent
//     whose last tools all resolved is done, however long ago.
// ref is the step that ends the idle (the resuming step, or the last step for
// a trailing stall).
/**
 * @param {AgentNode[]} agents @param {string} sid
 * @param {number} idleStallMs @param {number|null} nowMs
 * @returns {Signal[]}
 */
function idleStallSignals(agents, sid, idleStallMs, nowMs) {
  /** @param {AgentStep|null|undefined} step */
  const stepEnd = (step) => {
    let end = parseTime(step && step.timestamp);
    for (const t of (step && step.tools) || []) {
      if (!t) continue;
      const e = parseTime(t.ended_at);
      if (!Number.isNaN(e) && (Number.isNaN(end) || e > end)) end = e;
    }
    return end;
  };
  const out = [];
  /** @param {number} ai @param {number} si @param {number} gap @param {boolean} trailing @param {string} evidence */
  const emit = (ai, si, gap, trailing, evidence) => {
    const mult = gap / idleStallMs;
    const severity = Math.max(0, Math.min(1, (mult - 1) / 9));
    const tier = mult >= 5 ? 'high' : mult >= 2 ? 'med' : 'low';
    const mins = (gap / 60000).toFixed(1);
    const summary = trailing
      ? `Idle ${mins} min after the last activity`
      : `Idle ${mins} min between turns`;
    const reason = trailing
      ? `idle ${fmtMin(gap)} after the last activity, ${evidence} — threshold ${fmtMin(idleStallMs)}`
      : `idle ${fmtMin(gap)} between turns after ${evidence} — threshold ${fmtMin(idleStallMs)}`;
    out.push({ kind: 'idle-stall', severity, tier, ref: `${sid}#${ai}.${si}`, summary, reason });
  };
  /** @param {AgentStep|null|undefined} step */
  const lastToolEvidence = (step) => {
    const tools = ((step && step.tools) || []).filter(Boolean);
    const t = tools[tools.length - 1];
    if (!t) return '';
    const name = t.name || t.kind || 'tool';
    const joined = !!t.status || !Number.isNaN(parseTime(t.ended_at));
    return joined ? `${name} returned` : `${name}, which has no result`;
  };
  agents.forEach((a, ai) => {
    const steps = a.steps || [];
    let prevEnd = NaN, prevEndSi = -1, prevEvidence = '';
    steps.forEach((step, si) => {
      const start = parseTime(step && step.timestamp);
      if (prevEvidence && !Number.isNaN(prevEnd) && !Number.isNaN(start) && start - prevEnd > idleStallMs) {
        emit(ai, si, start - prevEnd, false, prevEvidence);
      }
      const end = stepEnd(step);
      if (!Number.isNaN(end)) { prevEnd = end; prevEndSi = si; prevEvidence = lastToolEvidence(step); }
    });
    if (nowMs == null || Number.isNaN(prevEnd) || nowMs - prevEnd <= idleStallMs) return;
    const last = steps[steps.length - 1];
    const pending = ((last && last.tools) || []).find(t => t && !t.status);
    let evidence = '';
    if (pending) evidence = `${pending.name || pending.kind || 'tool'} has no result`;
    else if (a.status === 'running') evidence = 'agent still running';
    if (evidence) emit(ai, prevEndSi, nowMs - prevEnd, true, evidence);
  });
  return out;
}

// costWhaleSignals: per session, flag each turn that costs at least whaleMinUSD
// AND at least whaleShare of the session's total step cost. The dollar floor
// keeps a cheap session from flagging a fraction of a cent; the share keeps an
// expensive session from flagging every ordinary turn. severity is the share
// itself; ref is the turn (sid#ai.si). Shares are normalized within the
// session, never the graph.
/**
 * @param {AgentNode[]} agents @param {string} sid
 * @param {number} whaleShare @param {number} whaleMinUSD
 * @returns {Signal[]}
 */
function costWhaleSignals(agents, sid, whaleShare, whaleMinUSD) {
  let total = 0;
  for (const a of agents) for (const step of (a.steps || [])) total += (step && step.cost_usd) || 0;
  if (total <= 0) return [];
  const out = [];
  agents.forEach((a, ai) => {
    (a.steps || []).forEach((step, si) => {
      const cost = (step && step.cost_usd) || 0;
      const share = cost / total;
      if (cost < whaleMinUSD || share < whaleShare) return;
      const tier = share >= 0.5 ? 'high' : share >= 0.3 ? 'med' : 'low';
      const summary = `Turn cost $${cost.toFixed(2)} — ${pct(share)} of session spend`;
      const reason = `turn $${cost.toFixed(2)} is ${pct(share)} of session $${total.toFixed(2)} — floor $${whaleMinUSD.toFixed(2)}, share ${pct(whaleShare)}`;
      out.push({ kind: 'cost-whale', severity: share, tier, ref: `${sid}#${ai}.${si}`, summary, reason });
    });
  });
  return out;
}
