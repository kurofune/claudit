// Tests for the shared graph/model helpers (agents-model.js), carved out
// of agents-logic.test.js. Imports stay on the agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  agentElapsedMs,
  agentLabel,
  agentOutcome,
  agentSpan,
  agentTokens,
  baseName,
  clampConvSidebarWidth,
  clampDrawerWidth,
  clampTreeWidth,
  currentToolKind,
  deepestRefs,
  defaultRef,
  flattenSession,
  formatElapsed,
  graphStats,
  originClass,
  parseRefKey,
  parseTime,
  refKey,
  spawnTargetIndex,
} from '../web/agents-logic.js';

// agents-logic.js holds the pure, DOM-free math behind the Agents tab:
// lane packing for the swimlane, time→x scaling, bar geometry, and the
// status/elapsed derivations the cards show. view-agents.js is the
// swappable DOM layer on top — keeping the math here means a redesign
// never touches tested logic.

// ── originClass ─────────────────────────────────────────────────────
test('originClass classifies sdk-* entrypoints as sdk', () => {
  assert.equal(originClass('sdk-cli'), 'sdk');
  assert.equal(originClass('SDK-CLI'), 'sdk');
});

test('originClass classifies cli and unknown/empty as interactive', () => {
  assert.equal(originClass('cli'), 'interactive');
  assert.equal(originClass(''), 'interactive');
  assert.equal(originClass(undefined), 'interactive');
  assert.equal(originClass(null), 'interactive');
  assert.equal(originClass(42), 'interactive');
});

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

// ── agentOutcome (the ✓/✗ chip rule for finished agents) ───────────────
test('agentOutcome: a done agent with no errors is ok', () => {
  const a = { kind: 'subagent', status: 'done', error_count: 0, steps: [
    { timestamp: 0, tools: [{ name: 'Read', status: 'ok' }] },
  ] };
  assert.equal(agentOutcome(a), 'ok');
});

test('agentOutcome: error_count > 0 means error', () => {
  const a = { kind: 'subagent', status: 'done', error_count: 2, steps: [] };
  assert.equal(agentOutcome(a), 'error');
});

test('agentOutcome: a clean error_count but an errored final tool is still error', () => {
  // Belt-and-braces: the last step's LAST tool carries the run's terminal
  // status; if it errored the run ended failing even if the rollup missed it.
  const a = { kind: 'subagent', status: 'done', error_count: 0, steps: [
    { timestamp: 0, tools: [{ name: 'Read', status: 'ok' }] },
    { timestamp: 1, tools: [{ name: 'Bash', status: 'ok' }, { name: 'Bash', status: 'error' }] },
  ] };
  assert.equal(agentOutcome(a), 'error');
});

test('agentOutcome: a clean running agent is running (no chip yet)', () => {
  const a = { kind: 'subagent', status: 'running', error_count: 0, steps: [
    { timestamp: 0, tools: [{ name: 'Read', status: 'ok' }] },
  ] };
  assert.equal(agentOutcome(a), 'running');
});

test('agentOutcome: error beats running — a mid-run failure already shows', () => {
  const a = { kind: 'subagent', status: 'running', error_count: 1, steps: [] };
  assert.equal(agentOutcome(a), 'error');
});

test('agentOutcome: an errored spawning Agent call marks the run failed', () => {
  // The Agent tool_use on the PARENT side can fail even when the child's own
  // tools all passed (e.g. the sub-agent was aborted). The caller passes the
  // spawning ToolInvocation when it has one.
  const a = { kind: 'subagent', status: 'done', error_count: 0, steps: [
    { timestamp: 0, tools: [{ name: 'Read', status: 'ok' }] },
  ] };
  assert.equal(agentOutcome(a, { name: 'Agent', kind: 'agent', status: 'error' }), 'error');
  assert.equal(agentOutcome(a, { name: 'Agent', kind: 'agent', status: 'ok' }), 'ok');
  assert.equal(agentOutcome(a, null), 'ok');
});

test('agentOutcome: null / step-less done agents are ok', () => {
  assert.equal(agentOutcome(null), 'ok');
  assert.equal(agentOutcome({ kind: 'subagent', status: 'done', error_count: 0, steps: [] }), 'ok');
  // A step whose tools are null (the wire's nil slice) doesn't trip the walk.
  assert.equal(agentOutcome({ kind: 'subagent', status: 'done', error_count: 0, steps: [
    { timestamp: 0, tools: null },
  ] }), 'ok');
});
