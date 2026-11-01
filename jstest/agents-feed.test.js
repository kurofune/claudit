import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderControl } from '../web/agents-feed.js';
import { setLastGraph } from '../web/agents-shared.js';

// A running Codex session and a running headless Claude Code session: the
// pinned live rows must carry the same provider/origin tags as history rows.
const GRAPH = {
  sessions: [
    {
      session_id: 'codex-1', cwd: '/home/u/cx', provider: 'codex',
      main: {
        kind: 'main', status: 'running', started_at: '2026-05-01T12:00:00Z',
        steps: [{ timestamp: '2026-05-01T12:00:10Z', tools: [{ name: 'exec', kind: 'exec' }] }],
        current_tool: 'exec',
      },
      children: [],
    },
    {
      session_id: 'claude-1', cwd: '/home/u/cc', provider: 'claude-code', entrypoint: 'sdk-cli',
      main: {
        kind: 'main', status: 'running', started_at: '2026-05-01T12:00:05Z',
        steps: [{ timestamp: '2026-05-01T12:00:12Z', tools: [{ name: 'Read', kind: 'read' }] }],
        current_tool: 'Read',
      },
      children: [],
    },
  ],
};

function liveRows(html) {
  const start = html.indexOf('<div class="feed-live">');
  const end = html.indexOf('<div class="fe-row', start);
  const live = html.slice(start, end < 0 ? html.length : end);
  return live.split('<div class="live-row').slice(1);
}

test('a running Codex session renders its live row with the codex badge', () => {
  setLastGraph(GRAPH);
  const rows = liveRows(renderControl(GRAPH.sessions));
  assert.equal(rows.length, 2);
  const codex = rows.find(r => r.includes('codex-1'));
  const claude = rows.find(r => r.includes('claude-1'));
  assert.ok(codex, 'no live row for the Codex session');
  assert.match(codex, /<span class="fe-provider" title="Codex session">codex<\/span>/);
  assert.ok(claude, 'no live row for the Claude Code session');
  assert.doesNotMatch(claude, /fe-provider/);
  assert.match(claude, /<span class="fe-origin"[^>]*>sdk<\/span>/);
});
