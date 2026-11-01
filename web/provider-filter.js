// @ts-check
// Provider filter — the sidebar radiogroup under the date-range button.
// Like the date picker, the URL is the filter: choosing a provider rewrites
// `provider=` in location.search (All removes it) and reloads, so every
// /_claudit/api/* call carries it (api.js withSearch) and the server's
// filter.go applies it. A reload or a shared link restores the choice.
//
// The pure helpers below are unit-tested in jstest/provider-filter.test.js;
// wireProviderFilter and setProviderBusy are the DOM half.

export const PROVIDERS = [
  { id: 'all', label: 'All' },
  { id: 'claude-code', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
];

// Where each provider's sessions come from, for the empty states.
const SOURCES = {
  'claude-code': '~/.claude/projects',
  codex: '~/.codex/sessions',
};

const KEY = 'provider';

/** @param {string} search @returns {string} 'all' | 'claude-code' | 'codex' */
export function providerFromSearch(search) {
  const v = new URLSearchParams(search).get(KEY) || '';
  return PROVIDERS.some(p => p.id === v && v !== 'all') ? v : 'all';
}

/** @param {string} search @param {string} id @returns {string} '' or '?…' */
export function searchWithProvider(search, id) {
  const p = new URLSearchParams(search);
  if (id === 'all') p.delete(KEY);
  else p.set(KEY, id);
  const qs = p.toString();
  return qs ? '?' + qs : '';
}

/** @param {string} id */
export function providerLabel(id) {
  const p = PROVIDERS.find(x => x.id === id);
  return p ? p.label : id;
}

/**
 * @typedef {{provider: string, sessions: number, turns: number, cost_usd: number, share: number}} ProviderRow
 */

// providerTiles picks the Overview tiles: every provider with sessions under
// All, only the chosen provider otherwise.
/** @param {ProviderRow[]|null|undefined} rows @param {string} selected @returns {ProviderRow[]} */
export function providerTiles(rows, selected) {
  return (rows || []).filter(r => (r.sessions || 0) > 0 &&
    (selected === 'all' || r.provider === selected));
}

/** @param {number} share */
export function shareText(share) {
  return `${(Number(share) || 0).toFixed(1)}%`;
}

/** @param {string} id */
export function emptyProviderText(id) {
  if (!SOURCES[id]) return '';
  return `No ${providerLabel(id)} sessions in this range (claudit reads ${SOURCES[id]})`;
}

// API_EQUIVALENT_TIP explains what kind of dollars the Overview shows, the
// same way for every provider.
export const API_EQUIVALENT_TIP =
  'These are the tokens priced at API list rates. Your subscription bills differently, not per token.';

// ─── DOM ───────────────────────────────────────────────────────────────

// Set before the reload a choice triggers, so the reloaded page puts focus
// back on the checked option and arrow keys keep working.
const REFOCUS = 'claudit-provider-refocus';

function group() {
  return document.getElementById('provider-filter');
}

function radios() {
  const g = group();
  return g ? Array.from(g.querySelectorAll('input[type="radio"]')) : [];
}

// setProviderBusy disables the radiogroup while a view's fetch is in flight
// and re-enables it after, success or error. Re-enabling restores the focus
// a provider change asked for.
/** @param {boolean} busy */
export function setProviderBusy(busy) {
  const g = group();
  if (!g) return;
  g.setAttribute('aria-busy', busy ? 'true' : 'false');
  g.classList.toggle('is-busy', busy);
  for (const r of radios()) /** @type {HTMLInputElement} */ (r).disabled = busy;
  if (busy) return;
  let refocus = false;
  try { refocus = sessionStorage.getItem(REFOCUS) === '1'; sessionStorage.removeItem(REFOCUS); } catch { /* storage off */ }
  if (!refocus) return;
  const checked = /** @type {HTMLInputElement|undefined} */ (radios().find(r => /** @type {HTMLInputElement} */ (r).checked));
  // focusVisible keeps the ring a keyboard user expects after the reload.
  if (checked) checked.focus(/** @type {any} */ ({ focusVisible: true }));
}

// busyPainter runs each route's paint with the radiogroup busy. Only the
// latest paint owns completion: an earlier paint that settles after a newer
// one started leaves busy alone, so the group stays disabled until the
// current route's fetch lands.
/**
 * @param {(busy: boolean) => void} setBusy
 * @returns {(paint: () => any) => Promise<void>}
 */
export function busyPainter(setBusy) {
  let generation = 0;
  return async (paint) => {
    const mine = ++generation;
    setBusy(true);
    try { await paint(); }
    catch (err) { console.error('paint failed:', err); }
    finally { if (mine === generation) setBusy(false); }
  };
}

export function wireProviderFilter() {
  const g = group();
  if (!g) return;
  const current = providerFromSearch(window.location.search);
  for (const r of radios()) {
    const input = /** @type {HTMLInputElement} */ (r);
    input.checked = input.value === current;
    // Native radios: Tab reaches the checked one, arrows move the check and
    // fire change — which applies it.
    input.addEventListener('change', () => {
      if (!input.checked) return;
      const next = searchWithProvider(window.location.search, input.value);
      if (next === (window.location.search || '')) return;
      try { sessionStorage.setItem(REFOCUS, '1'); } catch { /* storage off */ }
      setProviderBusy(true);
      window.location.search = next;
    });
  }
}
