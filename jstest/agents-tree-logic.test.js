// Tests for the Tree-lens derivations (agents-tree-logic.js), carved out
// of agents-logic.test.js. Imports stay on the agents-logic.js facade.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  orderTreeSessions,
  treeFollowMode,
} from '../web/agents-logic.js';

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
