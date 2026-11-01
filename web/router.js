// Hash-based router. The URL contract:
//   #overview                  → Overview
//   #spend/<tab>               → Spend, <tab>, Dollars
//   #spend/<tab>/tokens        → Spend, <tab>, Tokens
//   #overview/all              → Overview with the tool-patterns table open
//   #agents                    → the Sessions list
//   #agents/<session>[/<turn>] → that session's page, at <turn> when named
//
// Retired views redirect through REDIRECTS so old bookmarks still land:
// #cost, #tokens and #cache on the Spend view, #tools on the Overview's
// tool-patterns table.

const KNOWN_VIEWS = new Set(['overview', 'spend', 'agents']);

export const SPEND_DEFAULT_TAB = 'model';
const TOKENS_SUFFIX = 'tokens';

// spendHash is the canonical hash for a Spend tab + unit. Tokens mode
// always names a tab so the unit segment is unambiguous.
export function spendHash(tab, unit) {
  if (unit === 'tokens') return `#spend/${tab || SPEND_DEFAULT_TAB}/${TOKENS_SUFFIX}`;
  return tab ? `#spend/${tab}` : '#spend';
}

// REDIRECTS maps a retired view to its replacement. A rule with `to`
// names a fixed hash; a Spend rule's keepTab carries the old sub-path over
// as the Spend tab and its unit picks the Spend unit.
export const REDIRECTS = [
  { from: 'cost', unit: 'dollars', keepTab: true },
  { from: 'tokens', unit: 'tokens', keepTab: true },
  { from: 'cache', unit: 'dollars', keepTab: false },
  { from: 'tools', to: '#overview/all' },
];

function splitHash(raw) {
  const s = raw == null ? '' : String(raw);
  const body = s.startsWith('#') ? s.slice(1) : s;
  const [head, ...rest] = body.split('/');
  return { head, rest };
}

// redirectHash returns the hash a retired-view hash should be replaced
// with, or null when the hash is current.
export function redirectHash(raw) {
  const { head, rest } = splitHash(raw);
  const rule = REDIRECTS.find(r => r.from === head);
  if (!rule) return null;
  if (rule.to) return rule.to;
  const tab = rule.keepTab ? (rest[0] || '') : '';
  return spendHash(tab, rule.unit);
}

// sessionHash is the hash of a session page, optionally opened at one
// turn: #agents/<id>[/<turn>], each part URI-encoded.
export function sessionHash(sessionId, turnUuid = '') {
  const base = `#agents/${encodeURIComponent(sessionId)}`;
  return turnUuid ? `${base}/${encodeURIComponent(turnUuid)}` : base;
}

// agentsSessionHash is the hash that opens sessionId's session page.
export function agentsSessionHash(sessionId) {
  return sessionHash(sessionId);
}

// parseAgentsSub splits an Agents route's sub into the session it opens
// and the turn to land on ('' for each when absent or malformed; a
// malformed session id reads as the list).
export function parseAgentsSub(sub) {
  const [rawId = '', rawTurn = ''] = String(sub || '').split('/');
  const decode = s => { try { return decodeURIComponent(s); } catch { return ''; } };
  const sessionId = decode(rawId);
  return { sessionId, turnUuid: sessionId ? decode(rawTurn) : '' };
}

// parseHash reads a route from raw (a hash string, '#' optional), or
// from window.location.hash when raw is omitted.
export function parseHash(raw) {
  const src = raw === undefined ? window.location.hash : raw;
  const { head, rest } = splitHash(src || '#overview');
  const view = KNOWN_VIEWS.has(head) ? head : 'overview';
  let unit = 'dollars';
  if (view === 'spend' && rest.length > 1 && rest[rest.length - 1] === TOKENS_SUFFIX) {
    rest.pop();
    unit = 'tokens';
  }
  return { view, sub: rest.join('/'), unit };
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

// onChange wires a listener to hashchange + the initial load. A retired
// hash is rewritten in place (history.replaceState, so Back skips it)
// before the callback fires with the parsed route.
export function onChange(callback) {
  const fire = () => {
    const to = redirectHash(window.location.hash);
    if (to) history.replaceState(history.state, '', to);
    callback(parseHash());
  };
  window.addEventListener('hashchange', fire);
  fire();
}
