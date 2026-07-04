// @ts-check
// Insights-lens aggregations and anomaly detection: tool mix, percentiles,
// latency histogram, cost Pareto, error rates, token/context series,
// group-by, retry detection, and the Phase 3 signal detectors + timeline
// pip bucketing. Split out of agents-logic.js (re-exported by the facade).

import {
  agentLabel, agentTokens, flattenSession, parseRefKey, parseTime,
} from './agents-model.js';

/** @import { AgentGraph, AgentNode, AgentStep, ToolInvocation } from './api-types.js' */

/**
 * One Phase 3 anomaly finding from detectSignals.
 * @typedef {Object} Signal
 * @property {string} kind      'retry-storm'|'cost-whale'|'slow-tool'|'error-cascade'|'idle-stall'
 * @property {number} severity  [0,1] badness magnitude (cross-kind sort key)
 * @property {string} tier      'high'|'med'|'low'
 * @property {string} ref       refKey (sid#ai / sid#ai.si / sid#ai.si:ti)
 * @property {string} summary
 */

// toolMix aggregates an array of agent nodes (already scoped by the caller —
// whole graph, one session, or one agent) into per-kind tool totals for the
// Insights "Tool mix" panel: count · wall-clock · cost, grouped by ToolKind.
// Pure and scope-agnostic. Returns [] for a null/empty input.
/**
 * @param {AgentNode[]|null|undefined} agents
 * @returns {{kind: string, count: number, durationMs: number, costUSD: number}[]}
 */
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

// DURATION_EDGES are the default upper bounds (ms) for the latency histogram —
// a log-ish progression tuned for tool wall-clock (sub-100ms reads up to
// multi-minute execs). Each edge opens a [prev, edge) bucket; a trailing
// [lastEdge, ∞) bucket catches the long tail.
export const DURATION_EDGES = [100, 250, 500, 1000, 2500, 5000, 10000, 30000, 60000];

// durationHistogram buckets per-tool wall-clock.
/** @param {AgentNode[]|null|undefined} agents @param {{edges?: number[]}} [opts] */
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
/** @param {AgentNode[]|null|undefined} agents @param {{topN?: number, headlinePct?: number}} [opts] */
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

// errorRates measures how often tools fail, for the Insights "Error breakdown"
// panel. Pure and scope-agnostic — the caller passes the already-scoped agents
// (whole graph, one session, or one agent). The unit is the tool ROW, not the
// collapsed Count: distinctToolInvocations keys the collapse on (name, detail,
// input) and stamps one Status per surviving row (sessions.go), so a row is the
// only honest carrier of an outcome. An errored row is `status === 'error'`;
// every other status (ok / '' / missing) counts toward the denominator only.
// Returns:
//   { total, errors, rate, rows, worst }
//   total   — tool rows observed
//   errors  — rows whose status is 'error'
//   rate    — errors / total (0 when total is 0)
//   rows    — per-ToolKind breakdown, ONLY kinds with >=1 error, each
//             { kind, total, errors, rate } (total spans that kind's clean rows
//             too), sorted errors desc → rate desc → kind asc
//   worst   — the failing-tools list: per tool NAME, only names with >=1 error,
//             each { name, kind, total, errors, rate }, same sort by name, capped
//             at opts.topN (default 8). A name with no kind folds to 'other',
//             a nameless tool to '(unnamed)'.
/** @param {AgentNode[]|null|undefined} agents @param {{topN?: number}} [opts] */
export function errorRates(agents, opts = {}) {
  const topN = opts.topN != null ? opts.topN : 8;
  let total = 0, errors = 0;
  const byKind = new Map();
  const byName = new Map();
  for (const a of (Array.isArray(agents) ? agents : [])) {
    for (const step of (a && a.steps) || []) {
      for (const t of (step && step.tools) || []) {
        if (!t) continue;
        const errored = t.status === 'error';
        total += 1;
        if (errored) errors += 1;
        const kind = t.kind || 'other';
        const kr = byKind.get(kind) || { kind, total: 0, errors: 0, rate: 0 };
        kr.total += 1;
        if (errored) kr.errors += 1;
        byKind.set(kind, kr);
        const name = t.name || '(unnamed)';
        const nr = byName.get(name) || { name, kind, total: 0, errors: 0, rate: 0 };
        nr.total += 1;
        if (errored) nr.errors += 1;
        byName.set(name, nr);
      }
    }
  }
  const rows = [...byKind.values()]
    .filter(r => r.errors > 0)
    .map(r => ({ ...r, rate: r.errors / r.total }))
    .sort((a, b) => b.errors - a.errors || b.rate - a.rate || a.kind.localeCompare(b.kind));
  const worst = [...byName.values()]
    .filter(r => r.errors > 0)
    .map(r => ({ ...r, rate: r.errors / r.total }))
    .sort((a, b) => b.errors - a.errors || b.rate - a.rate || a.name.localeCompare(b.name))
    .slice(0, topN);
  return { total, errors, rate: total ? errors / total : 0, rows, worst };
}

