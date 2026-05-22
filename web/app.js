// SPA entry point. Wires the hash router, paints the active view,
// and subscribes to /events for the data-changed reload nudge.
//
// Phase 6 lands the Cost / Cache / Tools / Subagents painters next to
// Overview; each view module owns its own fetch + render lifecycle
// and is a no-op on subsequent route changes within the same view
// (just toggles the subview class).

import { onChange, activate, parseHash } from './router.js';
import { paint as paintOverview } from './view-overview.js';
import { paint as paintCost } from './view-cost.js';
import { paint as paintCache } from './view-cache.js';
import { paint as paintTools } from './view-tools.js';
import { paint as paintSubagents } from './view-subagents.js';
import { paint as paintSessions } from './view-sessions.js';
import { start as startSSE, wireReloadToast } from './sse.js';

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
