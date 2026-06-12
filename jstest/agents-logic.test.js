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
  agentLabel,
  buildEventFeed,
  buildFlowLayout,
  baseName,
  agentTokens,
  refKey,
  parseRefKey,
  deepestRefs,
  defaultRef,
  resolveRef,
  buildDrawerPayload,
  looksTruncated,
  timelineBounds,
  buildTimeline,
  stepSegments,
  fitSegmentLabel,
  costHeat,
  agentPhaseAt,
  playheadBounds,
  playheadStats,
  timelineAtTime,
  specActive,
  filterTrace,
  detectRetries,
  spawnTargetIndex,
  conversationSegments,
  conversationReplies,
  conversationSessionList,
  clampConvSidebarWidth,
  clampTreeWidth,
  clampDrawerWidth,
  orderTreeSessions,
  treeFollowMode,
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

// ── spawnTargetIndex ────────────────────────────────────────────────
test('spawnTargetIndex finds the child whose parent_tool_use_id matches', () => {
  const session = {
    main: { kind: 'main' },
    children: [
      { kind: 'subagent', parent_tool_use_id: 'toolu_A' },
      { kind: 'subagent', parent_tool_use_id: 'toolu_B' },
    ],
  };
  // flattenSession index: main=0, first child=1, second child=2.
  assert.equal(spawnTargetIndex(session, 'toolu_B'), 2);
  assert.equal(spawnTargetIndex(session, 'toolu_A'), 1);
});

