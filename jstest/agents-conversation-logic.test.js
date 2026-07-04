// Tests for the Conversation-lens derivations
// (agents-conversation-logic.js), carved out of agents-logic.test.js.
// Imports stay on the agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  conversationReplies,
  conversationSegments,
  conversationSessionList,
} from '../web/agents-logic.js';

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
    entrypoint: 'sdk-cli',
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
    entrypoint: 'sdk-cli',
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
