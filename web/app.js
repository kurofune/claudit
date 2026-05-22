// SPA entry point. Wires the hash router, paints the active view,
// and subscribes to /events for the data-changed reload nudge.
//
// Only the Overview view is implemented in Phase 5; the other tabs
// render placeholders pointing back at the legacy /. Each Phase-6
// view will register itself the same way Overview does here.

import { onChange, activate, parseHash } from './router.js';
import { paint as paintOverview } from './view-overview.js';
import { start as startSSE, wireReloadToast } from './sse.js';

const VIEW_PAINTERS = {
  overview: paintOverview,
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
const toastEl = document.getElementById('reload-toast');
const btnEl = document.getElementById('reload-toast-btn');
const onUpdate = wireReloadToast(toastEl, btnEl);
startSSE(onUpdate);