test('spawnTargetIndex returns null for no match or empty ref', () => {
  const session = {
    main: { kind: 'main' },
    children: [{ kind: 'subagent', parent_tool_use_id: 'toolu_A' }],
  };
  assert.equal(spawnTargetIndex(session, 'toolu_Z'), null);
  assert.equal(spawnTargetIndex(session, ''), null);
  assert.equal(spawnTargetIndex(null, 'toolu_A'), null);
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

// ── agentLabel ──────────────────────────────────────────────────────
test('agentLabel is "main" for the main agent, else the agent_type', () => {
  assert.equal(agentLabel({ kind: 'main' }), 'main');
  assert.equal(agentLabel({ kind: 'subagent', agent_type: 'Explore' }), 'Explore');
  assert.equal(agentLabel({ kind: 'subagent' }), 'subagent');
  assert.equal(agentLabel(null), '');
});

// ── buildEventFeed ──────────────────────────────────────────────────
const FEED_GRAPH = {
  sessions: [{
    session_id: 's1',
    main: {
      kind: 'main', status: 'done',
      started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:05Z',
      steps: [
        { timestamp: '2026-05-01T12:00:00Z', tools: [{ name: 'Bash', kind: 'exec', input: 'ls', status: 'ok' }] },
        { timestamp: '2026-05-01T12:00:05Z', tools: [{ name: 'Agent', kind: 'agent', detail: 'Explore' }] },
      ],
    },
    children: [{
      kind: 'subagent', agent_type: 'Explore', description: 'map the code', status: 'running',
      started_at: '2026-05-01T12:00:06Z', ended_at: '2026-05-01T12:00:08Z',
      steps: [
        { timestamp: '2026-05-01T12:00:08Z', tools: [{ name: 'Read', kind: 'read', detail: '.go', input: 'g.go', status: 'ok' }] },
      ],
    }],
  }],
};

test('buildEventFeed emits tool, spawn and done events sorted newest-first', () => {
  const feed = buildEventFeed(FEED_GRAPH);
  // Non-increasing timestamps (newest first).
  for (let i = 1; i < feed.length; i++) {
    assert.ok(feed[i - 1].t >= feed[i].t, `feed not sorted desc at ${i}`);
  }
  // Newest event is the Explore sub-agent's Read tool call.
  const top = feed[0];
  assert.equal(top.kind, 'tool');
  assert.equal(top.tool, 'Read');
  // toolKind carries the normalized ToolKind enum (Phase 1) so the feed row
  // can color its kind badge without re-matching the raw tool name.
  assert.equal(top.toolKind, 'read');
  assert.equal(top.status, 'ok');
  assert.equal(top.agentLabel, 'Explore');
  assert.equal(top.agentIndex, 1);
  assert.equal(top.sessionId, 's1');
});

test('buildEventFeed carries a spawn event for each sub-agent', () => {
  const feed = buildEventFeed(FEED_GRAPH);
  const spawns = feed.filter(e => e.kind === 'spawn');
  assert.equal(spawns.length, 1);
  assert.equal(spawns[0].agentLabel, 'Explore');
  assert.equal(spawns[0].description, 'map the code');
  assert.equal(spawns[0].t, Date.parse('2026-05-01T12:00:06Z'));
});

test('buildEventFeed emits a done event only for finished agents', () => {
  const feed = buildEventFeed(FEED_GRAPH);
  const dones = feed.filter(e => e.kind === 'done');
  // main is done; the Explore child is running → no done event for it.
  assert.equal(dones.length, 1);
  assert.equal(dones[0].agentLabel, 'main');
});

test('buildEventFeed respects a limit and tolerates an empty graph', () => {
  assert.deepEqual(buildEventFeed({ sessions: [] }), []);
  assert.deepEqual(buildEventFeed(null), []);
  const limited = buildEventFeed(FEED_GRAPH, { limit: 2 });
  assert.equal(limited.length, 2);
});

test('buildEventFeed tool events carry stepIndex/toolIndex locating the exact tool', () => {
  const graph = {
    sessions: [{
      session_id: 'sX',
      main: {
        kind: 'main', status: 'running',
        started_at: '2026-05-01T12:00:00Z',
        steps: [
          { timestamp: '2026-05-01T12:00:00Z', tools: [{ name: 'Bash' }] },
          { timestamp: '2026-05-01T12:00:05Z', tools: [{ name: 'Read' }, { name: 'Edit' }] },
        ],
      },
      children: [],
    }],
  };
  const feed = buildEventFeed(graph);
  const edit = feed.find(e => e.kind === 'tool' && e.tool === 'Edit');
  assert.ok(edit, 'expected an Edit tool event');
  assert.equal(edit.stepIndex, 1);
  assert.equal(edit.toolIndex, 1);
  assert.equal(edit.tool, 'Edit');
});

test('buildEventFeed tool events inherit the PARENT step cost_usd/duration_ms', () => {
  const graph = {
    sessions: [{
      session_id: 'sY',
      main: {
        kind: 'main', status: 'running', cost_usd: 9.99,
        started_at: '2026-05-01T12:00:00Z',
        steps: [
          { timestamp: '2026-05-01T12:00:00Z', cost_usd: 0.01, duration_ms: 100, tools: [{ name: 'Bash' }] },
          { timestamp: '2026-05-01T12:00:05Z', cost_usd: 0.04, duration_ms: 1200, tools: [{ name: 'Read' }] },
        ],
      },
      children: [],
    }],
  };
  const feed = buildEventFeed(graph);
  const read = feed.find(e => e.kind === 'tool' && e.tool === 'Read');
  assert.ok(read, 'expected a Read tool event');
  // Parent step's values, not the agent's (9.99) nor the sibling step's (0.01/100).
  assert.equal(read.cost_usd, 0.04);
  assert.equal(read.durationMs, 1200);
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

// ── baseName ────────────────────────────────────────────────────────
test('baseName returns the last segment of an absolute path', () => {
  assert.equal(baseName('/Users/x/Projects/claudit'), 'claudit');
});

test('baseName ignores a trailing slash', () => {
  assert.equal(baseName('/a/b/'), 'b');
});

test('baseName returns a bare name unchanged', () => {
  assert.equal(baseName('foo'), 'foo');
});

test('baseName returns "" for empty / null / undefined', () => {
  assert.equal(baseName(''), '');
  assert.equal(baseName(null), '');
  assert.equal(baseName(undefined), '');
});

// ── agentTokens ─────────────────────────────────────────────────────
test('agentTokens sums the Go-named token fields, combining 5m+1h cache writes', () => {
  const agent = {
    tokens: {
      InputTokens: 100, OutputTokens: 50,
      CacheCreate5mTokens: 10, CacheCreate1hTokens: 5,
      CacheReadTokens: 200,
    },
  };
  const t = agentTokens(agent);
  assert.equal(t.input, 100);
  assert.equal(t.output, 50);
  assert.equal(t.cacheWrite, 15); // 10 + 5
  assert.equal(t.cacheRead, 200);
  assert.equal(t.total, 365); // 100 + 50 + 15 + 200
});

test('agentTokens is all zeros for a null agent or missing tokens', () => {
  const zero = { input: 0, output: 0, cacheWrite: 0, cacheRead: 0, total: 0 };
  assert.deepEqual(agentTokens(null), zero);
  assert.deepEqual(agentTokens({}), zero);
});

// ── refKey / parseRefKey ────────────────────────────────────────────
test('refKey/parseRefKey round-trip agent, step and tool refs', () => {
  // agent
  assert.equal(refKey({ sessionId: 's1', agentIndex: 0 }), 's1#0');
  assert.deepEqual(parseRefKey('s1#0'), {
    sessionId: 's1', agentIndex: 0, stepIndex: null, toolIndex: null, type: 'agent',
  });
  // step
  assert.equal(refKey({ sessionId: 's1', agentIndex: 2, stepIndex: 3 }), 's1#2.3');
  assert.deepEqual(parseRefKey('s1#2.3'), {
    sessionId: 's1', agentIndex: 2, stepIndex: 3, toolIndex: null, type: 'step',
  });
  // tool
  assert.equal(refKey({ sessionId: 's1', agentIndex: 2, stepIndex: 3, toolIndex: 1 }), 's1#2.3:1');
  assert.deepEqual(parseRefKey('s1#2.3:1'), {
    sessionId: 's1', agentIndex: 2, stepIndex: 3, toolIndex: 1, type: 'tool',
  });
});

test('refKey returns "" for empty / garbage refs', () => {
  assert.equal(refKey(null), '');
  assert.equal(refKey({}), '');
  assert.equal(refKey({ sessionId: 's1' }), ''); // no agentIndex
  assert.equal(refKey({ agentIndex: 0 }), ''); // no sessionId
  assert.equal(refKey({ sessionId: 's1', agentIndex: 'x' }), ''); // non-numeric
});

test('parseRefKey returns null for empty / null / non-string / malformed', () => {
  assert.equal(parseRefKey(''), null);
  assert.equal(parseRefKey(null), null);
  assert.equal(parseRefKey(42), null);
  assert.equal(parseRefKey('no-hash'), null);
  assert.equal(parseRefKey('s1#x'), null); // non-numeric agent index
  assert.equal(parseRefKey('s1#0.y'), null); // non-numeric step index
});

test('refKey drops a dangling toolIndex when stepIndex is missing', () => {
  // A tool ref always nests a step; without a step there is nothing to
  // hang the tool on, so it degrades to the agent form.
  assert.equal(refKey({ sessionId: 's1', agentIndex: 0, toolIndex: 2 }), 's1#0');
  assert.equal(refKey({ sessionId: 's1', agentIndex: 0, stepIndex: null, toolIndex: 2 }), 's1#0');
});

test('refKey/parseRefKey round-trip a UUID sessionId with hyphens', () => {
  const sid = '0a1b2c3d-4e5f-6789-abcd-ef0123456789';
  const key = refKey({ sessionId: sid, agentIndex: 1, stepIndex: 2, toolIndex: 0 });
  assert.equal(key, `${sid}#1.2:0`);
  assert.deepEqual(parseRefKey(key), {
    sessionId: sid, agentIndex: 1, stepIndex: 2, toolIndex: 0, type: 'tool',
  });
});

// ── deepestRefs ─────────────────────────────────────────────────────
test('deepestRefs returns [] for empty array and empty Set input', () => {
  assert.deepEqual(deepestRefs([]), []);
  assert.deepEqual(deepestRefs(new Set()), []);
});

test('deepestRefs keeps a lone agent ref with no descendants', () => {
  assert.deepEqual(deepestRefs(['s#0']), ['s#0']);
});

test('deepestRefs: agent + step + tool all present → only the tool is a leaf', () => {
  assert.deepEqual(deepestRefs(['s#0', 's#0.3', 's#0.3:1']), ['s#0.3:1']);
});

test('deepestRefs: agent + tool, intermediate step absent → only the tool is a leaf', () => {
  // The agent is still non-leaf (it has a deeper descendant); the absent step
  // is irrelevant.
  assert.deepEqual(deepestRefs(['s#0', 's#0.3:1']), ['s#0.3:1']);
});

test('deepestRefs: two sibling steps + their agent → the two steps are leaves', () => {
  assert.deepEqual(deepestRefs(['s#0', 's#0.1', 's#0.2']), ['s#0.1', 's#0.2']);
});

test('deepestRefs prefix-collision guard: s#1 and s#12.0 are both leaves', () => {
  // s#12.0 descends from agent s#12, NOT s#1 — a naive startsWith would wrongly
  // treat s#1 as having a descendant. Both must survive as leaves.
  const out = deepestRefs(['s#1', 's#12.0']);
  assert.ok(out.includes('s#1'));
  assert.ok(out.includes('s#12.0'));
  assert.equal(out.length, 2);
});

test('deepestRefs accepts a Set input and returns an array', () => {
  const out = deepestRefs(new Set(['s#0', 's#0.3', 's#0.3:1']));
  assert.ok(Array.isArray(out));
  assert.deepEqual(out, ['s#0.3:1']);
});

test('deepestRefs treats cross-session refs as independent', () => {
  // a#0.1 and b#0 are unrelated; both are leaves.
  assert.deepEqual(deepestRefs(['a#0.1', 'b#0']), ['a#0.1', 'b#0']);
});

test('deepestRefs matches the O(n²) reference leaf scan on a hand-built list', () => {
  const input = [
    's#0', 's#0.1', 's#0.1:0', 's#0.2',
    's#1', 's#12.0', 's#12',
    'b#0', 'a#0.1',
    's#0.1:0', // duplicate
  ];
  const arr = [...new Set(input)];
  const expected = arr.filter(r =>
    !arr.some(o => o !== r && (o.startsWith(r + '.') || o.startsWith(r + ':'))));
  assert.deepEqual(deepestRefs(input).sort(), [...expected].sort());
});

// ── defaultRef ──────────────────────────────────────────────────────
test('defaultRef points at the first session that has an agent', () => {
  const graph = {
    sessions: [
      { session_id: 's1', main: { kind: 'main' }, children: [] },
      { session_id: 's2', main: { kind: 'main' }, children: [] },
    ],
  };
  assert.deepEqual(defaultRef(graph), { sessionId: 's1', agentIndex: 0 });
});

test('defaultRef skips a leading agent-less session', () => {
  const graph = {
    sessions: [
      { session_id: 'empty', main: null, children: [] },
      { session_id: 's2', main: { kind: 'main' }, children: [] },
    ],
  };
  assert.deepEqual(defaultRef(graph), { sessionId: 's2', agentIndex: 0 });
});

test('defaultRef is null on an empty / null graph', () => {
  assert.equal(defaultRef({ sessions: [] }), null);
  assert.equal(defaultRef(null), null);
  assert.equal(defaultRef({ sessions: [{ main: null, children: [] }] }), null);
});

// ── resolveRef / buildDrawerPayload fixture ─────────────────────────
// One session, a main agent with real tokens and two steps; step 0
// carries thinking/text/model/cost/duration and a single tool with
// input/output/status/detail. Mirrors the /_claudit/api/agents shape.
const DRAWER_GRAPH = {
  sessions: [{
    session_id: 'sess-uuid-1',
    cwd: '/Users/x/Projects/claudit',
    main: {
      kind: 'main', agent_type: '', description: '', status: 'done',
      started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:01:00Z',
      cost_usd: 0.42,
      tokens: {
        InputTokens: 100, OutputTokens: 50,
        CacheCreate5mTokens: 10, CacheCreate1hTokens: 5,
        CacheReadTokens: 200,
      },
      steps: [
        {
          timestamp: '2026-05-01T12:00:10Z', model: 'claude-opus-4',
          cost_usd: 0.10, duration_ms: 4200,
          thinking: 'plan the work', text: 'Listing files',
          tokens: {
            InputTokens: 80, OutputTokens: 40,
            CacheCreate5mTokens: 8, CacheCreate1hTokens: 2,
            CacheReadTokens: 120,
          },
          tools: [
            { name: 'Bash', kind: 'exec', detail: 'ls -la', input: 'ls -la', status: 'ok', output: 'file.go\n', id: 'toolu_x' },
          ],
        },
        {
          timestamp: '2026-05-01T12:00:40Z', model: 'claude-opus-4',
          cost_usd: 0.32, duration_ms: 3000, tools: [],
          tokens: {
            InputTokens: 0, OutputTokens: 5,
            CacheCreate5mTokens: 0, CacheCreate1hTokens: 0,
            CacheReadTokens: 0,
          },
        },
      ],
    },
    children: [],
  }],
};

test('resolveRef resolves an agent ref to type "agent"', () => {
  const r = resolveRef(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0 });
  assert.equal(r.type, 'agent');
  assert.equal(r.agent.kind, 'main');
  assert.equal(r.agentIndex, 0);
  assert.equal(r.stepIndex, null);
  assert.equal(r.step, null);
  assert.equal(r.tool, null);
  assert.equal(r.toolIndex, null);
  assert.equal(r.session.session_id, 'sess-uuid-1');
});

test('resolveRef resolves a step ref to type "step"', () => {
  const r = resolveRef(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 });
  assert.equal(r.type, 'step');
  assert.equal(r.stepIndex, 0);
  assert.equal(r.step.model, 'claude-opus-4');
  assert.equal(r.tool, null);
});

test('resolveRef resolves a tool ref to type "tool"', () => {
  const r = resolveRef(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 });
  assert.equal(r.type, 'tool');
  assert.equal(r.toolIndex, 0);
  assert.equal(r.tool.name, 'Bash');
  assert.equal(r.step.model, 'claude-opus-4');
});

test('resolveRef accepts a refKey string', () => {
  const r = resolveRef(DRAWER_GRAPH, 'sess-uuid-1#0.0:0');
  assert.equal(r.type, 'tool');
  assert.equal(r.tool.name, 'Bash');
});

test('resolveRef degrades an out-of-range stepIndex to type "agent"', () => {
  const r = resolveRef(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 99 });
  assert.equal(r.type, 'agent');
  assert.equal(r.step, null);
  assert.equal(r.stepIndex, null);
});

