// Tests for the Insights aggregations + signal detectors
// (agents-insights-logic.js), carved out of agents-logic.test.js. Imports
// stay on the agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  DURATION_EDGES,
  agentLabel,
  agentTimeRows,
  binSeries,
  contextSeries,
  costPareto,
  detectRetries,
  detectSignals,
  durationHistogram,
  errorRates,
  groupBy,
  percentiles,
  sessionTimeRows,
  signalPipsByAgent,
  toolMix,
} from '../web/agents-logic.js';

// ── detectRetries (Phase 3 — retry detection) ───────────────────────
// Keys/ofRef are agent-relative tool coords "si:ti" (the suffix of the full
// tool refKey), composable into a full refKey via `${sid}#${ai}.${coord}`.

test('detectRetries: an errored call followed by the same call marks the retry as attempt 2 linked to the first', () => {
  const agent = {
    steps: [
      { tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] },
      { tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },
    ],
  };
  const got = detectRetries(agent);
  assert.equal(got.has('0:0'), false); // the first attempt is not itself a retry
  assert.deepEqual(got.get('1:0'), { attempt: 2, ofRef: '0:0' });
});

test('detectRetries: a successful call followed by the same call is not a retry', () => {
  const agent = {
    steps: [
      { tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },
      { tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },
    ],
  };
  assert.equal(detectRetries(agent).size, 0);
});

test('detectRetries: a chain of two errors then a repeat marks attempts 2 and 3, both linked to the first', () => {
  const agent = {
    steps: [
      { tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] },
      { tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] },
      { tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },
    ],
  };
  const got = detectRetries(agent);
  assert.deepEqual(got.get('1:0'), { attempt: 2, ofRef: '0:0' });
  assert.deepEqual(got.get('2:0'), { attempt: 3, ofRef: '0:0' });
});

test('detectRetries: calls with a different (kind,name,detail) key are not grouped', () => {
  const agent = {
    steps: [{
      tools: [
        { kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' },
        { kind: 'exec', name: 'Bash', detail: 'go build', status: 'ok' }, // different detail
        { kind: 'read', name: 'Read', detail: 'go test', status: 'ok' },  // different kind/name
      ],
    }],
  };
  assert.equal(detectRetries(agent).size, 0);
});

// ── detectSignals (Phase 3a — anomaly detection) ────────────────────
// Signals are { kind, severity (number [0,1]), tier ('high'|'med'|'low'),
// ref (a refKey), summary }. detectSignals(graph, opts) returns them sorted
// worst-first by severity. Detectors so far: cost-whale, retry-storm.

test('detectSignals: returns [] for a null or empty-sessions graph', () => {
  assert.deepEqual(detectSignals(null), []);
  assert.deepEqual(detectSignals({ sessions: [] }), []);
});

test('detectSignals: a turn costing >= the whale share of session total emits a cost-whale at sid#ai.si with severity = share', () => {
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ cost_usd: 0.10 }, { cost_usd: 0.90 }] },
      children: [],
    }],
  };
  const signals = detectSignals(graph);
  assert.equal(signals.length, 1);
  assert.equal(signals[0].kind, 'cost-whale');
  assert.equal(signals[0].ref, 's1#0.1');
  assert.equal(signals[0].severity, 0.9);
});

test('detectSignals: turns below the whale share are excluded even alongside a whale', () => {
  const graph = {
    sessions: [{
      session_id: 's1',
      // six 0.05 turns (share 0.05 each) sit below 0.2; only the 0.70 turn is a whale.
      main: { kind: 'main', steps: [
        { cost_usd: 0.05 }, { cost_usd: 0.05 }, { cost_usd: 0.05 },
        { cost_usd: 0.05 }, { cost_usd: 0.05 }, { cost_usd: 0.05 },
        { cost_usd: 0.70 },
      ] },
      children: [],
    }],
  };
  const whales = detectSignals(graph).filter(s => s.kind === 'cost-whale');
  assert.equal(whales.length, 1);
  assert.equal(whales[0].ref, 's1#0.6');
});

test('detectSignals: cost-whale tier is high/med/low by share band', () => {
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ cost_usd: 0.5 }, { cost_usd: 0.3 }, { cost_usd: 0.2 }] },
      children: [],
    }],
  };
  const byRef = new Map(detectSignals(graph).map(s => [s.ref, s]));
  assert.equal(byRef.get('s1#0.0').tier, 'high'); // share 0.5
  assert.equal(byRef.get('s1#0.1').tier, 'med');  // share 0.3
  assert.equal(byRef.get('s1#0.2').tier, 'low');  // share 0.2
});

test('detectSignals: whale share is per-session, not against the whole-graph total', () => {
  const graph = {
    sessions: [
      // Session A is tiny: each 0.1 turn is half of A's 0.2 total (a whale).
      // Against the graph total (10.2) these would be ~1% and vanish.
      { session_id: 'sA', main: { kind: 'main', steps: [{ cost_usd: 0.1 }, { cost_usd: 0.1 }] }, children: [] },
      { session_id: 'sB', main: { kind: 'main', steps: [{ cost_usd: 10.0 }] }, children: [] },
    ],
  };
  const byRef = new Map(detectSignals(graph).map(s => [s.ref, s]));
  assert.equal(byRef.get('sA#0.0').severity, 0.5);
  assert.equal(byRef.get('sA#0.1').severity, 0.5);
});

test('detectSignals: a retry group reaching retryStormMin attempts emits a retry-storm at the worst attempt', () => {
  const graph = {
    sessions: [{
      session_id: 'sX',
      main: { kind: 'main', steps: [
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] }, // 0:0 attempt 1
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] }, // 1:0 attempt 2
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },    // 2:0 attempt 3
      ] },
      children: [],
    }],
  };
  const storms = detectSignals(graph).filter(s => s.kind === 'retry-storm');
  assert.equal(storms.length, 1);
  assert.equal(storms[0].ref, 'sX#0.2:0'); // the highest-attempt tool, not the first call
  assert.equal(storms[0].severity, 0.4);   // (3 - 1) / 5
});

test('detectSignals: a lone retry (2 attempts, below retryStormMin) is not a storm', () => {
  const graph = {
    sessions: [{
      session_id: 'sX',
      main: { kind: 'main', steps: [
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] }, // attempt 1
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },    // attempt 2
      ] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'retry-storm').length, 0);
});

test('detectSignals: retry-storm tier rises with attempt count (low=3, med=4, high>=5)', () => {
  const err = d => ({ cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: d, status: 'error' }] });
  const ok = d => ({ cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: d, status: 'ok' }] });
  const graph = {
    sessions: [{
      session_id: 'sX',
      main: { kind: 'main', steps: [
        err('a'), ok('a'), ok('a'),                   // group a: 3 attempts -> low
        err('b'), ok('b'), ok('b'), ok('b'),          // group b: 4 attempts -> med
        err('c'), ok('c'), ok('c'), ok('c'), ok('c'), // group c: 5 attempts -> high
      ] },
      children: [],
    }],
  };
  const bySev = new Map(detectSignals(graph)
    .filter(s => s.kind === 'retry-storm').map(s => [s.severity, s.tier]));
  assert.equal(bySev.get(0.4), 'low');  // (3-1)/5
  assert.equal(bySev.get(0.6), 'med');  // (4-1)/5
  assert.equal(bySev.get(0.8), 'high'); // (5-1)/5
});

