// Custom tooltip popover — the single nice tooltip used everywhere.
//
// A single floating #claudit-tooltip element is repositioned to whichever
// [title]/[data-tooltip] the cursor (or focus) is over. We migrate
// `title` → `data-tooltip` on each element so the native browser tooltip
// stays suppressed (otherwise both render) while the text remains
// discoverable for our handler. Styles live in app.css (#claudit-tooltip).
//
// Ported from the static report's tooltips() IIFE
// (internal/render/diff.html.tmpl). The placement + text-transform math is
// extracted into the two pure exports below so it can be unit-tested; the
// DOM wiring in init() is browser-verified.

// Lightweight markdown for tooltip bodies: escape &<> first (so nothing can
// inject HTML), then turn `backticks` into <code>. Returns an HTML string.
export function tooltipHTML(text) {
  return String(text)
    .replace(/[&<>]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]))
    .replace(/`([^`]+)`/g, '<code>$1</code>');
}

// Pure placement math. Given the target's bounding rect, the tooltip's own
// size, and the viewport width, return {top, left, arrowX, side}. Defaults
// above the target; flips below when there's no headroom. Centers
// horizontally on the target, clamped to the viewport with a 6px margin;
// arrowX is the arrow's offset from the (clamped) left edge so it keeps
// pointing at the true target center.
export function placeTooltip(target, tip, viewportWidth, margin = 10) {
  let top = target.top - tip.height - margin;
  let side = 'top';
  if (top < 6) {
    top = target.bottom + margin;
    side = 'bottom';
  }
  let left = target.left + target.width / 2 - tip.width / 2;
  left = Math.max(6, Math.min(left, viewportWidth - tip.width - 6));
  const arrowX = (target.left + target.width / 2) - left;
  return {
    top: Math.round(top),
    left: Math.round(left),
    arrowX: Math.round(arrowX),
    side,
  };
}

// Move title="" → data-tooltip="" within `root` so the native browser
// tooltip is suppressed but the text remains for our handler. `root` may be
// an Element or Document; it also captures the root element itself if it
// carries a bare title.
function captureTitles(root) {
  if (root.nodeType === 1 && root.hasAttribute('title')) {
    if (!root.dataset.tooltip) root.dataset.tooltip = root.getAttribute('title');
    root.removeAttribute('title');
  }
  if (root.querySelectorAll) {
    root.querySelectorAll('[title]').forEach(el => {
      if (!el.dataset.tooltip) el.dataset.tooltip = el.getAttribute('title');
      el.removeAttribute('title');
    });
  }
}

export function init() {
  if (!window.matchMedia) return;
  // Skip on touch devices — there's no hover affordance and the popover
  // would interfere with view-switch taps in the sidebar.
  if (window.matchMedia('(hover: none)').matches) return;

  const tip = document.createElement('div');
  tip.id = 'claudit-tooltip';
  tip.setAttribute('role', 'tooltip');
  document.body.appendChild(tip);

  let showTimer = null;
  let hideTimer = null;
  let activeEl = null;

  captureTitles(document);

  // Re-capture titles on newly-added nodes. Unlike the static report (which
  // never mutates), the SPA's Agents/Timeline tabs re-render thousands of
  // nodes via SSE. Scope the scan to each batch's added subtrees instead of
  // re-scanning the whole document, so this stays O(added) not O(DOM).
  //
  // Also watch the `title` attribute: a few painters set `el.title = ...` on
  // an element that already existed at init (e.g. nav-metric pills, the
  // sessions cap notice, sparkline headers). Those are attribute mutations,
  // not childList additions, so without attributeFilter they'd slip through
  // and render the native browser tooltip. The filter keeps this rare and
  // cheap — it fires only when a title actually changes, and we re-capture
  // just that one node.
  const mo = new MutationObserver(records => {
    for (const rec of records) {
      if (rec.type === 'attributes') {
        if (rec.target.nodeType === 1) captureTitles(rec.target);
        continue;
      }
      for (const node of rec.addedNodes) {
        if (node.nodeType === 1) captureTitles(node);
      }
    }
  });
  mo.observe(document.body, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ['title'],
  });

  function position(el) {
    const r = el.getBoundingClientRect();
    const tipR = tip.getBoundingClientRect();
    const p = placeTooltip(r, tipR, window.innerWidth);
    tip.style.top = `${p.top}px`;
    tip.style.left = `${p.left}px`;
    tip.style.setProperty('--arrow-x', `${p.arrowX}px`);
    tip.dataset.side = p.side;
  }

  function show(el) {
    const text = el.dataset.tooltip;
    if (!text) return;
    activeEl = el;
    tip.innerHTML = tooltipHTML(text);
    tip.classList.add('is-visible');
    // Two-frame trick: render → measure → position → reveal.
    requestAnimationFrame(() => requestAnimationFrame(() => {
      if (activeEl === el) position(el);
    }));
  }
  function hide() {
    tip.classList.remove('is-visible');
    activeEl = null;
  }

  function onEnter(e) {
    const el = e.target.closest('[data-tooltip]');
    if (!el) return;
    clearTimeout(hideTimer);
    clearTimeout(showTimer);
    showTimer = setTimeout(() => show(el), 220);
  }
  function onLeave(e) {
    const el = e.target.closest('[data-tooltip]');
    if (!el) return;
    clearTimeout(showTimer);
    hideTimer = setTimeout(hide, 80);
  }
  document.addEventListener('mouseover', onEnter);
  document.addEventListener('mouseout', onLeave);
  document.addEventListener('focusin', e => {
    const el = e.target.closest('[data-tooltip]');
    if (el) show(el);
  });
  document.addEventListener('focusout', e => {
    const el = e.target.closest('[data-tooltip]');
    if (el) hide();
  });
  window.addEventListener('scroll', hide, true);
  window.addEventListener('resize', hide);
  window.addEventListener('keydown', e => { if (e.key === 'Escape') hide(); });
}