// contextSeries summarizes token usage and context-window growth across the
// scoped agents, for the Insights "Token & context" panel. Pure and scope-
// agnostic (whole graph, one session, or one agent) — same contract as the
// sibling aggregations (toolMix / costPareto / errorRates). It flattens every
// agent's steps into ONE series ordered by step timestamp (NaN timestamps sort
// stably to the end), each point carrying that turn's context size and token
// breakdown. Token field names are the Go-marshalled (untagged) Tokens shape —
// InputTokens etc. — folded to {input, output, cacheWrite, cacheRead} via
// agentTokens (5m + 1h cache-creation combine into cacheWrite).
//
// "context" is the step's context_tokens (input + cache_read: the prompt size
// fed to the model that turn). It grows as the conversation accumulates and
// drops at compaction, so the series traces the context window over the run.
// "cacheHit" is cacheRead / (cacheRead + input + cacheWrite): the share of the
// prompt served cheaply from cache — the same formula the Cache view uses. The
// overall cacheHit aggregates the totals; it is NOT the mean of per-turn ratios
// (a cheap turn shouldn't weigh the same as an expensive one).
//
// Returns:
//   { turns, series, peakContext, cacheHit, totals }
//   turns       — number of steps in the series (points plotted)
//   series      — chronological, each { t, context, input, output, cacheWrite,
//                 cacheRead, total, cacheHit, agentLabel }; t is epoch ms (NaN
//                 when the step had no parseable timestamp). total is the four-
//                 band sum (matches agentTokens).
//   peakContext — max context across the series (0 when empty), an ABSOLUTE
//                 prompt size. We deliberately do NOT derive a "% of window":
//                 the transcript records neither the context-window size nor the
//                 1M-beta flag, so any denominator would be a guess that
//                 overstates fill whenever a run's prompts stayed under 200k.
//   cacheHit    — overall cacheRead / (cacheRead + input + cacheWrite), 0 when
//                 the denominator is 0
//   totals      — summed { input, output, cacheWrite, cacheRead, total }

/** @param {AgentNode[]|null|undefined} agents @param {Object} [opts] */
export function contextSeries(agents, opts = {}) {
  const series = [];
  let peakContext = 0;
  const totals = { input: 0, output: 0, cacheWrite: 0, cacheRead: 0, total: 0 };
  for (const a of (Array.isArray(agents) ? agents : [])) {
    const label = agentLabel(a);
    for (const step of (a && a.steps) || []) {
      const tok = agentTokens(step);
      const context = (step && step.context_tokens) || 0;
      if (context > peakContext) peakContext = context;
      totals.input += tok.input; totals.output += tok.output;
      totals.cacheWrite += tok.cacheWrite; totals.cacheRead += tok.cacheRead;
      totals.total += tok.total;
      const promptTokens = tok.cacheRead + tok.input + tok.cacheWrite;
      const cacheHit = promptTokens ? tok.cacheRead / promptTokens : 0;
      series.push({ t: parseTime(step && step.timestamp), context, input: tok.input, output: tok.output, cacheWrite: tok.cacheWrite, cacheRead: tok.cacheRead, total: tok.total, cacheHit, agentLabel: label });
    }
  }
  // Chronological across agents; NaN timestamps sort stably to the end. JS sort
  // is stable, so equal-t points keep agent-then-step insertion order.
  series.sort((x, y) => {
    if (Number.isNaN(x.t)) return Number.isNaN(y.t) ? 0 : 1;
    if (Number.isNaN(y.t)) return -1;
    return x.t - y.t;
  });
  const promptTotal = totals.cacheRead + totals.input + totals.cacheWrite;
  return {
    turns: series.length, series, peakContext,
    cacheHit: promptTotal ? totals.cacheRead / promptTotal : 0,
    totals,
  };
}

