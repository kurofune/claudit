// Skeleton loaders. Per-view shape-matched placeholder markup that
// paints while a view's data is in-flight. The factories return HTML
// strings so callers can drop them straight into innerHTML; classes
// hook into web/app.css's .skeleton + .skel-* rules.
//
// One module instead of inlining the markup per view keeps the shapes
// in one place — if the real layout changes (a new totals tile, a
// wider table), the skeleton tracks alongside it.

const skel = (variant) => `<span class="skeleton ${variant}"></span>`;

// Small reusable primitives.
export const skelPill = () => skel('skel-pill');
export const skelLine = (cls = '') => `<span class="skeleton skel-line ${cls}"></span>`;

// Nav-metric (sidebar) — narrow shimmer pill. Used for all six
// #nav-metric-* spans at boot.
export function navMetricSkeleton() {
  return skelPill();
}

// Date range in the brand block (#date-range).
export function dateRangeSkeleton() {
  return skelPill();
}

// Overview — totals row (4 tiles, first one larger) + chart area +
// hotspot card stack.
export function overviewSkeleton() {
  return `
    <div class="skel-totals">
      ${skel('skel-tile-lg')}
      ${skel('skel-tile')}
      ${skel('skel-tile')}
      ${skel('skel-tile')}
    </div>
    ${skel('skel-chart')}
    <div class="skel-list" style="margin-top: var(--s-4);">
      ${skel('skel-hotspot')}
      ${skel('skel-hotspot')}
      ${skel('skel-hotspot')}
      ${skel('skel-hotspot')}
      ${skel('skel-hotspot')}
    </div>`;
}

// Bar-list skeleton — used wherever .hbar-list is empty (Cost by-model,
// Cost by-project, Tools by-tool).
export function hbarListSkeleton(n = 6) {
  const bars = [];
  for (let i = 0; i < n; i++) bars.push(skel('skel-bar'));
  return `<div class="skel-hbars">${bars.join('')}</div>`;
}

// Table-body skeleton — N rows, each row is a single TD with a shimmer
// line that fills the row. colSpan must match the table's column count.
export function tableBodySkeleton(colSpan, n = 6) {
  let rows = '';
  for (let i = 0; i < n; i++) {
    rows += `<tr class="skel-tr"><td colspan="${colSpan}">${skelLine()}</td></tr>`;
  }
  return rows;
}

// Stacked-bar skeleton — single 32px shimmer matching .stacked.
export function stackedSkeleton() {
  return skel('skel-stacked');
}

// Summary band skeleton — used by Cache's #cache-summary.
export function summaryBandSkeleton() {
  return skel('skel-summary');
}

// Session list skeleton — N session-card-shaped blocks.
export function sessionListSkeleton(n = 8) {
  const cards = [];
  for (let i = 0; i < n; i++) cards.push(skel('skel-card'));
  return `<div class="skel-list">${cards.join('')}</div>`;
}

// Per-session timeline skeleton — a few prompt-block-shaped rows that
// stand in for the lazy-fetched timeline body.
export function timelineSkeleton(n = 3) {
  const rows = [];
  for (let i = 0; i < n; i++) rows.push(skel('skel-card'));
  return `<div class="skel-list">${rows.join('')}</div>`;
}

// Helper — fill all skeleton placeholders in nav metrics + date range.
// Pairs with skeletonResetIfPending() so a failed paintNav reverts the
// pill to "—" rather than shimmering forever.
const SKEL_ATTR = 'data-skel';

// Skeleton the pills that resolve at boot. nav-metric-overview is
// painted by view-overview.js's paint() (an async fetch), so we
// skeleton it only when Overview is the initial route — otherwise it
// would shimmer indefinitely until the user clicks Overview.
//
// #date-range is deliberately NOT skeletoned: in serve mode the date
// picker paints it synchronously from the URL on wire, and in static
// mode the inlined data fills it instantly — there is no async gap to
// cover, and a shimmer would only clobber the already-correct label.
export function paintNavSkeletons(includeOverview) {
  const ids = [
    'nav-metric-cost',
    'nav-metric-sessions',
    'nav-metric-cache',
    'nav-metric-tools',
    'nav-metric-subagents',
  ];
  if (includeOverview) {
    ids.push('nav-metric-overview');
  }
  for (const id of ids) {
    const el = document.getElementById(id);
    if (!el) continue;
    el.setAttribute(SKEL_ATTR, '1');
    el.innerHTML = skelPill();
  }
}

// If a paintNav* never replaced the skeleton (fetch failed), drop back
// to "—" so the pill doesn't shimmer indefinitely.
export function skeletonResetIfPending(id) {
  const el = document.getElementById(id);
  if (!el) return;
  if (el.getAttribute(SKEL_ATTR) === '1' && el.querySelector('.skeleton')) {
    el.removeAttribute(SKEL_ATTR);
    el.textContent = '—';
  }
}

// Helper for paintNav implementations — they call this after setting
// real content so the skel attr is cleared. Without it, a successful
// paintNav followed by an unrelated error wouldn't get spotted.
export function clearSkeletonFlag(id) {
  const el = document.getElementById(id);
  if (el) el.removeAttribute(SKEL_ATTR);
}
