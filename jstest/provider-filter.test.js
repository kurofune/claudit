import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  PROVIDERS, providerFromSearch, searchWithProvider, providerLabel,
  providerTiles, shareText,
  emptyProviderText, busyPainter,
} from '../web/provider-filter.js';

test('PROVIDERS lists All, Claude Code and Codex in that order', () => {
  assert.deepEqual(PROVIDERS.map(p => [p.id, p.label]),
    [['all', 'All'], ['claude-code', 'Claude Code'], ['codex', 'Codex']]);
});

test('providerFromSearch reads the provider key', () => {
  assert.equal(providerFromSearch(''), 'all');
  assert.equal(providerFromSearch('?since=2026-10-01'), 'all');
  assert.equal(providerFromSearch('?provider=codex'), 'codex');
  assert.equal(providerFromSearch('provider=claude-code&scope=all'), 'claude-code');
});

test('providerFromSearch treats an unknown value as All', () => {
  assert.equal(providerFromSearch('?provider=gemini'), 'all');
});

test('searchWithProvider sets the key and keeps every other key', () => {
  assert.equal(searchWithProvider('', 'codex'), '?provider=codex');
  assert.equal(searchWithProvider('?since=2026-10-01&scope=all', 'claude-code'),
    '?since=2026-10-01&scope=all&provider=claude-code');
  assert.equal(searchWithProvider('?provider=codex&last=7d', 'claude-code'),
    '?provider=claude-code&last=7d');
});

test('searchWithProvider with All removes the key', () => {
  assert.equal(searchWithProvider('?provider=codex', 'all'), '');
  assert.equal(searchWithProvider('?since=2026-10-01&provider=codex', 'all'), '?since=2026-10-01');
});

test('parse and serialize round-trip every option', () => {
  for (const p of PROVIDERS) {
    assert.equal(providerFromSearch(searchWithProvider('?scope=all', p.id)), p.id);
  }
});

test('providerLabel names each provider; unknown ids pass through', () => {
  assert.equal(providerLabel('claude-code'), 'Claude Code');
  assert.equal(providerLabel('codex'), 'Codex');
  assert.equal(providerLabel('other'), 'other');
});

const rows = [
  { provider: 'claude-code', sessions: 2, turns: 3, cost_usd: 0.96, share: 61.2775 },
  { provider: 'codex', sessions: 4, turns: 6, cost_usd: 0.61, share: 38.7225 },
];

test('providerTiles shows every provider under All and only the chosen one otherwise', () => {
  assert.deepEqual(providerTiles(rows, 'all').map(r => r.provider), ['claude-code', 'codex']);
  assert.deepEqual(providerTiles(rows, 'codex').map(r => r.provider), ['codex']);
  assert.deepEqual(providerTiles(rows, 'claude-code').map(r => r.provider), ['claude-code']);
  assert.deepEqual(providerTiles(null, 'all'), []);
});

test('providerTiles drops providers with no sessions', () => {
  assert.deepEqual(providerTiles([{ provider: 'codex', sessions: 0, turns: 0, cost_usd: 0, share: 0 }], 'all'), []);
});

test('shareText rounds to one decimal and the tiles sum to 100%', () => {
  assert.equal(shareText(61.2775), '61.3%');
  assert.equal(shareText(100), '100.0%');
  const sum = providerTiles(rows, 'all').reduce((s, r) => s + r.share, 0);
  assert.ok(Math.abs(sum - 100) < 1e-9);
});

test('emptyProviderText names the provider and where claudit reads it', () => {
  assert.equal(emptyProviderText('codex'), 'No Codex sessions in this range (claudit reads ~/.codex/sessions)');
  assert.equal(emptyProviderText('claude-code'), 'No Claude Code sessions in this range (claudit reads ~/.claude/projects)');
  assert.equal(emptyProviderText('all'), '');
});

function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}

test('busyPainter: an earlier paint settling does not clear the current paint\'s busy', async () => {
  const calls = [];
  const run = busyPainter(b => calls.push(b));
  const overview = deferred();
  const agents = deferred();
  const first = run(() => overview.promise);
  const second = run(() => agents.promise);
  overview.resolve();
  await first;
  assert.deepEqual(calls, [true, true], 'Overview finishing cleared busy while Agents is loading');
  agents.resolve();
  await second;
  assert.deepEqual(calls, [true, true, false]);
});

test('busyPainter: a superseded paint that fails leaves busy on; the current failure clears it', async () => {
  const calls = [];
  const run = busyPainter(b => calls.push(b));
  const errLog = console.error;
  console.error = () => {};
  try {
    const a = deferred();
    const b = deferred();
    const first = run(() => a.promise);
    const second = run(() => b.promise);
    a.reject(new Error('overview down'));
    await first;
    assert.deepEqual(calls, [true, true]);
    b.reject(new Error('agents down'));
    await second;
    assert.deepEqual(calls, [true, true, false]);
  } finally {
    console.error = errLog;
  }
});

test('busyPainter: a lone paint sets busy then clears it', async () => {
  const calls = [];
  const run = busyPainter(b => calls.push(b));
  await run(() => {});
  assert.deepEqual(calls, [true, false]);
});