test('detectSignals: signals are sorted worst-first by severity across mixed kinds', () => {
  // Emission order puts the retry-storm (severity 0.4) BEFORE the cost-whale
  // (severity 0.9); sorting must flip them so the whale leads.
  const graph = {
    sessions: [{
      session_id: 'sX',
      main: { kind: 'main', steps: [
        { cost_usd: 0.1, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] }, // 0:0 retry seed
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] },    // 1:0
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },       // 2:0 -> storm sev 0.4
        { cost_usd: 0.9 },                                                                                // 3 -> whale sev 0.9
      ] },
      children: [],
    }],
  };
  const signals = detectSignals(graph);
  assert.equal(signals.length, 2);
  assert.equal(signals[0].kind, 'cost-whale');  // 0.9 leads
  assert.equal(signals[1].kind, 'retry-storm'); // 0.4 trails
  assert.ok(signals[0].severity >= signals[1].severity);
});

test('detectSignals: cost-whale summary names the turn cost and its share', () => {
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ cost_usd: 0.10 }, { cost_usd: 0.90 }] },
      children: [],
    }],
  };
  const whale = detectSignals(graph).find(s => s.kind === 'cost-whale');
  assert.equal(whale.summary, 'Turn cost $0.90 — 90% of session spend');
});

test('detectSignals: retry-storm summary names the tool, attempt count, and detail', () => {
  const graph = {
    sessions: [{
      session_id: 'sX',
      main: { kind: 'main', steps: [
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] },
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] },
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },
      ] },
      children: [],
    }],
  };
  const storm = detectSignals(graph).find(s => s.kind === 'retry-storm');
  assert.equal(storm.summary, 'Bash retried 3× (go test)');
});

// ── detectSignals (Phase 3b — slow-tool / error-cascade / idle-stall) ──
// Same contract as 3a: { kind, severity [0,1], tier, ref, summary }. Tool wall-clock
// uses numeric started_at/ended_at (parseTime passes numbers through).

test('detectSignals: a tool whose wall-clock exceeds the session p95 emits a slow-tool at sid#ai.si:ti', () => {
  // Five 100ms tools and one 5000ms tool. p95 of those six lands at ~3775ms, so
  // only the 5000ms tool clears it. It lives at main step 1, tool 0 -> s1#0.1:0.
  const fast = () => ({ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 100 });
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { tools: [fast(), fast(), fast(), fast(), fast()] },
        { tools: [{ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 5000 }] },
      ] },
      children: [],
    }],
  };
  const slow = detectSignals(graph).filter(s => s.kind === 'slow-tool');
  assert.equal(slow.length, 1);
  assert.equal(slow[0].ref, 's1#0.1:0');
  assert.ok(slow[0].severity > 0 && slow[0].severity <= 1);
});

test('detectSignals: a session with fewer than slowToolMin timed tools yields no slow-tool', () => {
  // Three tools, one wildly slower — but below the 5-tool floor the percentile
  // is meaningless, so nothing is flagged. (Regression guard for the min cut.)
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ tools: [
        { kind: 'exec', name: 'Bash', started_at: 0, ended_at: 100 },
        { kind: 'exec', name: 'Bash', started_at: 0, ended_at: 100 },
        { kind: 'exec', name: 'Bash', started_at: 0, ended_at: 9000 },
      ] }] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'slow-tool').length, 0);
});

test('detectSignals: a tool within global p95 but over slowMultiple× its kind median is a slow-tool', () => {
  // Five 4000ms execs push p95 into exec territory; among the reads (median 10ms)
  // a single 50ms read is 5× its kind median yet far below p95, so it is flagged
  // only via the relative-to-peers path. It lives at main step 1, tool 2.
  const exec = () => ({ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 4000 });
  const read = ms => ({ kind: 'read', name: 'Read', started_at: 0, ended_at: ms });
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { tools: [exec(), exec(), exec(), exec(), exec()] },
        { tools: [read(10), read(10), read(50), read(10), read(10)] },
      ] },
      children: [],
    }],
  };
  const slow = detectSignals(graph).filter(s => s.kind === 'slow-tool');
  assert.equal(slow.length, 1);
  assert.equal(slow[0].ref, 's1#0.1:2');
});

test('detectSignals: slow-tool tier bands by severity (over-p95 ratio)', () => {
  // 38 tools at 100ms hold p95 ≈ 101.5; a 130ms tool is ~0.28 over (med band)
  // and a 300ms tool is ~2× over (clamped, high band). Tools 38 and 39.
  const base = Array.from({ length: 38 }, () => ({ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 100 }));
  const tools = [...base,
    { kind: 'exec', name: 'Bash', started_at: 0, ended_at: 130 },
    { kind: 'exec', name: 'Bash', started_at: 0, ended_at: 300 }];
  const graph = { sessions: [{ session_id: 's1', main: { kind: 'main', steps: [{ tools }] }, children: [] }] };
  const byRef = new Map(detectSignals(graph).filter(s => s.kind === 'slow-tool').map(s => [s.ref, s]));
  assert.equal(byRef.get('s1#0.0:39').tier, 'high'); // ~2× over p95
  assert.equal(byRef.get('s1#0.0:38').tier, 'med');  // ~0.28 over p95
});

test('detectSignals: slow-tool summary names the tool, its seconds, and the multiple', () => {
  // p95 path: 5000ms tool against p95 ≈ 3775ms (1.3×).
  const fast = () => ({ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 100 });
  const overP95 = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { tools: [fast(), fast(), fast(), fast(), fast()] },
        { tools: [{ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 5000 }] },
      ] },
      children: [],
    }],
  };
  const a = detectSignals(overP95).find(s => s.kind === 'slow-tool');
  assert.equal(a.summary, 'Bash took 5.0s — 1.3× the session p95');

  // kind-median path: a 50ms read, 5× the 10ms read median, under p95.
  const exec = () => ({ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 4000 });
  const read = ms => ({ kind: 'read', name: 'Read', started_at: 0, ended_at: ms });
  const overKind = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { tools: [exec(), exec(), exec(), exec(), exec()] },
        { tools: [read(10), read(10), read(50), read(10), read(10)] },
      ] },
      children: [],
    }],
  };
  const b = detectSignals(overKind).find(s => s.kind === 'slow-tool');
  assert.equal(b.summary, 'Read took 0.1s — 5.0× the typical read');
});

test('detectSignals: errorCascadeMin consecutive errored tools emit one error-cascade at the first error', () => {
  // Three errored Bash calls back-to-back across two steps (0:0, 0:1, 1:0).
  const err = () => ({ kind: 'exec', name: 'Bash', status: 'error' });
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { tools: [err(), err()] },
        { tools: [err()] },
      ] },
      children: [],
    }],
  };
  const cascades = detectSignals(graph).filter(s => s.kind === 'error-cascade');
  assert.equal(cascades.length, 1);
  assert.equal(cascades[0].ref, 's1#0.0:0'); // the cascade's first errored tool
});

test('detectSignals: two consecutive errors (below errorCascadeMin) are not a cascade', () => {
  const err = () => ({ kind: 'exec', name: 'Bash', status: 'error' });
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ tools: [err(), err()] }] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'error-cascade').length, 0);
});

test('detectSignals: error-cascade severity and tier rise with run length (low=3, med=4, high>=5)', () => {
  const err = () => ({ kind: 'exec', name: 'Bash', status: 'error' });
  const ok = () => ({ kind: 'exec', name: 'Bash', status: 'ok' });
  const run = n => Array.from({ length: n }, err);
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ tools: [
        ...run(3), ok(),   // len 3 -> low,  sev 0.4
        ...run(4), ok(),   // len 4 -> med,  sev 0.6
        ...run(5),         // len 5 -> high, sev 0.8
      ] }] },
      children: [],
    }],
  };
  const bySev = new Map(detectSignals(graph)
    .filter(s => s.kind === 'error-cascade').map(s => [s.severity, s.tier]));
  assert.equal(bySev.get(0.4), 'low');  // (3-1)/5
  assert.equal(bySev.get(0.6), 'med');  // (4-1)/5
  assert.equal(bySev.get(0.8), 'high'); // (5-1)/5
});

