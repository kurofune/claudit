// Tests for the graph-model helpers (agents-model.js) the session header
// reads its one-session graph with.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { agentLabel, flattenSession, parseRefKey, parseTime } from '../web/agents-model.js';

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

// ── agentLabel ──────────────────────────────────────────────────────
test('agentLabel is "main" for the main agent, else the agent_type', () => {
  assert.equal(agentLabel({ kind: 'main' }), 'main');
  assert.equal(agentLabel({ kind: 'subagent', agent_type: 'Explore' }), 'Explore');
  assert.equal(agentLabel({ kind: 'subagent' }), 'subagent');
  assert.equal(agentLabel(null), '');
});

// ── parseRefKey ─────────────────────────────────────────────────────
test('parseRefKey reads agent, step and tool refs', () => {
  assert.deepEqual(parseRefKey('s1#0'), {
    sessionId: 's1', agentIndex: 0, stepIndex: null, toolIndex: null, type: 'agent',
  });
  assert.deepEqual(parseRefKey('s1#2.3'), {
    sessionId: 's1', agentIndex: 2, stepIndex: 3, toolIndex: null, type: 'step',
  });
  assert.deepEqual(parseRefKey('s1#2.3:1'), {
    sessionId: 's1', agentIndex: 2, stepIndex: 3, toolIndex: 1, type: 'tool',
  });
});

test('parseRefKey returns null for empty / null / non-string / malformed', () => {
  assert.equal(parseRefKey(''), null);
  assert.equal(parseRefKey(null), null);
  assert.equal(parseRefKey(42), null);
  assert.equal(parseRefKey('no-hash'), null);
  assert.equal(parseRefKey('s1#x'), null); // non-numeric agent index
  assert.equal(parseRefKey('s1#0.y'), null); // non-numeric step index
});

test('parseRefKey splits a UUID sessionId with hyphens at its last #', () => {
  const sid = '0a1b2c3d-4e5f-6789-abcd-ef0123456789';
  assert.deepEqual(parseRefKey(`${sid}#1.2:0`), {
    sessionId: sid, agentIndex: 1, stepIndex: 2, toolIndex: 0, type: 'tool',
  });
});
