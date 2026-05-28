// Date-range picker for the brand-sub button in the sidebar. Clicking
// the button opens a popover with two native <input type="date"> fields
// and Apply/Clear/Cancel. Apply rewrites the URL with ?since=&until=
// &scope=all and reloads — the server's filter.go reads these params.
//
// The user-facing End is INCLUSIVE; the server's `until` is exclusive
// (matches the report CLI). We translate at the boundary: +1 day on
// Apply, -1 day when seeding from the URL.
//
// Offline / static-report mode: the static template renders
// #date-range as a plain <div> (no button), so wireDatePicker()'s
// initial querySelector returns null and the module no-ops. The
// SPA bundle is shared between serve and static modes; the markup
// difference is what gates the behavior.

// UTC math dodges local-timezone DST surprises — claudit treats
// date-only values as UTC midnight throughout.
export function addDays(ymdStr, delta) {
  const [y, m, d] = ymdStr.split('-').map(Number);
  const dt = new Date(Date.UTC(y, m - 1, d));
  dt.setUTCDate(dt.getUTCDate() + delta);
  const Y = dt.getUTCFullYear();
  const M = String(dt.getUTCMonth() + 1).padStart(2, '0');
  const D = String(dt.getUTCDate()).padStart(2, '0');
  return `${Y}-${M}-${D}`;
}

// Format a Date's LOCAL components as 'YYYY-MM-DD'. The default
// "last 7 days" seed is anchored to the user's local today.
function ymd(d) {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${dd}`;
}

// Translate the URL's ?since/&until into the user-facing INCLUSIVE
// range the picker and label both render. The server's `until` is
// exclusive, so the inclusive end is until-1. `now` is injected for
// deterministic tests — never read the clock internally.
export function urlToRange(search, now = new Date()) {
  const params = new URLSearchParams(search);
  const since = params.get('since');
  const untilExcl = params.get('until');
  let end = untilExcl ? addDays(untilExcl, -1) : '';
  let start = since || '';
  if (!start && !end) {
    // Default seed mirrors the server's last=7d default scope.
    const weekAgo = new Date(now);
    weekAgo.setDate(now.getDate() - 7);
    start = ymd(weekAgo);
    end = ymd(now);
  }
  return { start, end };
}

export function wireDatePicker() {
  const btn = document.getElementById('date-range-button');
  if (!btn) return; // static-report mode, or markup not yet rendered

  let popover = null;

  function build() {
    if (popover) return popover;
    popover = document.createElement('div');
    popover.className = 'claudit-date-popover';
    popover.setAttribute('role', 'dialog');
    popover.setAttribute('aria-label', 'Select date range');
    popover.innerHTML =
      '<div class="row"><label for="claudit-date-start">Start</label>' +
      '<input id="claudit-date-start" type="date"></div>' +
      '<div class="row"><label for="claudit-date-end">End</label>' +
      '<input id="claudit-date-end" type="date"></div>' +
      '<div class="buttons">' +
      '<button type="button" class="subtle" data-action="clear">Clear</button>' +
      '<button type="button" data-action="cancel">Cancel</button>' +
      '<button type="button" class="primary" data-action="apply">Apply</button>' +
      '</div>';
    document.body.appendChild(popover);
    popover.querySelector('[data-action="apply"]').addEventListener('click', apply);
    popover.querySelector('[data-action="clear"]').addEventListener('click', clear);
    popover.querySelector('[data-action="cancel"]').addEventListener('click', close);
    popover.addEventListener('click', (e) => e.stopPropagation());
    popover.querySelector('#claudit-date-start').addEventListener('keydown', onPopoverKey);
    popover.querySelector('#claudit-date-end').addEventListener('keydown', onPopoverKey);
    return popover;
  }

  function onPopoverKey(e) {
    if (e.key === 'Enter') { e.preventDefault(); apply(); }
  }

  function position() {
    if (!popover) return;
    const rect = btn.getBoundingClientRect();
    popover.style.top = (rect.bottom + window.scrollY + 6) + 'px';
    popover.style.left = (rect.left + window.scrollX) + 'px';
  }

  function seedInputs() {
    // Single source of truth: urlToRange does the exclusive→inclusive
    // translation and the last=7d default seed, shared with the label.
    const { start, end } = urlToRange(window.location.search);
    popover.querySelector('#claudit-date-start').value = start;
    popover.querySelector('#claudit-date-end').value = end;
  }

  function open() {
    build();
    seedInputs();
    position();
    popover.classList.add('is-visible');
    btn.setAttribute('aria-expanded', 'true');
    // Delay the doc-click binding by a tick so the click that opened
    // us doesn't immediately close us.
    setTimeout(() => {
      document.addEventListener('click', onDocClick);
      document.addEventListener('keydown', onDocKey);
      window.addEventListener('resize', position);
      window.addEventListener('scroll', position, true);
    }, 0);
    popover.querySelector('#claudit-date-start').focus();
  }

  function close() {
    if (popover) popover.classList.remove('is-visible');
    btn.setAttribute('aria-expanded', 'false');
    document.removeEventListener('click', onDocClick);
    document.removeEventListener('keydown', onDocKey);
    window.removeEventListener('resize', position);
    window.removeEventListener('scroll', position, true);
  }

  function onDocClick(e) {
    if (popover && !popover.contains(e.target) && e.target !== btn && !btn.contains(e.target)) close();
  }

  function onDocKey(e) {
    if (e.key === 'Escape') { close(); btn.focus(); }
  }

  function apply() {
    const s = popover.querySelector('#claudit-date-start').value;
    const en = popover.querySelector('#claudit-date-end').value;
    if (s && en && s > en) {
      alert('Start date must be on or before end date.');
      return;
    }
    const p = new URLSearchParams(window.location.search);
    if (s) p.set('since', s); else p.delete('since');
    // Translate the user-facing inclusive End to the server's
    // exclusive `until` by adding one day.
    if (en) p.set('until', addDays(en, 1)); else p.delete('until');
    p.delete('last');       // since/until override last
    p.set('scope', 'all');  // explicit user choice — disable server defaults
    const qs = p.toString();
    window.location.search = qs ? ('?' + qs) : '';
  }

  function clear() {
    const p = new URLSearchParams(window.location.search);
    p.delete('since');
    p.delete('until');
    p.delete('last');
    p.set('scope', 'all');
    const qs = p.toString();
    window.location.search = qs ? ('?' + qs) : '';
  }

  // Paint the brand-sub button label from the SELECTED WINDOW so the
  // pill matches what the picker shows — one source (urlToRange) backs
  // both. The URL only changes via a full reload, so painting once on
  // wire is enough. Static-report mode has no #date-range-button, so
  // wireDatePicker already returned above and view-overview paints the
  // data span into #date-range instead.
  function renderLabel() {
    const span = document.getElementById('date-range');
    if (!span) return;
    const { start, end } = urlToRange(window.location.search);
    span.textContent = (start || end) ? `${start || '…'} → ${end || '…'}` : '—';
  }
  renderLabel();

  btn.addEventListener('click', (e) => {
    e.stopPropagation();
    if (popover && popover.classList.contains('is-visible')) close();
    else open();
  });
}