test('detectSignals: error-cascade summary names the run length and the first tool', () => {
  const err = name => ({ kind: 'exec', name, status: 'error' });
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ tools: [err('Bash'), err('Bash'), err('Bash')] }] },
      children: [],
    }],
  };
  const cascade = detectSignals(graph).find(s => s.kind === 'error-cascade');
  assert.equal(cascade.summary, '3 tools errored back-to-back, starting with Bash');
});

test('detectSignals: a gap over idleStallMs between two steps emits an idle-stall at the later step', () => {
  // Step 0 ends at t=0 (no tools); step 1 starts 10 min later — well over the
  // 2-min default. The stall is refed at the resuming step (main step 1).
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { timestamp: 0 },
        { timestamp: 600000 },
      ] },
      children: [],
    }],
  };
  const stalls = detectSignals(graph).filter(s => s.kind === 'idle-stall');
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].ref, 's1#0.1');
  assert.ok(stalls[0].severity > 0 && stalls[0].severity <= 1);
  assert.equal(stalls[0].tier, 'high'); // gap is 5× the threshold
});

test('detectSignals: with opts.nowMs, a dangling final step idle past the threshold emits a trailing idle-stall', () => {
  // One step ending at t=0; "now" is 10 min later — the run stalled and never
  // resumed. The trailing stall is refed at that last step.
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ timestamp: 0 }] },
      children: [],
    }],
  };
  const stalls = detectSignals(graph, { nowMs: 600000 }).filter(s => s.kind === 'idle-stall');
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].ref, 's1#0.0');
});

test('detectSignals: without opts.nowMs, a dangling final step yields no trailing idle-stall (static-safe)', () => {
  // The same dangling step, but no live clock supplied — a static report must
  // not fabricate a trailing stall (no Date.now fallback).
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ timestamp: 0 }] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'idle-stall').length, 0);
});

test('detectSignals: idle-stall summary names the idle minutes', () => {
  const interStep = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ timestamp: 0 }, { timestamp: 600000 }] },
      children: [],
    }],
  };
  const a = detectSignals(interStep).find(s => s.kind === 'idle-stall');
  assert.equal(a.summary, 'Idle 10.0 min between turns');

  const trailing = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ timestamp: 0 }] },
      children: [],
    }],
  };
  const b = detectSignals(trailing, { nowMs: 600000 }).find(s => s.kind === 'idle-stall');
  assert.equal(b.summary, 'Idle 10.0 min after the last activity');
});

test('detectSignals never emits a runaway-context signal (the inferred-window guess was removed)', () => {
  // Even a prompt that nearly fills the standard tier yields no context signal:
  // the window size is unknowable from the transcript, so we no longer guess it.
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { context_tokens: 50000 },
        { context_tokens: 180000 },
        { context_tokens: 100000 },
      ] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'runaway-context').length, 0);
});

test('detectSignals: a long-running tool spanning the gap is not an idle-stall', () => {
  // Step 0 starts at t=0 but runs a tool until t=600000; step 1 starts right
  // when it finishes. Measured from the tool's end the gap is 0 — the agent was
  // working, not idle. (Would be a false positive if measured from step start.)
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { timestamp: 0, tools: [{ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 600000 }] },
        { timestamp: 600000 },
      ] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'idle-stall').length, 0);
});

test('detectSignals: a successful tool breaks the run so two short error runs are not a cascade', () => {
  // error, error, ok, error, error — two runs of 2, never 3 in a row.
  const err = () => ({ kind: 'exec', name: 'Bash', status: 'error' });
  const ok = () => ({ kind: 'exec', name: 'Bash', status: 'ok' });
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ tools: [err(), err(), ok(), err(), err()] }] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'error-cascade').length, 0);
});

// ── signalPipsByAgent (Phase 3d — Timeline gutter pips) ──────────────
// signalPipsByAgent buckets detectSignals output into per-agent pip lists for
// the ONE session the Timeline plots. Returns Map<agentIndex, { pips, overflow }>.
test('signalPipsByAgent: returns an empty Map for non-array signals or a falsy sessionId', () => {
  assert.equal(signalPipsByAgent(null, 's1').size, 0);
  assert.equal(signalPipsByAgent(undefined, 's1').size, 0);
  assert.equal(signalPipsByAgent([{ ref: 's1#0', tier: 'high', summary: 'x', kind: 'cost-whale' }], '').size, 0);
});

test('signalPipsByAgent: groups signals by agent index, preserving worst-first input order, with {ref, tier, summary, kind} pips', () => {
  // detectSignals returns severity-sorted; the bucketer must keep that order so
  // the kept pips per agent are the worst. Agent 0 gets two (in order), agent 2 one.
  const signals = [
    { ref: 's1#0.3', tier: 'high', summary: 'whale', kind: 'cost-whale' },
    { ref: 's1#2.1:0', tier: 'med', summary: 'slow Bash', kind: 'slow-tool' },
    { ref: 's1#0.5', tier: 'low', summary: 'retry', kind: 'retry-storm' },
  ];
  const byAgent = signalPipsByAgent(signals, 's1');
  assert.deepEqual([...byAgent.keys()].sort(), [0, 2]);
  assert.deepEqual(byAgent.get(0), {
    pips: [
      { ref: 's1#0.3', tier: 'high', summary: 'whale', kind: 'cost-whale' },
      { ref: 's1#0.5', tier: 'low', summary: 'retry', kind: 'retry-storm' },
    ],
    overflow: 0,
  });
  assert.deepEqual(byAgent.get(2), {
    pips: [{ ref: 's1#2.1:0', tier: 'med', summary: 'slow Bash', kind: 'slow-tool' }],
    overflow: 0,
  });
});

test('signalPipsByAgent: excludes signals whose ref belongs to a different session', () => {
  // The Timeline plots one session; a signal for s2 must not leak into s1's pips,
  // even though both reference agent index 0.
  const signals = [
    { ref: 's1#0.1', tier: 'high', summary: 'mine', kind: 'cost-whale' },
    { ref: 's2#0.1', tier: 'high', summary: 'theirs', kind: 'cost-whale' },
  ];
  const byAgent = signalPipsByAgent(signals, 's1');
  assert.equal(byAgent.size, 1);
  assert.deepEqual(byAgent.get(0).pips.map(p => p.summary), ['mine']);
});

test('signalPipsByAgent: skips signals with a malformed or missing ref', () => {
  const signals = [
    { ref: 's1#0.1', tier: 'high', summary: 'good', kind: 'cost-whale' },
    { ref: 'garbage', tier: 'high', summary: 'bad', kind: 'cost-whale' },
    { ref: undefined, tier: 'high', summary: 'none', kind: 'cost-whale' },
  ];
  const byAgent = signalPipsByAgent(signals, 's1');
  assert.equal(byAgent.size, 1);
  assert.deepEqual(byAgent.get(0).pips.map(p => p.summary), ['good']);
});

test('signalPipsByAgent: caps an agent at `cap` pips and reports the rest as overflow', () => {
  // A heavy agent can flag many anomalies; the gutter keeps the worst `cap` and
  // discloses the remainder as a count (the full list lives in Insights → Signals).
  const signals = [0, 1, 2, 3, 4].map(i =>
    ({ ref: `s1#0.${i}`, tier: 'low', summary: `s${i}`, kind: 'slow-tool' }));
  const e = signalPipsByAgent(signals, 's1', 3).get(0);
  assert.deepEqual(e.pips.map(p => p.summary), ['s0', 's1', 's2']);
  assert.equal(e.overflow, 2);
});

