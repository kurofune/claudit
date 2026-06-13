import { test } from 'node:test';
import assert from 'node:assert/strict';

import { tooltipHTML, placeTooltip } from '../web/tooltip.js';

// ── tooltipHTML: escape + backtick→<code> markdown ──────────────────
test('tooltipHTML passes plain text through unchanged', () => {
  assert.equal(tooltipHTML('hello world'), 'hello world');
});

test('tooltipHTML escapes &, <, >', () => {
  assert.equal(tooltipHTML('a & b < c > d'), 'a &amp; b &lt; c &gt; d');
});

test('tooltipHTML turns `backticks` into <code>', () => {
  assert.equal(tooltipHTML('run `make install`'), 'run <code>make install</code>');
});

test('tooltipHTML escapes before code substitution (no HTML injection)', () => {
  // The escaping runs first, so a literal <script> can never reach the DOM.
  assert.equal(
    tooltipHTML('<script>alert(1)</script>'),
    '&lt;script&gt;alert(1)&lt;/script&gt;',
  );
});

test('tooltipHTML handles multiple code spans', () => {
  assert.equal(tooltipHTML('`a` and `b`'), '<code>a</code> and <code>b</code>');
});

// ── placeTooltip: above/below flip + horizontal clamp + arrow offset ─
const tip = { width: 100, height: 40 };

test('placeTooltip defaults above the target when there is room', () => {
  // target near the middle of the viewport with plenty of headroom
  const target = { top: 300, bottom: 320, left: 400, width: 20 };
  const p = placeTooltip(target, tip, 1000);
  assert.equal(p.side, 'top');
  assert.equal(p.top, 300 - 40 - 10); // r.top - tipH - margin
});

test('placeTooltip flips below when there is no room above', () => {
  // target hugging the top of the viewport: top - tipH - margin < 6
  const target = { top: 5, bottom: 25, left: 400, width: 20 };
  const p = placeTooltip(target, tip, 1000);
  assert.equal(p.side, 'bottom');
  assert.equal(p.top, 25 + 10); // r.bottom + margin
});

test('placeTooltip centers horizontally on the target', () => {
  const target = { top: 300, bottom: 320, left: 400, width: 20 };
  const p = placeTooltip(target, tip, 1000);
  // center = 410; left = 410 - 50 = 360; arrow points back to center
  assert.equal(p.left, 360);
  assert.equal(p.arrowX, 50);
});

test('placeTooltip clamps to the left viewport edge', () => {
  const target = { top: 300, bottom: 320, left: 0, width: 10 };
  const p = placeTooltip(target, tip, 1000);
  assert.equal(p.left, 6); // clamped to min margin
  // arrow still points at the true target center (5) relative to clamped left
  assert.equal(p.arrowX, 5 - 6);
});

test('placeTooltip clamps to the right viewport edge', () => {
  const target = { top: 300, bottom: 320, left: 990, width: 10 };
  const p = placeTooltip(target, tip, 1000);
  // max left = viewportWidth - tipW - 6 = 1000 - 100 - 6 = 894
  assert.equal(p.left, 894);
  assert.equal(p.arrowX, 995 - 894); // center 995 minus clamped left
});

test('placeTooltip rounds all numeric outputs', () => {
  const target = { top: 300.4, bottom: 320, left: 401, width: 21 };
  const p = placeTooltip(target, tip, 1000);
  assert.ok(Number.isInteger(p.top));
  assert.ok(Number.isInteger(p.left));
  assert.ok(Number.isInteger(p.arrowX));
});
