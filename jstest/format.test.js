import { test } from 'node:test';
import assert from 'node:assert/strict';

import { bucketLabel, fmtNum, fmtMoney } from '../web/format.js';

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

// fmtNum and fmtMoney read like toLocaleString, which they replace with one
// shared formatter: the Sessions list calls them per row, and a formatter
// built per call cost hundreds of milliseconds over the real corpus.
test('fmtNum groups digits as toLocaleString does', () => {
  for (const v of [0, 7, 999, 1000, 1234567, -4321, 12.5]) {
    assert.equal(fmtNum(v), Number(v).toLocaleString());
  }
  assert.equal(fmtNum('2048'), Number('2048').toLocaleString());
  assert.equal(fmtNum(null), '0');
  assert.equal(fmtNum(undefined), '0');
});

test('fmtMoney rounds whole dollars from 1000 and keeps cents below', () => {
  assert.equal(fmtMoney(1234.56), '$' + (1235).toLocaleString());
  assert.equal(fmtMoney(999.994), '$999.99');
  assert.equal(fmtMoney(0), '$0.00');
  assert.equal(fmtMoney(null), '—');
});

test('fmtNum and fmtMoney build no formatter per call (no toLocaleString)', () => {
  const real = Number.prototype.toLocaleString;
  Number.prototype.toLocaleString = () => { throw new Error('toLocaleString builds a formatter per call'); };
  try {
    assert.equal(fmtNum(1234567), real.call(1234567));
    assert.equal(fmtMoney(4321.2), '$' + real.call(4321));
  } finally {
    Number.prototype.toLocaleString = real;
  }
});