test('resolveRef returns null for a missing session or agent', () => {
  assert.equal(resolveRef(DRAWER_GRAPH, { sessionId: 'nope', agentIndex: 0 }), null);
  assert.equal(resolveRef(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 9 }), null);
});

// ── buildDrawerPayload ──────────────────────────────────────────────
test('buildDrawerPayload builds the agent payload', () => {
  const p = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0 });
  assert.equal(p.type, 'agent');
  assert.equal(p.refKey, 'sess-uuid-1#0');
  assert.equal(p.project, 'claudit');
  assert.equal(p.cwd, '/Users/x/Projects/claudit');
  assert.equal(p.sessionId, 'sess-uuid-1');
  assert.equal(p.title, 'main');
  assert.equal(p.agentLabel, 'main');
  assert.equal(p.agentKind, 'main');
  assert.equal(p.kind, 'agent');
  assert.equal(p.description, ''); // main → no description
  assert.equal(p.status, 'done');
  assert.equal(p.tokens.total, 365);
  assert.equal(p.cost_usd, 0.42);
  assert.equal(p.durationMs, 60_000); // started/ended a minute apart
  assert.equal(p.stepCount, 2);
  // Agent has no tool/step detail.
  assert.equal(p.detail, '');
  assert.equal(p.input, '');
  assert.equal(p.output, '');
  assert.equal(p.thinking, '');
  assert.equal(p.text, '');
  assert.equal(p.model, '');
});

