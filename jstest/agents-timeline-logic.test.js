// Tests for the Timeline (Gantt) math (agents-timeline-logic.js), carved
// out of agents-logic.test.js. Imports stay on the agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  agentBar,
  agentPhaseAt,
  agentTimeRollup,
  buildFlowLayout,
  buildTimeline,
  costHeat,
  criticalSpans,
  fitSegmentLabel,
  flattenSession,
  idleSegments,
  laneCount,
  makeTimeScale,
  narrativeStrip,
  packLanes,
  pctOfAgent,
  pickTimelineSid,
  playheadBounds,
  promptBands,
  playheadStats,
  refKey,
  segKindColor,
  segTooltip,
  sessionStats,
  sessionTimeRollup,
  stepSegments,
  timelineAtTime,
  timelineBounds,
  timelineKinds,
  timelineRowOrder,
  timelineSessionList,
  toolSegments,
  turnTimeBuckets,
  zoomAnchorScrollLeft,
  zoomClampPxPerMs,
} from '../web/agents-logic.js';

// ── packLanes ───────────────────────────────────────────────────────
const span = (id, startMs, endMs) => ({ id, started_at: startMs, ended_at: endMs });

test('packLanes: a single agent goes on lane 0', () => {
  const packed = packLanes([span('m', 0, 100)]);
  assert.equal(packed.length, 1);
  assert.equal(packed[0].lane, 0);
});

test('packLanes: overlapping agents get separate lanes', () => {
  const packed = packLanes([span('a', 0, 100), span('b', 50, 150)]);
  const byId = Object.fromEntries(packed.map(p => [p.agent.id, p.lane]));
  assert.notEqual(byId.a, byId.b);
  assert.equal(laneCount(packed), 2);
});

test('packLanes: non-overlapping agents share a lane', () => {
  // a:[0,100], b:[100,200] just touch — they can share lane 0.
  const packed = packLanes([span('a', 0, 100), span('b', 100, 200)]);
  const byId = Object.fromEntries(packed.map(p => [p.agent.id, p.lane]));
  assert.equal(byId.a, byId.b);
  assert.equal(laneCount(packed), 1);
});

test('packLanes: greedy across three agents (two stack, third reuses freed lane)', () => {
  // a:[0,300] spans all; b:[0,100], c:[150,250] are disjoint so they share
  // a second lane. Result: 2 lanes total.
  const packed = packLanes([span('a', 0, 300), span('b', 0, 100), span('c', 150, 250)]);
  assert.equal(laneCount(packed), 2);
  const byId = Object.fromEntries(packed.map(p => [p.agent.id, p.lane]));
  assert.equal(byId.b, byId.c); // b and c reuse the same lane
  assert.notEqual(byId.a, byId.b);
});

test('packLanes: sorts by start time regardless of input order', () => {
  const packed = packLanes([span('late', 200, 300), span('early', 0, 100)]);
  assert.equal(packed[0].agent.id, 'early');
  assert.equal(packed[1].agent.id, 'late');
});

test('packLanes: drops agents with unparseable start', () => {
  const packed = packLanes([span('ok', 0, 100), { id: 'bad', started_at: 'x', ended_at: 'y' }]);
  assert.equal(packed.length, 1);
  assert.equal(packed[0].agent.id, 'ok');
});

test('laneCount of empty is 0', () => {
  assert.equal(laneCount([]), 0);
});

// ── makeTimeScale / agentBar ────────────────────────────────────────
test('makeTimeScale maps endpoints to [0,width]', () => {
  const sc = makeTimeScale({ startMs: 0, endMs: 1000, width: 500 });
  assert.equal(sc.x(0), 0);
  assert.equal(sc.x(1000), 500);
  assert.equal(sc.x(500), 250);
});

test('makeTimeScale clamps out-of-range timestamps', () => {
  const sc = makeTimeScale({ startMs: 0, endMs: 1000, width: 500 });
  assert.equal(sc.x(-100), 0);
  assert.equal(sc.x(5000), 500);
});

test('makeTimeScale collapses a zero/inverted span to x=0', () => {
  const sc = makeTimeScale({ startMs: 100, endMs: 100, width: 500 });
  assert.equal(sc.x(100), 0);
});

test('agentBar floors width to minBlock so a near-instant agent stays visible', () => {
  const sc = makeTimeScale({ startMs: 0, endMs: 10_000, width: 1000, minBlock: 3 });
  const item = packLanes([span('x', 5000, 5001)])[0];
  const bar = agentBar(item, sc);
  assert.equal(bar.x, 500);
  assert.equal(bar.width, 3); // raw width ~0.1px floored to minBlock
  assert.equal(bar.lane, 0);
});

test('agentBar computes proportional width for a real span', () => {
  const sc = makeTimeScale({ startMs: 0, endMs: 1000, width: 100, minBlock: 2 });
  const item = packLanes([span('x', 250, 750)])[0];
  const bar = agentBar(item, sc);
  assert.equal(bar.x, 25);
  assert.equal(bar.width, 50);
});


// ── buildFlowLayout ─────────────────────────────────────────────────
test('buildFlowLayout centers the main node and links each child', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', status: 'running' },
    children: [
      { kind: 'subagent', agent_type: 'Explore', status: 'done' },
      { kind: 'subagent', agent_type: 'review', status: 'running' },
    ],
  };
  const layout = buildFlowLayout(session, { width: 800, nodeW: 120, nodeH: 48 });
  assert.equal(layout.nodes.length, 3);
  const main = layout.nodes.find(n => n.kind === 'main');
  // Main horizontally centered.
  assert.equal(main.x + main.w / 2, 400);
  assert.equal(main.key, 's1#0');
  // One edge per child, from main down to each child.
  assert.equal(layout.edges.length, 2);
  for (const e of layout.edges) {
    assert.equal(e.x1, main.x + main.w / 2);
    assert.equal(e.y1, main.y + main.h);
  }
  // Children keyed by flatten order; height encloses both rows.
  const childKeys = layout.nodes.filter(n => n.kind === 'subagent').map(n => n.key).sort();
  assert.deepEqual(childKeys, ['s1#1', 's1#2']);
  assert.ok(layout.height > main.y + main.h);
});

test('buildFlowLayout handles a main with no children', () => {
  const layout = buildFlowLayout({ session_id: 's1', main: { kind: 'main' }, children: [] }, { width: 400 });
  assert.equal(layout.nodes.length, 1);
  assert.equal(layout.edges.length, 0);
});

// ── timelineBounds ──────────────────────────────────────────────────
test('timelineBounds: a single done agent bounds to its own span', () => {
  const b = timelineBounds([{ started_at: 0, ended_at: 100, status: 'done' }], 500);
  assert.deepEqual(b, { startMs: 0, endMs: 100 });
});

test('timelineBounds: a running agent extends the window to now', () => {
  const b = timelineBounds([
    { started_at: 0, ended_at: 100, status: 'done' },
    { started_at: 50, status: 'running' },
  ], 500);
  assert.deepEqual(b, { startMs: 0, endMs: 500 });
});

test('timelineBounds: an empty array is null', () => {
  assert.equal(timelineBounds([], 500), null);
});

test('timelineBounds: all unparseable starts is null', () => {
  assert.equal(timelineBounds([{ started_at: 'x' }], 999), null);
});

test('timelineBounds: a NaN ended_at falls back to the agent start', () => {
  const b = timelineBounds([{ started_at: 300, ended_at: null, status: 'done' }], 999);
  assert.deepEqual(b, { startMs: 300, endMs: 300 });
});

// ── buildTimeline ───────────────────────────────────────────────────
test('buildTimeline returns the empty layout for a session with no agents', () => {
  const layout = buildTimeline({ session_id: 's1' }, { hostW: 400, labelW: 100, axisH: 20, pad: 8 });
  assert.equal(layout.sessionId, 's1');
  assert.ok(Number.isNaN(layout.startMs));
  assert.ok(Number.isNaN(layout.endMs));
  assert.equal(layout.span, 0);
  assert.equal(layout.chartX, 100); // labelW
  assert.equal(layout.chartW, 0);
  assert.equal(layout.contentW, 400); // hostW
  assert.equal(layout.width, 400); // hostW
  assert.equal(layout.height, 28); // axisH + pad
  assert.equal(layout.nowX, null);
  assert.deepEqual(layout.rows, []);
  assert.deepEqual(layout.ticks, []);
});

test('buildTimeline lays out main + two sub-agents on a real time axis', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done' },
    children: [
      { kind: 'subagent', started_at: 200, ended_at: 400, status: 'done' },
      { kind: 'subagent', started_at: 500, status: 'running' },
    ],
  };
  const layout = buildTimeline(session, {
    hostW: 400, labelW: 100, pad: 0, axisH: 20, rowH: 20,
    minBlock: 2, minPxPerMs: 0, tickCount: 2, nowMs: 1000,
  });
  assert.equal(layout.startMs, 0);
  assert.equal(layout.endMs, 1000);
  assert.equal(layout.span, 1000);
  assert.equal(layout.chartX, 100);
  assert.equal(layout.chartW, 300);
  assert.equal(layout.contentW, 400);
  assert.equal(layout.width, 400);
  assert.equal(layout.height, 80);
  assert.equal(layout.nowX, 400);
  assert.equal(layout.rows.length, 3);

  const [main, c0, c1] = layout.rows;
  assert.equal(main.depth, 0);
  assert.equal(main.x, 100);
  assert.equal(main.w, 300);
  assert.equal(main.y, 20);
  assert.equal(main.labelX, 0);
  assert.equal(main.key, 's1#0');

  assert.equal(c0.depth, 1);
  assert.equal(c0.x, 160);
  assert.equal(c0.w, 60);
  assert.equal(c0.y, 40);
  assert.equal(c0.labelX, 12); // pad 0 + 1 * indent(default 12)

  assert.equal(c1.depth, 1);
  assert.equal(c1.x, 250);
  assert.equal(c1.w, 150);
  assert.equal(c1.y, 60);
  assert.equal(c1.running, true);
  assert.equal(c1.labelX, 12);

  assert.deepEqual(layout.ticks, [{ x: 100, t: 0 }, { x: 400, t: 1000 }]);
});

test('buildTimeline widens the chart for a long session (horizontal scroll)', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done' },
    children: [],
  };
  const layout = buildTimeline(session, {
    hostW: 400, labelW: 100, pad: 0, minPxPerMs: 1, nowMs: 1000,
  });
  // chartHostW = 400 - 100 - 0 = 300; ceil(1000 * 1) = 1000 → widen to 1000.
  assert.equal(layout.chartW, 1000);
  assert.equal(layout.contentW, 1100); // chartX(100) + 1000 + pad(0)
  assert.ok(layout.contentW > 400); // wider than hostW → scrolls
});

test('buildTimeline indents sub-agent labels by pad + depth*indent', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done' },
    children: [{ kind: 'subagent', started_at: 200, ended_at: 400, status: 'done' }],
  };
  const layout = buildTimeline(session, { nowMs: 1000, pad: 8, indent: 12 });
  assert.equal(layout.rows[0].labelX, 8); // main: pad
  assert.equal(layout.rows[1].labelX, 20); // sub: pad + indent
});

test('buildTimeline nowX is null when now precedes the window', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 1000, ended_at: 2000, status: 'done' },
    children: [],
  };
  const layout = buildTimeline(session, { nowMs: 0 });
  assert.equal(layout.nowX, null);
});

// ── agentPhaseAt ────────────────────────────────────────────────────
test('agentPhaseAt is pending when start > T', () => {
  const a = { started_at: 500, ended_at: 1000, status: 'done' };
  assert.equal(agentPhaseAt(a, 300), 'pending');
});

