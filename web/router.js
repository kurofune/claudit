// Hash-based router preserving the legacy URL contract:
//   #overview              → Overview tab
//   #cost                  → Cost tab (root)
//   #cost/model            → Cost tab, model subview
//   #sessions/<id>         → Sessions tab, expand session <id>
//
// The fat HTML used the same shape, so old bookmarks keep working
// when the cutover phase flips / to the SPA.

const KNOWN_VIEWS = new Set(['overview', 'cost', 'tokens', 'sessions', 'cache', 'tools', 'subagents']);

export function parseHash() {
  const raw = (window.location.hash || '#overview').slice(1);
  const [head, ...rest] = raw.split('/');
  const view = KNOWN_VIEWS.has(head) ? head : 'overview';
  return { view, sub: rest.join('/') };
}

// activate sets the is-active class on the matching .view + .nav-item.
// Views and nav items are pre-rendered in the shell HTML, so the
// router doesn't construct DOM — it only toggles class names. Returns
// the activated route so callers can hand it to a view-specific
// handler (which fetches data, paints chart, etc.).
export function activate(route) {
  const views = document.querySelectorAll('.view');
  views.forEach(v => v.classList.toggle('is-active', v.dataset.view === route.view));
  const navItems = document.querySelectorAll('.nav-item');
  navItems.forEach(n => n.classList.toggle('is-active', n.dataset.view === route.view));
  return route;
}

// onChange wires a listener to hashchange + the initial load. Callback
// fires with the parsed route each time.
export function onChange(callback) {
  const fire = () => callback(parseHash());
  window.addEventListener('hashchange', fire);
  fire();
}