// binSeries downsamples a contextSeries result to at most maxBins points so the
// Token panel's per-turn bars and context sparkline stay legible (and cheap to
// render) at graph scope, where a run can hold tens of thousands of turns. Each
// bin aggregates consecutive turns: context is the MAX in the bin (preserving the
// growth envelope and compaction peaks the line should show), the token bands SUM
// (so the bars represent that slice's real volume), and count records how many
// turns folded in. When the series already fits, every turn is its own bin
// (count 1). No turn is dropped — render code must not silently truncate.
/**
 * @param {{context: number, input: number, output: number, cacheWrite: number,
 *   cacheRead: number, total: number}[]|null|undefined} series
 * @param {number} maxBins
 */
export function binSeries(series, maxBins) {
  const src = Array.isArray(series) ? series : [];
  const cap = maxBins > 0 ? maxBins : 1;
  if (src.length <= cap) {
    return src.map(p => ({
      context: p.context, input: p.input, output: p.output,
      cacheWrite: p.cacheWrite, cacheRead: p.cacheRead, total: p.total, count: 1,
    }));
  }
  const binSize = Math.ceil(src.length / cap);
  const bins = [];
  for (let i = 0; i < src.length; i += binSize) {
    const slice = src.slice(i, i + binSize);
    const bin = { context: 0, input: 0, output: 0, cacheWrite: 0, cacheRead: 0, total: 0, count: 0 };
    for (const p of slice) {
      if (p.context > bin.context) bin.context = p.context;  // max preserves the growth envelope
      bin.input += p.input; bin.output += p.output;
      bin.cacheWrite += p.cacheWrite; bin.cacheRead += p.cacheRead;
      bin.total += p.total; bin.count += 1;
    }
    bins.push(bin);
  }
  return bins;
}

// groupBy is the lightweight BubbleUp slice for the Insights "Group-by" panel:
// it buckets tool rows along ONE dimension and reports, per group, how many
// calls / how long / how much. Pure and scope-agnostic — the caller passes the
// already-scoped agents (whole graph, one session, or one agent). The unit is
// the tool ROW, generalizing toolMix: count sums t.count (calls), durationMs/
// medianMs are over tool wall-clock (started_at→ended_at, same source as
// durationHistogram — rows without a parseable span are uncounted there but
// still counted in `count`), and cost is the turn cost_usd apportioned across
// the turn's tools by call-share (the honest split toolMix uses; a tool-less
// turn contributes no cost).
//
// opts.dimension selects the bucket key:
//   'kind'   — tool.kind (missing → 'other')           [default]
//   'status' — tool.status (missing/'' → 'none')
//   'model'  — parent step.model (missing → '(none)')
//   'agent'  — agentLabel(parent) ('main' or agent_type)
//
// Returns:
//   { dimension, total, rows }
//   total — summed { count, durationMs, costUSD } across all groups
//   rows  — per group, sorted cost desc → count desc → key asc, each
//           { key, count, durationMs, medianMs, costUSD, costShare, countShare }.
//           medianMs is the median tool-row wall-clock (NaN when the group has
//           no measured rows); costShare/countShare are the group's fraction of
//           the totals (0 when the total is 0).
/**
 * @param {AgentNode[]|null|undefined} agents
 * @param {{dimension?: 'kind'|'status'|'model'|'agent'}} [opts]
 */