test('agentPhaseAt is active when start <= T < end', () => {
  const a = { started_at: 100, ended_at: 1000, status: 'done' };
  assert.equal(agentPhaseAt(a, 500), 'active');
});

test('agentPhaseAt is done when T >= ended_at', () => {
  const a = { started_at: 100, ended_at: 1000, status: 'done' };
  assert.equal(agentPhaseAt(a, 1500), 'done');
});

test('agentPhaseAt: a running agent is active for any T at/after start, incl far-future', () => {
  const a = { started_at: 100, status: 'running' };
  assert.equal(agentPhaseAt(a, 100), 'active');
  assert.equal(agentPhaseAt(a, 9_999_999_999), 'active');
});

test('agentPhaseAt: an unparseable start is pending', () => {
  const a = { started_at: 'nope', ended_at: 1000, status: 'done' };
  assert.equal(agentPhaseAt(a, 5000), 'pending');
});

test('agentPhaseAt: done status with unparseable end is done at T >= start (zero-width)', () => {
  const a = { started_at: 100, ended_at: 'nope', status: 'done' };
  assert.equal(agentPhaseAt(a, 100), 'done');
  assert.equal(agentPhaseAt(a, 500), 'done');
});

test('agentPhaseAt: boundaries are inclusive — T===start is active, T===end is done', () => {
  const a = { started_at: 100, ended_at: 1000, status: 'done' };
  assert.equal(agentPhaseAt(a, 100), 'active'); // T===start
  assert.equal(agentPhaseAt(a, 1000), 'done');  // T===end
});

// ── playheadBounds ──────────────────────────────────────────────────
test('playheadBounds spans min-start..max-effEnd across multiple sessions', () => {
  const graph = {
    sessions: [
      { main: { started_at: 100, ended_at: 400, status: 'done' }, children: [] },
      { main: { started_at: 50, ended_at: 900, status: 'done' }, children: [] },
    ],
  };
  assert.deepEqual(playheadBounds(graph, 0), { startMs: 50, endMs: 900 });
});

test('playheadBounds: a running agent extends endMs to nowMs', () => {
  const graph = {
    sessions: [
      { main: { started_at: 100, ended_at: 300, status: 'done' }, children: [
        { started_at: 200, status: 'running' },
      ] },
    ],
  };
  assert.deepEqual(playheadBounds(graph, 5000), { startMs: 100, endMs: 5000 });
});

test('playheadBounds is null for an empty/agent-less/null graph', () => {
  assert.equal(playheadBounds(null, 0), null);
  assert.equal(playheadBounds({ sessions: [] }, 0), null);
  assert.equal(playheadBounds({ sessions: [{ children: [] }] }, 0), null);
});

// ── playheadStats ───────────────────────────────────────────────────
test('playheadStats counts mixed pending/active/done at a mid T', () => {
  const graph = {
    sessions: [
      { main: { started_at: 0, ended_at: 1000, status: 'done' }, children: [
        { started_at: 200, ended_at: 400, status: 'done' }, // done at T=500
        { started_at: 600, status: 'running' },             // pending at T=500
      ] },
    ],
  };
  // T=500: main active, c0 done, c1 pending.
  assert.deepEqual(playheadStats(graph, 500), { pending: 1, active: 1, done: 1 });
});

test('playheadStats: all-pending before first start, all-done after last end', () => {
  const graph = {
    sessions: [
      { main: { started_at: 100, ended_at: 500, status: 'done' }, children: [
        { started_at: 200, ended_at: 400, status: 'done' },
      ] },
    ],
  };
  assert.deepEqual(playheadStats(graph, 0), { pending: 2, active: 0, done: 0 });
  assert.deepEqual(playheadStats(graph, 9999), { pending: 0, active: 0, done: 2 });
});

test('playheadStats is zeros for a null/empty graph', () => {
  assert.deepEqual(playheadStats(null, 100), { pending: 0, active: 0, done: 0 });
  assert.deepEqual(playheadStats({ sessions: [] }, 100), { pending: 0, active: 0, done: 0 });
});

// ── sessionStats ────────────────────────────────────────────────────
test('sessionStats is all zeros for a null/empty session', () => {
  const zero = { durationMs: 0, turnCount: 0, toolCount: 0, errorCount: 0, agentCount: 0, tokenCount: 0, cost_usd: 0 };
  assert.deepEqual(sessionStats(null, 1000), zero);
  assert.deepEqual(sessionStats({}, 1000), zero);
});

// A finished session with main + one sub-agent. Top-level error_count/cost_usd
// are set to the hand walk so the test can assert sessionStats prefers them AND
// that they agree with a manual count (the contract the prompt calls for).
const STATS_SESSION = {
  session_id: 'stats-1',
  started_at: '2026-05-01T12:00:00Z',
  ended_at: '2026-05-01T12:01:00Z', // 60s
  error_count: 1,
  cost_usd: 0.47,
  main: {
    kind: 'main', started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:40Z',
    status: 'done',
    // total = 100+50+10+5+200 = 365
    tokens: { InputTokens: 100, OutputTokens: 50, CacheCreate5mTokens: 10, CacheCreate1hTokens: 5, CacheReadTokens: 200 },
    steps: [
      { timestamp: '2026-05-01T12:00:10Z', cost_usd: 0.10, tools: [
        { name: 'Bash', status: 'ok' },
      ] },
      { timestamp: '2026-05-01T12:00:40Z', cost_usd: 0.32, tools: [] },
    ],
  },
  children: [
    { kind: 'subagent', started_at: '2026-05-01T12:00:15Z', ended_at: '2026-05-01T12:00:35Z',
      status: 'done',
      // total = 20+10+100 = 130
      tokens: { InputTokens: 20, OutputTokens: 10, CacheReadTokens: 100 },
      steps: [
        { timestamp: '2026-05-01T12:00:15Z', cost_usd: 0.05, tools: [
          { name: 'Read', status: 'ok' },
          { name: 'Grep', status: 'error' },
        ] },
      ] },
  ],
};

test('sessionStats rolls up a finished session with a sub-agent', () => {
  const s = sessionStats(STATS_SESSION, 9_999_999);
  assert.equal(s.durationMs, 60000); // 12:01:00 − 12:00:00
  assert.equal(s.agentCount, 2);     // main + 1 child
  assert.equal(s.turnCount, 3);      // 2 main steps + 1 child step
  assert.equal(s.toolCount, 3);      // 1 + 0 + 2
  assert.equal(s.errorCount, 1);     // top-level error_count
  assert.equal(s.cost_usd, 0.47);    // top-level cost_usd
  // …and the preferred top-level numbers equal a hand walk of the underlying data.
  const handErrors = flattenSession(STATS_SESSION)
    .flatMap(a => a.steps).flatMap(st => st.tools).filter(t => t.status === 'error').length;
  assert.equal(s.errorCount, handErrors);
  const handCost = flattenSession(STATS_SESSION)
    .flatMap(a => a.steps).reduce((sum, st) => sum + st.cost_usd, 0);
  assert.ok(Math.abs(s.cost_usd - handCost) < 1e-9);
});

test('sessionStats sums total tokens (input+output+cache) across all agents', () => {
  // main 365 + child 130 = 495.
  assert.equal(sessionStats(STATS_SESSION, 9_999_999).tokenCount, 495);
});

test('sessionStats falls back to counting tool errors when error_count is absent', () => {
  const { error_count, ...noErrCount } = STATS_SESSION;
  assert.equal(sessionStats(noErrCount, 9_999_999).errorCount, 1); // the one Grep status:'error'
});

test('sessionStats falls back to summing step cost when cost_usd is absent', () => {
  const { cost_usd, ...noCost } = STATS_SESSION;
  assert.ok(Math.abs(sessionStats(noCost, 9_999_999).cost_usd - 0.47) < 1e-9); // 0.10+0.32+0.05
});

test('sessionStats measures a running session (no ended_at) up to nowMs', () => {
  const running = {
    session_id: 'run-1',
    started_at: '2026-05-01T12:00:00Z', // ended_at omitted
    main: { kind: 'main', started_at: '2026-05-01T12:00:00Z', status: 'running', steps: [] },
    children: [],
  };
  const nowMs = Date.parse('2026-05-01T12:00:30Z');
  assert.equal(sessionStats(running, nowMs).durationMs, 30000);
});

// ── timelineAtTime ──────────────────────────────────────────────────
const playheadSession = {
  session_id: 's1',
  main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done' },
  children: [
    { kind: 'subagent', started_at: 200, ended_at: 400, status: 'done' },
    { kind: 'subagent', started_at: 500, status: 'running' },
  ],
};
const playheadOpts = {
  hostW: 400, labelW: 100, pad: 0, axisH: 20, rowH: 20,
  minBlock: 2, minPxPerMs: 0, tickCount: 2, nowMs: 1000,
};

test('timelineAtTime: T=300 anchor — playheadX, axis, and per-row phase/w/running/x', () => {
  const t = timelineAtTime(playheadSession, 300, playheadOpts);
  assert.equal(t.chartX, 100);
  assert.equal(t.contentW, 400);
  assert.equal(t.playheadX, 190); // 100 + 300*300/1000
  assert.equal(t.atMs, 300);
  // rows[0] main: active (running up to playhead), bar 0..300 → w 90
  assert.equal(t.rows[0].phase, 'active');
  assert.equal(t.rows[0].w, 90);
  assert.equal(t.rows[0].running, true);
  assert.equal(t.rows[0].x, 100);
  // rows[1] c0 200..400, T<400 → active, bar 200..300 → w 30
  assert.equal(t.rows[1].phase, 'active');
  assert.equal(t.rows[1].w, 30);
  assert.equal(t.rows[1].running, true);
  assert.equal(t.rows[1].x, 160);
  // rows[2] c1 start 500 > 300 → pending
  assert.equal(t.rows[2].phase, 'pending');
  assert.equal(t.rows[2].w, 0);
  assert.equal(t.rows[2].running, false);
});

test('timelineAtTime: T=450 anchor — c0 done full bar, c1 pending, playheadX', () => {
  const t = timelineAtTime(playheadSession, 450, playheadOpts);
  // rows[1] c0 200..400, end 400 <= 450 → done, full bar 200..400 → w 60
  assert.equal(t.rows[1].phase, 'done');
  assert.equal(t.rows[1].w, 60);
  assert.equal(t.rows[1].running, false);
  // rows[2] c1 start 500 > 450 → pending
  assert.equal(t.rows[2].phase, 'pending');
  assert.equal(t.rows[2].w, 0);
  assert.equal(t.playheadX, 235); // 100 + 300*450/1000
});

test('timelineAtTime: playheadX is null when T < startMs (session not begun)', () => {
  const t = timelineAtTime(playheadSession, -50, playheadOpts);
  assert.equal(t.playheadX, null);
  // all rows pending before the session starts
  assert.equal(t.rows[0].phase, 'pending');
  assert.equal(t.rows[0].w, 0);
});

test('timelineAtTime: an empty session yields rows:[] and playheadX null', () => {
  const t = timelineAtTime({ session_id: 'e', main: null, children: [] }, 500, playheadOpts);
  assert.deepEqual(t.rows, []);
  assert.equal(t.playheadX, null);
  assert.equal(t.atMs, 500);
});

