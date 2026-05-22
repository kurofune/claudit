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

const apiBase = '/_claudit/api';

function withSearch(path) {
  const search = window.location.search || '';
  return apiBase + path + search;
}

async function getJSON(path) {
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
export const fetchCache = () => getJSON('/cache');
export const fetchTools = () => getJSON('/tools');
export const fetchSubagents = () => getJSON('/subagents');
export const fetchSessions = () => getJSON('/sessions');
export const fetchAnomalies = () => getJSON('/anomalies');
export const fetchTrends = (dim) => getJSON('/trends?dim=' + encodeURIComponent(dim));
export const fetchSessionTimeline = (id) =>
  getJSON('/sessions/' + encodeURIComponent(id) + '/timeline');
