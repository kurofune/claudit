// SPA entry point. Wires the hash router, paints the active view,
// and subscribes to /events for the data-changed reload nudge.
//
// Phase 6 lands the Cost / Cache / Tools / Subagents painters next to
// Overview; each view module owns its own fetch + render lifecycle
// and is a no-op on subsequent route changes within the same view
// (just toggles the subview class).

import { onChange, activate, parseHash } from './router.js';
import { paint as paintOverview } from './view-overview.js';
import { paint as paintCost, paintNav as paintNavCost } from './view-cost.js';
import { paint as paintCache, paintNav as paintNavCache } from './view-cache.js';
import { paint as paintTools, paintNav as paintNavTools } from './view-tools.js';
import { paint as paintSubagents, paintNav as paintNavSubagents } from './view-subagents.js';
import { paint as paintSessions, paintNav as paintNavSessions } from './view-sessions.js';
import { start as startSSE, wireReloadToast } from './sse.js';
import { wireDatePicker } from './date-picker.js';
import { paintNavSkeletons, skeletonResetIfPending } from './skeleton.js';
import { init as initThemes } from './themes.js';

const VIEW_PAINTERS = {
  overview: paintOverview,
  cost: paintCost,
  cache: paintCache,
  tools: paintTools,
  subagents: paintSubagents,
  sessions: paintSessions,
};

onChange(async (route) => {
  activate(route);
  const painter = VIEW_PAINTERS[route.view];
  if (painter) {
    try { await painter(route); }
    catch (err) { console.error('paint failed:', err); }
  }
});

// SSE-driven reload toast. The current Phase-5 behavior is "any data
// change after page load → surface the toast"; later phases can opt
// to silently invalidate per-section caches instead of forcing a full
// reload.
//
// Offline / static-report mode: when window.__claudit_static_data is
// set, there is no server to push generation events from — skip the
// EventSource so we don't fire spurious connection-failure noise.
// wireReloadToast is also a no-op against missing DOM, so the static
// template's omission of the toast markup keeps the bundle quiet.
const toastEl = document.getElementById('reload-toast');
const btnEl = document.getElementById('reload-toast-btn');
const onUpdate = wireReloadToast(toastEl, btnEl);
if (!window.__claudit_static_data) {
  startSSE(onUpdate);
}

// Date-range picker — serve-mode only. The static report renders a
// plain <div id="date-range">, so wireDatePicker() no-ops there.
wireDatePicker();

// Theme picker — gear icon next to the version footer. The chosen
// theme is already applied by the inline FOUC-prevention script in
// index.html; init() just binds the popover.
initThemes();

// Sidebar metric prefetch — fires the five non-overview sections in
// parallel at startup so their nav-metric dashes resolve before the
// user clicks each tab. Each paintNav() short-circuits if its full
// paint() has already run (e.g. via deep link), and falls back to
// leaving the metric as "—" if the fetch fails.
//
// Paint shimmer pills first so the bare `—` doesn't flash before the
// fetch lands. skeletonResetIfPending() reverts to "—" for any pill
// whose paintNav silently failed (catch+return without writing the
// metric), so we never shimmer forever.
// Overview's metric + date-range are painted by view-overview.js's
// paint(), which only fires when Overview is the active route — skeleton
// them only if we're starting on Overview, otherwise leave them as "—".
paintNavSkeletons(parseHash().view === 'overview');
// Pills fed by the five paintNavs below — nav-metric-overview and
// date-range are owned by view-overview.js and reset there on error.
const NAV_SKEL_IDS = [
  'nav-metric-cost', 'nav-metric-sessions',
  'nav-metric-cache', 'nav-metric-tools', 'nav-metric-subagents',
];
Promise.all([
  paintNavCost(),
  paintNavCache(),
  paintNavTools(),
  paintNavSubagents(),
  paintNavSessions(),
])
  .catch(err => console.error('nav metric prefetch failed:', err))
  .finally(() => NAV_SKEL_IDS.forEach(skeletonResetIfPending));
