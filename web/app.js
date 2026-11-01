// SPA entry point. Wires the hash router, paints the active view,
// and subscribes to /events for the data-changed reload nudge.
//
// Each view module (Overview, Spend, Agents) owns its own fetch +
// render lifecycle and is a no-op on subsequent route changes within the
// same view (just toggles the subview class).

import { onChange, activate, parseHash } from './router.js';
import { paint as paintOverview } from './view-overview.js';
import { paint as paintSpend, paintNav as paintNavSpend } from './view-spend.js';
import { paint as paintAgents, paintNav as paintNavAgents } from './view-sessions.js';
import { start as startSSE, clearLiveHandler } from './sse.js';
import { wireDatePicker } from './date-picker.js';
import { wireProviderFilter, setProviderBusy, busyPainter } from './provider-filter.js';
import { paintNavSkeletons, skeletonResetIfPending } from './skeleton.js';
import { init as initThemes } from './themes.js';
import { init as initTooltips } from './tooltip.js';

const VIEW_PAINTERS = {
  overview: paintOverview,
  spend: paintSpend,
  agents: paintAgents,
};

const paintBusy = busyPainter(setProviderBusy);

onChange(async (route) => {
  activate(route);
  // The Sessions list registers an in-place SSE updater while it's open;
  // any other route must release it so the classic full-reload path runs.
  if (route.view !== 'agents') clearLiveHandler();
  const painter = VIEW_PAINTERS[route.view];
  if (painter) {
    // The provider radiogroup stays disabled while the current route's fetch
    // is in flight; each painter renders its own error state.
    await paintBusy(() => painter(route));
  }
});

// SSE-driven silent auto-reload. When the server pushes a new
// generation, sse.js waits for a safe moment (tab visible, no
// <details> open, no recent user input) and then reloads. After
// TOAST_AFTER_MS of unsafe-to-reload pile-up it surfaces the toast as
// the manual-reload fallback.
startSSE({
  toastEl: document.getElementById('reload-toast'),
  btnEl: document.getElementById('reload-toast-btn'),
});

wireDatePicker();
wireProviderFilter();

// Theme picker — gear icon next to the version footer. The chosen
// theme is already applied by the inline FOUC-prevention script in
// index.html; init() just binds the popover.
initThemes();

// Custom tooltip popover — replaces native `title=` everywhere. init()
// migrates existing title= → data-tooltip= and watches for nodes added by
// later view re-renders, so the ~150 native call sites upgrade automatically.
initTooltips();

// Sidebar metric prefetch — fires the two non-overview sections in
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
// Pills fed by the two paintNavs below — nav-metric-overview and
// date-range are owned by view-overview.js and reset there on error.
const NAV_SKEL_IDS = ['nav-metric-cost', 'nav-metric-agents'];
Promise.all([
  paintNavSpend(),
  paintNavAgents(),
])
  .catch(err => console.error('nav metric prefetch failed:', err))
  .finally(() => NAV_SKEL_IDS.forEach(skeletonResetIfPending));