export function groupBy(agents, opts = {}) {
  const dimension = opts.dimension || 'kind';
  const dimKey = (t, step, aLabel) => {
    switch (dimension) {
      case 'status': return t.status || 'none';
      case 'model': return (step && step.model) || '(none)';
      case 'agent': return aLabel;
      case 'kind':
      default: return t.kind || 'other';
    }
  };
  const groups = new Map();
  for (const a of (Array.isArray(agents) ? agents : [])) {
    const aLabel = agentLabel(a);
    for (const step of (a && a.steps) || []) {
      const tools = (step && step.tools) || [];
      const stepCalls = tools.reduce((n, t) => n + ((t && t.count) || (t ? 1 : 0)), 0);
      const stepCost = (step && step.cost_usd) || 0;
      for (const t of tools) {
        if (!t) continue;
        const calls = t.count || 1;
        const key = dimKey(t, step, aLabel);
        const g = groups.get(key) || { key, count: 0, durationMs: 0, costUSD: 0, durs: [] };
        g.count += calls;
        const start = parseTime(t.started_at), end = parseTime(t.ended_at);
        if (!Number.isNaN(start) && !Number.isNaN(end) && end > start) {
          const ms = end - start;
          g.durationMs += ms;
          g.durs.push(ms);
        }
        if (stepCalls > 0) g.costUSD += stepCost * (calls / stepCalls);
        groups.set(key, g);
      }
    }
  }
  const base = [...groups.values()].map(g => {
    const { durs, ...rest } = g;
    return { ...rest, medianMs: percentiles(durs, [50])[0] };
  });
  const total = base.reduce(
    (acc, r) => ({ count: acc.count + r.count, durationMs: acc.durationMs + r.durationMs, costUSD: acc.costUSD + r.costUSD }),
    { count: 0, durationMs: 0, costUSD: 0 });
  const rows = base
    .map(r => ({
      ...r,
      costShare: total.costUSD ? r.costUSD / total.costUSD : 0,
      countShare: total.count ? r.count / total.count : 0,
    }))
    .sort((a, b) => b.costUSD - a.costUSD || b.count - a.count || a.key.localeCompare(b.key));
  return { dimension, total, rows };
}

// detectRetries finds repeated tool calls where an earlier attempt errored,
// returning a Map from each retry's agent-relative coord ("si:ti") to
// { attempt, ofRef } — attempt is the 1-based ordinal of the call within its
// group, ofRef the coord of the group's first call. Tool calls are grouped by
// (kind, name, detail) and walked in step-then-tool order (steps are already
// chronological). A call is a retry only when some earlier call in its group
// had status "error", so a clean repeat (ok → ok) is never flagged. Coords are
// the suffix of the full tool refKey, composable via `${sid}#${ai}.${coord}`.
/**
 * @param {AgentNode|null|undefined} agent
 * @returns {Map<string, {attempt: number, ofRef: string}>}
 */
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

// detectSignals is the single entry point for Phase 3 anomaly detection: it runs
// the pure detectors over the whole graph and returns their findings sorted
// worst-first. Each signal is { kind, severity, tier, ref, summary }: severity is
// a [0,1] "badness" magnitude (the cross-kind sort key), tier ('high'|'med'|'low')
// is the detector's own banding for pip styling, ref is a refKey (sid#ai /
// sid#ai.si / sid#ai.si:ti) so the existing select()/filter machinery can land on
// it, and summary is a self-contained sentence for the panel/tooltip.
/**
 * @param {AgentGraph|null|undefined} graph
 * @param {{whaleShare?: number, retryStormMin?: number, slowToolMin?: number,
 *   slowMultiple?: number, errorCascadeMin?: number, idleStallMs?: number,
 *   nowMs?: number}} [opts]
 * @returns {Signal[]}
 */
