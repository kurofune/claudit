// EventSource wrapper for /events plus the silent-auto-reload loop the
// SPA runs against it. The server (internal/serve/events.go) emits one
// frame per snapshot-generation bump:
//   data: {"generation": 42}
//
// First event after page load = "this is the generation you loaded
// against." Stash it so later events can be compared. Any later event
// with a higher generation flags a pending reload; the loop then waits
// for a safe moment to actually run window.location.reload().
//
// Safe moment = decideReload below: not while the tab is hidden, not
// while a <details> is open, not within INTERACTION_IDLE_MS of the
// user's last mouse/keyboard/scroll input. After TOAST_AFTER_MS of
// pile-up we give up deferring and surface the toast instead — a tab
// that's been idle-but-unreloadable for that long is past the point
// where a silent swap is friendlier than asking.

// INTERACTION_IDLE_MS is how long we wait after the user's last input
// event before treating the tab as idle and safe to reload. Short
// enough that a moment of attention away from the keyboard releases
// the deferral; long enough that mid-click / mid-scroll doesn't yank
// the page out from under them.
export const INTERACTION_IDLE_MS = 10_000;

// TOAST_AFTER_MS bounds the pile-up window. If a reload has been
// pending this long with no safe moment in sight, the silent path
// gives up and the toast appears for manual reload.
export const TOAST_AFTER_MS = 5 * 60_000;

// MIN_RELOAD_INTERVAL_MS is the floor between reloads. The corpus
// poller bumps the generation every ~2s while a session writes
// turns; without a floor the dashboard reloads every 2s, which is
// distracting and wastes bandwidth on a tab that's not the focus of
// attention. 15s is calm-but-not-stale for the dashboard surface;
// watch mode (ticker tape) has its own cadence and isn't gated by
// this.
export const MIN_RELOAD_INTERVAL_MS = 15_000;

// RECHECK_INTERVAL_MS is the loop's heartbeat. It drives the "the
// user's interaction is now stale, reload" and "we've been pending
// long enough, show the toast" transitions, which would otherwise
// only fire on a fresh DOM event.
const RECHECK_INTERVAL_MS = 2_000;

// decideReload is the pure decision the auto-reload loop consults on
// every tick. Pure so jstest can pin every branch without a DOM.
//
// Verbs: 'reload' fires window.location.reload(); 'toast' switches
// the toast on and stops the loop; 'defer' waits for the next tick.
export function decideReload(state) {
  if (state.pendingSince == null) return 'defer';
  const stalenessMs = state.now - state.pendingSince;
  if (stalenessMs >= TOAST_AFTER_MS) return 'toast';
  if (state.isHidden) return 'defer';
  if (state.anyDetailsOpen) return 'defer';
  if (state.lastInteractionAt != null &&
      state.now - state.lastInteractionAt < INTERACTION_IDLE_MS) {
    return 'defer';
  }
  // Reload floor: the page must have been on screen at least
  // MIN_RELOAD_INTERVAL_MS before we replace it. Null pageLoadedAt
  // means "no floor" — used by tests that don't care about throttle.
  if (state.pageLoadedAt != null &&
      state.now - state.pageLoadedAt < MIN_RELOAD_INTERVAL_MS) {
    return 'defer';
  }
  return 'reload';
}

// start opens the EventSource and wires up the silent-auto-reload
// loop. Returns the EventSource so the caller can close it if needed.
// toastEl/btnEl may be null — if so, no toast fallback (the loop
// keeps deferring forever, which is fine for environments without
// the markup, e.g. older shells).
export function start({ toastEl = null, btnEl = null } = {}) {
  let loadedGeneration = null;
  let pendingSince = null;
  let lastInteractionAt = null;
  const pageLoadedAt = Date.now();

  const showToast = () => {
    if (!toastEl) return;
    toastEl.hidden = false;
    toastEl.classList.add('is-visible');
  };

  if (btnEl) {
    btnEl.addEventListener('click', () => window.location.reload());
  }

  // Track user input so we don't reload mid-action. passive=true
  // because we never preventDefault — keeps these listeners off the
  // hot path for scroll performance.
  const markInteraction = () => { lastInteractionAt = Date.now(); };
  ['mousemove', 'keydown', 'wheel', 'touchstart'].forEach(ev => {
    window.addEventListener(ev, markInteraction, { passive: true });
  });
  // Scroll fires on the document for most browsers; listen there too.
  document.addEventListener('scroll', markInteraction, { passive: true, capture: true });

  const anyDetailsOpen = () => {
    // Querying every tick is fine — the SPA's DOM is small and this
    // only runs on RECHECK_INTERVAL_MS / event boundaries.
    const list = document.querySelectorAll('details[open]');
    return list.length > 0;
  };

  let timerId = null;
  const tick = () => {
    const verb = decideReload({
      pendingSince,
      lastInteractionAt,
      anyDetailsOpen: anyDetailsOpen(),
      isHidden: document.hidden,
      pageLoadedAt,
      now: Date.now(),
    });
    if (verb === 'reload') {
      window.location.reload();
      return; // page is unloading; no point scheduling another tick
    }
    if (verb === 'toast') {
      showToast();
      // Stop the loop — the user is now in control. We don't clear
      // pendingSince so a subsequent SSE event still has somewhere to
      // attribute itself, but the toast stays up until reload.
      if (timerId != null) { clearInterval(timerId); timerId = null; }
      return;
    }
    // defer: next tick will re-check.
  };

  const armLoop = () => {
    if (timerId != null) return;
    timerId = setInterval(tick, RECHECK_INTERVAL_MS);
  };

  // Re-check immediately when conditions that gate the reload change.
  // visibilitychange handles tab focus/blur; toggle (bubbles from any
  // <details>) handles open/close.
  document.addEventListener('visibilitychange', tick);
  document.addEventListener('toggle', tick, true);

  const es = new EventSource('/events');
  es.addEventListener('message', (e) => {
    let payload;
    try { payload = JSON.parse(e.data); }
    catch { return; }
    const gen = payload && payload.generation;
    if (typeof gen !== 'number') return;
    if (loadedGeneration === null) {
      loadedGeneration = gen;
      return;
    }
    if (gen > loadedGeneration) {
      // Track the OLDEST pending generation so the pile-up timer
      // measures from first-signal, not latest-signal. Later bumps
      // (gen 43 → 44 → 45) all collapse into "there's something new."
      if (pendingSince == null) pendingSince = Date.now();
      armLoop();
      tick();
    }
  });
  return es;
}
