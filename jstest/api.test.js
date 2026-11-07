import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as api from '../web/api.js';
const { apiURL } = api;

test('apiURL appends the page query to a bare path', () => {
  assert.equal(apiURL('/cost', '?since=2026-01-01'), '/_claudit/api/cost?since=2026-01-01');
});
test('apiURL joins the page query with & when the path already has one', () => {
  assert.equal(apiURL('/trends?dim=model', '?since=2026-01-01&until=2026-01-02'),
    '/_claudit/api/trends?dim=model&since=2026-01-01&until=2026-01-02');
});
test('apiURL leaves the path alone without a page query', () => {
  assert.equal(apiURL('/trends?dim=model', ''), '/_claudit/api/trends?dim=model');
  assert.equal(apiURL('/cost', ''), '/_claudit/api/cost');
});

// fetchURL runs one api.js fetcher against a stubbed window/fetch and
// returns the URL it requested.
async function fetchURL(call, search = '') {
  const seen = [];
  const prev = { window: globalThis.window, fetch: globalThis.fetch };
  globalThis.window = /** @type {any} */ ({ location: { search } });
  globalThis.fetch = /** @type {any} */ (async (url) => {
    seen.push(url);
    return { ok: true, json: async () => ({}) };
  });
  try {
    await call();
  } finally {
    globalThis.window = prev.window;
    globalThis.fetch = prev.fetch;
  }
  assert.equal(seen.length, 1);
  return seen[0];
}

test('fetchSessions requests the session index with the page query', async () => {
  assert.equal(await fetchURL(() => api.fetchSessions()), '/_claudit/api/sessions');
  assert.equal(await fetchURL(() => api.fetchSessions(), '?since=2026-10-01'),
    '/_claudit/api/sessions?since=2026-10-01');
  assert.equal(await fetchURL(() => api.fetchSessions('c42-1700000000000-abcdef12'), '?since=2026-10-01'),
    '/_claudit/api/sessions?since=c42-1700000000000-abcdef12&since=2026-10-01');
});
test('fetchSessionStory encodes the session and pages by prompt and offset', async () => {
  assert.equal(await fetchURL(() => api.fetchSessionStory('a b/c&d', { prompt: 2, offset: 200 })),
    '/_claudit/api/sessions/story?session=a%20b%2Fc%26d&prompt=2&offset=200');
  assert.equal(await fetchURL(() => api.fetchSessionStory('s1')),
    '/_claudit/api/sessions/story?session=s1&prompt=0&offset=0');
  assert.equal(await fetchURL(() => api.fetchSessionStory('s1', { offset: 400 }), '?project=x'),
    '/_claudit/api/sessions/story?session=s1&prompt=0&offset=400&project=x');
});
test('fetchSessionStory with a turn asks for the page that carries it', async () => {
  assert.equal(await fetchURL(() => api.fetchSessionStory('s1', { turn: 'u/1' })),
    '/_claudit/api/sessions/story?session=s1&turn=u%2F1');
});
test('fetchSessionSummary asks for the session summary with the page query', async () => {
  assert.equal(await fetchURL(() => api.fetchSessionSummary('a b/c&d')),
    '/_claudit/api/sessions/summary?session=a%20b%2Fc%26d');
  assert.equal(await fetchURL(() => api.fetchSessionSummary('s1'), '?project=x'),
    '/_claudit/api/sessions/summary?session=s1&project=x');
});
test('fetchAgentToolFull and fetchAgentTurnFull keep their URLs', async () => {
  assert.equal(api.fetchAgents, undefined);
  assert.equal(await fetchURL(() => api.fetchAgentToolFull('s 1', 'toolu_&1')),
    '/_claudit/api/agents/full?session=s%201&tool=toolu_%261');
  assert.equal(await fetchURL(() => api.fetchAgentTurnFull('s1', 'u/1')),
    '/_claudit/api/agents/full?session=s1&turn=u%2F1');
});
