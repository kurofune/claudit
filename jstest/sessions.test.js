import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  classifyEntrypoint,
  splitSessionsRoute,
  filterSessionsByTab,
  SESSIONS_PAGE_SIZE,
  pageCount,
  clampPage,
  paginate,
  pageForIndex,
} from '../web/sessions-logic.js';

test('classifyEntrypoint treats sdk-prefixed origins as sdk', () => {
  assert.equal(classifyEntrypoint('sdk-cli'), 'sdk');
  assert.equal(classifyEntrypoint('sdk'), 'sdk');
});

test('classifyEntrypoint treats cli and anything else as interactive', () => {
  assert.equal(classifyEntrypoint('cli'), 'interactive');
  assert.equal(classifyEntrypoint('vscode'), 'interactive');
});

test('classifyEntrypoint defaults missing/empty to interactive', () => {
  assert.equal(classifyEntrypoint(''), 'interactive');
  assert.equal(classifyEntrypoint(undefined), 'interactive');
  assert.equal(classifyEntrypoint(null), 'interactive');
});

test('splitSessionsRoute reads a known tab as the first segment', () => {
  assert.deepEqual(splitSessionsRoute('sdk'), { tab: 'sdk', anchor: '' });
  assert.deepEqual(splitSessionsRoute('interactive'), { tab: 'interactive', anchor: '' });
  assert.deepEqual(splitSessionsRoute('all'), { tab: 'all', anchor: '' });
});

test('splitSessionsRoute carries an anchor after a known tab', () => {
  assert.deepEqual(splitSessionsRoute('sdk/session-abc'), { tab: 'sdk', anchor: 'session-abc' });
});

test('splitSessionsRoute treats a bare session deep-link as the all tab (back-compat)', () => {
  // Legacy #sessions/session-{id} and #sessions/{id} must still open.
  assert.deepEqual(splitSessionsRoute('session-abc'), { tab: 'all', anchor: 'session-abc' });
  assert.deepEqual(splitSessionsRoute('abc-123'), { tab: 'all', anchor: 'abc-123' });
});

test('splitSessionsRoute defaults empty/missing sub to the all tab', () => {
  assert.deepEqual(splitSessionsRoute(''), { tab: 'all', anchor: '' });
  assert.deepEqual(splitSessionsRoute(undefined), { tab: 'all', anchor: '' });
});

test('filterSessionsByTab splits sessions by entrypoint', () => {
  const sessions = [
    { session_id: '1', entrypoint: 'cli' },
    { session_id: '2', entrypoint: 'sdk-cli' },
    { session_id: '3', entrypoint: '' },
    { session_id: '4', entrypoint: 'sdk-cli' },
  ];
  assert.deepEqual(filterSessionsByTab(sessions, 'all').map(s => s.session_id), ['1', '2', '3', '4']);
  assert.deepEqual(filterSessionsByTab(sessions, 'sdk').map(s => s.session_id), ['2', '4']);
  assert.deepEqual(filterSessionsByTab(sessions, 'interactive').map(s => s.session_id), ['1', '3']);
});

test('filterSessionsByTab is defensive: unknown tab returns all', () => {
  const sessions = [{ session_id: '1', entrypoint: 'cli' }];
  assert.deepEqual(filterSessionsByTab(sessions, 'bogus'), sessions);
});

test('SESSIONS_PAGE_SIZE is 10', () => {
  assert.equal(SESSIONS_PAGE_SIZE, 10);
});

test('pageCount divides items into pages, rounding up', () => {
  assert.equal(pageCount(0, 10), 0);
  assert.equal(pageCount(1, 10), 1);
  assert.equal(pageCount(10, 10), 1);
  assert.equal(pageCount(11, 10), 2);
  assert.equal(pageCount(25, 10), 3);
});

test('clampPage keeps the page within [1, pageCount]', () => {
  // 25 items, size 10 → 3 pages.
  assert.equal(clampPage(0, 25, 10), 1);
  assert.equal(clampPage(1, 25, 10), 1);
  assert.equal(clampPage(2, 25, 10), 2);
  assert.equal(clampPage(99, 25, 10), 3);
  // No items → clamp to page 1 (the empty state still lives on page 1).
  assert.equal(clampPage(5, 0, 10), 1);
});

test('paginate returns the clamped page slice', () => {
  const list = Array.from({ length: 25 }, (_, i) => i);
  assert.deepEqual(paginate(list, 1, 10), [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
  assert.deepEqual(paginate(list, 3, 10), [20, 21, 22, 23, 24]);
  // Out-of-range page clamps to the last page rather than returning empty.
  assert.deepEqual(paginate(list, 99, 10), [20, 21, 22, 23, 24]);
  // Under-range clamps to the first page.
  assert.deepEqual(paginate(list, 0, 10), [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
});

test('paginate on an empty list yields an empty page', () => {
  assert.deepEqual(paginate([], 1, 10), []);
});

test('pageForIndex maps a 0-based index to its 1-based page', () => {
  assert.equal(pageForIndex(0, 10), 1);
  assert.equal(pageForIndex(9, 10), 1);
  assert.equal(pageForIndex(10, 10), 2);
  assert.equal(pageForIndex(24, 10), 3);
  // Defensive: a not-found index (-1) maps to page 1.
  assert.equal(pageForIndex(-1, 10), 1);
});
