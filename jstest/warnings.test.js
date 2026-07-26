import { test } from 'node:test';
import assert from 'node:assert/strict';

import { warningsHTML } from '../web/view-overview.js';

test('warningsHTML is empty when nothing went unpriced', () => {
  assert.equal(warningsHTML(null), '');
  assert.equal(warningsHTML(undefined), '');
  assert.equal(warningsHTML([]), '');
});

test('warningsHTML quantifies each unpriced model', () => {
  const html = warningsHTML([
    { model: 'claude-future-9', turns: 1200, tokens: 45_600_000 },
  ]);
  assert.match(html, /claude-future-9/);
  // Turn count and token volume both have to be visible — the name alone
  // doesn't say whether the gap is worth acting on.
  assert.match(html, /1,200/);
  assert.match(html, /45\.6M|45,600,000/);
  assert.match(html, /prices\.yaml/);
});

test('warningsHTML lists every unpriced model', () => {
  const html = warningsHTML([
    { model: 'model-a', turns: 10, tokens: 5000 },
    { model: 'model-b', turns: 2, tokens: 100 },
  ]);
  assert.match(html, /model-a/);
  assert.match(html, /model-b/);
});

test('warningsHTML escapes model names', () => {
  const html = warningsHTML([
    { model: '<img src=x onerror=alert(1)>', turns: 1, tokens: 1 },
  ]);
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /&lt;img/);
});

test('warningsHTML says the totals are understated', () => {
  const html = warningsHTML([{ model: 'model-a', turns: 3, tokens: 900 }]);
  assert.match(html, /understated|\$0/i);
});
