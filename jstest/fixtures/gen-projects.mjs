#!/usr/bin/env node
// gen-projects.mjs <dir> [n] — writes a synthetic Claude Code projects
// root with n distinct cwds (default 1500), one session each, one
// assistant turn per session, timestamps spread over the 30 days before
// now. Serve it with:
//   go run ./cmd/claudit serve --root <dir> --open=false --last=
// to load the Spend view's By project tab at scale.
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const [dir, nArg] = process.argv.slice(2);
if (!dir) {
  console.error('usage: gen-projects.mjs <dir> [n]');
  process.exit(2);
}
const n = Number(nArg || 1500);
if (!Number.isInteger(n) || n < 1) {
  console.error(`n must be a positive integer, got ${nArg}`);
  process.exit(2);
}

const MODELS = ['claude-opus-4-7', 'claude-sonnet-4-6', 'claude-haiku-4-5'];
const DAY_MS = 24 * 60 * 60 * 1000;
const now = Date.now();

for (let i = 0; i < n; i++) {
  const cwd = `/work/proj-${String(i).padStart(4, '0')}`;
  const encoded = cwd.replace(/[^A-Za-z0-9]/g, '-');
  const session = `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`;
  const ts = new Date(now - (i % 30) * DAY_MS - (i % 24) * 60 * 60 * 1000).toISOString();
  const line = {
    type: 'assistant',
    uuid: `a-${i}`,
    parentUuid: '',
    timestamp: ts,
    sessionId: session,
    cwd,
    message: {
      model: MODELS[i % MODELS.length],
      role: 'assistant',
      content: [{ type: 'text', text: 'x' }],
      usage: {
        input_tokens: 100 + i,
        output_tokens: 200 + 3 * i,
        cache_creation_input_tokens: 50 * (i % 7),
        cache_read_input_tokens: 1000 * (i % 11),
        cache_creation: { ephemeral_5m_input_tokens: 50 * (i % 7), ephemeral_1h_input_tokens: 0 },
      },
    },
  };
  const sub = join(dir, encoded);
  mkdirSync(sub, { recursive: true });
  writeFileSync(join(sub, `${session}.jsonl`), JSON.stringify(line) + '\n');
}
console.log(`wrote ${n} projects under ${dir}`);