test('timelineAtTime: axis (chartX/contentW/ticks) is identical to buildTimeline regardless of T', () => {
  const base = buildTimeline(playheadSession, playheadOpts);
  for (const T of [-100, 0, 300, 700, 5000]) {
    const t = timelineAtTime(playheadSession, T, playheadOpts);
    assert.equal(t.chartX, base.chartX);
    assert.equal(t.contentW, base.contentW);
    assert.equal(t.chartW, base.chartW);
    assert.equal(t.startMs, base.startMs);
    assert.equal(t.endMs, base.endMs);
    assert.deepEqual(t.ticks, base.ticks);
  }
});

// ── stepSegments (per-turn timeline segments) ───────────────────────
// scale maps [0,1000] → [0,300]px, so x(ts) = ts*0.3; chartX 100.
const segScale = makeTimeScale({ startMs: 0, endMs: 1000, width: 300, minBlock: 2 });

test('stepSegments lays out each timed step as a segment on the scale', () => {
  const agent = {
    kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
    steps: [
      { timestamp: 0, duration_ms: 400, cost_usd: 0.01, tools: [] },
      { timestamp: 400, duration_ms: 0, cost_usd: 0.02, tools: [{ status: 'ok' }] },
    ],
  };
  const segs = stepSegments(agent, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000,
  });
  assert.equal(segs.length, 2);
  // step0: 0..400 → x 100, w 120, span 400ms
  assert.deepEqual(segs[0], {
    x: 100, w: 120, stepIndex: 0, refKey: 's1#0.0', status: '', cost_usd: 0.01, durationMs: 400, kind: 'step',
  });
  // step1 (duration 0) extends to effEnd 1000: 400..1000 → x 220, w 180, span 600ms
  assert.deepEqual(segs[1], {
    x: 220, w: 180, stepIndex: 1, refKey: 's1#0.1', status: '', cost_usd: 0.02, durationMs: 600, kind: 'step',
  });
});

test('stepSegments marks a segment error when any tool in its step failed', () => {
  const agent = {
    kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
    steps: [
      { timestamp: 0, duration_ms: 500, tools: [{ status: 'ok' }] },
      { timestamp: 500, duration_ms: 0, tools: [{ status: 'ok' }, { status: 'error' }] },
    ],
  };
  const segs = stepSegments(agent, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000,
  });
  assert.equal(segs[0].status, '');
  assert.equal(segs[1].status, 'error');
});

test('stepSegments skips a step with an unparseable timestamp, keeping true stepIndex', () => {
  const agent = {
    kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
    steps: [
      { timestamp: 'not-a-time', duration_ms: 100, tools: [] },
      { timestamp: 500, duration_ms: 0, tools: [] },
    ],
  };
  const segs = stepSegments(agent, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000,
  });
  assert.equal(segs.length, 1);
  assert.equal(segs[0].stepIndex, 1);
  assert.equal(segs[0].refKey, 's1#0.1');
});

test('stepSegments returns [] for an agent with no steps', () => {
  assert.deepEqual(stepSegments({ kind: 'main' }, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000,
  }), []);
});

test('stepSegments carries each segment\'s visual span (end-start) as durationMs', () => {
  const agent = {
    kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
    steps: [
      { timestamp: 0, duration_ms: 400, cost_usd: 0.01, tools: [] },
      { timestamp: 400, duration_ms: 0, cost_usd: 0.02, tools: [] },
    ],
  };
  const segs = stepSegments(agent, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000,
  });
  // step0 spans 0..400; step1 (duration 0) stretches to effEnd 1000 → 400..1000.
  assert.equal(segs[0].durationMs, 400);
  assert.equal(segs[1].durationMs, 600);
});

// ── toolSegments (per-tool sub-spans, falling back to turn segments) ─
test('toolSegments falls back to one turn segment per step when no tool is timed', () => {
  // Tools carry status but no ended_at (older data) → identical to stepSegments,
  // and crucially WITHOUT a toolIndex key so the geometry stays byte-compatible.
  const agent = {
    kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
    steps: [
      { timestamp: 0, duration_ms: 400, cost_usd: 0.01, tools: [{ status: 'ok' }] },
      { timestamp: 400, duration_ms: 0, cost_usd: 0.02, tools: [] },
    ],
  };
  const segs = toolSegments(agent, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000,
  });
  assert.deepEqual(segs, [
    { x: 100, w: 120, stepIndex: 0, refKey: 's1#0.0', status: '', cost_usd: 0.01, durationMs: 400, kind: 'step' },
    { x: 220, w: 180, stepIndex: 1, refKey: 's1#0.1', status: '', cost_usd: 0.02, durationMs: 600, kind: 'step' },
  ]);
});

test('toolSegments tiles a step\'s timed tools as sub-spans by ended_at', () => {
  // Step spans 0..1000. Bash ends at 600 (slow), Read ends at 700 (quick after
  // Bash). Tools tile contiguously from the step start: Bash [0,600], Read
  // [600,700] — so an 8s-equivalent Bash is wide and the trailing Read a sliver.
  const agent = {
    kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
    steps: [
      {
        timestamp: 0, duration_ms: 1000, cost_usd: 0.05,
        tools: [
          { name: 'Bash', status: 'ok', ended_at: 600 },
          { name: 'Read', status: 'error', ended_at: 700 },
        ],
      },
    ],
  };
  const segs = toolSegments(agent, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000,
  });
  assert.deepEqual(segs, [
    { x: 100, w: 180, stepIndex: 0, toolIndex: 0, refKey: 's1#0.0:0', status: '', cost_usd: 0.05, durationMs: 600, kind: 'other' },
    { x: 280, w: 30, stepIndex: 0, toolIndex: 1, refKey: 's1#0.0:1', status: 'error', cost_usd: 0.05, durationMs: 100, kind: 'other' },
  ]);
});

test('toolSegments clamps tool sub-spans to the playhead cap', () => {
  // Scrub cap at 700: Bash [0,600] is fully before it; Read straddles (ends
  // 1200) so it truncates to [600,700]; the third tool (starts at the cap) has
  // not begun at T and is dropped — same drop/truncate rules as turn segments.
  const agent = {
    kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
    steps: [
      {
        timestamp: 0, duration_ms: 1000, cost_usd: 0.05,
        tools: [
          { name: 'Bash', status: 'ok', ended_at: 600 },
          { name: 'Read', status: 'ok', ended_at: 1200 },
          { name: 'Edit', status: 'ok', ended_at: 1500 },
        ],
      },
    ],
  };
  const segs = toolSegments(agent, {
    scale: segScale, chartX: 100, sessionId: 's1', agentIndex: 0, effEnd: 1000, until: 700,
  });
  assert.equal(segs.length, 2);
  assert.deepEqual(segs[0], {
    x: 100, w: 180, stepIndex: 0, toolIndex: 0, refKey: 's1#0.0:0', status: '', cost_usd: 0.05, durationMs: 600, kind: 'other',
  });
  // Read truncated at the cap: 600..700 → x 280, w 30, span 100ms.
  assert.deepEqual(segs[1], {
    x: 280, w: 30, stepIndex: 0, toolIndex: 1, refKey: 's1#0.0:1', status: '', cost_usd: 0.05, durationMs: 100, kind: 'other',
  });
});

// ADAPTED (collapse-by-default, Phase-3 item 14): segments were unconditional;
// now they are behind row expansion, so this test opts the row in via
// opts.expanded. The expected segment values are unchanged.
test('buildTimeline attaches per-turn segments to each expanded row', () => {
  const session = {
    session_id: 's1',
    main: {
      kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
      steps: [
        { timestamp: 0, duration_ms: 400, cost_usd: 0.01, tools: [] },
        { timestamp: 400, duration_ms: 0, cost_usd: 0.02, tools: [] },
      ],
    },
    children: [],
  };
  const layout = buildTimeline(session, {
    hostW: 400, labelW: 100, pad: 0, axisH: 20, rowH: 20,
    minBlock: 2, minPxPerMs: 0, tickCount: 2, nowMs: 1000,
    expanded: { rows: new Set(['s1#0']) },
  });
  assert.deepEqual(layout.rows[0].segments, [
    { x: 100, w: 120, stepIndex: 0, refKey: 's1#0.0', status: '', cost_usd: 0.01, durationMs: 400, kind: 'step' },
    { x: 220, w: 180, stepIndex: 1, refKey: 's1#0.1', status: '', cost_usd: 0.02, durationMs: 600, kind: 'step' },
  ]);
});

// ADAPTED (two-level disclosure, Phase-3 item 14): tool sub-spans were emitted
// whenever tools were timed; now they require BOTH the row and the turn to be
// expanded, and the turn span stays (leading) as the tools' context/collapse
// target. The two tool sub-span values are unchanged.
test('buildTimeline rows expose per-tool sub-spans when their turn is expanded', () => {
  const session = {
    session_id: 's1',
    main: {
      kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
      steps: [
        {
          timestamp: 0, duration_ms: 1000, cost_usd: 0.03,
          tools: [
            { name: 'Bash', status: 'ok', ended_at: 600 },
            { name: 'Read', status: 'ok', ended_at: 900 },
          ],
        },
      ],
    },
    children: [],
  };
  const layout = buildTimeline(session, {
    hostW: 400, labelW: 100, pad: 0, axisH: 20, rowH: 20,
    minBlock: 2, minPxPerMs: 0, tickCount: 2, nowMs: 1000,
    expanded: { rows: new Set(['s1#0']), turns: new Set(['s1#0.0']) },
  });
  assert.deepEqual(layout.rows[0].segments, [
    { x: 100, w: 300, stepIndex: 0, refKey: 's1#0.0', status: '', cost_usd: 0.03, durationMs: 1000, kind: 'step' },
    { x: 100, w: 180, stepIndex: 0, toolIndex: 0, refKey: 's1#0.0:0', status: '', cost_usd: 0.03, durationMs: 600, kind: 'other' },
    { x: 280, w: 90, stepIndex: 0, toolIndex: 1, refKey: 's1#0.0:1', status: '', cost_usd: 0.03, durationMs: 300, kind: 'other' },
  ]);
});

test('timelineAtTime clamps segments to the playhead (after T dropped, straddling truncated, pending agent empty)', () => {
  const session = {
    session_id: 's1',
    main: {
      kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
      steps: [
        { timestamp: 0, duration_ms: 400, tools: [] },
        { timestamp: 400, duration_ms: 0, tools: [] },
      ],
    },
    children: [
      { kind: 'subagent', started_at: 600, status: 'running', steps: [{ timestamp: 600, duration_ms: 0, tools: [] }] },
    ],
  };
  // ADAPTED (collapse-by-default, Phase-3 item 14): segments require the rows
  // to be expanded now; expansion added, clamping assertions unchanged.
  const t = timelineAtTime(session, 300, {
    hostW: 400, labelW: 100, pad: 0, axisH: 20, rowH: 20,
    minBlock: 2, minPxPerMs: 0, tickCount: 2, nowMs: 1000,
    expanded: { rows: new Set(['s1#0', 's1#1']) },
  });
  // main active at T=300: step0 (0..400) truncated to 0..300 (w 90); step1 (start 400) dropped.
  assert.equal(t.rows[0].segments.length, 1);
  assert.equal(t.rows[0].segments[0].x, 100);
  assert.equal(t.rows[0].segments[0].w, 90);
  assert.equal(t.rows[0].segments[0].stepIndex, 0);
  // durationMs follows the clamp: step0's 0..400 span truncated to 0..300 → 300ms.
  assert.equal(t.rows[0].segments[0].durationMs, 300);
  // child pending at T=300 (starts 600): no segments.
  assert.deepEqual(t.rows[1].segments, []);
});

// ── fitSegmentLabel (inline segment labels) ─────────────────────────
// charW 6, padX 4 → a W-wide segment fits floor((W - 8) / 6) glyphs.

