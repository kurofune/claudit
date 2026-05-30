import { test } from 'node:test';
import assert from 'node:assert/strict';

import { bucketLabel } from '../web/format.js';

test('bucketLabel day shows MM-DD', () => {
  assert.equal(bucketLabel('2026-05-06T00:00:00Z', 'day'), '05-06');
});

test('bucketLabel week prefixes wk', () => {
  assert.equal(bucketLabel('2026-05-04T00:00:00Z', 'week'), 'wk 05-04');
});

test('bucketLabel month shows YYYY-MM', () => {
  assert.equal(bucketLabel('2026-05-01T00:00:00Z', 'month'), '2026-05');
});

test('bucketLabel hour shows a 12-hour am/pm clock', () => {
  // Hour buckets ship local-zoned RFC3339 (offset, not Z); the label
  // is the wall-clock hour in compact 12-hour form: 12a, 1a, …, 12p, …
  assert.equal(bucketLabel('2026-05-06T00:00:00-07:00', 'hour'), '12a');
  assert.equal(bucketLabel('2026-05-06T01:00:00-07:00', 'hour'), '1a');
  assert.equal(bucketLabel('2026-05-06T09:00:00-07:00', 'hour'), '9a');
  assert.equal(bucketLabel('2026-05-06T11:00:00-07:00', 'hour'), '11a');
  assert.equal(bucketLabel('2026-05-06T12:00:00-07:00', 'hour'), '12p');
  assert.equal(bucketLabel('2026-05-06T13:00:00Z', 'hour'), '1p');
  assert.equal(bucketLabel('2026-05-06T23:00:00-07:00', 'hour'), '11p');
});

test('bucketLabel empty timestamp is blank', () => {
  assert.equal(bucketLabel('', 'hour'), '');
});