test('signalPipsByAgent: normalizes an unknown or missing tier to low (so the pip always has a color class)', () => {
  const signals = [
    { ref: 's1#0.0', tier: 'high', summary: 'a', kind: 'cost-whale' },
    { ref: 's1#1.0', tier: 'weird', summary: 'b', kind: 'cost-whale' },
    { ref: 's1#2.0', summary: 'c', kind: 'cost-whale' },
  ];
  const byAgent = signalPipsByAgent(signals, 's1');
  assert.equal(byAgent.get(0).pips[0].tier, 'high');
  assert.equal(byAgent.get(1).pips[0].tier, 'low');
  assert.equal(byAgent.get(2).pips[0].tier, 'low');
});

// ── toolMix (Insights 2a) ───────────────────────────────────────────
test('toolMix returns [] for null/empty agents', () => {
  assert.deepEqual(toolMix(null), []);
  assert.deepEqual(toolMix([]), []);
});

test('toolMix groups tools by kind, summing call count (tool.count, default 1)', () => {
  const agents = [{
    steps: [
      { tools: [ { kind: 'read', count: 2 }, { kind: 'exec' } ] },
      { tools: [ { kind: 'read', count: 3 } ] },
    ],
  }];
  const mix = toolMix(agents);
  const byKind = Object.fromEntries(mix.map(r => [r.kind, r.count]));
  assert.equal(byKind.read, 5); // 2 + 3
  assert.equal(byKind.exec, 1); // default count
});

