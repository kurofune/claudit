import { test } from 'node:test';
import assert from 'node:assert/strict';
import { apiURL } from '../web/api.js';

test('apiURL appends the page query to a bare path', () => {
  assert.equal(apiURL('/cost', '?since=2026-01-01'), '/_claudit/api/cost?since=2026-01-01');
});
test('apiURL joins the page query with & when the path already has one', () => {
  assert.equal(apiURL('/trends?dim=model', '?since=2026-01-01&until=2026-01-02'),
    '/_claudit/api/trends?dim=model&since=2026-01-01&until=2026-01-02');
});
test('apiURL leaves the path alone without a page query', () => {
  assert.equal(apiURL('/trends?dim=model', ''), '/_claudit/api/trends?dim=model');
  assert.equal(apiURL('/cost', ''), '/_claudit/api/cost');
});