test('fitSegmentLabel returns "dur · cost" when both fit the width', () => {
  // "5s · $0.20" is 10 chars → needs 8 + 60 = 68px; give it 80.
  assert.equal(fitSegmentLabel(80, '5s', '$0.20'), '5s · $0.20');
});

test('fitSegmentLabel falls back to the duration when only it fits', () => {
  // 30px: avail 22 fits "5s" (12px) but not the 10-char combined label (60px).
  assert.equal(fitSegmentLabel(30, '5s', '$0.20'), '5s');
});

test('fitSegmentLabel returns "" when even the duration does not fit', () => {
  // 10px: avail 2 fits nothing → stay tooltip-only.
  assert.equal(fitSegmentLabel(10, '5s', '$0.20'), '');
});

test('fitSegmentLabel ignores an empty cost, showing the duration alone', () => {
  // A free turn (costText '') on a wide segment shows just the duration.
  assert.equal(fitSegmentLabel(80, '5s', ''), '5s');
});

// ── costHeat (segment cost ramp) ─────────────────────────────────────

test('costHeat is 0 when there is no cost or no max', () => {
  assert.equal(costHeat(0, 5), 0);     // a free turn is coolest
  assert.equal(costHeat(2, 0), 0);     // nothing has cost → no ramp
  assert.equal(costHeat(-1, 5), 0);    // negative cost guarded
});

test('costHeat is 1 for the most expensive turn (at the max)', () => {
  assert.equal(costHeat(5, 5), 1);
  // costs above the max (shouldn\'t happen) clamp to 1, not beyond.
  assert.equal(costHeat(8, 5), 1);
});

test('costHeat gamma keeps a mid-cost turn cool (ratio 0.5, gamma 2 → 0.25)', () => {
  assert.equal(costHeat(5, 10), 0.25);
  assert.equal(costHeat(5, 10, { gamma: 1 }), 0.5); // linear ramp when gamma 1
});

// ── timelineSessionList ─────────────────────────────────────────────
test('timelineSessionList returns [] for empty, null, or undefined input', () => {
  assert.deepEqual(timelineSessionList([]), []);
  assert.deepEqual(timelineSessionList(null), []);
  assert.deepEqual(timelineSessionList(undefined), []);
});

test('timelineSessionList maps sessionId/cwd/index plus the sessionStats rollups', () => {
  // Reuse the hand-walked STATS_SESSION (durationMs 60000, agentCount 2,
  // turnCount 3, toolCount 3, errorCount 1, tokenCount 495, cost_usd 0.47),
  // adding a cwd so the picker has a project label.
  const session = { ...STATS_SESSION, cwd: '/home/me/proj' };
  const out = timelineSessionList([session], 9_999_999);
  assert.equal(out.length, 1);
  assert.deepEqual(out[0], {
    sessionId: 'stats-1',
    cwd: '/home/me/proj',
    entrypoint: '',
    index: 0,
    durationMs: 60000,
    turnCount: 3,
    toolCount: 3,
    errorCount: 1,
    agentCount: 2,
    tokenCount: 495,
    cost_usd: 0.47,
  });
});

test('timelineSessionList carries the session entrypoint for the SDK badge', () => {
  const sdk = { ...STATS_SESSION, session_id: 'sid-sdk', entrypoint: 'sdk-cli' };
  const out = timelineSessionList([sdk], 9_999_999);
  assert.equal(out.length, 1);
  assert.equal(out[0].entrypoint, 'sdk-cli');
});

test('timelineSessionList excludes null and main-less sessions', () => {
  const withMain = { ...STATS_SESSION, session_id: 'sid-ok' };
  const mainless = { session_id: 'sid-no', cwd: '/y', main: null };
  const out = timelineSessionList([null, mainless, withMain, undefined], 9_999_999);
  assert.equal(out.length, 1);
  assert.equal(out[0].sessionId, 'sid-ok');
});

test('timelineSessionList index reflects the ORIGINAL input position', () => {
  const second = { ...STATS_SESSION, session_id: 'sid-2' };
  // input[0] is null and filtered out, but the surviving session keeps index 1.
  const out = timelineSessionList([null, second], 9_999_999);
  assert.equal(out.length, 1);
  assert.equal(out[0].index, 1);
});

// ── pickTimelineSid ─────────────────────────────────────────────────
// Two plottable sessions (each with a main), used across the resolver cases.
const PICK_A = { ...STATS_SESSION, session_id: 'sid-a' };
const PICK_B = { ...STATS_SESSION, session_id: 'sid-b' };

test('pickTimelineSid defaults to the first list entry when timelineSid and selectedRef are null', () => {
  assert.equal(pickTimelineSid([PICK_A, PICK_B], null, null), 'sid-a');
});

test('pickTimelineSid honors an explicit timelineSid when that session is still present', () => {
  assert.equal(pickTimelineSid([PICK_A, PICK_B], 'sid-b', null), 'sid-b');
});

test('pickTimelineSid falls back to the first entry when timelineSid names an absent session', () => {
  // The chosen session aged out of the window → drop back to the first.
  assert.equal(pickTimelineSid([PICK_A, PICK_B], 'sid-gone', null), 'sid-a');
});

test('pickTimelineSid falls back to selectedRefs session when timelineSid is null', () => {
  // A turn selected in another lens (refKey for sid-b's main) scopes the Gantt.
  const ref = refKey({ sessionId: 'sid-b', agentIndex: 0 });
  assert.equal(pickTimelineSid([PICK_A, PICK_B], null, ref), 'sid-b');
});

test('pickTimelineSid returns null when there are no plottable sessions', () => {
  const ref = refKey({ sessionId: 'sid-x', agentIndex: 0 });
  assert.equal(pickTimelineSid([], 'sid-x', ref), null);
  assert.equal(pickTimelineSid(null, null, null), null);
  // A main-less session is not plottable either.
  assert.equal(pickTimelineSid([{ session_id: 'sid-x', main: null }], 'sid-x', null), null);
});

// ── zoomClampPxPerMs (timeline scroll-wheel zoom) ───────────────────
// The Timeline Gantt zooms by raising minPxPerMs (px per ms of the time axis).
// zoomClampPxPerMs bounds a requested density to a usable range: the floor is
// "fit to width" (chartHostW / span — below it the chart would under-fill the
// host and scroll pointlessly), the ceiling a fixed legibility cap.
test('zoomClampPxPerMs leaves an in-range density untouched', () => {
  // span 1e6 ms, chartHostW 300 → floor 0.0003; 0.01 sits between floor and cap.
  assert.equal(zoomClampPxPerMs(0.01, { span: 1_000_000, chartHostW: 300 }), 0.01);
});

test('zoomClampPxPerMs floors a density below fit-to-width', () => {
  // floor = 300 / 1e6 = 0.0003; a smaller request snaps up to it.
  assert.equal(zoomClampPxPerMs(0.0001, { span: 1_000_000, chartHostW: 300 }), 0.0003);
});

test('zoomClampPxPerMs caps a density above the ceiling', () => {
  assert.equal(zoomClampPxPerMs(5, { span: 1_000_000, chartHostW: 300 }), 0.12);
});

test('zoomClampPxPerMs honours a custom ceiling', () => {
  assert.equal(zoomClampPxPerMs(5, { span: 1_000_000, chartHostW: 300, maxPxPerMs: 0.2 }), 0.2);
});

test('zoomClampPxPerMs pins a short session at fit (floor above the ceiling)', () => {
  // span 1000, chartHostW 300 → floor 0.3 > cap 0.12; the session already fits,
  // so every request collapses to the floor — no zoom in either direction.
  assert.equal(zoomClampPxPerMs(0.5, { span: 1000, chartHostW: 300 }), 0.3);
  assert.equal(zoomClampPxPerMs(0.05, { span: 1000, chartHostW: 300 }), 0.3);
});

test('zoomClampPxPerMs returns the ceiling for a zero/negative span', () => {
  assert.equal(zoomClampPxPerMs(0.01, { span: 0, chartHostW: 300 }), 0.12);
});

test('zoomClampPxPerMs returns the floor for a non-positive request', () => {
  assert.equal(zoomClampPxPerMs(0, { span: 1_000_000, chartHostW: 300 }), 0.0003);
  assert.equal(zoomClampPxPerMs(NaN, { span: 1_000_000, chartHostW: 300 }), 0.0003);
});

// ── zoomAnchorScrollLeft (cursor-anchored zoom) ─────────────────────
// As the chart pixel width changes under a zoom, keep the timestamp under the
// pointer fixed: returns the new scrollLeft that puts the same fractional
// content position back under the cursor, clamped to [0, maxScroll].
test('zoomAnchorScrollLeft keeps the mid-cursor timestamp fixed when zooming in', () => {
  // cursor at content px 250 of 1000 (frac .25); doubling to 2000 → that point
  // is now px 500, minus the 250 cursor offset = scrollLeft 250.
  assert.equal(zoomAnchorScrollLeft({
    cursorX: 250, scrollLeft: 0, viewportW: 500, oldChartW: 1000, newChartW: 2000,
  }), 250);
});

test('zoomAnchorScrollLeft holds the left edge at zero', () => {
  assert.equal(zoomAnchorScrollLeft({
    cursorX: 0, scrollLeft: 0, viewportW: 500, oldChartW: 1000, newChartW: 2000,
  }), 0);
});

test('zoomAnchorScrollLeft clamps to maxScroll near the right edge', () => {
  // frac .95 of a 2000-wide chart wants 1900 - 0, but maxScroll is 2000-500=1500.
  assert.equal(zoomAnchorScrollLeft({
    cursorX: 0, scrollLeft: 1900, viewportW: 500, oldChartW: 2000, newChartW: 2000,
  }), 1500);
});

test('zoomAnchorScrollLeft clamps a negative result to zero (zoom out near start)', () => {
  assert.equal(zoomAnchorScrollLeft({
    cursorX: 100, scrollLeft: 0, viewportW: 500, oldChartW: 2000, newChartW: 1000,
  }), 0);
});

test('zoomAnchorScrollLeft returns 0 when the old width is degenerate', () => {
  assert.equal(zoomAnchorScrollLeft({
    cursorX: 100, scrollLeft: 0, viewportW: 500, oldChartW: 0, newChartW: 1000,
  }), 0);
});

// ── segKindColor (kind → palette family for the Timeline) ───────────
test('segKindColor passes a known tool kind through unchanged', () => {
  for (const k of ['read', 'web', 'exec', 'edit', 'skill', 'mcp', 'command', 'todo', 'other']) {
    assert.equal(segKindColor(k), k);
  }
});

test('segKindColor passes the agent/step pseudo-kinds through', () => {
  assert.equal(segKindColor('agent'), 'agent');
  assert.equal(segKindColor('step'), 'step');
});

test('segKindColor falls back to other for an unknown/missing kind', () => {
  assert.equal(segKindColor('bogus'), 'other');
  assert.equal(segKindColor(''), 'other');
  assert.equal(segKindColor(undefined), 'other');
  assert.equal(segKindColor(null), 'other');
});

// ── pctOfAgent (a segment's share of its agent's runtime) ───────────
test('pctOfAgent returns the rounded integer percent of the whole', () => {
  assert.equal(pctOfAgent(250, 1000), 25);
  assert.equal(pctOfAgent(1, 3), 33);   // 33.33 → 33
  assert.equal(pctOfAgent(2, 3), 67);   // 66.66 → 67
});

test('pctOfAgent clamps a part larger than the whole to 100', () => {
  assert.equal(pctOfAgent(1500, 1000), 100);
});

test('pctOfAgent is 0 for a zero/negative part', () => {
  assert.equal(pctOfAgent(0, 1000), 0);
  assert.equal(pctOfAgent(-5, 1000), 0);
});