test('buildDrawerPayload builds the tool payload, inheriting the parent step', () => {
  const p = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 });
  assert.equal(p.type, 'tool');
  assert.equal(p.refKey, 'sess-uuid-1#0.0:0');
  assert.equal(p.title, 'Bash');
  // kind is the normalized ToolKind enum (Phase 1), not the raw tool name —
  // it drives the drawer's colored kind badge. Title still shows the raw name.
  assert.equal(p.kind, 'exec');
  assert.equal(p.status, 'ok');
  assert.equal(p.detail, 'ls -la');
  assert.equal(p.input, 'ls -la');
  assert.equal(p.output, 'file.go\n');
  // Turn-level fields inherited from the parent step.
  assert.equal(p.thinking, 'plan the work');
  assert.equal(p.text, 'Listing files');
  assert.equal(p.model, 'claude-opus-4');
  assert.equal(p.cost_usd, 0.10);
  assert.equal(p.durationMs, 4200);
  // A tool inherits its parent turn's tokens, exactly as it inherits
  // cost/model/duration. Step-0 fixture: 80 + 40 + (8 + 2) + 120 = 250.
  assert.equal(p.tokens.input, 80);
  assert.equal(p.tokens.output, 40);
  assert.equal(p.tokens.cacheWrite, 10);
  assert.equal(p.tokens.cacheRead, 120);
  assert.equal(p.tokens.total, 250);
});

test('buildDrawerPayload builds the step payload as "Turn N" with no tool I/O', () => {
  const p = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 });
  assert.equal(p.type, 'step');
  assert.equal(p.title, 'Turn 1'); // stepIndex 0 → "Turn 1"
  assert.equal(p.kind, 'step');
  assert.equal(p.status, '');
  assert.equal(p.thinking, 'plan the work');
  assert.equal(p.text, 'Listing files');
  assert.equal(p.model, 'claude-opus-4');
  assert.equal(p.cost_usd, 0.10);
  assert.equal(p.durationMs, 4200);
  // A step aggregates tools — it carries no single tool's I/O.
  assert.equal(p.input, '');
  assert.equal(p.output, '');
  assert.equal(p.detail, '');
});

test('buildDrawerPayload: a step carries its own per-turn tokens', () => {
  const p = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 });
  assert.equal(p.type, 'step');
  // 80 + 40 + (8 + 2) + 120 = 250
  assert.equal(p.tokens.input, 80);
  assert.equal(p.tokens.output, 40);
  assert.equal(p.tokens.cacheWrite, 10);
  assert.equal(p.tokens.cacheRead, 120);
  assert.equal(p.tokens.total, 250);
});

test('buildDrawerPayload: a step carries a navigable list of its tool calls', () => {
  const p = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 });
  assert.equal(p.type, 'step');
  assert.equal(p.tools.length, 1);
  const row = p.tools[0];
  assert.equal(row.name, 'Bash');
  assert.equal(row.kind, 'exec');
  assert.equal(row.detail, 'ls -la');
  assert.equal(row.status, 'ok');
  // refKey points at the exact tool so the drawer row can click through to it.
  assert.equal(row.refKey, 'sess-uuid-1#0.0:0');
});

test('buildDrawerPayload: a tool-less step has an empty tools list, and non-steps have none', () => {
  const step1 = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 1 });
  assert.deepEqual(step1.tools, []);
  const agent = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0 });
  assert.deepEqual(agent.tools, []);
  const tool = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 });
  assert.deepEqual(tool.tools, []);
});

test('buildDrawerPayload carries the tool_use id so the drawer can load full I/O', () => {
  const tool = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 });
  assert.equal(tool.toolId, 'toolu_x');
  // Non-tool refs have nothing to load full → empty toolId.
  const agent = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0 });
  assert.equal(agent.toolId, '');
  const step = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 });
  assert.equal(step.toolId, '');
});

test('buildDrawerPayload: fullByTool output substitutes the tool output, input stays the snippet', () => {
  const p = buildDrawerPayload(
    DRAWER_GRAPH,
    { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 },
    { 'toolu_x': { output: 'FULL OUTPUT...' } },
  );
  assert.equal(p.output, 'FULL OUTPUT...');
  assert.equal(p.input, 'ls -la');
});

test('buildDrawerPayload: fullByTool substitutes both input and output', () => {
  const p = buildDrawerPayload(
    DRAWER_GRAPH,
    { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 },
    { 'toolu_x': { input: 'FULL INPUT...', output: 'FULL OUTPUT...' } },
  );
  assert.equal(p.input, 'FULL INPUT...');
  assert.equal(p.output, 'FULL OUTPUT...');
});

