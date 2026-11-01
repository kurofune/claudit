// @ts-check
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

/** @import { AgentGraph, SessionIndex, SessionStoryPage, ToolUseDetail, TurnTextDetail } from './api-types.js' */

const apiBase = '/_claudit/api';

// apiURL appends the page's query string to an API path, joining with
// '&' when the path already carries its own query (e.g. /trends?dim=).
/** @param {string} path @param {string} search */
export function apiURL(path, search) {
  if (!search) return apiBase + path;
  return apiBase + path + (path.includes('?') ? '&' + search.slice(1) : search);
}

/** @param {string} path */
function withSearch(path) {
  return apiURL(path, window.location.search || '');
}

/** @param {string} path @returns {Promise<any>} */
async function getJSON(path) {
  const url = withSearch(path);
  const res = await fetch(url, {
    headers: { 'Accept': 'application/json' },
    // credentials default ('same-origin') is correct — claudit binds
    // to loopback only and has no auth layer.
  });
  if (!res.ok) {
    const err = new Error(`GET ${url}: ${res.status} ${res.statusText}`);
    // status lets a view name the HTTP failure without parsing the message.
    /** @type {any} */ (err).status = res.status;
    /** @type {any} */ (err).statusText = res.statusText;
    throw err;
  }
  return res.json();
}

export const fetchSnapshot = () => getJSON('/snapshot');
export const fetchOverview = () => getJSON('/overview');
export const fetchCost = () => getJSON('/cost');
export const fetchTokens = () => getJSON('/tokens');
export const fetchTools = () => getJSON('/tools');
export const fetchSubagents = () => getJSON('/subagents');
export const fetchAnomalies = () => getJSON('/anomalies');
/** @returns {Promise<AgentGraph>} */
export const fetchAgents = () => getJSON('/agents');
// fetchAgentToolFull pulls the untruncated input/output for one tool_use back
// from disk (the drawer's "show full" action).
/**
 * @param {string} sessionId
 * @param {string} toolId
 * @returns {Promise<ToolUseDetail>}
 */
export const fetchAgentToolFull = (sessionId, toolId) =>
  getJSON('/agents/full?session=' + encodeURIComponent(sessionId) +
    '&tool=' + encodeURIComponent(toolId));
// fetchAgentTurnFull pulls the untruncated thinking/text for one turn back
// from disk (the drawer's "show full" action on Reasoning/Message). Serve-mode
// only, same as fetchAgentToolFull — the drawer guards on serve mode.
/**
 * @param {string} sessionId
 * @param {string} turnUuid
 * @returns {Promise<TurnTextDetail>}
 */
export const fetchAgentTurnFull = (sessionId, turnUuid) =>
  getJSON('/agents/full?session=' + encodeURIComponent(sessionId) +
    '&turn=' + encodeURIComponent(turnUuid));
// fetchSessions pulls the Sessions list's index: one light row per session
// in the page's filter window, each with its prompt-family key.
/** @returns {Promise<SessionIndex>} */
export const fetchSessions = () => getJSON('/sessions');
// fetchSessionStory pulls one page of a session's story: the turns of user
// prompt `prompt` (default 0), 200 at a time from turn `offset` (default 0).
/**
 * @param {string} sessionId
 * @param {{prompt?: number, offset?: number}} [page]
 * @returns {Promise<SessionStoryPage>}
 */
export const fetchSessionStory = (sessionId, { prompt = 0, offset = 0 } = {}) =>
  getJSON('/sessions/story?session=' + encodeURIComponent(sessionId) +
    '&prompt=' + encodeURIComponent(String(prompt)) +
    '&offset=' + encodeURIComponent(String(offset)));
/** @param {string} dim */
export const fetchTrends = (dim) => getJSON('/trends?dim=' + encodeURIComponent(dim));