test('toolMix sums per-tool wall-clock (ended_at − started_at) per kind; missing timing is 0', () => {
  const agents = [{
    steps: [
      { tools: [
        { kind: 'read', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:02Z' }, // 2000ms
        { kind: 'exec', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:05Z' }, // 5000ms
        { kind: 'read' }, // no timing → 0
      ] },
      { tools: [
        { kind: 'read', started_at: '2026-05-01T12:00:10Z', ended_at: '2026-05-01T12:00:11Z' }, // 1000ms
      ] },
    ],
  }];
  const byKind = Object.fromEntries(toolMix(agents).map(r => [r.kind, r.durationMs]));
  assert.equal(byKind.read, 3000); // 2000 + 0 + 1000
  assert.equal(byKind.exec, 5000);
});

test('toolMix distributes each step cost across its tools by call-share, per kind', () => {
  const agents = [{
    steps: [
      // stepCalls = 1 + 1 = 2 → each kind gets 0.30 * (1/2) = 0.15
      { cost_usd: 0.30, tools: [ { kind: 'read', count: 1 }, { kind: 'exec', count: 1 } ] },
      // stepCalls = 3 → read (the only tool) gets the whole 0.10
      { cost_usd: 0.10, tools: [ { kind: 'read', count: 3 } ] },
    ],
  }];
  const byKind = Object.fromEntries(toolMix(agents).map(r => [r.kind, r.costUSD]));
  assert.ok(Math.abs(byKind.read - 0.25) < 1e-9); // 0.15 + 0.10
  assert.ok(Math.abs(byKind.exec - 0.15) < 1e-9);
});

test('toolMix folds tools with no kind into "other"', () => {
  const agents = [{ steps: [ { tools: [ { name: 'Mystery' }, { kind: 'other' } ] } ] }];
  const byKind = Object.fromEntries(toolMix(agents).map(r => [r.kind, r.count]));
  assert.equal(byKind.other, 2);
});

test('toolMix sorts rows by costUSD descending', () => {
  const agents = [{
    steps: [
      { cost_usd: 0.05, tools: [ { kind: 'read', count: 1 } ] },
      { cost_usd: 0.50, tools: [ { kind: 'exec', count: 1 } ] },
      { cost_usd: 0.20, tools: [ { kind: 'web', count: 1 } ] },
    ],
  }];
  assert.deepEqual(toolMix(agents).map(r => r.kind), ['exec', 'web', 'read']);
});

// ── percentiles (Insights 2b) ───────────────────────────────────────
test('percentiles returns one NaN per requested point on empty values', () => {
  const out = percentiles([], [50, 95]);
  assert.equal(out.length, 2);
  assert.ok(out.every(Number.isNaN));
});

test('percentiles at p0/p100 returns min/max', () => {
  assert.deepEqual(percentiles([5, 1, 9, 3], [0, 100]), [1, 9]);
});

test('percentiles interpolates linearly between ranks (p50 of [1,2,3,4] = 2.5)', () => {
  assert.deepEqual(percentiles([1, 2, 3, 4], [50]), [2.5]);
});

test('percentiles returns the lone value for any point on a single-element input', () => {
  assert.deepEqual(percentiles([7], [0, 50, 95, 100]), [7, 7, 7, 7]);
});

test('percentiles sorts unsorted input and ignores non-numeric/NaN values', () => {
  // Non-numbers (NaN, '3' string, null, undefined) are dropped, leaving the
  // numbers [4,1,2] → sorted [1,2,4]; p50 interpolates to 2, p100 = 4.
  assert.deepEqual(percentiles([4, NaN, '3', 1, null, 2, undefined], [50, 100]), [2, 4]);
});

// ── durationHistogram (Insights 2b) ─────────────────────────────────
test('durationHistogram returns zero-count buckets + empty values for null/empty agents', () => {
  const h = durationHistogram(null, { edges: [100, 1000] });
  assert.deepEqual(h.values, []);
  // edges [100,1000] → 3 buckets: [0,100) [100,1000) [1000,∞)
  assert.deepEqual(h.buckets.map(b => [b.lo, b.hi]), [[0, 100], [100, 1000], [1000, Infinity]]);
  assert.ok(h.buckets.every(b => b.count === 0 && Array.isArray(b.kinds) && b.kinds.length === 0));
});

test('durationHistogram counts each timed tool into its [lo,hi) bucket and collects the flat values', () => {
  const agents = [{
    steps: [
      { tools: [
        { kind: 'read', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:00.050Z' }, // 50ms → [0,100)
        { kind: 'read', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:00.300Z' }, // 300ms → [100,1000)
      ] },
      { tools: [
        { kind: 'exec', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:02Z' }, // 2000ms → [1000,∞)
      ] },
    ],
  }];
  const h = durationHistogram(agents, { edges: [100, 1000] });
  assert.deepEqual(h.buckets.map(b => b.count), [1, 1, 1]);
  assert.deepEqual(h.values.slice().sort((a, b) => a - b), [50, 300, 2000]);
});

test('durationHistogram skips tools with missing/invalid/non-positive timing', () => {
  const agents = [{
    steps: [{ tools: [
      { kind: 'read' },                                                                            // no timing
      { kind: 'read', started_at: '2026-05-01T12:00:00Z' },                                        // half timing
      { kind: 'read', started_at: '2026-05-01T12:00:05Z', ended_at: '2026-05-01T12:00:05Z' },      // zero span
      { kind: 'read', started_at: '2026-05-01T12:00:05Z', ended_at: '2026-05-01T12:00:00Z' },      // negative span
      { kind: 'read', started_at: 'garbage', ended_at: 'also-garbage' },                           // unparseable
      { kind: 'exec', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:00.080Z' },  // 80ms → kept
    ] }],
  }];
  const h = durationHistogram(agents, { edges: [100, 1000] });
  assert.deepEqual(h.values, [80]);
  assert.deepEqual(h.buckets.map(b => b.count), [1, 0, 0]);
});

test('durationHistogram collects the distinct tool kinds present in each bucket', () => {
  const agents = [{
    steps: [{ tools: [
      { kind: 'read', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:00.050Z' }, // 50ms → [0,100)
      { kind: 'read', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:00.060Z' }, // 60ms → [0,100), dup kind
      { kind: 'exec', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:00.070Z' }, // 70ms → [0,100)
      { started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:02Z' },                   // 2000ms → [1000,∞), no kind → 'other'
    ] }],
  }];
  const h = durationHistogram(agents, { edges: [100, 1000] });
  assert.deepEqual(h.buckets.map(b => b.kinds), [['read', 'exec'], [], ['other']]);
});

test('durationHistogram defaults to the log-scale DURATION_EDGES when no opts given', () => {
  const agents = [{
    steps: [{ tools: [
      { kind: 'read', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:00.300Z' }, // 300ms → [250,500)
      { kind: 'exec', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:45Z' },      // 45s → [30000,60000)
    ] }],
  }];
  const h = durationHistogram(agents);
  // DURATION_EDGES = [100,250,500,1000,2500,5000,10000,30000,60000] → 10 buckets.
  assert.equal(h.buckets.length, DURATION_EDGES.length + 1);
  const bucketWith = ms => h.buckets.find(b => b.lo <= ms && ms < b.hi);
  assert.deepEqual(bucketWith(300), { lo: 250, hi: 500, count: 1, kinds: ['read'] });
  assert.deepEqual(bucketWith(45000), { lo: 30000, hi: 60000, count: 1, kinds: ['exec'] });
});

// ── costPareto (Insights 2c) ────────────────────────────────────────
test('costPareto returns a zeroed/empty result for null/empty agents', () => {
  const empty = { total: 0, count: 0, rows: [], headline: null };
  assert.deepEqual(costPareto(null), empty);
  assert.deepEqual(costPareto([]), empty);
});

test('costPareto ignores turns with no positive cost', () => {
  const agents = [{ steps: [
    { cost_usd: 0 },
    { cost_usd: 0.5 },
    { /* missing cost_usd */ },
    { cost_usd: -1 },
  ] }];
  const p = costPareto(agents);
  assert.equal(p.count, 1);
  assert.equal(p.total, 0.5);
});

test('costPareto sums total and counts across all agents', () => {
  const agents = [
    { steps: [{ cost_usd: 1 }, { cost_usd: 2 }] },
    { steps: [{ cost_usd: 3 }] },
  ];
  const p = costPareto(agents);
  assert.equal(p.count, 3);
  assert.equal(p.total, 6);
});

test('costPareto ranks rows by cost descending with 1-based rank, cost, agentLabel', () => {
  const agents = [
    { kind: 'main', steps: [{ cost_usd: 1 }, { cost_usd: 5 }] },
    { kind: 'sub', agent_type: 'Explore', steps: [{ cost_usd: 3 }] },
  ];
  const rows = costPareto(agents).rows;
  assert.deepEqual(rows.map(r => r.rank), [1, 2, 3]);
  assert.deepEqual(rows.map(r => r.cost), [5, 3, 1]);
  assert.deepEqual(rows.map(r => r.agentLabel), ['main', 'Explore', 'main']);
});

test('costPareto carries per-row share and running cumulative share', () => {
  const agents = [{ steps: [{ cost_usd: 5 }, { cost_usd: 3 }, { cost_usd: 2 }] }];
  const rows = costPareto(agents).rows; // total 10, ranked [5,3,2]
  assert.deepEqual(rows.map(r => r.share), [0.5, 0.3, 0.2]);
  assert.deepEqual(rows.map(r => r.cumShare), [0.5, 0.8, 1.0]);
});

test('costPareto caps rows at opts.topN (default 10) but total/count span every turn', () => {
  const steps = Array.from({ length: 15 }, (_, i) => ({ cost_usd: i + 1 })); // 1..15, total 120
  const p = costPareto([{ steps }]);            // default topN = 10
  assert.equal(p.count, 15);
  assert.equal(p.total, 120);
  assert.equal(p.rows.length, 10);
  assert.equal(p.rows[0].cost, 15);             // ranking still over the full set
  assert.equal(p.rows[9].rank, 10);
  // cumShare keeps accumulating against the full total, not just the shown rows
  assert.ok(p.rows[9].cumShare < 1);
  const p3 = costPareto([{ steps }], { topN: 3 });
  assert.equal(p3.rows.length, 3);
  assert.equal(p3.count, 15);
});

test('costPareto headline: top headlinePct% of turns (ceil, >=1) drives spendShare, with thresholdCost', () => {
  // 10 turns costing 10,9,...,1 → total 55. Top 10% = ceil(10*0.1)=1 turn (cost 10).
  const steps = Array.from({ length: 10 }, (_, i) => ({ cost_usd: 10 - i }));
  const h = costPareto([{ steps }], { headlinePct: 10 }).headline;
  assert.equal(h.turnsPct, 10);
  assert.equal(h.turnCount, 1);
  assert.equal(h.spendShare, 10 / 55);
  assert.equal(h.thresholdCost, 10);           // cheapest turn still inside the top slice
});

test('costPareto headline rounds the slice up and thresholdCost is the slice boundary', () => {
  // 10 turns, top 25% = ceil(10*0.25)=3 turns: costs 10+9+8=27 of 55.
  const steps = Array.from({ length: 10 }, (_, i) => ({ cost_usd: 10 - i }));
  const h = costPareto([{ steps }], { headlinePct: 25 }).headline;
  assert.equal(h.turnCount, 3);
  assert.equal(h.spendShare, 27 / 55);
  assert.equal(h.thresholdCost, 8);
});

test('errorRates returns a zeroed/empty result for null/empty agents', () => {
  const empty = { total: 0, errors: 0, rate: 0, rows: [], worst: [] };
  assert.deepEqual(errorRates(null), empty);
  assert.deepEqual(errorRates([]), empty);
});

test('errorRates counts total tool rows and errored rows; non-error statuses count toward total only', () => {
  const agents = [{ steps: [
    { tools: [
      { name: 'Bash', kind: 'exec', status: 'error' },
      { name: 'Read', kind: 'read', status: 'ok' },
      { name: 'Edit', kind: 'edit' },              // no status → not an error
      { name: 'Bash', kind: 'exec', status: 'error' },
    ] },
  ] }];
  const e = errorRates(agents);
  assert.equal(e.total, 4);
  assert.equal(e.errors, 2);
  assert.equal(e.rate, 0.5);
});

test('errorRates groups rows by kind, only kinds with >=1 error, total spans non-errored rows of that kind', () => {
  const agents = [{ steps: [
    { tools: [
      { name: 'Bash', kind: 'exec', status: 'error' },
      { name: 'Bash', kind: 'exec', status: 'ok' },    // same kind, clean → counts in exec.total
      { name: 'Read', kind: 'read', status: 'ok' },     // clean kind → excluded from rows
      { name: 'Read', kind: 'read', status: 'ok' },
    ] },
  ] }];
  const rows = errorRates(agents).rows;
  assert.deepEqual(rows, [{ kind: 'exec', total: 2, errors: 1, rate: 0.5 }]);
});

test('errorRates sorts rows by errors desc, then rate desc, then kind asc', () => {
  // Insertion order (fetch, edit, read, exec) is the REVERSE of the expected
  // sorted order, so a no-op (insertion-order) result would fail this.
  const agents = [{ steps: [{ tools: [
    // fetch: 2 errors of 8 → rate .25 (same errors as read/edit, lower rate → last)
    ...Array.from({ length: 2 }, () => ({ kind: 'fetch', status: 'error' })),
    ...Array.from({ length: 6 }, () => ({ kind: 'fetch', status: 'ok' })),
    // edit: 2 errors of 4 → rate .5
    ...Array.from({ length: 2 }, () => ({ kind: 'edit', status: 'error' })),
    ...Array.from({ length: 2 }, () => ({ kind: 'edit', status: 'ok' })),
    // read: 2 errors of 2 → rate 1.0
    ...Array.from({ length: 2 }, () => ({ kind: 'read', status: 'error' })),
    // exec: 3 errors of 6 → rate .5 (most errors → first)
    ...Array.from({ length: 3 }, () => ({ kind: 'exec', status: 'error' })),
    ...Array.from({ length: 3 }, () => ({ kind: 'exec', status: 'ok' })),
  ] }] }];
  const rows = errorRates(agents).rows;
  // exec (3 errors) first; then the 2-error kinds by rate desc: read(1.0), edit(.5), fetch(.25)
  assert.deepEqual(rows.map(r => r.kind), ['exec', 'read', 'edit', 'fetch']);
});

test('errorRates folds tools with no kind into "other"', () => {
  const agents = [{ steps: [{ tools: [
    { name: 'mystery', status: 'error' },             // no kind
    { name: 'mystery2', status: 'ok' },               // no kind
  ] }] }];
  const rows = errorRates(agents).rows;
  assert.deepEqual(rows, [{ kind: 'other', total: 2, errors: 1, rate: 0.5 }]);
});

test('errorRates worst lists erroring tools by name with kind, sorted errors desc / rate desc / name asc', () => {
  const agents = [{ steps: [{ tools: [
    // Apple: 1 error of 2 → rate .5
    { name: 'Apple', kind: 'exec', status: 'error' },
    { name: 'Apple', kind: 'exec', status: 'ok' },
    // Beta: 2 errors of 2 → rate 1.0 (most errors → first)
    { name: 'Beta', kind: 'read', status: 'error' },
    { name: 'Beta', kind: 'read', status: 'error' },
    // Cherry: 1 error of 1 → rate 1.0 (ties Apple on errors, higher rate → before Apple)
    { name: 'Cherry', kind: 'edit', status: 'error' },
    // Daisy: clean → excluded
    { name: 'Daisy', kind: 'fetch', status: 'ok' },
  ] }] }];
  const worst = errorRates(agents).worst;
  assert.deepEqual(worst, [
    { name: 'Beta', kind: 'read', total: 2, errors: 2, rate: 1 },
    { name: 'Cherry', kind: 'edit', total: 1, errors: 1, rate: 1 },
    { name: 'Apple', kind: 'exec', total: 2, errors: 1, rate: 0.5 },
  ]);
});

test('errorRates caps worst at opts.topN (default 8) but overall + rows span every tool', () => {
  // 12 distinct erroring tool names, all kind 'exec', each 1 error.
  const tools = Array.from({ length: 12 }, (_, i) => ({
    name: `T${String(i).padStart(2, '0')}`, kind: 'exec', status: 'error',
  }));
  const e = errorRates([{ steps: [{ tools }] }]);
  assert.equal(e.total, 12);
  assert.equal(e.errors, 12);
  assert.equal(e.worst.length, 8);                 // default cap
  assert.equal(e.rows[0].errors, 12);              // per-kind row spans all 12
  const e3 = errorRates([{ steps: [{ tools }] }], { topN: 3 });
  assert.equal(e3.worst.length, 3);
  assert.equal(e3.total, 12);
});

test('errorRates tolerates null agents, null steps, and null tool entries', () => {
  const agents = [
    null,
    { steps: null },
    { steps: [null, { tools: null }, { tools: [null, { kind: 'exec', name: 'Bash', status: 'error' }, null] }] },
  ];
  const e = errorRates(agents);
  assert.equal(e.total, 1);
  assert.equal(e.errors, 1);
  assert.deepEqual(e.rows, [{ kind: 'exec', total: 1, errors: 1, rate: 1 }]);
  assert.deepEqual(e.worst, [{ name: 'Bash', kind: 'exec', total: 1, errors: 1, rate: 1 }]);
});

// ── contextSeries (Insights 2e) ─────────────────────────────────────
test('contextSeries returns a zeroed/empty result for null/empty agents', () => {
  const empty = {
    turns: 0, series: [], peakContext: 0, cacheHit: 0,
    totals: { input: 0, output: 0, cacheWrite: 0, cacheRead: 0, total: 0 },
  };
  assert.deepEqual(contextSeries(null), empty);
  assert.deepEqual(contextSeries([]), empty);
});

test('contextSeries reports the absolute peak prompt size (no inferred window %)', () => {
  // The transcript records neither the context-window size nor the 1M-beta flag,
  // so contextSeries exposes only the real peak — never a guessed fill fraction.
  const r = contextSeries([{ steps: [{ context_tokens: 180000 }, { context_tokens: 397000 }] }]);
  assert.equal(r.peakContext, 397000);
  assert.equal(r.capacity, undefined);
  assert.equal(r.peakFill, undefined);
});

test('contextSeries folds each step\'s Go-named token fields, combining 5m+1h cache writes', () => {
  const agents = [{ steps: [{
    tokens: { InputTokens: 100, OutputTokens: 30, CacheCreate5mTokens: 7, CacheCreate1hTokens: 3, CacheReadTokens: 60 },
  }] }];
  const pt = contextSeries(agents).series[0];
  assert.equal(pt.input, 100);
  assert.equal(pt.output, 30);
  assert.equal(pt.cacheWrite, 10);   // 7 + 3
  assert.equal(pt.cacheRead, 60);
  assert.equal(pt.total, 200);       // 100 + 30 + 10 + 60
});

test('contextSeries reads per-turn context from context_tokens and reports peakContext as the max', () => {
  const agents = [{ steps: [
    { context_tokens: 1200 },
    { context_tokens: 8000 },
    { context_tokens: 3500 },
  ] }];
  const r = contextSeries(agents);
  assert.deepEqual(r.series.map(p => p.context), [1200, 8000, 3500]);
  assert.equal(r.peakContext, 8000);
});

test('contextSeries per-turn cacheHit is cacheRead/(cacheRead+input+cacheWrite), 0 when the denominator is 0', () => {
  const agents = [{ steps: [
    { tokens: { InputTokens: 100, CacheCreate5mTokens: 10, CacheReadTokens: 60 } },  // 60/(60+100+10)
    { tokens: { OutputTokens: 50 } },                                                // denom 0 → 0
  ] }];
  const series = contextSeries(agents).series;
  assert.equal(series[0].cacheHit, 60 / 170);
  assert.equal(series[1].cacheHit, 0);
});

test('contextSeries overall cacheHit aggregates totals (not the mean of per-turn ratios)', () => {
  const agents = [{ steps: [
    { tokens: { InputTokens: 10, CacheReadTokens: 90 } },   // per-turn 0.9, denom 100
    { tokens: { InputTokens: 300, CacheReadTokens: 0 } },   // per-turn 0,   denom 300
  ] }];
  // mean-of-ratios would be 0.45; aggregate is 90/(100+300) = 0.225
  assert.equal(contextSeries(agents).cacheHit, 0.225);
});

test('contextSeries totals sum every band across all steps and all agents', () => {
  const agents = [
    { steps: [{ tokens: { InputTokens: 10, OutputTokens: 1, CacheCreate5mTokens: 2, CacheReadTokens: 5 } }] },
    { steps: [{ tokens: { InputTokens: 20, OutputTokens: 3, CacheCreate1hTokens: 4, CacheReadTokens: 7 } }] },
  ];
  const t = contextSeries(agents).totals;
  assert.deepEqual(t, { input: 30, output: 4, cacheWrite: 6, cacheRead: 12, total: 52 });
});

test('contextSeries orders the series chronologically across agents, carrying t (epoch ms) and agentLabel', () => {
  // Insertion order (300,100 then 200) deliberately differs from time order so a
  // missing sort can't pass by coincidence.
  const agents = [
    { kind: 'main', steps: [
      { timestamp: '2026-01-01T00:00:03Z', context_tokens: 3 },
      { timestamp: '2026-01-01T00:00:01Z', context_tokens: 1 },
    ] },
    { kind: 'sub', agent_type: 'Explore', steps: [
      { timestamp: '2026-01-01T00:00:02Z', context_tokens: 2 },
    ] },
  ];
  const series = contextSeries(agents).series;
  assert.deepEqual(series.map(p => p.context), [1, 2, 3]);          // sorted by time
  assert.deepEqual(series.map(p => p.agentLabel), ['main', 'Explore', 'main']);
  assert.deepEqual(series.map(p => p.t), [
    Date.parse('2026-01-01T00:00:01Z'),
    Date.parse('2026-01-01T00:00:02Z'),
    Date.parse('2026-01-01T00:00:03Z'),
  ]);
});

test('contextSeries keeps a step with no/garbage timestamp, sets t=NaN, and orders it last', () => {
  // The undated step is inserted FIRST so a wrong impl that kept insertion order
  // (or dropped it) would be caught.
  const agents = [{ steps: [
    { context_tokens: 9 },                                  // no timestamp → t NaN
    { timestamp: '2026-01-01T00:00:05Z', context_tokens: 5 },
  ] }];
  const r = contextSeries(agents);
  assert.equal(r.turns, 2);                                 // undated step still counted
  assert.deepEqual(r.series.map(p => p.context), [5, 9]);   // dated first, undated last
  assert.ok(Number.isNaN(r.series[1].t));
});

test('contextSeries tolerates null agents, null steps, and missing token tuples', () => {
  const agents = [
    null,
    { steps: null },
    { steps: [null, { context_tokens: 4 }, { tokens: { CacheReadTokens: 6, InputTokens: 4 } }] },
  ];
  const r = contextSeries(agents);
  assert.equal(r.turns, 3);                                        // 3 real steps survive
  assert.equal(r.peakContext, 4);
  assert.deepEqual(r.totals, { input: 4, output: 0, cacheWrite: 0, cacheRead: 6, total: 10 });
  assert.equal(r.cacheHit, 6 / 10);                                // 6 / (6 + 4)
});

// ── binSeries (Insights 2e — render downsampling) ───────────────────
test('binSeries returns an empty array for an empty series', () => {
  assert.deepEqual(binSeries([], 10), []);
});

test('binSeries returns one bin per turn (count 1, values preserved) when length <= maxBins', () => {
  const series = [
    { context: 100, input: 10, output: 1, cacheWrite: 2, cacheRead: 5, total: 18 },
    { context: 200, input: 20, output: 3, cacheWrite: 0, cacheRead: 7, total: 30 },
  ];
  assert.deepEqual(binSeries(series, 10), [
    { context: 100, input: 10, output: 1, cacheWrite: 2, cacheRead: 5, total: 18, count: 1 },
    { context: 200, input: 20, output: 3, cacheWrite: 0, cacheRead: 7, total: 30, count: 1 },
  ]);
});

test('binSeries buckets consecutive turns when length > maxBins: context is the max, bands sum, count is #turns', () => {
  const mk = (context, k) => ({ context, input: k, output: k, cacheWrite: k, cacheRead: k, total: k * 4 });
  const series = [mk(100, 1), mk(300, 2), mk(200, 3), mk(150, 4)];  // 4 turns
  assert.deepEqual(binSeries(series, 2), [
    { context: 300, input: 3, output: 3, cacheWrite: 3, cacheRead: 3, total: 12, count: 2 },  // turns 1+2, max 300
    { context: 200, input: 7, output: 7, cacheWrite: 7, cacheRead: 7, total: 28, count: 2 },  // turns 3+4, max 200
  ]);
});

test('binSeries never emits more than maxBins bins, and every turn lands in exactly one bin', () => {
  const mk = i => ({ context: i, input: 1, output: 0, cacheWrite: 0, cacheRead: 0, total: 1 });
  for (const [n, cap] of [[10, 3], [10, 4], [10, 7], [100, 12], [1000, 120]]) {
    const series = Array.from({ length: n }, (_, i) => mk(i));
    const bins = binSeries(series, cap);
    assert.ok(bins.length <= cap, `n=${n} cap=${cap} → ${bins.length} bins`);
    assert.equal(bins.reduce((s, b) => s + b.count, 0), n);   // no turn dropped or double-counted
  }
});

// ── groupBy (Insights 2f) ───────────────────────────────────────────
test('groupBy returns a zeroed/empty result for null/empty agents (default dimension kind)', () => {
  const empty = { dimension: 'kind', total: { count: 0, durationMs: 0, costUSD: 0 }, rows: [] };
  assert.deepEqual(groupBy(null), empty);
  assert.deepEqual(groupBy([]), empty);
});

test('groupBy groups tool rows by kind (default): count of calls, total wall-clock, total cost', () => {
  const agents = [{ steps: [
    { cost_usd: 0.10, tools: [{ kind: 'exec', started_at: '2026-01-01T00:00:00.000Z', ended_at: '2026-01-01T00:00:01.000Z' }] }, // 1000ms
    { cost_usd: 0.04, tools: [{ kind: 'read', started_at: '2026-01-01T00:00:00.000Z', ended_at: '2026-01-01T00:00:00.500Z' }] }, //  500ms
    { cost_usd: 0.06, tools: [{ kind: 'exec', started_at: '2026-01-01T00:00:00.000Z', ended_at: '2026-01-01T00:00:03.000Z' }] }, // 3000ms
  ] }];
  const rows = groupBy(agents).rows;
  const exec = rows.find(r => r.key === 'exec');
  const read = rows.find(r => r.key === 'read');
  assert.equal(exec.count, 2);
  assert.equal(exec.durationMs, 4000);
  assert.ok(Math.abs(exec.costUSD - 0.16) < 1e-9);
  assert.equal(read.count, 1);
  assert.equal(read.durationMs, 500);
  assert.ok(Math.abs(read.costUSD - 0.04) < 1e-9);
});

test('groupBy reports medianMs per group (reuses percentiles); NaN when the group has no measured rows', () => {
  const d = (kind, ms) => ({ kind, started_at: '2026-01-01T00:00:00.000Z', ended_at: new Date(Date.parse('2026-01-01T00:00:00.000Z') + ms).toISOString() });
  const agents = [{ steps: [
    { tools: [d('exec', 1000), d('exec', 3000), d('exec', 2000)] },        // median 2000
    { tools: [{ kind: 'web' }] },                                          // no timestamps → unmeasured
  ] }];
  const rows = groupBy(agents).rows;
  assert.equal(rows.find(r => r.key === 'exec').medianMs, 2000);
  assert.ok(Number.isNaN(rows.find(r => r.key === 'web').medianMs));
});

test("groupBy dimension 'status' groups by tool status; missing/empty folds to 'none'", () => {
  const agents = [{ steps: [{ tools: [
    { kind: 'exec', status: 'error' },
    { kind: 'read', status: 'ok' },
    { kind: 'edit' },               // missing status → 'none'
    { kind: 'exec', status: 'error' },
  ] }] }];
  const res = groupBy(agents, { dimension: 'status' });
  assert.equal(res.dimension, 'status');
  const by = Object.fromEntries(res.rows.map(r => [r.key, r.count]));
  assert.deepEqual(by, { error: 2, ok: 1, none: 1 });
});

test("groupBy dimension 'model' groups by the parent step.model; missing folds to '(none)'", () => {
  const agents = [{ steps: [
    { model: 'claude-opus-4-8', tools: [{ kind: 'exec' }, { kind: 'read' }] },
    { model: 'claude-haiku-4-5', tools: [{ kind: 'exec' }] },
    { tools: [{ kind: 'web' }] },   // no model → '(none)'
  ] }];
  const res = groupBy(agents, { dimension: 'model' });
  assert.equal(res.dimension, 'model');
  const by = Object.fromEntries(res.rows.map(r => [r.key, r.count]));
  assert.deepEqual(by, { 'claude-opus-4-8': 2, 'claude-haiku-4-5': 1, '(none)': 1 });
});

test("groupBy dimension 'agent' groups by agentLabel (main vs agent_type)", () => {
  const agents = [
    { kind: 'main', steps: [{ tools: [{ kind: 'exec' }, { kind: 'read' }] }] },
    { agent_type: 'Explore', steps: [{ tools: [{ kind: 'read' }] }] },
  ];
  const res = groupBy(agents, { dimension: 'agent' });
  assert.equal(res.dimension, 'agent');
  const by = Object.fromEntries(res.rows.map(r => [r.key, r.count]));
  assert.deepEqual(by, { main: 2, Explore: 1 });
});

test('groupBy sorts rows by cost desc, then count desc, then key asc', () => {
  // Insertion order (mm, bb, aa, cc) is NOT the expected sorted order, so a
  // no-op (Map insertion-order) result would fail. cc wins on cost; aa/bb/mm
  // tie on cost (.10) → count desc puts aa(5) first; bb/mm tie on count → key asc.
  const agents = [{ steps: [
    { cost_usd: 0.10, tools: [{ kind: 'mm', count: 2 }] },
    { cost_usd: 0.10, tools: [{ kind: 'bb', count: 2 }] },
    { cost_usd: 0.10, tools: [{ kind: 'aa', count: 5 }] },
    { cost_usd: 0.30, tools: [{ kind: 'cc', count: 1 }] },
  ] }];
  const rows = groupBy(agents).rows;
  assert.deepEqual(rows.map(r => r.key), ['cc', 'aa', 'bb', 'mm']);
});

test('groupBy gives each group its costShare and countShare of the totals', () => {
  const agents = [{ steps: [
    { cost_usd: 0.30, tools: [{ kind: 'exec', count: 1 }] },   // cost-heavy, call-light
    { cost_usd: 0.10, tools: [{ kind: 'read', count: 3 }] },   // cost-light, call-heavy
  ] }];
  const res = groupBy(agents);
  assert.deepEqual(res.total, { count: 4, durationMs: 0, costUSD: 0.4 });
  const exec = res.rows.find(r => r.key === 'exec');
  const read = res.rows.find(r => r.key === 'read');
  assert.ok(Math.abs(exec.costShare - 0.75) < 1e-9 && Math.abs(exec.countShare - 0.25) < 1e-9);
  assert.ok(Math.abs(read.costShare - 0.25) < 1e-9 && Math.abs(read.countShare - 0.75) < 1e-9);
});

test('groupBy apportions a turn cost across its tools by call-share (toolMix convention)', () => {
  // One $0.90 turn, 3 calls (exec×2, read×1) → exec 0.60, read 0.30.
  const agents = [{ steps: [
    { cost_usd: 0.90, tools: [{ kind: 'exec', count: 2 }, { kind: 'read', count: 1 }] },
  ] }];
  const rows = groupBy(agents).rows;
  assert.ok(Math.abs(rows.find(r => r.key === 'exec').costUSD - 0.60) < 1e-9);
  assert.ok(Math.abs(rows.find(r => r.key === 'read').costUSD - 0.30) < 1e-9);
});

test('groupBy tolerates null agents/steps/tool entries and sums t.count for repeated calls', () => {
  const agents = [
    null,
    { steps: null },
    { steps: [null, { tools: null }, { tools: [null, { kind: 'exec', count: 3 }, null] }] },
  ];
  const res = groupBy(agents);
  assert.equal(res.total.count, 3);
  assert.deepEqual(res.rows.map(r => ({ key: r.key, count: r.count })), [{ key: 'exec', count: 3 }]);
});

// ── agentTimeRows / sessionTimeRows (Insights "Time" panel, item 16) ──
test('agentTimeRows: one row per agent with label + buckets, sorted by totalMs desc', () => {
  const small = { kind: 'subagent', agent_type: 'Explore', status: 'done', started_at: 0, ended_at: 500, steps: [
    { timestamp: 0, duration_ms: 500, gen_ms: 100, tools: [{ ended_at: 300 }] },
  ] };
  const big = { kind: 'main', status: 'done', started_at: 0, ended_at: 2000, steps: [
    { timestamp: 0, duration_ms: 2000, gen_ms: 600, tools: [{ ended_at: 1000 }] },
  ] };
  const { rows, overflow, total } = agentTimeRows([small, big]);
  assert.equal(overflow, 0);
  assert.deepEqual(rows.map(r => r.label), ['main', 'Explore']);
  // big: gen 600, tool 1000, idle 400. small: gen 100, tool 300, idle 100.
  assert.deepEqual(rows[0], { label: 'main', genMs: 600, toolMs: 1000, idleMs: 400, totalMs: 2000 });
  assert.deepEqual(rows[1], { label: 'Explore', genMs: 100, toolMs: 300, idleMs: 100, totalMs: 500 });
  assert.deepEqual(total, { genMs: 700, toolMs: 1300, idleMs: 500, totalMs: 2500 });
});

test('agentTimeRows: drops zero-total rows; empty/null input yields empty rows + zero total', () => {
  const idle = { kind: 'subagent', agent_type: 'noop', status: 'done', started_at: 0, ended_at: 0, steps: [] };
  const busy = { kind: 'main', status: 'done', started_at: 0, ended_at: 1000, steps: [
    { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [] },
  ] };
  const { rows } = agentTimeRows([idle, busy]);
  assert.deepEqual(rows.map(r => r.label), ['main']);
  assert.deepEqual(agentTimeRows(null), {
    rows: [], overflow: 0, total: { genMs: 0, toolMs: 0, idleMs: 0, totalMs: 0 },
  });
});

test('agentTimeRows: caps at topN with an overflow count; total still sums ALL rows', () => {
  const mk = (i, dur) => ({ kind: 'subagent', agent_type: `a${i}`, status: 'done',
    started_at: 0, ended_at: dur, steps: [{ timestamp: 0, duration_ms: dur, gen_ms: dur, tools: [] }] });
  const agents = [mk(1, 100), mk(2, 300), mk(3, 200)];
  const { rows, overflow, total } = agentTimeRows(agents, { topN: 2 });
  assert.deepEqual(rows.map(r => r.label), ['a2', 'a3']);
  assert.equal(overflow, 1);
  assert.equal(total.totalMs, 600); // the truncated a1 still counts in the totals
});

test('sessionTimeRows: one row per session with id/cwd + buckets, sorted, capped with overflow', () => {
  const mkSession = (id, cwd, dur) => ({
    session_id: id, cwd,
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: dur, steps: [
      { timestamp: 0, duration_ms: dur, gen_ms: dur / 2, tools: [{ ended_at: dur / 2 }] },
    ] },
    children: [],
  });
  const sessions = [mkSession('s1', '/a', 400), mkSession('s2', '/b', 1000), mkSession('s3', '/c', 600)];
  const { rows, overflow, total } = sessionTimeRows(sessions, { topN: 2 });
  assert.deepEqual(rows.map(r => r.sessionId), ['s2', 's3']);
  assert.deepEqual(rows.map(r => r.cwd), ['/b', '/c']);
  // s2: gen 500, tool 500, idle 0 → the buckets ride on each row.
  assert.deepEqual(rows[0], { sessionId: 's2', cwd: '/b', genMs: 500, toolMs: 500, idleMs: 0, totalMs: 1000 });
  assert.equal(overflow, 1);
  assert.equal(total.totalMs, 2000); // truncated s1 still counts
});

test('sessionTimeRows: skips null / zero-time sessions; null input is empty', () => {
  const empty = { session_id: 'e', cwd: '/e', main: null, children: [] };
  const busy = { session_id: 'b', cwd: '/b',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 100, steps: [
      { timestamp: 0, duration_ms: 100, gen_ms: 100, tools: [] },
    ] },
    children: [] };
  const { rows } = sessionTimeRows([null, empty, busy]);
  assert.deepEqual(rows.map(r => r.sessionId), ['b']);
  assert.deepEqual(sessionTimeRows(null), {
    rows: [], overflow: 0, total: { genMs: 0, toolMs: 0, idleMs: 0, totalMs: 0 },
  });
});
