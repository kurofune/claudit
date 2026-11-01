// Tests for the session header's anomaly detectors, retry grouping and
// percentile math (agents-insights-logic.js).

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { detectRetries, detectSignals, percentiles } from '../web/agents-insights-logic.js';

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
  assert.deepEqual(got.get('1:0'), { attempt: 2, ofRef: '0:0', errors: 1 });
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
  assert.deepEqual(got.get('1:0'), { attempt: 2, ofRef: '0:0', errors: 2 });
  assert.deepEqual(got.get('2:0'), { attempt: 3, ofRef: '0:0', errors: 2 });
});

test('detectRetries: calls with a different (kind,name,input-or-detail) key are not grouped', () => {
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

test('detectRetries groups by whole-input identity (input_key) when a call carries one, not by the capped input', () => {
  const capped = 'go test ./internal/aggregate/... -run TestContextCost'.padEnd(500, 'x');
  const call = (status, input_key) => ({ kind: 'exec', name: 'Bash', detail: 'go', input: capped, input_key, status });
  const distinct = { steps: [{ tools: [call('error', 'aaaa')] }, { tools: [call('ok', 'bbbb')] }] };
  assert.equal(detectRetries(distinct).size, 0, 'equal snippets with distinct input_key are two commands');
  const same = { steps: [{ tools: [call('error', 'aaaa')] }, { tools: [call('ok', 'aaaa')] }] };
  assert.deepEqual(detectRetries(same).get('1:0'), { attempt: 2, ofRef: '0:0', errors: 1 });
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
      main: { kind: 'main', steps: [{ cost_usd: 1.00 }, { cost_usd: 9.00 }] },
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
      // six $0.50 turns (share 0.05 each, at the floor) sit below 0.2; only the
      // $7.00 turn is a whale.
      main: { kind: 'main', steps: [
        { cost_usd: 0.50 }, { cost_usd: 0.50 }, { cost_usd: 0.50 },
        { cost_usd: 0.50 }, { cost_usd: 0.50 }, { cost_usd: 0.50 },
        { cost_usd: 7.00 },
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
      main: { kind: 'main', steps: [{ cost_usd: 5 }, { cost_usd: 3 }, { cost_usd: 2 }] },
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
      // Session A is small: each $1 turn is half of A's $2 total (a whale).
      // Against the graph total ($102) these would be ~1% and vanish.
      { session_id: 'sA', main: { kind: 'main', steps: [{ cost_usd: 1 }, { cost_usd: 1 }] }, children: [] },
      { session_id: 'sB', main: { kind: 'main', steps: [{ cost_usd: 100 }] }, children: [] },
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
        { cost_usd: 1.0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] }, // 0:0 retry seed
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'error' }] },    // 1:0
        { cost_usd: 0, tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', status: 'ok' }] },       // 2:0 -> storm sev 0.4
        { cost_usd: 9.0 },                                                                                // 3 -> whale sev 0.9
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
      main: { kind: 'main', steps: [{ cost_usd: 1.00 }, { cost_usd: 9.00 }] },
      children: [],
    }],
  };
  const whale = detectSignals(graph).find(s => s.kind === 'cost-whale');
  assert.equal(whale.summary, 'Turn cost $9.00 — 90% of session spend');
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
  // Step 0's Bash returns at t=0; step 1 starts 10 min later — well over the
  // 2-min default. The stall is refed at the resuming step (main step 1).
  const graph = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [
        { timestamp: 0, tools: [{ kind: 'exec', name: 'Bash', status: 'ok', started_at: 0, ended_at: 0 }] },
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
      main: { kind: 'main', status: 'running', steps: [{ timestamp: 0 }] },
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
      main: { kind: 'main', status: 'running', steps: [{ timestamp: 0 }] },
      children: [],
    }],
  };
  assert.equal(detectSignals(graph).filter(s => s.kind === 'idle-stall').length, 0);
});