test('pctOfAgent returns null when the whole is zero/invalid', () => {
  assert.equal(pctOfAgent(100, 0), null);
  assert.equal(pctOfAgent(100, -1), null);
  assert.equal(pctOfAgent(100, NaN), null);
});

// ── segTooltip (richer hover string, not richer in-bar labels) ──────
test('segTooltip joins head, dur(% of agent), cost and tokens with dots', () => {
  assert.equal(
    segTooltip({ head: 'Bash · build', durText: '1m 5s', pct: 24, costText: '$0.0021', tokensText: '12.3k tok' }),
    'Bash · build · 1m 5s (24% of agent) · $0.0021 · 12.3k tok',
  );
});

test('segTooltip omits the percent when pct is null', () => {
  assert.equal(
    segTooltip({ head: 'Turn 3', durText: '5s', pct: null, costText: '$0.01', tokensText: '900 tok' }),
    'Turn 3 · 5s · $0.01 · 900 tok',
  );
});

test('segTooltip skips empty cost/tokens pieces', () => {
  assert.equal(
    segTooltip({ head: 'Read · x.go', durText: '120ms', pct: 3, costText: '', tokensText: '' }),
    'Read · x.go · 120ms (3% of agent)',
  );
});

test('segTooltip appends an error marker last', () => {
  assert.equal(
    segTooltip({ head: 'Read · x.go', durText: '120ms', pct: 3, costText: '', tokensText: '', isError: true }),
    'Read · x.go · 120ms (3% of agent) · error',
  );
});

// ── timelineKinds (distinct segment kinds, for the scrubber legend) ─
test('timelineKinds collects a session\'s tool kinds in canonical order', () => {
  const session = {
    session_id: 's1',
    main: {
      kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
      steps: [
        { timestamp: 0, duration_ms: 500, tools: [
          { kind: 'exec', ended_at: 200 },
          { kind: 'read', ended_at: 400 },
        ] },
        { timestamp: 500, duration_ms: 0, tools: [{ kind: 'edit', ended_at: 700 }] },
      ],
    },
    children: [],
  };
  // canonical order is read, edit, exec, … so the legend never reshuffles.
  assert.deepEqual(timelineKinds(session), ['read', 'edit', 'exec']);
});

test('timelineKinds reports step for a turn with no timed tools', () => {
  const session = {
    session_id: 's1',
    main: {
      kind: 'main', started_at: 0, ended_at: 1000, status: 'done',
      steps: [
        { timestamp: 0, duration_ms: 500, tools: [] },              // pure-think turn
        { timestamp: 500, duration_ms: 0, tools: [{ kind: 'read' }] }, // untimed tool
      ],
    },
    children: [],
  };
  // no ended_at anywhere → both turns fall back to a 'step' segment.
  assert.deepEqual(timelineKinds(session), ['step']);
});

test('timelineKinds is empty for a null/agent-less session', () => {
  assert.deepEqual(timelineKinds(null), []);
  assert.deepEqual(timelineKinds({ session_id: 's1' }), []);
});

// ── turnTimeBuckets (per-turn think/generate · tool-exec · idle split) ─
test('turnTimeBuckets splits a turn into gen, serial tool exec, and idle', () => {
  // gen 200; tools tile from stepStart: [0..300]=300, [300..500]=200 → tool 500;
  // idle = 1000 − 200 − 500 = 300.
  const step = { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [
    { ended_at: 300 }, { ended_at: 500 },
  ] };
  assert.deepEqual(turnTimeBuckets(step), { genMs: 200, toolMs: 500, idleMs: 300 });
});

test('turnTimeBuckets sorts tools by ended_at before chaining, like toolSegments', () => {
  const step = { timestamp: 0, duration_ms: 1000, gen_ms: 0, tools: [
    { ended_at: 600 }, { ended_at: 300 },
  ] };
  // sorted: [0..300]=300, [300..600]=300 → tool 600.
  assert.equal(turnTimeBuckets(step).toolMs, 600);
});

test('turnTimeBuckets attributes NO idle when gen_ms is missing (pre-0a fallback)', () => {
  // No gen_ms → we can't honestly separate generate from wait, so idle stays 0
  // rather than mislabelling generation time as idle.
  const step = { timestamp: 0, duration_ms: 1000, tools: [{ ended_at: 400 }] };
  assert.deepEqual(turnTimeBuckets(step), { genMs: 0, toolMs: 400, idleMs: 0 });
});

test('turnTimeBuckets floors idle at 0 when gen + tool exceed the turn', () => {
  const step = { timestamp: 0, duration_ms: 500, gen_ms: 400, tools: [{ ended_at: 300 }] };
  assert.deepEqual(turnTimeBuckets(step), { genMs: 400, toolMs: 300, idleMs: 0 });
});

test('turnTimeBuckets clamps a tool that outlasts its turn to the turn end', () => {
  const step = { timestamp: 0, duration_ms: 400, gen_ms: 0, tools: [{ ended_at: 900 }] };
  assert.equal(turnTimeBuckets(step).toolMs, 400);
});

test('turnTimeBuckets puts all leftover into idle for a tool-less turn with gen', () => {
  const step = { timestamp: 0, duration_ms: 1000, gen_ms: 250, tools: [] };
  assert.deepEqual(turnTimeBuckets(step), { genMs: 250, toolMs: 0, idleMs: 750 });
});

test('turnTimeBuckets is all-zero for a zero-duration (last) step and tolerates null', () => {
  assert.deepEqual(turnTimeBuckets({ timestamp: 0, duration_ms: 0, gen_ms: 0, tools: [] }), { genMs: 0, toolMs: 0, idleMs: 0 });
  assert.deepEqual(turnTimeBuckets(null), { genMs: 0, toolMs: 0, idleMs: 0 });
});

// ── idleSegments (the wait/idle gap drawn flush before the next turn) ──
test('idleSegments draws a trailing span of width idleMs ending at the turn end', () => {
  const agent = { steps: [
    { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [{ ended_at: 300, kind: 'exec' }] },
  ] };
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 1000, minBlock: 0 });
  const segs = idleSegments(agent, { scale, chartX: 0, sessionId: 's', agentIndex: 0, effEnd: 1000 });
  assert.equal(segs.length, 1);
  assert.equal(segs[0].kind, 'idle');
  assert.equal(segs[0].stepIndex, 0);
  // idle = 1000 − 200 − 300 = 500 → span [500, 1000].
  assert.equal(Math.round(segs[0].x), 500);
  assert.equal(Math.round(segs[0].w), 500);
  assert.equal(segs[0].refKey, refKey({ sessionId: 's', agentIndex: 0, stepIndex: 0 }));
});

test('idleSegments draws nothing when gen_ms is missing (no idle attribution)', () => {
  const agent = { steps: [
    { timestamp: 0, duration_ms: 1000, tools: [{ ended_at: 300, kind: 'exec' }] },
  ] };
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 1000, minBlock: 0 });
  assert.deepEqual(idleSegments(agent, { scale, chartX: 0, effEnd: 1000 }), []);
});

test('idleSegments draws nothing for an untimed turn (no tool-end to anchor the gap)', () => {
  // gen 200, dur 1000, but no tool carries ended_at → toolSegments draws one
  // full-width turn segment that would occlude any gap, so emit none.
  const agent = { steps: [
    { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [{ kind: 'read' }] },
  ] };
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 1000, minBlock: 0 });
  assert.deepEqual(idleSegments(agent, { scale, chartX: 0, effEnd: 1000 }), []);
});

test('idleSegments truncates an idle span straddling the playhead cap', () => {
  const agent = { steps: [
    { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [{ ended_at: 300, kind: 'exec' }] },
  ] };
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 1000, minBlock: 0 });
  const segs = idleSegments(agent, { scale, chartX: 0, effEnd: 1000, until: 700 });
  // idle [500, 1000] capped at 700 → [500, 700].
  assert.equal(segs.length, 1);
  assert.equal(Math.round(segs[0].x), 500);
  assert.equal(Math.round(segs[0].w), 200);
});

test('idleSegments drops an idle span that has not been reached at the playhead', () => {
  const agent = { steps: [
    { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [{ ended_at: 300, kind: 'exec' }] },
  ] };
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 1000, minBlock: 0 });
  // cap 400 is before the idle start (500) → nothing.
  assert.deepEqual(idleSegments(agent, { scale, chartX: 0, effEnd: 1000, until: 400 }), []);
});

// ── criticalSpans (longest span + cost whale, per agent and per session) ─
test('criticalSpans marks the longest span and cost whale per agent and per session', () => {
  const rows = [
    { key: 'A', segments: [
      { refKey: 'a0', durationMs: 100, cost_usd: 0.01 },
      { refKey: 'a1', durationMs: 500, cost_usd: 0.05 },
    ] },
    { key: 'B', segments: [
      { refKey: 'b0', durationMs: 900, cost_usd: 0.02 },
    ] },
  ];
  const c = criticalSpans(rows);
  assert.deepEqual(c.agents.A, { longestRef: 'a1', whaleRef: 'a1' });
  assert.deepEqual(c.agents.B, { longestRef: 'b0', whaleRef: 'b0' });
  // session: longest span is b0 (900ms); cost whale is a1 ($0.05).
  assert.deepEqual(c.session, { longestRef: 'b0', whaleRef: 'a1' });
});

test('criticalSpans resolves ties to the first occurrence', () => {
  const rows = [{ key: 'A', segments: [
    { refKey: 'a0', durationMs: 100, cost_usd: 0.01 },
    { refKey: 'a1', durationMs: 100, cost_usd: 0.01 },
  ] }];
  assert.deepEqual(criticalSpans(rows).agents.A, { longestRef: 'a0', whaleRef: 'a0' });
});

test('criticalSpans ignores zero-duration / zero-cost segments', () => {
  const rows = [{ key: 'A', segments: [{ refKey: 'a0', durationMs: 0, cost_usd: 0 }] }];
  const c = criticalSpans(rows);
  assert.equal(c.agents.A, undefined);
  assert.deepEqual(c.session, {});
});

test('criticalSpans can mark a cost whale on a row with no measured duration', () => {
  const rows = [{ key: 'A', segments: [{ refKey: 'a0', durationMs: 0, cost_usd: 0.03 }] }];
  const c = criticalSpans(rows);
  assert.deepEqual(c.agents.A, { whaleRef: 'a0' });
  assert.deepEqual(c.session, { whaleRef: 'a0' });
});

// ── timelineRowOrder (depth-first re-parented row order) ────────────
test('timelineRowOrder: a main-only session is one entry at depth 0 with no spawn', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [] },
    children: [],
  };
  assert.deepEqual(timelineRowOrder(session), [
    { agentIndex: 0, depth: 0, spawn: null },
  ]);
});

test('timelineRowOrder: children with no parent link keep flatten order at depth 1', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [] },
    children: [
      { kind: 'subagent', started_at: 200, ended_at: 400, status: 'done' },
      { kind: 'subagent', started_at: 500, ended_at: 600, status: 'done' },
    ],
  };
  assert.deepEqual(timelineRowOrder(session), [
    { agentIndex: 0, depth: 0, spawn: null },
    { agentIndex: 1, depth: 1, spawn: null },
    { agentIndex: 2, depth: 1, spawn: null },
  ]);
});

