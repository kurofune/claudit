// Thin fetch wrappers for /_claudit/api/*. Each helper carries the
// browser's location.search forward so the URL contract from the
// fat-HTML era — "the URL is the filter" — still holds: bookmark a
// `?since=…&until=…&project=…` URL, get the same filtered view.
//
// ETag short-circuit: the server emits `W/"gen-<gen>-<section>"` on
// every section endpoint (see internal/serve/api.go:buildAPIEtag).
// Browsers handle conditional revalidation automatically as long as
// we don't break the cache contract with no-store, so we just lean on
// the default fetch() behavior. The `Cache-Control: no-cache,
// must-revalidate` header from the server is what triggers a
// revalidation on each call; a 304 returns the cached body without
// re-decoding the JSON payload.
//
// Offline / static-report mode (Phase 9): when the page sets
// window.__claudit_static_data (a section-keyed snapshot the static
// HTML report inlines), getJSON routes the call to the inline blob
// instead of issuing a fetch. That makes a downloaded report fully
// interactive without a server. Filter querystrings are ignored
// offline — the static blob was already built with whatever filter
// the report-author chose; client-side re-filtering across the wire
// has no meaning when there is no wire.

const apiBase = '/_claudit/api';

function withSearch(path) {
  const search = window.location.search || '';
  return apiBase + path + search;
}

// offlineLookup routes an /_claudit/api/<...> path to its inlined
// payload in window.__claudit_static_data. Returns the payload, or
// throws if the path is unknown — same failure shape getJSON
// produces for an HTTP error, so callers don't need to branch on
// online/offline.
function offlineLookup(path) {
  const data = window.__claudit_static_data;
  // Strip a trailing querystring before pattern-matching — offline
  // mode ignores filter params (the inline blob is pre-filtered).
  const q = path.indexOf('?');
  const bare = q >= 0 ? path.slice(0, q) : path;

  switch (bare) {
    case '/snapshot':  return data.snapshot;
    case '/overview':  return data.overview;
    case '/cost':      return data.cost;
    case '/tokens':    return data.tokens;
    case '/cache':     return data.cache;
    case '/tools':     return data.tools;
    case '/subagents': return data.subagents;
    case '/sessions':  return data.sessions;
    case '/anomalies': return data.anomalies;
  }
  // /trends?dim=X — the dim is in the original querystring, parsed
  // here from `path` rather than location.search so a caller that
  // builds a synthetic URL still resolves to the right series.
  if (bare === '/trends' && q >= 0) {
    const params = new URLSearchParams(path.slice(q));
    const dim = params.get('dim');
    if (dim && data.trends && data.trends[dim]) return data.trends[dim];
  }
  // /sessions/<id>/timeline
  const m = bare.match(/^\/sessions\/(.+)\/timeline$/);
  if (m && data.session_timelines) {
    const tl = data.session_timelines[m[1]];
    if (tl) return tl;
  }
  throw new Error('offline: no inline payload for ' + path);
}

async function getJSON(path) {
  if (typeof window !== 'undefined' && window.__claudit_static_data) {
    return offlineLookup(path);
  }
  const url = withSearch(path);
  const res = await fetch(url, {
    headers: { 'Accept': 'application/json' },
    // credentials default ('same-origin') is correct — claudit binds
    // to loopback only and has no auth layer.
  });
  if (!res.ok) {
    throw new Error(`GET ${url}: ${res.status} ${res.statusText}`);
  }
  return res.json();
}

export const fetchSnapshot = () => getJSON('/snapshot');
export const fetchOverview = () => getJSON('/overview');
export const fetchCost = () => getJSON('/cost');
export const fetchTokens = () => getJSON('/tokens');
export const fetchCache = () => getJSON('/cache');
export const fetchTools = () => getJSON('/tools');
export const fetchSubagents = () => getJSON('/subagents');
export const fetchSessions = () => getJSON('/sessions');
export const fetchAnomalies = () => getJSON('/anomalies');
export const fetchTrends = (dim) => getJSON('/trends?dim=' + encodeURIComponent(dim));
export const fetchSessionTimeline = (id) =>
  getJSON('/sessions/' + encodeURIComponent(id) + '/timeline');
