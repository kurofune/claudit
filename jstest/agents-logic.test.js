import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  parseTime,
  agentSpan,
  flattenSession,
  packLanes,
  laneCount,
  makeTimeScale,
  agentBar,
  agentElapsedMs,
  formatElapsed,
  graphStats,
} from '../web/agents-logic.js';

// agents-logic.js holds the pure, DOM-free math behind the Agents tab:
// lane packing for the swimlane, time→x scaling, bar geometry, and the
// status/elapsed derivations the cards show. view-agents.js is the
// swappable DOM layer on top — keeping the math here means a redesign
// never touches tested logic.

// ── parseTime ───────────────────────────────────────────────────────
test('parseTime passes numbers through', () => {
  assert.equal(parseTime(1_700_000_000_000), 1_700_000_000_000);
});

test('parseTime parses ISO strings to epoch ms', () => {
  assert.equal(parseTime('2026-05-01T12:00:00Z'), Date.parse('2026-05-01T12:00:00Z'));
});

test('parseTime returns NaN for null/garbage', () => {
  assert.ok(Number.isNaN(parseTime(null)));
  assert.ok(Number.isNaN(parseTime('not-a-date')));
  assert.ok(Number.isNaN(parseTime(undefined)));
});

// ── agentSpan ───────────────────────────────────────────────────────
test('agentSpan reads started_at/ended_at into epoch ms', () => {
  const a = { started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:01:00Z' };
  const { start, end } = agentSpan(a);
  assert.equal(end - start, 60_000);
});

// ── flattenSession ──────────────────────────────────────────────────
test('flattenSession returns main first, then children in order', () => {
  const session = {
    main: { kind: 'main' },
    children: [{ kind: 'subagent', agent_type: 'a' }, { kind: 'subagent', agent_type: 'b' }],
  };
  const flat = flattenSession(session);
  assert.equal(flat.length, 3);
  assert.equal(flat[0].kind, 'main');
  assert.equal(flat[1].agent_type, 'a');
  assert.equal(flat[2].agent_type, 'b');
});

test('flattenSession tolerates a missing main or children', () => {
  assert.deepEqual(flattenSession({}), []);
  assert.deepEqual(flattenSession({ main: { kind: 'main' } }).length, 1);
  assert.deepEqual(flattenSession(null), []);
});

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

// ── agentElapsedMs ──────────────────────────────────────────────────
test('agentElapsedMs for a done agent is end-start (now ignored)', () => {
  const a = { status: 'done', started_at: 0, ended_at: 5000 };
  assert.equal(agentElapsedMs(a, 9_999_999), 5000);
});

test('agentElapsedMs for a running agent counts up to now', () => {
  const a = { status: 'running', started_at: 1000, ended_at: 2000 };
  assert.equal(agentElapsedMs(a, 8000), 7000); // now-start, not end-start
});

test('agentElapsedMs never goes negative', () => {
  const a = { status: 'running', started_at: 5000, ended_at: 6000 };
  assert.equal(agentElapsedMs(a, 1000), 0);
});

// ── formatElapsed ───────────────────────────────────────────────────
test('formatElapsed renders seconds/minutes/hours', () => {
  assert.equal(formatElapsed(0), '0s');
  assert.equal(formatElapsed(5_000), '5s');
  assert.equal(formatElapsed(65_000), '1m 5s');
  assert.equal(formatElapsed(120_000), '2m');
  assert.equal(formatElapsed(3_600_000), '1h');
  assert.equal(formatElapsed(3_900_000), '1h 5m');
});

// ── graphStats ──────────────────────────────────────────────────────
test('graphStats counts sessions, agents, and running agents', () => {
  const graph = {
    sessions: [
      {
        main: { status: 'running' },
        children: [{ status: 'done' }, { status: 'running' }],
      },
      { main: { status: 'done' }, children: [] },
    ],
  };
  const stats = graphStats(graph);
  assert.equal(stats.sessions, 2);
  assert.equal(stats.agents, 4); // 1+2 + 1
  assert.equal(stats.running, 2);
});

test('graphStats on an empty graph is all zeros', () => {
  assert.deepEqual(graphStats({ sessions: [] }), { sessions: 0, agents: 0, running: 0 });
  assert.deepEqual(graphStats(null), { sessions: 0, agents: 0, running: 0 });
});