export function detectSignals(graph, opts = {}) {
  const whaleShare = opts.whaleShare > 0 ? opts.whaleShare : 0.2;
  const retryStormMin = opts.retryStormMin > 0 ? opts.retryStormMin : 3;
  const slowToolMin = opts.slowToolMin > 0 ? opts.slowToolMin : 5;
  const slowMultiple = opts.slowMultiple > 0 ? opts.slowMultiple : 3;
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
    signals.push(...costWhaleSignals(agents, sid, whaleShare));
    signals.push(...slowToolSignals(agents, sid, slowToolMin, slowMultiple));
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
    const groups = new Map(); // ofRef -> { maxAttempt, worstCoord }
    for (const [coord, { attempt, ofRef }] of detectRetries(a)) {
      const g = groups.get(ofRef);
      if (!g || attempt > g.maxAttempt) groups.set(ofRef, { maxAttempt: attempt, worstCoord: coord });
    }
    for (const { maxAttempt, worstCoord } of groups.values()) {
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
      out.push({ kind: 'retry-storm', severity, tier, ref: `${sid}#${ai}.${worstCoord}`, summary });
    }
  });
  return out;
}

// slowToolSignals: flag tool calls whose wall-clock is anomalous within the
// session — either over the session-wide p95 of all tool durations, or over
// slowMultiple× the median for the tool's own kind (so a slow Bash among fast
// Reads is caught even when it's not in the global top tail). Needs at least
// slowToolMin valid-duration tools for a meaningful percentile; below that the
// session is too small to call anything an outlier. ref is the tool (sid#ai.si:ti).
/**
 * @param {AgentNode[]} agents @param {string} sid
 * @param {number} slowToolMin @param {number} slowMultiple
 * @returns {Signal[]}
 */
function slowToolSignals(agents, sid, slowToolMin, slowMultiple) {
  // Collect every tool with a real wall-clock (NaN-guarded like durationHistogram).
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
    const km = kindMedian.get(t.kind);
    const overP95 = t.dur > p95;
    const overKind = km > 0 && t.dur > slowMultiple * km;
    if (!overP95 && !overKind) continue;
    // Severity from the global p95 ratio; a kind-median-only flag (within p95)
    // floors to a small positive value so it still sorts as a real signal.
    let severity = p95 > 0 ? (t.dur - p95) / p95 : 0;
    if (severity <= 0) severity = 0.05;
    severity = Math.max(0, Math.min(1, severity));
    const tier = severity >= 0.5 ? 'high' : severity >= 0.2 ? 'med' : 'low';
    const secs = (t.dur / 1000).toFixed(1);
    const summary = overP95
      ? `${t.name} took ${secs}s — ${(t.dur / p95).toFixed(1)}× the session p95`
      : `${t.name} took ${secs}s — ${(t.dur / km).toFixed(1)}× the typical ${t.kind}`;
    out.push({ kind: 'slow-tool', severity, tier, ref: `${sid}#${t.ai}.${t.si}:${t.ti}`, summary });
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
        out.push({ kind: 'error-cascade', severity, tier, ref: `${sid}#${ai}.${run.startCoord}`, summary });
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

// idleStallSignals: per agent, flag a gap longer than idleStallMs between one
// step's END (its last tool's ended_at, or its own timestamp when tool-less) and
// the next step's START — wall-clock the agent spent idle, not working (a single
// long-running tool is NOT a stall, since the gap is measured from when it
// finished). When nowMs is provided, the tail from the final step's end to "now"
// is also checked, catching a run that stalled and never resumed. ref is the step
// that ends the idle (the resuming step, or the last step for a trailing stall).
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
  const emit = (ai, si, gap, trailing) => {
    const mult = gap / idleStallMs;
    const severity = Math.max(0, Math.min(1, (mult - 1) / 9));
    const tier = mult >= 5 ? 'high' : mult >= 2 ? 'med' : 'low';
    const mins = (gap / 60000).toFixed(1);
    const summary = trailing
      ? `Idle ${mins} min after the last activity`
      : `Idle ${mins} min between turns`;
    out.push({ kind: 'idle-stall', severity, tier, ref: `${sid}#${ai}.${si}`, summary });
  };
  agents.forEach((a, ai) => {
    const steps = a.steps || [];
    let prevEnd = NaN, prevEndSi = -1;
    steps.forEach((step, si) => {
      const start = parseTime(step && step.timestamp);
      if (!Number.isNaN(prevEnd) && !Number.isNaN(start) && start - prevEnd > idleStallMs) {
        emit(ai, si, start - prevEnd, false);
      }
      const end = stepEnd(step);
      if (!Number.isNaN(end)) { prevEnd = end; prevEndSi = si; }
    });
    if (nowMs != null && !Number.isNaN(prevEnd) && nowMs - prevEnd > idleStallMs) {
      emit(ai, prevEndSi, nowMs - prevEnd, true);
    }
  });
  return out;
}

