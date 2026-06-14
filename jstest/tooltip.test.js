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

test('placeTooltip centers on an explicit anchorX (cursor) instead of the target center', () => {
  // A very wide target (e.g. a full-width timeline bar) whose geometric
  // center is far from the cursor: anchoring on the cursor keeps the
  // tooltip — and its arrow — over the spot actually being hovered.
  const target = { top: 300, bottom: 320, left: 100, width: 800 };
  const p = placeTooltip(target, tip, 1000, 10, 250);
  // anchor 250 → left = 250 - 50 = 200; arrow points back to 250.
  assert.equal(p.left, 200);
  assert.equal(p.arrowX, 50);
});

test('placeTooltip with a wide target but no anchorX still centers on the target (off-screen center clamps right)', () => {
  // Reproduces the bug shape: a bar wider than the viewport. Without an
  // anchor, the center (510) drives placement; with the right-edge clamp
  // the tooltip pins to the far right regardless of cursor — which is why
  // callers pass anchorX for wide targets.
  const target = { top: 300, bottom: 320, left: 10, width: 1000 };
  const p = placeTooltip(target, tip, 1000);
  assert.equal(p.left, 510 - 50); // center 510, no clamp needed here
});

test('placeTooltip clamps an anchorX past the right edge', () => {
  const target = { top: 300, bottom: 320, left: 100, width: 800 };
  const p = placeTooltip(target, tip, 1000, 10, 980);
  // max left = 1000 - 100 - 6 = 894; arrow = anchor 980 - 894 = 86.
  assert.equal(p.left, 894);
  assert.equal(p.arrowX, 86);
});

test('placeTooltip rounds all numeric outputs', () => {
  const target = { top: 300.4, bottom: 320, left: 401, width: 21 };
  const p = placeTooltip(target, tip, 1000);
  assert.ok(Number.isInteger(p.top));
  assert.ok(Number.isInteger(p.left));
  assert.ok(Number.isInteger(p.arrowX));
});
