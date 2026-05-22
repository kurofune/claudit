// EventSource wrapper for /events. The server (internal/serve/events.go)
// emits one frame per snapshot-generation bump:
//   data: {"generation": 42}
//
// The SPA cares about two things:
//   1. First event after page load = "this is the generation you
//      loaded against." Stash it so we know what later events should
//      be compared to.
//   2. Any later event with a higher generation = data has changed
//      underfoot. We surface the reload toast rather than auto-
//      navigating; an in-flight user shouldn't lose their place
//      because a new turn streamed in.
//
// On EventSource error the browser auto-retries with exponential
// backoff. We don't override that — silent retries fit the
// "minimal" project ethos.

let loadedGeneration = null;
let onUpdateCallback = null;

export function start(onUpdate) {
  onUpdateCallback = onUpdate;
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
    if (gen > loadedGeneration && onUpdateCallback) {
      onUpdateCallback(gen);
    }
  });
  return es;
}

// wireReloadToast hooks up the toast element to a "new data" event
// from start(). Click → location.reload(). Calling start(...) directly
// instead would tie the SSE wiring to a specific DOM contract; this
// split keeps the SSE module reusable.
export function wireReloadToast(toastEl, btnEl) {
  if (!toastEl || !btnEl) return;
  btnEl.addEventListener('click', () => {
    window.location.reload();
  });
  return (gen) => {
    toastEl.hidden = false;
  };
}