test('buildDrawerPayload: fullByTool with no entry for this tool id keeps both snippets', () => {
  const p = buildDrawerPayload(
    DRAWER_GRAPH,
    { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 },
    { 'some_other_tool': { output: 'UNRELATED' } },
  );
  assert.equal(p.input, 'ls -la');
  assert.equal(p.output, 'file.go\n');
});

test('buildDrawerPayload: fullByTool is ignored for agent and step refs', () => {
  const full = { 'toolu_x': { input: 'FULL INPUT...', output: 'FULL OUTPUT...' } };
  const agent = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0 }, full);
  assert.equal(agent.input, '');
  assert.equal(agent.output, '');
  const step = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 }, full);
  assert.equal(step.input, '');
  assert.equal(step.output, '');
});

test('looksTruncated detects the bounded-snippet ellipsis marker', () => {
  assert.equal(looksTruncated('a long snippet…'), true);
  assert.equal(looksTruncated('complete'), false);
  assert.equal(looksTruncated(''), false);
  assert.equal(looksTruncated(null), false);
  assert.equal(looksTruncated(undefined), false);
});

// A main agent whose step 0 spawned a sub-agent via an Agent tool_use, plus a
// plain tool with no spawn — exercises the drawer's spawn-link branch.
const SPAWN_GRAPH = {
  sessions: [{
    session_id: 'sess-spawn',
    cwd: '/Users/x/Projects/claudit',
    main: {
      kind: 'main', status: 'done',
      started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:01:00Z',
      cost_usd: 1, steps: [{
        timestamp: '2026-05-01T12:00:10Z', model: 'claude-opus-4', cost_usd: 0.1,
        tools: [
          {
            name: 'Agent', kind: 'agent', detail: 'Explore', id: 'toolu_spawn',
            input: 'go find callers', status: 'ok',
            spawned: { agent_ref: 'toolu_spawn', cost_usd: 0.05, error_count: 2, duration_ms: 4000, tokens: { InputTokens: 9 } },
          },
          { name: 'Read', kind: 'read', detail: 'x.go', id: 'toolu_read', input: 'x.go', status: 'ok' },
        ],
      }],
    },
    children: [{
      kind: 'subagent', agent_type: 'Explore', description: 'find callers',
      parent_tool_use_id: 'toolu_spawn', status: 'done',
      started_at: '2026-05-01T12:00:11Z', ended_at: '2026-05-01T12:00:15Z',
      cost_usd: 0.05, steps: [],
    }],
  }],
};

test('buildDrawerPayload exposes a spawn rollup with a navigable child ref for an Agent tool', () => {
  const p = buildDrawerPayload(SPAWN_GRAPH, { sessionId: 'sess-spawn', agentIndex: 0, stepIndex: 0, toolIndex: 0 });
  assert.ok(p.spawned, 'expected a spawned rollup on the Agent tool');
  assert.equal(p.spawned.cost_usd, 0.05);
  assert.equal(p.spawned.error_count, 2);
  // childRef resolves to the sub-agent whose parent_tool_use_id matches — the
  // first child, flattenSession index 1.
  assert.equal(p.spawned.childRef, 'sess-spawn#1');
});

test('buildDrawerPayload leaves spawned null for a tool that launched nothing', () => {
  const p = buildDrawerPayload(SPAWN_GRAPH, { sessionId: 'sess-spawn', agentIndex: 0, stepIndex: 0, toolIndex: 1 });
  assert.equal(p.spawned, null);
});

