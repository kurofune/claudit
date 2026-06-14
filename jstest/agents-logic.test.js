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
  currentToolKind,
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
  toolSegments,
  fitSegmentLabel,
  costHeat,
  agentPhaseAt,
  playheadBounds,
  playheadStats,
  sessionStats,
  timelineAtTime,
  specActive,
  filterTrace,
  detectRetries,
  detectSignals,
  signalPipsByAgent,
  spawnTargetIndex,
  conversationSegments,
  conversationReplies,
  conversationSessionList,
  timelineSessionList,
  pickTimelineSid,
  clampConvSidebarWidth,
  clampTreeWidth,
  clampDrawerWidth,
  orderTreeSessions,
  treeFollowMode,
  zoomClampPxPerMs,
  zoomAnchorScrollLeft,
  segKindColor,
  pctOfAgent,
  segTooltip,
  timelineKinds,
  turnTimeBuckets,
  idleSegments,
  criticalSpans,
  toolMix,
  percentiles,
  durationHistogram,
  DURATION_EDGES,
  costPareto,
  errorRates,
  contextSeries,
  binSeries,
  groupBy,
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

// One session with a cwd, per-step tokens, and an agent-level token tuple so
// the feed can show project→thread context and a per-row token count.
const FEED_TOK_GRAPH = {
  sessions: [{
    session_id: 'sess-abc123def456',
    cwd: '/Users/x/Projects/claudit',
    main: {
      kind: 'main', status: 'done', cost_usd: 1.0,
      started_at: '2026-05-01T12:00:00Z', ended_at: '2026-05-01T12:00:05Z',
      tokens: { InputTokens: 100, OutputTokens: 50, CacheCreate5mTokens: 10, CacheCreate1hTokens: 5, CacheReadTokens: 200 },
      steps: [
        {
          timestamp: '2026-05-01T12:00:00Z', cost_usd: 0.02, duration_ms: 300,
          tokens: { InputTokens: 80, OutputTokens: 40, CacheCreate5mTokens: 10, CacheReadTokens: 120 },
          tools: [{ name: 'Bash', kind: 'exec', status: 'ok' }],
        },
      ],
    },
    children: [],
  }],
};

test('buildEventFeed carries the session cwd on every event so rows can show project→thread', () => {
  const feed = buildEventFeed(FEED_TOK_GRAPH);
  assert.ok(feed.length > 0);
  for (const e of feed) {
    assert.equal(e.cwd, '/Users/x/Projects/claudit', `cwd missing on ${e.kind} event`);
    assert.equal(e.sessionId, 'sess-abc123def456');
  }
});

test('buildEventFeed tool events carry the parent step token total', () => {
  const feed = buildEventFeed(FEED_TOK_GRAPH);
  const tool = feed.find(e => e.kind === 'tool' && e.tool === 'Bash');
  assert.ok(tool, 'expected a Bash tool event');
  // Step tuple: 80 + 40 + 10 + 120 = 250.
  assert.equal(tool.tokens, 250);
});

test('buildEventFeed done events carry the agent-level token total', () => {
  const feed = buildEventFeed(FEED_TOK_GRAPH);
  const done = feed.find(e => e.kind === 'done');
  assert.ok(done, 'expected a done event');
  // Agent tuple: 100 + 50 + (10 + 5) + 200 = 365.
  assert.equal(done.tokens, 365);
});

// ── currentToolKind ─────────────────────────────────────────────────
test('currentToolKind returns the kind of the last tool in the last step that has tools', () => {
  const agent = {
    steps: [
      { tools: [{ name: 'Bash', kind: 'exec' }] },
      { tools: [{ name: 'Read', kind: 'read' }, { name: 'Edit', kind: 'edit' }] },
    ],
  };
  assert.equal(currentToolKind(agent), 'edit');
});

test('currentToolKind walks back past trailing tool-less steps', () => {
  const agent = {
    steps: [
      { tools: [{ name: 'Bash', kind: 'exec' }] },
      { tools: [] },
    ],
  };
  assert.equal(currentToolKind(agent), 'exec');
});

test('currentToolKind is empty for a null/step-less agent or a missing kind', () => {
  assert.equal(currentToolKind(null), '');
  assert.equal(currentToolKind({}), '');
  assert.equal(currentToolKind({ steps: [{ tools: [{ name: 'X' }] }] }), '');
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
    { x: 100, w: 120, stepIndex: 0, refKey: 's1#0.0', status: '', cost_usd: 0.01, durationMs: 400, kind: 'step' },
    { x: 220, w: 180, stepIndex: 1, refKey: 's1#0.1', status: '', cost_usd: 0.02, durationMs: 600, kind: 'step' },
  ]);
});

test('buildTimeline rows expose per-tool sub-spans when tools are timed', () => {
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
  });
  assert.deepEqual(layout.rows[0].segments, [
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

test('criticalSpans tolerates a null / non-array argument', () => {
  assert.deepEqual(criticalSpans(null), { session: {}, agents: {} });
  assert.deepEqual(criticalSpans([]), { session: {}, agents: {} });
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
