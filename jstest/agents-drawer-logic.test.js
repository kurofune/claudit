// Tests for ref resolution + the drawer payload (agents-drawer-logic.js
// and resolveRef), carved out of agents-logic.test.js. Imports stay on the
// agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  agentLabel,
  buildDrawerPayload,
  looksTruncated,
  refKey,
  resolveRef,
} from '../web/agents-logic.js';

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
          uuid: 'turn-uuid-0',
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

test('buildDrawerPayload: a step payload carries the turn uuid so the drawer can load full reasoning', () => {
  const p = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 });
  assert.equal(p.turnUuid, 'turn-uuid-0');
});

test('buildDrawerPayload: a tool payload inherits its parent step\'s turn uuid', () => {
  const p = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 });
  assert.equal(p.turnUuid, 'turn-uuid-0');
});

test('buildDrawerPayload: turnUuid is empty for an agent ref and for a step without a uuid', () => {
  const agent = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0 });
  assert.equal(agent.turnUuid, '');
  // Step 1 in the fixture carries no uuid (older payloads omit it).
  const step1 = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 1 });
  assert.equal(step1.turnUuid, '');
});

test('buildDrawerPayload: fullByTurn substitutes thinking and text on a step ref', () => {
  const p = buildDrawerPayload(
    DRAWER_GRAPH,
    { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 },
    null,
    { 'turn-uuid-0': { thinking: 'FULL THINKING...', text: 'FULL TEXT...' } },
  );
  assert.equal(p.thinking, 'FULL THINKING...');
  assert.equal(p.text, 'FULL TEXT...');
});

test('buildDrawerPayload: fullByTurn substitutes on a tool ref sharing the uuid; a partial entry keeps the other snippet', () => {
  const p = buildDrawerPayload(
    DRAWER_GRAPH,
    { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 },
    null,
    { 'turn-uuid-0': { thinking: 'FULL THINKING...' } },
  );
  assert.equal(p.thinking, 'FULL THINKING...');
  assert.equal(p.text, 'Listing files'); // no text override → snippet stays
});

test('buildDrawerPayload: fullByTurn with no entry for this turn keeps the snippets, and agent refs stay empty', () => {
  const unrelated = { 'some-other-turn': { thinking: 'UNRELATED', text: 'UNRELATED' } };
  const step = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0 }, null, unrelated);
  assert.equal(step.thinking, 'plan the work');
  assert.equal(step.text, 'Listing files');
  // An agent ref has no turn — fullByTurn can never attach to it.
  const agent = buildDrawerPayload(DRAWER_GRAPH, { sessionId: 'sess-uuid-1', agentIndex: 0 }, null,
    { 'turn-uuid-0': { thinking: 'FULL THINKING...', text: 'FULL TEXT...' } });
  assert.equal(agent.thinking, '');
  assert.equal(agent.text, '');
});

test('buildDrawerPayload: fullByTool and fullByTurn compose on a tool ref without interfering', () => {
  const p = buildDrawerPayload(
    DRAWER_GRAPH,
    { sessionId: 'sess-uuid-1', agentIndex: 0, stepIndex: 0, toolIndex: 0 },
    { 'toolu_x': { output: 'FULL OUTPUT...' } },
    { 'turn-uuid-0': { text: 'FULL TEXT...' } },
  );
  assert.equal(p.output, 'FULL OUTPUT...'); // fullByTool still applies
  assert.equal(p.input, 'ls -la');
  assert.equal(p.text, 'FULL TEXT...'); // fullByTurn applies alongside
  assert.equal(p.thinking, 'plan the work');
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