test('timelineRowOrder: a resolved child carries spawn {agentIndex, stepIndex, toolIndex}', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 0, duration_ms: 200, tools: [{ name: 'Read', id: 't-read', kind: 'read' }] },
      { timestamp: 200, duration_ms: 800, tools: [
        { name: 'Bash', id: 't-bash', kind: 'exec' },
        { name: 'Agent', id: 't-spawn', kind: 'agent' },
      ] },
    ] },
    children: [
      { kind: 'subagent', parent_tool_use_id: 't-spawn', started_at: 250, ended_at: 400, status: 'done' },
    ],
  };
  assert.deepEqual(timelineRowOrder(session), [
    { agentIndex: 0, depth: 0, spawn: null },
    { agentIndex: 1, depth: 1, spawn: { agentIndex: 0, stepIndex: 1, toolIndex: 1 } },
  ]);
});

test('timelineRowOrder: a grandchild nests depth-first under its spawning child at depth 2', () => {
  // flatten order: main(0), gc(1), child(2) — the grandchild appears BEFORE its
  // parent in children[] to prove ordering is by spawn tree, not input order.
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 0, duration_ms: 1000, tools: [{ name: 'Agent', id: 'spawn-child', kind: 'agent' }] },
    ] },
    children: [
      { kind: 'subagent', parent_tool_use_id: 'spawn-gc', started_at: 300, ended_at: 400, status: 'done' },
      { kind: 'subagent', parent_tool_use_id: 'spawn-child', started_at: 100, ended_at: 500, status: 'done',
        steps: [{ timestamp: 100, duration_ms: 400, tools: [{ name: 'Agent', id: 'spawn-gc', kind: 'agent' }] }] },
    ],
  };
  assert.deepEqual(timelineRowOrder(session), [
    { agentIndex: 0, depth: 0, spawn: null },
    { agentIndex: 2, depth: 1, spawn: { agentIndex: 0, stepIndex: 0, toolIndex: 0 } },
    { agentIndex: 1, depth: 2, spawn: { agentIndex: 2, stepIndex: 0, toolIndex: 0 } },
  ]);
});

test('timelineRowOrder: siblings under one parent order by spawning (stepIndex, toolIndex)', () => {
  // children[] lists the later-spawned agent first; the tree re-orders by the
  // spawning tool position: step0:tool1 before step2:tool0.
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 0, duration_ms: 200, tools: [
        { name: 'Read', id: 'r0', kind: 'read' },
        { name: 'Agent', id: 'sp-a', kind: 'agent' },
      ] },
      { timestamp: 200, duration_ms: 200, tools: [] },
      { timestamp: 400, duration_ms: 600, tools: [{ name: 'Agent', id: 'sp-b', kind: 'agent' }] },
    ] },
    children: [
      { kind: 'subagent', parent_tool_use_id: 'sp-b', started_at: 450, ended_at: 700, status: 'done' },
      { kind: 'subagent', parent_tool_use_id: 'sp-a', started_at: 50, ended_at: 150, status: 'done' },
    ],
  };
  assert.deepEqual(timelineRowOrder(session).map(e => e.agentIndex), [0, 2, 1]);
});

test('timelineRowOrder: an unresolvable parent link falls to depth 1 after resolved children', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 0, duration_ms: 1000, tools: [{ name: 'Agent', id: 'sp-known', kind: 'agent' }] },
    ] },
    children: [
      { kind: 'subagent', parent_tool_use_id: 'toolu_gone', started_at: 100, ended_at: 200, status: 'done' },
      { kind: 'subagent', parent_tool_use_id: 'sp-known', started_at: 300, ended_at: 600, status: 'done' },
    ],
  };
  assert.deepEqual(timelineRowOrder(session), [
    { agentIndex: 0, depth: 0, spawn: null },
    { agentIndex: 2, depth: 1, spawn: { agentIndex: 0, stepIndex: 0, toolIndex: 0 } },
    { agentIndex: 1, depth: 1, spawn: null }, // unresolved → flat fallback, appended after
  ]);
});

test('timelineRowOrder: a spawn cycle still emits every agent exactly once', () => {
  // Two children point at tools inside EACH OTHER (never reachable from main)
  // — a pathological cycle. Both must still appear, once, at the flat depth 1.
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [] },
    children: [
      { kind: 'subagent', parent_tool_use_id: 'in-c2', started_at: 100, ended_at: 300, status: 'done',
        steps: [{ timestamp: 100, duration_ms: 100, tools: [{ name: 'Agent', id: 'in-c1', kind: 'agent' }] }] },
      { kind: 'subagent', parent_tool_use_id: 'in-c1', started_at: 200, ended_at: 400, status: 'done',
        steps: [{ timestamp: 200, duration_ms: 100, tools: [{ name: 'Agent', id: 'in-c2', kind: 'agent' }] }] },
    ],
  };
  const order = timelineRowOrder(session);
  assert.deepEqual(order.map(e => e.agentIndex).sort(), [0, 1, 2]);
  assert.equal(order.length, 3);
  // The cycle members landed at the flat fallback depth, under main.
  for (const e of order.slice(1)) assert.equal(e.depth >= 1, true);
});

// ── buildTimeline: re-parented rows (b) + progressive disclosure (c) ─
// A session where flatten order ≠ spawn-tree order: children[] lists the
// grandchild first, so display order must become [main(0), child(2), gc(1)]
// while every key stays `${sid}#${flattenIndex}`.
const NESTED_SESSION = {
  session_id: 'sn',
  main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
    { timestamp: 0, duration_ms: 1000, tools: [{ name: 'Agent', id: 'spawn-child', kind: 'agent', ended_at: 500 }] },
  ] },
  children: [
    { kind: 'subagent', parent_tool_use_id: 'spawn-gc', started_at: 300, ended_at: 400, status: 'done' },
    { kind: 'subagent', parent_tool_use_id: 'spawn-child', started_at: 100, ended_at: 500, status: 'done',
      steps: [{ timestamp: 100, duration_ms: 400, tools: [{ name: 'Agent', id: 'spawn-gc', kind: 'agent' }] }] },
  ],
};
const NESTED_OPTS = {
  hostW: 400, labelW: 100, pad: 0, axisH: 20, rowH: 20,
  minBlock: 2, minPxPerMs: 0, tickCount: 2, nowMs: 1000,
};

test('buildTimeline re-parents rows depth-first but keeps every key at its flattenSession index', () => {
  const layout = buildTimeline(NESTED_SESSION, NESTED_OPTS);
  // Display order per timelineRowOrder; keys/rowIndex pinned to flatten order.
  assert.deepEqual(layout.rows.map(r => r.key), ['sn#0', 'sn#2', 'sn#1']);
  assert.deepEqual(layout.rows.map(r => r.rowIndex), [0, 2, 1]);
  // Key-stability proof: same keys flattenSession implies, independent of order.
  const flat = flattenSession(NESTED_SESSION).map((_, i) => `sn#${i}`).sort();
  assert.deepEqual(layout.rows.map(r => r.key).slice().sort(), flat);
  // Geometry follows DISPLAY position: y stacks in the emitted order.
  assert.deepEqual(layout.rows.map(r => r.y), [20, 40, 60]);
});

test('buildTimeline rows carry nesting depth and spawn connector metadata', () => {
  const layout = buildTimeline(NESTED_SESSION, NESTED_OPTS);
  const [main, child, gc] = layout.rows;
  assert.equal(main.depth, 0);
  assert.equal(main.spawn, null);
  assert.equal(child.depth, 1);
  assert.deepEqual(child.spawn, { agentIndex: 0, stepIndex: 0, toolIndex: 0 });
  assert.equal(gc.depth, 2);
  assert.deepEqual(gc.spawn, { agentIndex: 2, stepIndex: 0, toolIndex: 0 });
  // Label indent follows depth (pad 0 + depth * default indent 12).
  assert.deepEqual(layout.rows.map(r => r.labelX), [0, 12, 24]);
});

// A busy fixture: 2 agents × many timed tools. Collapsed, the layout must stay
// O(agents) — no per-tool (or per-turn) segments at all.
function busySession(toolsPerStep = 20, steps = 10) {
  const mkSteps = (t0) => Array.from({ length: steps }, (_, k) => ({
    timestamp: t0 + k * 100, duration_ms: 100, cost_usd: 0.01, gen_ms: 10,
    tools: Array.from({ length: toolsPerStep }, (_, j) => ({
      name: 'Bash', kind: 'exec', status: 'ok',
      ended_at: t0 + k * 100 + (j + 1) * Math.floor(100 / (toolsPerStep + 1)),
    })),
  }));
  return {
    session_id: 'busy',
    main: { kind: 'main', started_at: 0, ended_at: 2000, status: 'done', steps: mkSteps(0) },
    children: [
      { kind: 'subagent', started_at: 1000, ended_at: 2000, status: 'done', steps: mkSteps(1000) },
    ],
  };
}

test('buildTimeline default (no opts.expanded) emits NO segments — O(agents), not O(tools)', () => {
  const layout = buildTimeline(busySession(), NESTED_OPTS);
  assert.equal(layout.rows.length, 2);
  for (const r of layout.rows) {
    assert.deepEqual(r.segments, []);
    assert.deepEqual(r.idleSegments, []);
    assert.equal(r.expanded, false);
  }
  // The whole layout is O(agents): rows + their bars, zero sub-spans, for a
  // fixture holding 2×10×20 = 400 tools.
  const subSpans = layout.rows.reduce((n, r) => n + r.segments.length + r.idleSegments.length, 0);
  assert.equal(subSpans, 0);
});

test('buildTimeline: expanding a row doubles its height, opens segY, and shifts the rows below', () => {
  const collapsed = buildTimeline(busySession(), NESTED_OPTS);
  const layout = buildTimeline(busySession(), {
    ...NESTED_OPTS, expanded: { rows: new Set(['busy#0']) },
  });
  const [main, child] = layout.rows;
  assert.equal(main.expanded, true);
  assert.equal(main.h, 40);            // 2 × rowH 20
  assert.equal(main.laneH, 20);        // the bar band stays one lane tall
  assert.equal(main.segY, 40);         // y 20 + laneH 20 — the turn band below the bar
  assert.equal(main.segments.length, 10);  // one turn span per step, no tools yet
  assert.ok(main.idleSegments.length > 0); // gen_ms fixture → idle spans return
  // The next row is pushed down by the extra lane; total height accounts for it.
  assert.equal(child.y, 60);           // axisH 20 + main h 40
  assert.equal(child.expanded, false);
  assert.deepEqual(child.segments, []);
  assert.equal(layout.height, collapsed.height + 20); // one extra lane, pad 0
});

test('buildTimeline: expanding one turn adds ITS tool sub-spans; sibling turns stay single spans', () => {
  const layout = buildTimeline(busySession(), {
    ...NESTED_OPTS, expanded: { rows: new Set(['busy#0']), turns: new Set(['busy#0.3']) },
  });
  const segs = layout.rows[0].segments;
  const toolSegs = segs.filter(s => s.toolIndex != null);
  const turnSegs = segs.filter(s => s.toolIndex == null);
  assert.equal(turnSegs.length, 10);            // every turn keeps its span
  assert.equal(toolSegs.length, 20);            // only step 3's 20 tools disclosed
  assert.ok(toolSegs.every(s => s.stepIndex === 3));
  // Tools tile immediately after their turn's span in the list (paint order).
  const turn3At = segs.findIndex(s => s.refKey === 'busy#0.3');
  assert.ok(segs.slice(turn3At + 1, turn3At + 21).every(s => s.toolIndex != null));
});

