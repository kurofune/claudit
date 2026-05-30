import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  classifyEntrypoint,
  splitSessionsRoute,
  filterSessionsByTab,
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
