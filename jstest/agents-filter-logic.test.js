// Tests for the trace filter (agents-filter-logic.js), carved out of
// agents-logic.test.js. Imports stay on the agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  filterTrace,
  specActive,
} from '../web/agents-logic.js';

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