test('timelineAtTime: scrubbing keeps collapsed rows collapsed and clamps disclosed tools to T', () => {
  // Collapsed rows emit no segments at ANY T.
  for (const T of [100, 950, 5000]) {
    const t = timelineAtTime(busySession(), T, NESTED_OPTS);
    for (const r of t.rows) {
      assert.deepEqual(r.segments, []);
      assert.deepEqual(r.idleSegments, []);
    }
  }
  // An expanded row+turn discloses tool sub-spans, clamped to the playhead:
  // step 3 spans [300,400); at T=350 its tools cap at 350.
  const t = timelineAtTime(busySession(), 350, {
    ...NESTED_OPTS, expanded: { rows: new Set(['busy#0']), turns: new Set(['busy#0.3']) },
  });
  const tools = t.rows[0].segments.filter(s => s.toolIndex != null);
  assert.ok(tools.length > 0);
  const turnStart = 300;
  let covered = turnStart;
  for (const s of tools) covered += s.durationMs;
  assert.ok(covered <= 350, `tool spans must not pass the playhead (covered to ${covered})`);
  // …and turns after the playhead are dropped entirely.
  assert.ok(t.rows[0].segments.every(s => s.stepIndex <= 3));
});

test('timelineAtTime maps phase per AGENT (rowIndex), not per display position, when rows reorder', () => {
  // NESTED_SESSION display order is [main(0), child(2), gc(1)]. gc spans
  // [300,400], child [100,500]. At T=200 the child is active but gc is still
  // pending — a positional agents[i] mapping would swap them.
  const t = timelineAtTime(NESTED_SESSION, 200, NESTED_OPTS);
  const byKey = Object.fromEntries(t.rows.map(r => [r.key, r]));
  assert.equal(byKey['sn#0'].phase, 'active');
  assert.equal(byKey['sn#2'].phase, 'active'); // child started at 100
  assert.equal(byKey['sn#1'].phase, 'pending'); // gc starts at 300
  assert.equal(byKey['sn#1'].w, 0);
  assert.ok(byKey['sn#2'].w > 0);
  // Display order itself is preserved from buildTimeline.
  assert.deepEqual(t.rows.map(r => r.key), ['sn#0', 'sn#2', 'sn#1']);
});

// ── promptBands (prompt-segmented waterfall bands) ──────────────────
test('promptBands returns [] for a session with no/empty prompts', () => {
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 300, minBlock: 2 });
  assert.deepEqual(promptBands(null, { scale, chartX: 100 }), []);
  assert.deepEqual(promptBands({ session_id: 's1' }, { scale, chartX: 100 }), []);
  assert.deepEqual(promptBands({ session_id: 's1', prompts: [] }, { scale, chartX: 100 }), []);
});

test('promptBands: a single prompt spans its first turn start to the chart end', () => {
  // scale [0,1000]→[0,300]px, chartX 100. Prompt at t≈90 whose first turn
  // (step 0) starts at 100 → band [100,1000] → x 130, w 270.
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 300, minBlock: 2 });
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 100, duration_ms: 900, tools: [] },
    ] },
    children: [],
    prompts: [{ uuid: 'u1', text: 'fix the bug', timestamp: 90, first_step_index: 0 }],
  };
  assert.deepEqual(promptBands(session, { scale, chartX: 100 }), [
    { uuid: 'u1', text: 'fix the bug', x: 130, w: 270, firstStepIndex: 0 },
  ]);
});

test('promptBands: the first band ends where the next prompt\'s first turn starts', () => {
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 300, minBlock: 2 });
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 0, duration_ms: 500, tools: [] },
      { timestamp: 500, duration_ms: 500, tools: [] },
    ] },
    children: [],
    prompts: [
      { uuid: 'u1', text: 'first ask', timestamp: 0, first_step_index: 0 },
      { uuid: 'u2', text: 'second ask', timestamp: 480, first_step_index: 1 },
    ],
  };
  assert.deepEqual(promptBands(session, { scale, chartX: 100 }), [
    { uuid: 'u1', text: 'first ask', x: 100, w: 150, firstStepIndex: 0 },
    { uuid: 'u2', text: 'second ask', x: 250, w: 150, firstStepIndex: 1 },
  ]);
});

test('promptBands includes an orphan prompt (uuid "") with its own band', () => {
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 300, minBlock: 2 });
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 0, duration_ms: 600, tools: [] },
      { timestamp: 600, duration_ms: 400, tools: [] },
    ] },
    children: [],
    prompts: [
      { uuid: '', text: '', timestamp: 0, first_step_index: 0 },
      { uuid: 'u2', text: 'real ask', timestamp: 590, first_step_index: 1 },
    ],
  };
  const bands = promptBands(session, { scale, chartX: 0 });
  assert.equal(bands.length, 2);
  assert.deepEqual(bands[0], { uuid: '', text: '', x: 0, w: 180, firstStepIndex: 0 });
});

test('promptBands sorts out-of-order prompts by band start time', () => {
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 300, minBlock: 2 });
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 0, duration_ms: 500, tools: [] },
      { timestamp: 500, duration_ms: 500, tools: [] },
    ] },
    children: [],
    prompts: [
      { uuid: 'u2', text: 'later', timestamp: 480, first_step_index: 1 },
      { uuid: 'u1', text: 'earlier', timestamp: 0, first_step_index: 0 },
    ],
  };
  assert.deepEqual(promptBands(session, { scale, chartX: 0 }).map(b => b.uuid), ['u1', 'u2']);
});

test('promptBands falls back to the prompt timestamp when the step index resolves to no timed step, and skips unparseable-both', () => {
  const scale = makeTimeScale({ startMs: 0, endMs: 1000, width: 300, minBlock: 2 });
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 0, ended_at: 1000, status: 'done', steps: [
      { timestamp: 'not-a-time', duration_ms: 0, tools: [] },
    ] },
    children: [],
    prompts: [
      { uuid: 'u1', text: 'fallback', timestamp: 200, first_step_index: 0 },   // step ts unparseable → prompt ts 200
      { uuid: 'u2', text: 'gone', timestamp: 'nope', first_step_index: 99 },   // both unparseable → skipped
    ],
  };
  const bands = promptBands(session, { scale, chartX: 0 });
  assert.equal(bands.length, 1);
  assert.deepEqual(bands[0], { uuid: 'u1', text: 'fallback', x: 60, w: 240, firstStepIndex: 0 });
});

test('promptBands clamps a pre-window prompt to the chart left edge', () => {
  // Scale starts at 500; a prompt whose turn starts at 100 clamps to x(500)=0.
  const scale = makeTimeScale({ startMs: 500, endMs: 1000, width: 300, minBlock: 2 });
  const session = {
    session_id: 's1',
    main: { kind: 'main', started_at: 500, ended_at: 1000, status: 'done', steps: [
      { timestamp: 100, duration_ms: 900, tools: [] },
    ] },
    children: [],
    prompts: [{ uuid: 'u1', text: 'early', timestamp: 100, first_step_index: 0 }],
  };
  assert.deepEqual(promptBands(session, { scale, chartX: 50 }), [
    { uuid: 'u1', text: 'early', x: 50, w: 300, firstStepIndex: 0 },
  ]);
});

test('criticalSpans tolerates a null / non-array argument', () => {
  assert.deepEqual(criticalSpans(null), { session: {}, agents: {} });
  assert.deepEqual(criticalSpans([]), { session: {}, agents: {} });
});

// ── narrativeStrip ──────────────────────────────────────────────────
test('narrativeStrip: null / main-less / step-less session yields []', () => {
  assert.deepEqual(narrativeStrip(null), []);
  assert.deepEqual(narrativeStrip({ session_id: 's', main: null, children: [], prompts: [] }), []);
  assert.deepEqual(narrativeStrip({
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 10, steps: [] },
    children: [], prompts: [],
  }), []);
});

test('narrativeStrip: steps but no prompt markers → one orphan segment over all of them', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 4000, cost_usd: 0, tools: [{ kind: 'read' }, { kind: 'exec' }] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0, tools: [{ kind: 'edit' }] },
    ] },
    children: [], prompts: [],
  };
  const rows = narrativeStrip(session);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].uuid, '');
  assert.equal(rows[0].text, '');
  assert.equal(rows[0].turnCount, 2);
  assert.equal(rows[0].toolCount, 3);
  assert.deepEqual(rows[0].agents, []);
});

test('narrativeStrip: two prompts split turn/tool counts at the first_step_index boundary', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 2000, cost_usd: 0, tools: [{ kind: 'read' }] },
      { timestamp: 2000, duration_ms: 2000, cost_usd: 0, tools: [] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0, tools: [{ kind: 'edit' }, { kind: 'exec' }] },
    ] },
    children: [],
    prompts: [
      { uuid: 'p1', text: 'first ask', timestamp: 0, first_step_index: 0 },
      { uuid: 'p2', text: 'second ask', timestamp: 3900, first_step_index: 2 },
    ],
  };
  const rows = narrativeStrip(session);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].uuid, 'p1');
  assert.equal(rows[0].text, 'first ask');
  assert.equal(rows[0].turnCount, 2);
  assert.equal(rows[0].toolCount, 1);
  assert.equal(rows[1].uuid, 'p2');
  assert.equal(rows[1].turnCount, 1);
  assert.equal(rows[1].toolCount, 2);
});

test('narrativeStrip: segment windows tile [first step … main end] and durationMs = end - start', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 2000, cost_usd: 0, tools: [] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0, tools: [] },
    ] },
    children: [],
    prompts: [
      { uuid: 'p1', text: 'a', timestamp: 0, first_step_index: 0 },
      { uuid: 'p2', text: 'b', timestamp: 3900, first_step_index: 1 },
    ],
  };
  const rows = narrativeStrip(session);
  assert.equal(rows[0].startMs, 0);      // its first step's timestamp
  assert.equal(rows[0].endMs, 4000);     // next segment's start
  assert.equal(rows[0].durationMs, 4000);
  assert.equal(rows[1].startMs, 4000);
  assert.equal(rows[1].endMs, 9000);     // main's ended_at
  assert.equal(rows[1].durationMs, 5000);
});

test('narrativeStrip: a running main leaves the last segment open-ended to the injected nowMs', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'running', started_at: 0, ended_at: '', steps: [
      { timestamp: 1000, duration_ms: 0, cost_usd: 0, tools: [] },
    ] },
    children: [],
    prompts: [{ uuid: 'p1', text: 'go', timestamp: 1000, first_step_index: 0 }],
  };
  const rows = narrativeStrip(session, { nowMs: 60_000 });
  assert.equal(rows[0].startMs, 1000);
  assert.equal(rows[0].endMs, 60_000);
  assert.equal(rows[0].durationMs, 59_000);
});

test('narrativeStrip: costUsd sums the main agent step costs inside each segment', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 2000, cost_usd: 0.01, tools: [] },
      { timestamp: 2000, duration_ms: 2000, cost_usd: 0.02, tools: [] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0.04, tools: [] },
    ] },
    children: [],
    prompts: [
      { uuid: 'p1', text: 'a', timestamp: 0, first_step_index: 0 },
      { uuid: 'p2', text: 'b', timestamp: 3900, first_step_index: 2 },
    ],
  };
  const rows = narrativeStrip(session);
  assert.ok(Math.abs(rows[0].costUsd - 0.03) < 1e-9);
  assert.ok(Math.abs(rows[1].costUsd - 0.04) < 1e-9);
});