test('detectSignals: idle-stall summary names the idle minutes', () => {
  const interStep = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', steps: [{ timestamp: 0, tools: [{ kind: 'exec', name: 'Bash', status: 'ok', started_at: 0, ended_at: 0 }] }, { timestamp: 600000 }] },
      children: [],
    }],
  };
  const a = detectSignals(interStep).find(s => s.kind === 'idle-stall');
  assert.equal(a.summary, 'Idle 10.0 min between turns');

  const trailing = {
    sessions: [{
      session_id: 's1',
      main: { kind: 'main', status: 'running', steps: [{ timestamp: 0 }] },
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

// ── detectSignals calibration — evidence, not shapes ─────────────────
// Each detector needs evidence beyond its shape: cost-whale an absolute dollar
// floor, retry-storm an earlier error on the SAME input, idle-stall unfinished
// work. Every Signal carries a `reason` naming the measured value and threshold.

const oneSession = (main, extra = {}) =>
  ({ sessions: [{ session_id: 's1', main: { kind: 'main', ...main }, children: [], ...extra }] });

test('detectSignals: a turn below the whale dollar floor is no cost-whale even at a dominant share', () => {
  // The session totals $0.001; the $0.0005 turns are each 50% of it.
  const graph = oneSession({ steps: [{ cost_usd: 0.0005 }, { cost_usd: 0.0004 }, { cost_usd: 0.0001 }] });
  assert.equal(detectSignals(graph).filter(s => s.kind === 'cost-whale').length, 0);
});

test('detectSignals: opts.whaleMinUSD moves the cost-whale floor', () => {
  const graph = oneSession({ steps: [{ cost_usd: 0.30 }, { cost_usd: 0.10 }] });
  assert.equal(detectSignals(graph).filter(s => s.kind === 'cost-whale').length, 0); // default $0.50
  const whales = detectSignals(graph, { whaleMinUSD: 0.25 }).filter(s => s.kind === 'cost-whale');
  assert.deepEqual(whales.map(s => s.ref), ['s1#0.0']);
});

test('detectSignals: a turn at the floor with share >= whaleShare is a cost-whale', () => {
  const graph = oneSession({ steps: [{ cost_usd: 0.50 }, { cost_usd: 0.50 }] });
  const whales = detectSignals(graph).filter(s => s.kind === 'cost-whale');
  assert.equal(whales.length, 2);
  assert.equal(whales[0].tier, 'high'); // share 0.5
});

test('detectSignals: cost-whale reason names the turn cost, share, session total, floor and share threshold', () => {
  const graph = oneSession({ steps: [{ cost_usd: 3.20 }, { cost_usd: 4.60 }] });
  const whale = detectSignals(graph).find(s => s.ref === 's1#0.0');
  assert.equal(whale.reason, 'turn $3.20 is 41% of session $7.80 — floor $0.50, share 20%');
});

test('detectRetries: groups on tool.input when non-empty, so same-detail calls with different inputs are distinct', () => {
  // Same kind/name/detail ("*.go"), different file inputs: not repeats of each other.
  const agent = {
    steps: [
      { tools: [{ kind: 'read', name: 'Read', detail: 'go', input: 'a.go', status: 'error' }] },
      { tools: [{ kind: 'read', name: 'Read', detail: 'go', input: 'b.go', status: 'ok' }] },
      { tools: [{ kind: 'read', name: 'Read', detail: 'go', input: 'c.go', status: 'ok' }] },
      { tools: [{ kind: 'read', name: 'Read', detail: 'go', input: 'd.go', status: 'ok' }] },
    ],
  };
  assert.equal(detectRetries(agent).size, 0);
});

test('detectSignals: three successful Read calls with different inputs produce no retry-storm', () => {
  const read = input => ({ tools: [{ kind: 'read', name: 'Read', detail: 'go', input, status: 'ok' }] });
  const graph = oneSession({ steps: [read('a.go'), read('b.go'), read('c.go')] });
  assert.equal(detectSignals(graph).filter(s => s.kind === 'retry-storm').length, 0);
});

test('detectSignals: three identical Bash inputs with the first two errored produce one retry-storm with its reason', () => {
  const bash = status => ({ tools: [{ kind: 'exec', name: 'Bash', detail: 'go', input: 'go test ./...', status }] });
  const graph = oneSession({ steps: [bash('error'), bash('error'), bash('ok')] });
  const storms = detectSignals(graph).filter(s => s.kind === 'retry-storm');
  assert.equal(storms.length, 1);
  assert.equal(storms[0].ref, 's1#0.2:0');
  assert.equal(storms[0].reason, '3 attempts of Bash `go test ./...`, 2 errored — threshold 3');
});

test('detectSignals: retry-storm reason falls back to tool.detail when input is empty', () => {
  const bash = status => ({ tools: [{ kind: 'exec', name: 'Bash', detail: 'go test', input: '', status }] });
  const graph = oneSession({ steps: [bash('error'), bash('ok'), bash('ok')] });
  const storm = detectSignals(graph).find(s => s.kind === 'retry-storm');
  assert.equal(storm.reason, '3 attempts of Bash `go test`, 1 errored — threshold 3');
});

test('detectSignals: a trailing gap after a tool with no result emits an idle-stall naming that tool', () => {
  // status "done" — the pending Read alone is the evidence of unfinished work.
  const graph = oneSession({ status: 'done', steps: [
    { timestamp: 0, tools: [{ kind: 'read', name: 'Read', started_at: 0 }] },
  ] });
  const stalls = detectSignals(graph, { nowMs: 252000 }).filter(s => s.kind === 'idle-stall');
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].ref, 's1#0.0');
  assert.equal(stalls[0].reason, 'idle 4.2 min after the last activity, Read has no result — threshold 2.0 min');
});

test('detectSignals: a trailing gap on a running agent emits an idle-stall even with every tool resolved', () => {
  const graph = oneSession({ status: 'running', steps: [
    { timestamp: 0, tools: [{ kind: 'exec', name: 'Bash', status: 'ok', started_at: 0, ended_at: 0 }] },
  ] });
  const stalls = detectSignals(graph, { nowMs: 252000 }).filter(s => s.kind === 'idle-stall');
  assert.equal(stalls.length, 1);
  assert.equal(stalls[0].reason, 'idle 4.2 min after the last activity, agent still running — threshold 2.0 min');
});

test('detectSignals: the same trailing gap on a done agent whose last tools all have a status emits no idle-stall', () => {
  const graph = oneSession({ status: 'done', steps: [
    { timestamp: 0, tools: [
      { kind: 'exec', name: 'Bash', status: 'ok', started_at: 0, ended_at: 0 },
      { kind: 'read', name: 'Read', status: 'error', started_at: 0, ended_at: 0 },
    ] },
  ] });
  assert.equal(detectSignals(graph, { nowMs: 252000 }).filter(s => s.kind === 'idle-stall').length, 0);
});

test('detectSignals: a gap after a text-only turn is the user’s turn, not an idle-stall', () => {
  // The main agent answered without calling a tool; the 10-min gap is the
  // operator reading, not the agent stalling.
  const graph = oneSession({ steps: [{ timestamp: 0 }, { timestamp: 600000 }] });
  assert.equal(detectSignals(graph).filter(s => s.kind === 'idle-stall').length, 0);
});

test('detectSignals: an inter-turn idle-stall reason names the gap, the tool it followed, and the threshold', () => {
  const graph = oneSession({ steps: [
    { timestamp: 0, tools: [{ kind: 'exec', name: 'Bash', status: 'ok', started_at: 0, ended_at: 0 }] },
    { timestamp: 600000 },
  ] });
  const stall = detectSignals(graph).find(s => s.kind === 'idle-stall');
  assert.equal(stall.reason, 'idle 10.0 min between turns after Bash returned — threshold 2.0 min');
});

test('detectSignals: an inter-turn idle-stall after a tool with no joined result says so, not "returned"', () => {
  const graph = oneSession({ steps: [
    { timestamp: 0, tools: [{ kind: 'read', name: 'Read', started_at: 0 }] },
    { timestamp: 600000 },
  ] });
  const stall = detectSignals(graph).find(s => s.kind === 'idle-stall');
  assert.equal(stall.reason, 'idle 10.0 min between turns after Read, which has no result — threshold 2.0 min');
});

test('detectSignals: slow-tool reason names the duration and the threshold it crossed', () => {
  const fast = () => ({ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 100 });
  const overP95 = oneSession({ steps: [
    { tools: [fast(), fast(), fast(), fast(), fast()] },
    { tools: [{ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 5000 }] },
  ] });
  assert.equal(detectSignals(overP95).find(s => s.kind === 'slow-tool').reason,
    'Bash 5.0s vs session p95 3.8s — threshold: above p95');

  const exec = () => ({ kind: 'exec', name: 'Bash', started_at: 0, ended_at: 4000 });
  const read = ms => ({ kind: 'read', name: 'Read', started_at: 0, ended_at: ms });
  const overKind = oneSession({ steps: [
    { tools: [exec(), exec(), exec(), exec(), exec()] },
    { tools: [read(10), read(10), read(50), read(10), read(10)] },
  ] });
  assert.equal(detectSignals(overKind).find(s => s.kind === 'slow-tool').reason,
    'Read 50ms vs read median 10ms — threshold 3× the kind median');
});

test('detectSignals: error-cascade reason names the run length, the first tool, and the threshold', () => {
  const err = () => ({ kind: 'exec', name: 'Bash', status: 'error' });
  const graph = oneSession({ steps: [{ tools: [err(), err(), err()] }] });
  assert.equal(detectSignals(graph).find(s => s.kind === 'error-cascade').reason,
    '3 consecutive tool errors starting with Bash — threshold 3');
});

test('detectSignals: every emitted signal carries a non-empty reason', () => {
  const err = () => ({ kind: 'exec', name: 'Bash', input: 'make', status: 'error' });
  const fast = () => ({ kind: 'read', name: 'Read', status: 'ok', started_at: 0, ended_at: 100 });
  const graph = oneSession({ status: 'running', steps: [
    { timestamp: 0, cost_usd: 5, tools: [err(), err(), err()] },
    { timestamp: 1000, cost_usd: 1, tools: [fast(), fast(), fast(), fast(), fast(),
      { kind: 'read', name: 'Read', status: 'ok', started_at: 0, ended_at: 9000 }] },
  ] });
  const signals = detectSignals(graph, { nowMs: 10 * 60000 });
  assert.deepEqual([...new Set(signals.map(s => s.kind))].sort(),
    ['cost-whale', 'error-cascade', 'idle-stall', 'retry-storm', 'slow-tool']);
  for (const s of signals) assert.ok(typeof s.reason === 'string' && s.reason.length > 0, s.kind);
});

// ── Named session fixtures ───────────────────────────────────────────
// Four sessions, one per outcome, each asserting the exact set of signal
// kinds it yields. Times are epoch ms; "now" sits 10 min past the last step,
// so a trailing gap is always past the 2-min idle threshold.
const T = 60000;
const NOW = 20 * T;
const tool = (name, input, status, at) =>
  ({ kind: name === 'Bash' ? 'exec' : 'read', name, detail: '', input, ...(status ? { status } : {}), started_at: at, ...(status ? { ended_at: at + 200 } : {}) });
const FIXTURES = {
  // A tidy run: cheap turns, every tool resolved, no gaps, agent done.
  clean: oneSession({ status: 'done', steps: [
    { timestamp: 0, cost_usd: 0.04, tools: [tool('Read', 'a.go', 'ok', 0)] },
    { timestamp: 1000, cost_usd: 0.05, tools: [tool('Read', 'b.go', 'ok', 1000)] },
    { timestamp: 2000, cost_usd: 0.03, tools: [tool('Bash', 'go test ./...', 'ok', 2000)] },
    { timestamp: 3000, cost_usd: 0.02 },
  ] }),
  // The same command failing three times running: a cascade and a storm.
  failed: oneSession({ status: 'done', steps: [
    { timestamp: 0, cost_usd: 0.04, tools: [tool('Bash', 'go test ./...', 'error', 0)] },
    { timestamp: 1000, cost_usd: 0.04, tools: [tool('Bash', 'go test ./...', 'error', 1000)] },
    { timestamp: 2000, cost_usd: 0.04, tools: [tool('Bash', 'go test ./...', 'error', 2000)] },
    { timestamp: 3000, cost_usd: 0.02 },
  ] }),
  // One failure, fixed on the next attempt: below every threshold.
  recovered: oneSession({ status: 'done', steps: [
    { timestamp: 0, cost_usd: 0.04, tools: [tool('Bash', 'go test ./...', 'error', 0)] },
    { timestamp: 1000, cost_usd: 0.04, tools: [tool('Bash', 'go test ./...', 'ok', 1000)] },
    { timestamp: 2000, cost_usd: 0.02 },
  ] }),
  // The last Read never got a result and nothing has happened since.
  incomplete: oneSession({ status: 'done', steps: [
    { timestamp: 0, cost_usd: 0.04, tools: [tool('Read', 'a.go', 'ok', 0)] },
    { timestamp: 1000, cost_usd: 0.04, tools: [tool('Read', 'b.go', undefined, 1000)] },
  ] }),
};

for (const [name, want] of Object.entries({
  clean: [],
  failed: ['error-cascade', 'retry-storm'],
  recovered: [],
  incomplete: ['idle-stall'],
})) {
  test(`detectSignals fixture "${name}" yields exactly [${want.join(', ')}]`, () => {
    const kinds = [...new Set(detectSignals(FIXTURES[name], { nowMs: NOW }).map(s => s.kind))].sort();
    assert.deepEqual(kinds, want);
  });
}

// ── percentiles ─────────────────────────────────────────────────────
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

test('detectSignals: Reads of different .png paths after one errored Read emit no retry-storm', () => {
  // The path tools carry the file path as input; detail is only the extension.
  const read = (input, status) => ({ tools: [{ kind: 'read', name: 'Read', detail: '.png', input, status }] });
  const graph = oneSession({ steps: [
    read('/x/a.png', 'error'), read('/x/b.png', 'ok'), read('/x/c.png', 'ok'), read('/x/d.png', 'ok'),
  ] });
  assert.equal(detectSignals(graph).filter(s => s.kind === 'retry-storm').length, 0);
});

test('detectSignals: three Reads of one path with the first two errored emit one retry-storm naming the path', () => {
  const read = status => ({ tools: [{ kind: 'read', name: 'Read', detail: '.png', input: '/x/a.png', status }] });
  const graph = oneSession({ steps: [read('error'), read('error'), read('ok')] });
  const storms = detectSignals(graph).filter(s => s.kind === 'retry-storm');
  assert.equal(storms.length, 1);
  assert.equal(storms[0].reason, '3 attempts of Read `/x/a.png`, 2 errored — threshold 3');
});