test('buildDrawerPayload returns null for a ref whose agent is missing', () => {
  assert.equal(buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 9 }), null);
  assert.equal(buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'nope', agentIndex: 0 }), null);
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
    x: 100, w: 120, stepIndex: 0, refKey: 's1#0.0', status: '', cost_usd: 0.01, durationMs: 400,
  });
  // step1 (duration 0) extends to effEnd 1000: 400..1000 → x 220, w 180, span 600ms
  assert.deepEqual(segs[1], {
    x: 220, w: 180, stepIndex: 1, refKey: 's1#0.1', status: '', cost_usd: 0.02, durationMs: 600,
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

test('buildTimeline attaches per-turn segments to each row', () => {
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
  });
  assert.deepEqual(layout.rows[0].segments, [
    { x: 100, w: 120, stepIndex: 0, refKey: 's1#0.0', status: '', cost_usd: 0.01, durationMs: 400 },
    { x: 220, w: 180, stepIndex: 1, refKey: 's1#0.1', status: '', cost_usd: 0.02, durationMs: 600 },
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
  const t = timelineAtTime(session, 300, {
    hostW: 400, labelW: 100, pad: 0, axisH: 20, rowH: 20,
    minBlock: 2, minPxPerMs: 0, tickCount: 2, nowMs: 1000,
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

// ── filterTrace / specActive (Phase 2 — trace filter) ────────────────
// One session with a main agent (two steps, mixed tool kinds, one error)
// and one Explore sub-agent — exercises every filter dimension.
const FILTER_GRAPH = {
  sessions: [{
    session_id: 's1',
    main: {
      kind: 'main', agent_type: '', description: '', cost_usd: 0.5,
      steps: [
        {
          thinking: 'plan payload.go', text: '', duration_ms: 1000, cost_usd: 0.10,
          tools: [
            { name: 'Read', kind: 'read', detail: 'payload.go', input: 'payload.go', output: 'ok', status: 'ok' },
            { name: 'Bash', kind: 'exec', input: 'go test', output: 'FAIL', status: 'error' },
          ],
        },
        {
          thinking: '', text: 'spawning', duration_ms: 8000, cost_usd: 0.30,
          tools: [{ name: 'Agent', kind: 'agent', detail: 'Explore' }],
        },
      ],
    },
    children: [{
      kind: 'subagent', agent_type: 'Explore', description: 'map the code', cost_usd: 0.2,
      steps: [
        {
          duration_ms: 500, cost_usd: 0.05,
          tools: [{ name: 'Edit', kind: 'edit', detail: 'graph.go', input: 'x', output: 'y', status: 'ok' }],
        },
      ],
    }],
  }],
};

test('specActive is false for an empty spec and true for any single active field', () => {
  assert.equal(specActive({}), false);
  assert.equal(specActive({ text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: 0, agentType: '' }), false);
  assert.equal(specActive({ text: 'x' }), true);
  assert.equal(specActive({ kinds: ['exec'] }), true);
  assert.equal(specActive({ errorsOnly: true }), true);
  assert.equal(specActive({ minDurationMs: 1 }), true);
  assert.equal(specActive({ minCostUSD: 0.01 }), true);
  assert.equal(specActive({ agentType: 'Explore' }), true);
});

test('filterTrace returns an empty Set for an inactive spec or a null graph', () => {
  assert.deepEqual(filterTrace(FILTER_GRAPH, {}), new Set());
  assert.deepEqual(filterTrace(FILTER_GRAPH, { text: '   ', kinds: [] }), new Set());
  assert.deepEqual(filterTrace(null, { errorsOnly: true }), new Set());
  assert.deepEqual(filterTrace({ sessions: [] }, { errorsOnly: true }), new Set());
});

test('filterTrace kinds:[exec] matches only the Bash tool and its ancestors', () => {
  const got = filterTrace(FILTER_GRAPH, { kinds: ['exec'] });
  assert.deepEqual(got, new Set(['s1#0.0:1', 's1#0.0', 's1#0']));
});

test('filterTrace errorsOnly matches only the errored Bash tool and its ancestors', () => {
  const got = filterTrace(FILTER_GRAPH, { errorsOnly: true });
  assert.deepEqual(got, new Set(['s1#0.0:1', 's1#0.0', 's1#0']));
});

test('filterTrace text matches a tool by its own input only', () => {
  // 'go test' lives only in Bash.input — not in Read, not in step thinking.
  const got = filterTrace(FILTER_GRAPH, { text: 'go test' });
  assert.deepEqual(got, new Set(['s1#0.0:1', 's1#0.0', 's1#0']));
});

test('filterTrace text matches every tool in a step whose thinking contains it', () => {
  // 'payload.go' is in Read directly AND in step0.thinking, so both step-0
  // tools match (Bash via the shared step context); step1/child stay out.
  const got = filterTrace(FILTER_GRAPH, { text: 'payload.go' });
  assert.deepEqual(got, new Set(['s1#0.0:0', 's1#0.0:1', 's1#0.0', 's1#0']));
});

test('filterTrace agentType selects only the matching sub-agent subtree', () => {
  const got = filterTrace(FILTER_GRAPH, { agentType: 'Explore' });
  assert.deepEqual(got, new Set(['s1#1.0:0', 's1#1.0', 's1#1']));
});

test('filterTrace minDurationMs surfaces only the slow step subtree', () => {
  const got = filterTrace(FILTER_GRAPH, { minDurationMs: 5000 });
  assert.deepEqual(got, new Set(['s1#0.1:0', 's1#0.1', 's1#0']));
});

test('filterTrace minCostUSD surfaces the expensive step subtree', () => {
  const got = filterTrace(FILTER_GRAPH, { minCostUSD: 0.25 });
  assert.deepEqual(got, new Set(['s1#0.1:0', 's1#0.1', 's1#0']));
});

test('filterTrace minCostUSD falls back to an agent ref when only cumulative agent cost qualifies', () => {
  // No single step costs ≥0.4; main's cumulative 0.5 surfaces the agent ref
  // alone (no step/tool), child's 0.2 stays out.
  const got = filterTrace(FILTER_GRAPH, { minCostUSD: 0.4 });
  assert.deepEqual(got, new Set(['s1#0']));
});

test('filterTrace ANDs dimensions: kinds+errorsOnly intersect, disjoint pair yields empty', () => {
  assert.deepEqual(
    filterTrace(FILTER_GRAPH, { kinds: ['exec'], errorsOnly: true }),
    new Set(['s1#0.0:1', 's1#0.0', 's1#0']),
  );
  // Read is the only 'read' tool but it's status ok → no tool satisfies both.
  assert.deepEqual(filterTrace(FILTER_GRAPH, { kinds: ['read'], errorsOnly: true }), new Set());
});

test('filterTrace includes the step and agent ref for every matched tool', () => {
  const got = filterTrace(FILTER_GRAPH, { kinds: ['read', 'exec', 'agent', 'edit'] });
  for (const key of got) {
    const m = /^(.*#\d+)\.(\d+):(\d+)$/.exec(key);
    if (!m) continue; // only assert the invariant for tool-level refs
    const stepRef = `${m[1]}.${m[2]}`;
    const agentRef = m[1];
    assert.ok(got.has(stepRef), `missing step ref ${stepRef} for tool ${key}`);
    assert.ok(got.has(agentRef), `missing agent ref ${agentRef} for tool ${key}`);
  }
});

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

// ── conversationSegments ────────────────────────────────────────────
test('conversationSegments splits main steps into one segment per prompt marker', () => {
  // Two prompts: A produced steps 0 and 1, B produced step 2. Each segment
  // carries its marker fields and exactly the steps it owns, sliced by the
  // markers' first_step_index boundaries.
  const session = {
    main: {
      steps: [
        { timestamp: 't0', model: 'm' },
        { timestamp: 't1', model: 'm' },
        { timestamp: 't2', model: 'm' },
      ],
    },
    prompts: [
      { uuid: 'uA', text: 'first', timestamp: 'ta', first_step_index: 0 },
      { uuid: 'uB', text: 'second', timestamp: 'tb', first_step_index: 2 },
    ],
  };
  const segs = conversationSegments(session);
  assert.equal(segs.length, 2);
  assert.equal(segs[0].uuid, 'uA');
  assert.equal(segs[0].text, 'first');
  assert.equal(segs[0].firstStepIndex, 0);
  assert.deepEqual(segs[0].steps.map(s => s.timestamp), ['t0', 't1']);
  assert.equal(segs[1].uuid, 'uB');
  assert.equal(segs[1].firstStepIndex, 2);
  assert.deepEqual(segs[1].steps.map(s => s.timestamp), ['t2']);
});

test('conversationSegments returns [] when the main agent has no steps', () => {
  assert.deepEqual(conversationSegments(null), []);
  assert.deepEqual(conversationSegments({}), []);
  assert.deepEqual(conversationSegments({ main: { steps: [] }, prompts: [] }), []);
});

test('conversationSegments degrades to one prompt-less segment when markers are missing', () => {
  // A session with turns but no prompt markers (older payload / unresolved
  // chain) still renders every step — under a single empty-uuid segment.
  const session = { main: { steps: [{ timestamp: 't0' }, { timestamp: 't1' }] } };
  const segs = conversationSegments(session);
  assert.equal(segs.length, 1);
  assert.equal(segs[0].uuid, '');
  assert.equal(segs[0].firstStepIndex, 0);
  assert.deepEqual(segs[0].steps.map(s => s.timestamp), ['t0', 't1']);
});

// ── conversationReplies ─────────────────────────────────────────────
test('conversationReplies keeps only the steps that spoke, with absolute indices', () => {
  // A segment of three steps: step 0 is tool-only (no text), step 1 replies,
  // step 2 is whitespace-only (still silent). Only step 1 becomes a reply, and
  // its stepIndex is absolute (firstStepIndex + local k) so the bubble keeps
  // the shared refKey.
  const seg = {
    firstStepIndex: 4,
    steps: [
      { timestamp: 't0', text: '', model: 'm', cost_usd: 0.01 },
      { timestamp: 't1', text: 'Done — fixed it.', model: 'claude', cost_usd: 0.02 },
      { timestamp: 't2', text: '   ', model: 'm', cost_usd: 0.03 },
    ],
  };
  const replies = conversationReplies(seg);
  assert.equal(replies.length, 1);
  assert.equal(replies[0].stepIndex, 5);
  assert.equal(replies[0].text, 'Done — fixed it.');
  assert.equal(replies[0].timestamp, 't1');
  assert.equal(replies[0].model, 'claude');
  assert.equal(replies[0].cost_usd, 0.02);
});

test('conversationReplies returns [] for an empty or missing segment', () => {
  assert.deepEqual(conversationReplies(null), []);
  assert.deepEqual(conversationReplies({}), []);
  assert.deepEqual(conversationReplies({ firstStepIndex: 0, steps: [] }), []);
  assert.deepEqual(
    conversationReplies({ firstStepIndex: 0, steps: [{ text: '' }, { text: '  ' }] }),
    [],
  );
});

// ── conversationSessionList ─────────────────────────────────────────
test('conversationSessionList maps the per-session summary fields', () => {
  const session = {
    session_id: 'sid-1',
    cwd: '/home/me/proj',
    prompts: [{ uuid: 'u1', text: 'hi', first_step_index: 0 }],
    main: {
      steps: [
        { text: 'reply one' },
        { text: 'reply two' },
      ],
    },
  };
  const out = conversationSessionList([session]);
  assert.equal(out.length, 1);
  assert.deepEqual(out[0], {
    sessionId: 'sid-1',
    cwd: '/home/me/proj',
    index: 0,
    promptCount: 1,
    replyCount: 2,
  });
});

test('conversationSessionList excludes null and main-less sessions', () => {
  const withMain = {
    session_id: 'sid-ok',
    cwd: '/x',
    prompts: [{ uuid: 'u1', text: 'hi', first_step_index: 0 }],
    main: { steps: [{ text: 'a' }] },
  };
  const mainless = { session_id: 'sid-no', cwd: '/y', prompts: [], main: null };
  const out = conversationSessionList([null, mainless, withMain, undefined]);
  assert.equal(out.length, 1);
  assert.equal(out[0].sessionId, 'sid-ok');
});

test('conversationSessionList index reflects the ORIGINAL input position', () => {
  const second = {
    session_id: 'sid-2',
    cwd: '/x',
    prompts: [{ uuid: 'u1', first_step_index: 0 }],
    main: { steps: [{ text: 'a' }] },
  };
  // input[0] is null and filtered out, but the surviving session keeps index 1.
  const out = conversationSessionList([null, second]);
  assert.equal(out.length, 1);
  assert.equal(out[0].index, 1);
});

test('conversationSessionList promptCount counts only uuid-bearing segments', () => {
  // Two markers: one with a uuid (real prompt), one without (orphan). The
  // second marker carves a segment with uuid '' which must NOT be counted.
  const session = {
    session_id: 'sid',
    cwd: '/x',
    prompts: [
      { uuid: 'u1', text: 'first', first_step_index: 0 },
      { uuid: '', text: '', first_step_index: 1 },
    ],
    main: { steps: [{ text: 'r0' }, { text: 'r1' }] },
  };
  const out = conversationSessionList([session]);
  assert.equal(out[0].promptCount, 1);
});

test('conversationSessionList replyCount sums replies across all segments', () => {
  // Two prompts → two segments. Segment 0 has 2 spoken replies, segment 1 has 1
  // (a tool-only step contributes none). Total replyCount = 3.
  const session = {
    session_id: 'sid',
    cwd: '/x',
    prompts: [
      { uuid: 'u1', first_step_index: 0 },
      { uuid: 'u2', first_step_index: 2 },
    ],
    main: {
      steps: [
        { text: 'a' },     // seg0 reply
        { text: 'b' },     // seg0 reply
        { text: '' },      // seg1 tool-only, no reply
        { text: 'c' },     // seg1 reply
      ],
    },
  };
  const out = conversationSessionList([session]);
  assert.equal(out[0].replyCount, 3);
  assert.equal(out[0].promptCount, 2);
});

test('conversationSessionList returns [] for empty, null, or undefined input', () => {
  assert.deepEqual(conversationSessionList([]), []);
  assert.deepEqual(conversationSessionList(null), []);
  assert.deepEqual(conversationSessionList(undefined), []);
});

// ── clampConvSidebarWidth ───────────────────────────────────────────
test('clampConvSidebarWidth passes an in-range value through, rounded', () => {
  assert.equal(clampConvSidebarWidth(300), 300);
});

test('clampConvSidebarWidth clamps a value below MIN to 160', () => {
  assert.equal(clampConvSidebarWidth(50), 160);
});

test('clampConvSidebarWidth clamps a value above MAX to 560', () => {
  assert.equal(clampConvSidebarWidth(9999), 560);
});

test('clampConvSidebarWidth returns DEFAULT 240 for non-finite input', () => {
  assert.equal(clampConvSidebarWidth(NaN), 240);
  assert.equal(clampConvSidebarWidth(undefined), 240);
  assert.equal(clampConvSidebarWidth(null), 240);
  assert.equal(clampConvSidebarWidth(Infinity), 240);
  assert.equal(clampConvSidebarWidth('wide'), 240);
});

test('clampConvSidebarWidth rounds a fractional in-range value', () => {
  assert.equal(clampConvSidebarWidth(300.6), 301);
  assert.equal(clampConvSidebarWidth(300.4), 300);
});

// ── clampTreeWidth ──────────────────────────────────────────────────
// The Tree lens is a fixed-width left rail (the detail pane takes the rest);
// dragging the handle resizes that rail, clamped by clampTreeWidth.
test('clampTreeWidth passes an in-range value through, rounded', () => {
  assert.equal(clampTreeWidth(400), 400);
});

test('clampTreeWidth clamps a value below MIN to 220', () => {
  assert.equal(clampTreeWidth(100), 220);
});

test('clampTreeWidth clamps a value above MAX to 680', () => {
  assert.equal(clampTreeWidth(9999), 680);
});

test('clampTreeWidth returns DEFAULT 320 for non-finite input', () => {
  assert.equal(clampTreeWidth(NaN), 320);
  assert.equal(clampTreeWidth(undefined), 320);
  assert.equal(clampTreeWidth(null), 320);
  assert.equal(clampTreeWidth(Infinity), 320);
  assert.equal(clampTreeWidth('wide'), 320);
});

test('clampTreeWidth rounds a fractional in-range value', () => {
  assert.equal(clampTreeWidth(400.6), 401);
  assert.equal(clampTreeWidth(400.4), 400);
});

// ── clampDrawerWidth ────────────────────────────────────────────────
// On the Feed/Timeline lenses the detail drawer is the fixed-width RIGHT
// column (the lens flexes); dragging the handle resizes that drawer,
// clamped by clampDrawerWidth. Same contract as the other width clamps.
test('clampDrawerWidth passes an in-range value through, rounded', () => {
  assert.equal(clampDrawerWidth(420), 420);
});

test('clampDrawerWidth clamps a value below MIN to 280', () => {
  assert.equal(clampDrawerWidth(100), 280);
});

test('clampDrawerWidth clamps a value above MAX to 640', () => {
  assert.equal(clampDrawerWidth(9999), 640);
});

test('clampDrawerWidth returns DEFAULT 360 for non-finite input', () => {
  assert.equal(clampDrawerWidth(NaN), 360);
  assert.equal(clampDrawerWidth(undefined), 360);
  assert.equal(clampDrawerWidth(null), 360);
  assert.equal(clampDrawerWidth(Infinity), 360);
  assert.equal(clampDrawerWidth('wide'), 360);
});

test('clampDrawerWidth rounds a fractional in-range value', () => {
  assert.equal(clampDrawerWidth(400.6), 401);
  assert.equal(clampDrawerWidth(400.4), 400);
});

// ── orderTreeSessions ───────────────────────────────────────────────
// The tree lists sessions newest-first, but while a user is reading a live
// session we freeze the display order so a live tick can't reshuffle rows
// under them. orderTreeSessions reorders the natural (newest-first) list to
// match a frozen id order; sessions absent from the frozen list (newly
// arrived) tack on at the end in their natural order.
const sess = id => ({ session_id: id });

test('orderTreeSessions returns the list unchanged when frozenOrderIds is null', () => {
  const list = [sess('a'), sess('b'), sess('c')];
  assert.equal(orderTreeSessions(list, null), list);
});

test('orderTreeSessions with an empty frozen list keeps natural order', () => {
  const list = [sess('a'), sess('b'), sess('c')];
  assert.deepEqual(orderTreeSessions(list, []).map(s => s.session_id), ['a', 'b', 'c']);
});

test('orderTreeSessions reorders an all-known list to the frozen order', () => {
  // Natural order is c,b,a (newest-first churned) but the user froze a,b,c.
  const list = [sess('c'), sess('b'), sess('a')];
  assert.deepEqual(orderTreeSessions(list, ['a', 'b', 'c']).map(s => s.session_id), ['a', 'b', 'c']);
});

test('orderTreeSessions appends sessions new since the freeze, in natural order', () => {
  // Frozen knew a,b; natural now leads with two newcomers d,c then a,b.
  const list = [sess('d'), sess('c'), sess('a'), sess('b')];
  assert.deepEqual(
    orderTreeSessions(list, ['a', 'b']).map(s => s.session_id),
    ['a', 'b', 'd', 'c'],
  );
});

test('orderTreeSessions skips a frozen id whose session has since vanished', () => {
  // Frozen referenced b, but b is gone from the natural list.
  const list = [sess('c'), sess('a')];
  assert.deepEqual(orderTreeSessions(list, ['a', 'b', 'c']).map(s => s.session_id), ['a', 'c']);
});

// ── treeFollowMode ──────────────────────────────────────────────────
// Decides whether the tree follows the live newest-first order ('follow') or
// holds the frozen order ('frozen'). It follows when the user is at the top
// or has been idle for >= idleMs; otherwise it stays frozen so reading isn't
// interrupted.
test('treeFollowMode follows while the user is scrolled to the top', () => {
  // atTop wins even if the user just scrolled.
  assert.equal(treeFollowMode(1000, 1100, true), 'follow');
});

test('treeFollowMode freezes while a recent scroll keeps the user active', () => {
  assert.equal(treeFollowMode(1000, 5000, false), 'frozen'); // 4s < 15s idle
});

test('treeFollowMode follows again once the idle window elapses', () => {
  assert.equal(treeFollowMode(1000, 20000, false), 'follow'); // 19s >= 15s
});

test('treeFollowMode follows exactly at the idle boundary', () => {
  assert.equal(treeFollowMode(1000, 16000, false), 'follow'); // 15000 >= 15000
});

test('treeFollowMode follows when the user has never scrolled', () => {
  assert.equal(treeFollowMode(null, 99999, false), 'follow');
});

test('treeFollowMode honours a custom idle window', () => {
  assert.equal(treeFollowMode(1000, 4000, false, 5000), 'frozen'); // 3s < 5s
  assert.equal(treeFollowMode(1000, 6000, false, 5000), 'follow'); // 5s >= 5s
});