test('narrativeStrip: a sub-agent spawned from a segment turn is that segment\'s chip and rolls in whole-agent', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      // p1's only turn spawns the sub-agent via tool id 'spawn-1'.
      { timestamp: 0, duration_ms: 4000, cost_usd: 0.01, tools: [{ id: 'spawn-1', kind: 'agent' }] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0.02, tools: [] },
    ] },
    children: [{
      kind: 'subagent', agent_type: 'Explore', parent_tool_use_id: 'spawn-1',
      status: 'done', started_at: 100, ended_at: 3500, error_count: 0,
      steps: [
        { timestamp: 200, duration_ms: 1000, cost_usd: 0.10, tools: [{ kind: 'read' }, { kind: 'read' }] },
        { timestamp: 1200, duration_ms: 2000, cost_usd: 0.20, tools: [{ kind: 'exec' }] },
      ],
    }],
    prompts: [
      { uuid: 'p1', text: 'a', timestamp: 0, first_step_index: 0 },
      { uuid: 'p2', text: 'b', timestamp: 3900, first_step_index: 1 },
    ],
  };
  const rows = narrativeStrip(session);
  // Segment 1: 1 main turn + 2 sub-agent turns, 1 main tool + 3 sub-agent tools,
  // cost = 0.01 main + 0.30 whole sub-agent.
  assert.equal(rows[0].turnCount, 3);
  assert.equal(rows[0].toolCount, 4);
  assert.ok(Math.abs(rows[0].costUsd - 0.31) < 1e-9);
  assert.deepEqual(rows[0].agents, [{ agentIndex: 1, label: 'Explore', ok: true }]);
  // Segment 2 is untouched by the sub-agent.
  assert.equal(rows[1].turnCount, 1);
  assert.equal(rows[1].toolCount, 0);
  assert.ok(Math.abs(rows[1].costUsd - 0.02) < 1e-9);
  assert.deepEqual(rows[1].agents, []);
});

test('narrativeStrip: a nested sub-agent attributes to the segment that spawned its root parent', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 4000, cost_usd: 0, tools: [{ id: 'spawn-1', kind: 'agent' }] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0, tools: [] },
    ] },
    children: [
      { kind: 'subagent', agent_type: 'parent', parent_tool_use_id: 'spawn-1',
        status: 'done', started_at: 100, ended_at: 3500, error_count: 0,
        steps: [{ timestamp: 200, duration_ms: 500, cost_usd: 0.10, tools: [{ id: 'spawn-2', kind: 'agent' }] }] },
      { kind: 'subagent', agent_type: 'child', parent_tool_use_id: 'spawn-2',
        status: 'done', started_at: 300, ended_at: 3000, error_count: 0,
        steps: [{ timestamp: 400, duration_ms: 500, cost_usd: 0.05, tools: [{ kind: 'read' }] }] },
    ],
    prompts: [
      { uuid: 'p1', text: 'a', timestamp: 0, first_step_index: 0 },
      { uuid: 'p2', text: 'b', timestamp: 3900, first_step_index: 1 },
    ],
  };
  const rows = narrativeStrip(session);
  assert.deepEqual(rows[0].agents.map(c => c.agentIndex), [1, 2]);
  assert.equal(rows[0].turnCount, 3);   // 1 main + 1 parent + 1 child
  assert.equal(rows[0].toolCount, 3);   // spawn-1 + spawn-2 + read
  assert.ok(Math.abs(rows[0].costUsd - 0.15) < 1e-9);
  assert.deepEqual(rows[1].agents, []);
});

test('narrativeStrip: chip ok=false when the agent error_count > 0, true when clean', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 9000, cost_usd: 0, tools: [
        { id: 'spawn-1', kind: 'agent' }, { id: 'spawn-2', kind: 'agent' },
      ] },
    ] },
    children: [
      { kind: 'subagent', agent_type: 'clean', parent_tool_use_id: 'spawn-1',
        status: 'done', started_at: 100, ended_at: 3000, error_count: 0, steps: [] },
      { kind: 'subagent', agent_type: 'broken', parent_tool_use_id: 'spawn-2',
        status: 'done', started_at: 200, ended_at: 4000, error_count: 2, steps: [] },
    ],
    prompts: [{ uuid: 'p1', text: 'a', timestamp: 0, first_step_index: 0 }],
  };
  const rows = narrativeStrip(session);
  assert.deepEqual(rows[0].agents, [
    { agentIndex: 1, label: 'clean', ok: true },
    { agentIndex: 2, label: 'broken', ok: false },
  ]);
});

test('narrativeStrip: an unresolvable spawn falls back to the segment containing its start time', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 4000, cost_usd: 0, tools: [] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0, tools: [] },
    ] },
    children: [
      // No parent_tool_use_id → timelineRowOrder can't resolve the spawn;
      // started_at 5000 lands inside the second segment [4000, 9000).
      { kind: 'subagent', agent_type: 'orphan', status: 'done',
        started_at: 5000, ended_at: 6000, error_count: 0,
        steps: [{ timestamp: 5100, duration_ms: 500, cost_usd: 0.07, tools: [{ kind: 'read' }] }] },
    ],
    prompts: [
      { uuid: 'p1', text: 'a', timestamp: 0, first_step_index: 0 },
      { uuid: 'p2', text: 'b', timestamp: 3900, first_step_index: 1 },
    ],
  };
  const rows = narrativeStrip(session);
  assert.deepEqual(rows[0].agents, []);
  assert.deepEqual(rows[1].agents, [{ agentIndex: 1, label: 'orphan', ok: true }]);
  assert.equal(rows[1].turnCount, 2);
  assert.equal(rows[1].toolCount, 1);
  assert.ok(Math.abs(rows[1].costUsd - 0.07) < 1e-9);
});

test('narrativeStrip: rows carry firstStepIndex (the promptBands join key)', () => {
  const session = {
    session_id: 's',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 9000, steps: [
      { timestamp: 0, duration_ms: 4000, cost_usd: 0, tools: [] },
      { timestamp: 4000, duration_ms: 5000, cost_usd: 0, tools: [] },
    ] },
    children: [],
    prompts: [
      { uuid: 'p1', text: 'a', timestamp: 0, first_step_index: 0 },
      { uuid: 'p2', text: 'b', timestamp: 3900, first_step_index: 1 },
    ],
  };
  assert.deepEqual(narrativeStrip(session).map(r => r.firstStepIndex), [0, 1]);
});

// ── agentTimeRollup (whole-agent gen · tool · idle rollup, item 16) ─────
test('agentTimeRollup is all-zero for a null / step-less agent', () => {
  assert.deepEqual(agentTimeRollup(null), { genMs: 0, toolMs: 0, idleMs: 0, totalMs: 0 });
  assert.deepEqual(agentTimeRollup({ kind: 'main', status: 'done', steps: [] }),
    { genMs: 0, toolMs: 0, idleMs: 0, totalMs: 0 });
});

test('agentTimeRollup on a single closed turn matches turnTimeBuckets, totalMs = sum', () => {
  const step = { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [
    { ended_at: 300 }, { ended_at: 500 },
  ] };
  const agent = { kind: 'main', status: 'done', started_at: 0, ended_at: 1000, steps: [step] };
  // turnTimeBuckets(step) = { genMs: 200, toolMs: 500, idleMs: 300 } (tested above).
  assert.deepEqual(agentTimeRollup(agent), { genMs: 200, toolMs: 500, idleMs: 300, totalMs: 1000 });
});

test('agentTimeRollup over multiple turns sums per-turn turnTimeBuckets', () => {
  const s1 = { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [{ ended_at: 300 }] };
  const s2 = { timestamp: 1000, duration_ms: 2000, gen_ms: 500, tools: [{ ended_at: 2200 }] };
  const agent = { kind: 'main', status: 'done', started_at: 0, ended_at: 3000, steps: [s1, s2] };
  const b1 = turnTimeBuckets(s1), b2 = turnTimeBuckets(s2);
  const got = agentTimeRollup(agent);
  assert.equal(got.genMs, b1.genMs + b2.genMs);
  assert.equal(got.toolMs, b1.toolMs + b2.toolMs);
  assert.equal(got.idleMs, b1.idleMs + b2.idleMs);
  assert.equal(got.totalMs, got.genMs + got.toolMs + got.idleMs);
});

test('agentTimeRollup clamps a done agent\'s open-ended last turn to ended_at', () => {
  // Last turn has duration_ms 0 (the wire convention); the agent ended at 2000,
  // so the turn's wall-clock is [1000..2000]: gen 300, tool 400, idle 300.
  const agent = { kind: 'main', status: 'done', started_at: 0, ended_at: 2000, steps: [
    { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [{ ended_at: 300 }] },
    { timestamp: 1000, duration_ms: 0, gen_ms: 300, tools: [{ ended_at: 1400 }] },
  ] };
  // First turn: gen 200, tool 300, idle 500. Last turn: gen 300, tool 400, idle 300.
  assert.deepEqual(agentTimeRollup(agent), { genMs: 500, toolMs: 700, idleMs: 800, totalMs: 2000 });
});

test('agentTimeRollup clamps a RUNNING agent\'s open last turn to nowMs', () => {
  const agent = { kind: 'main', status: 'running', started_at: 0, steps: [
    { timestamp: 0, duration_ms: 0, gen_ms: 100, tools: [{ ended_at: 500 }] },
  ] };
  // At nowMs 800 the turn spans [0..800]: gen 100, tool 500, idle 200.
  assert.deepEqual(agentTimeRollup(agent, { nowMs: 800 }),
    { genMs: 100, toolMs: 500, idleMs: 200, totalMs: 800 });
  // No injected clock (nowMs defaults 0) → no wall-clock to attribute beyond gen.
  assert.deepEqual(agentTimeRollup(agent),
    { genMs: 100, toolMs: 0, idleMs: 0, totalMs: 100 });
});

test('agentTimeRollup never goes negative on degenerate timestamps', () => {
  // nowMs BEFORE the running turn started, and a done agent whose ended_at
  // precedes its last turn — both collapse to zero-width, never negative.
  const running = { kind: 'main', status: 'running', started_at: 1000, steps: [
    { timestamp: 1000, duration_ms: 0, gen_ms: 100, tools: [] },
  ] };
  const gotR = agentTimeRollup(running, { nowMs: 500 });
  assert.ok(gotR.genMs >= 0 && gotR.toolMs >= 0 && gotR.idleMs >= 0 && gotR.totalMs >= 0);
  const done = { kind: 'main', status: 'done', started_at: 1000, ended_at: 900, steps: [
    { timestamp: 1000, duration_ms: 0, gen_ms: 0, tools: [{ ended_at: 1200 }] },
  ] };
  const gotD = agentTimeRollup(done);
  assert.ok(gotD.genMs >= 0 && gotD.toolMs >= 0 && gotD.idleMs >= 0 && gotD.totalMs >= 0);
});

test('sessionTimeRollup sums the main agent and every child', () => {
  const session = {
    session_id: 's1',
    main: { kind: 'main', status: 'done', started_at: 0, ended_at: 2000, steps: [
      { timestamp: 0, duration_ms: 1000, gen_ms: 200, tools: [{ ended_at: 300 }] },
    ] },
    children: [
      { kind: 'subagent', agent_type: 'Explore', status: 'done', started_at: 100, ended_at: 900, steps: [
        { timestamp: 100, duration_ms: 500, gen_ms: 100, tools: [{ ended_at: 400 }] },
      ] },
    ],
  };
  // main: gen 200, tool 300, idle 500. child: gen 100, tool 300, idle 100.
  assert.deepEqual(sessionTimeRollup(session),
    { genMs: 300, toolMs: 600, idleMs: 600, totalMs: 1500 });
});

test('sessionTimeRollup is all-zero for a null / main-less session', () => {
  assert.deepEqual(sessionTimeRollup(null), { genMs: 0, toolMs: 0, idleMs: 0, totalMs: 0 });
  assert.deepEqual(sessionTimeRollup({ session_id: 's', main: null, children: [] }),
    { genMs: 0, toolMs: 0, idleMs: 0, totalMs: 0 });
});
