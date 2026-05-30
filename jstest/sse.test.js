import { test } from 'node:test';
import assert from 'node:assert/strict';

import { decideReload, INTERACTION_IDLE_MS, TOAST_AFTER_MS } from '../web/sse.js';

// decideReload is the pure decision the SSE auto-reload loop runs on
// each tick: given the current state, should we reload now, defer
// (try again later), or fall back to the toast because we've been
// deferring too long?
//
// Inputs (all numeric times are Date.now()-style epoch ms):
//   pendingSince        — when the current new-data signal arrived;
//                         null means "nothing pending"
//   lastInteractionAt   — last mousemove/keydown/scroll timestamp;
//                         null means "user has not interacted yet"
//   anyDetailsOpen      — bool, true if any <details> is currently open
//   isHidden            — bool, document.hidden
//   now                 — current time
//
// Verb returned: 'reload' | 'defer' | 'toast'.

test('decideReload returns defer when nothing is pending', () => {
  assert.equal(decideReload({
    pendingSince: null, lastInteractionAt: null,
    anyDetailsOpen: false, isHidden: false, now: 1_000_000,
  }), 'defer');
});

test('decideReload returns reload when conditions are clear', () => {
  assert.equal(decideReload({
    pendingSince: 1_000_000,
    lastInteractionAt: null,
    anyDetailsOpen: false,
    isHidden: false,
    now: 1_000_500,
  }), 'reload');
});

test('decideReload defers while tab is hidden', () => {
  assert.equal(decideReload({
    pendingSince: 1_000_000,
    lastInteractionAt: null,
    anyDetailsOpen: false,
    isHidden: true,
    now: 1_000_500,
  }), 'defer');
});

test('decideReload defers while a <details> is open', () => {
  assert.equal(decideReload({
    pendingSince: 1_000_000,
    lastInteractionAt: null,
    anyDetailsOpen: true,
    isHidden: false,
    now: 1_000_500,
  }), 'defer');
});

test('decideReload defers while user activity is fresh', () => {
  assert.equal(decideReload({
    pendingSince: 1_000_000,
    lastInteractionAt: 1_000_500,
    anyDetailsOpen: false,
    isHidden: false,
    now: 1_000_500 + INTERACTION_IDLE_MS - 1,
  }), 'defer');
});

test('decideReload reloads once user activity goes stale', () => {
  assert.equal(decideReload({
    pendingSince: 1_000_000,
    lastInteractionAt: 1_000_500,
    anyDetailsOpen: false,
    isHidden: false,
    now: 1_000_500 + INTERACTION_IDLE_MS,
  }), 'reload');
});

test('decideReload escalates to toast after the pile-up window', () => {
  // Tab hidden the whole time so we'd otherwise defer forever; the
  // pile-up clock wins.
  assert.equal(decideReload({
    pendingSince: 1_000_000,
    lastInteractionAt: null,
    anyDetailsOpen: false,
    isHidden: true,
    now: 1_000_000 + TOAST_AFTER_MS,
  }), 'toast');
});

test('decideReload prefers toast over reload at the pile-up boundary', () => {
  // Defer conditions all clear, but we've been pending longer than the
  // pile-up window. The toast signals "we waited; manual reload now."
  assert.equal(decideReload({
    pendingSince: 1_000_000,
    lastInteractionAt: null,
    anyDetailsOpen: false,
    isHidden: false,
    now: 1_000_000 + TOAST_AFTER_MS,
  }), 'toast');
});
