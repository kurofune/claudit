import { test } from 'node:test';
import assert from 'node:assert/strict';

import { addDays, urlToRange } from '../web/date-picker.js';

test('addDays shifts forward within a month', () => {
  assert.equal(addDays('2026-05-20', 1), '2026-05-21');
});

test('addDays crosses month and year boundaries', () => {
  assert.equal(addDays('2026-05-31', 1), '2026-06-01');
  assert.equal(addDays('2026-03-01', -1), '2026-02-28');
  assert.equal(addDays('2026-12-31', 1), '2027-01-01');
});

test('urlToRange shifts since+until end to inclusive (until-1)', () => {
  assert.deepEqual(
    urlToRange('?since=2026-05-20&until=2026-05-28'),
    { start: '2026-05-20', end: '2026-05-27' },
  );
});

test('urlToRange works without a leading ?', () => {
  assert.deepEqual(
    urlToRange('since=2026-05-20&until=2026-05-28'),
    { start: '2026-05-20', end: '2026-05-27' },
  );
});

test('urlToRange with no params defaults to last 7 days ending at injected now', () => {
  // Local noon on May 27 2026 (month is 0-indexed).
  assert.deepEqual(
    urlToRange('', new Date(2026, 4, 27, 12, 0, 0)),
    { start: '2026-05-20', end: '2026-05-27' },
  );
});

test('urlToRange with since only leaves end empty', () => {
  assert.deepEqual(
    urlToRange('?since=2026-05-20'),
    { start: '2026-05-20', end: '' },
  );
});

test('urlToRange with until only leaves start empty and shifts end inclusive', () => {
  assert.deepEqual(
    urlToRange('?until=2026-05-28'),
    { start: '', end: '2026-05-27' },
  );
});
