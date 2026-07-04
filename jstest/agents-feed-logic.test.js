// Tests for the Feed-lens derivations (agents-feed-logic.js), carved out
// of agents-logic.test.js. Imports stay on the agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  agentLabel,
  buildEventFeed,
  buildLiveFeed,
  currentToolKind,
} from '../web/agents-logic.js';

// ── buildEventFeed ──────────────────────────────────────────────────
const FEED_GRAPH = {
  sessions: [{
    session_id: 's1',
    entrypoint: 'sdk-cli',
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

test('buildEventFeed stamps every event with its session entrypoint', () => {
  const feed = buildEventFeed(FEED_GRAPH);
  // Lifted from the session so the Feed row can mark headless (SDK) origin
  // without a separate lookup — every kind (tool/spawn/done) carries it.
  assert.ok(feed.length > 0);
  for (const e of feed) assert.equal(e.entrypoint, 'sdk-cli');
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

// ── buildLiveFeed ───────────────────────────────────────────────────
// Two running agents (different elapsed) plus a finished one. NOW is past
// every start so elapsed is deterministic for the sort assertion.
const LIVE_NOW = Date.parse('2026-05-01T12:01:00Z');
const LIVE_GRAPH = {
  sessions: [{
    session_id: 's1', cwd: '/home/u/proj',
    main: {
      kind: 'main', status: 'running', cost_usd: 0.5,
      started_at: '2026-05-01T12:00:00Z', // running 60s — longest
      steps: [
        { timestamp: '2026-05-01T12:00:10Z', tools: [{ name: 'Bash', kind: 'exec' }] },
        { timestamp: '2026-05-01T12:00:40Z', tools: [{ name: 'Read', kind: 'read' }], current_tool: 'Read' },
      ],
      current_tool: 'Read',
    },
    children: [
      {
        kind: 'subagent', agent_type: 'Explore', description: 'map the code',
        status: 'running', cost_usd: 0.2,
        started_at: '2026-05-01T12:00:30Z', // running 30s — shorter
        steps: [{ timestamp: '2026-05-01T12:00:35Z', tools: [{ name: 'Grep', kind: 'read' }] }],
        current_tool: 'Grep',
      },
      {
        kind: 'subagent', agent_type: 'general-purpose', status: 'done',
        started_at: '2026-05-01T12:00:05Z', ended_at: '2026-05-01T12:00:20Z',
        steps: [{ timestamp: '2026-05-01T12:00:10Z', tools: [{ name: 'Edit', kind: 'edit' }] }],
      },
    ],
  }],
};

test('buildLiveFeed returns only running agents, longest-running first', () => {
  const live = buildLiveFeed(LIVE_GRAPH, LIVE_NOW);
  assert.equal(live.length, 2); // the done general-purpose child is excluded
  assert.equal(live[0].agentLabel, 'main');       // 60s elapsed
  assert.equal(live[1].agentLabel, 'Explore');    // 30s elapsed
  assert.ok(live[0].elapsedMs >= live[1].elapsedMs, 'not sorted by elapsed desc');
  assert.equal(live[0].elapsedMs, 60000);
  assert.equal(live[1].elapsedMs, 30000);
});

test('buildLiveFeed descriptors carry the fields a live row renders', () => {
  const live = buildLiveFeed(LIVE_GRAPH, LIVE_NOW);
  const main = live[0];
  assert.equal(main.sessionId, 's1');
  assert.equal(main.cwd, '/home/u/proj');
  assert.equal(main.agentIndex, 0);            // flatten index: main first
  assert.equal(main.kind, 'main');
  assert.equal(main.currentTool, 'Read');
  assert.equal(main.currentToolKind, 'read');  // kind of the last tool in the last tool-bearing step
  assert.equal(main.cost_usd, 0.5);
  assert.equal(main.steps, 2);
  assert.equal(main.startedAt, Date.parse('2026-05-01T12:00:00Z'));
  assert.equal(main.status, 'running');

  const explore = live[1];
  assert.equal(explore.agentIndex, 1);
  assert.equal(explore.description, 'map the code');
  assert.equal(explore.currentTool, 'Grep');
});

test('buildLiveFeed tolerates an empty or null graph', () => {
  assert.deepEqual(buildLiveFeed({ sessions: [] }, LIVE_NOW), []);
  assert.deepEqual(buildLiveFeed(null, LIVE_NOW), []);
});