// costWhaleSignals: per session, flag each turn whose cost is at least whaleShare
// of the session's total step cost. severity is the share itself; ref is the
// turn (sid#ai.si). Shares are normalized within the session, never the graph.
/** @param {AgentNode[]} agents @param {string} sid @param {number} whaleShare @returns {Signal[]} */
function costWhaleSignals(agents, sid, whaleShare) {
  let total = 0;
  for (const a of agents) for (const step of (a.steps || [])) total += (step && step.cost_usd) || 0;
  if (total <= 0) return [];
  const out = [];
  agents.forEach((a, ai) => {
    (a.steps || []).forEach((step, si) => {
      const cost = (step && step.cost_usd) || 0;
      const share = cost / total;
      if (share < whaleShare) return;
      const tier = share >= 0.5 ? 'high' : share >= 0.3 ? 'med' : 'low';
      const summary = `Turn cost $${cost.toFixed(2)} — ${Math.round(share * 100)}% of session spend`;
      out.push({ kind: 'cost-whale', severity: share, tier, ref: `${sid}#${ai}.${si}`, summary });
    });
  });
  return out;
}

// signalPipsByAgent buckets detectSignals output into per-agent pip lists for the
// Timeline gutter: it keeps only signals whose ref belongs to `sessionId` (the one
// Gantt being plotted) and groups them by agentIndex. Because detectSignals already
// returns findings worst-first by severity, the grouping preserves that order — so
// each agent's kept pips are its worst. A heavy agent can flag many anomalies, so
// each list is capped at `cap`; the remainder is reported as `overflow` (never
// silently dropped — the Insights → Signals panel holds the full list). Malformed
// or cross-session refs are skipped, and each pip's tier is normalized to
// high|med|low so the renderer always has a color class. Returns
// Map<agentIndex, { pips: [{ ref, tier, summary, kind }], overflow }>.
/**
 * @param {Signal[]|null|undefined} signals
 * @param {string|null|undefined} sessionId
 * @param {number} [cap]
 * @returns {Map<number, {pips: {ref: string, tier: string, summary: string, kind: string}[], overflow: number}>}
 */
export function signalPipsByAgent(signals, sessionId, cap = 3) {
  const byAgent = new Map();
  if (!Array.isArray(signals) || !sessionId) return byAgent;
  const lim = cap > 0 ? cap : 3;
  for (const s of signals) {
    const p = parseRefKey(s && s.ref);
    if (!p || p.sessionId !== sessionId) continue;
    let e = byAgent.get(p.agentIndex);
    if (!e) { e = { pips: [], overflow: 0 }; byAgent.set(p.agentIndex, e); }
    if (e.pips.length < lim) {
      const tier = s.tier === 'high' || s.tier === 'med' ? s.tier : 'low';
      e.pips.push({ ref: s.ref, tier, summary: s.summary, kind: s.kind });
    } else {
      e.overflow += 1;
    }
  }
  return byAgent;
}
