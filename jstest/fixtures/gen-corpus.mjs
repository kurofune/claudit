#!/usr/bin/env node
// gen-corpus.mjs <dir> [sessions] — writes a synthetic Claude Code projects
// root of `sessions` sessions (default 3000) beside whatever <dir> already
// holds. Each session is one prompt and 40 assistant turns, every other
// one running a Bash call (20 calls, one in 37 failing), asked as one of
// 90 prompt families with a per-session bead id the family key blanks.
// Sessions spread over 30 project directories and start at random
// instants in the last 7 days, so a 7-day window holds them all. Use it
// beside a copy of a real corpus to measure the server at 3x volume:
//   node jstest/fixtures/gen-corpus.mjs "$d/projects" 3000
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const [dir, nArg] = process.argv.slice(2);
if (!dir) {
  console.error('usage: gen-corpus.mjs <dir> [sessions]');
  process.exit(2);
}
const n = Number(nArg || 3000);
if (!Number.isInteger(n) || n < 1) {
  console.error(`sessions must be a positive integer, got ${nArg}`);
  process.exit(2);
}

const TURNS = 40;
const FAMILIES = 90;
const PROJECTS = 30;
const MODELS = ['claude-opus-4-7', 'claude-sonnet-4-6', 'claude-haiku-4-5'];
const STEP_MS = 4000;
const WEEK_MS = 7 * 24 * 60 * 60 * 1000;
const now = Date.now();

// A small deterministic PRNG, so two runs write the same corpus.
let seed = 0x5eed;
const rand = () => ((seed = (seed * 1103515245 + 12345) >>> 0) / 2 ** 32);

const hex = (v, len) => v.toString(16).padStart(len, '0').slice(-len);
const sessionId = i => `${hex(0xc0de0000 + i, 8)}-${hex(i, 4)}-4000-8000-${hex(i * 7919, 12)}`;

const verbs = ['Implement', 'Review', 'Fix', 'Refactor', 'Document', 'Test', 'Profile', 'Triage', 'Port'];
const nouns = ['the session list', 'the story pager', 'the cost tiles', 'the overview chart', 'the corpus cache',
  'the codex parser', 'the price table', 'the replay dedup', 'the SSE stream', 'the date picker'];
const family = f => `${verbs[f % verbs.length]} ${nouns[Math.floor(f / verbs.length) % nouns.length]}`;

const usage = (i, k) => ({
  input_tokens: 30 + (k % 40),
  output_tokens: 100 + ((i + k) % 400),
  cache_creation_input_tokens: 300 * (k % 4),
  cache_read_input_tokens: 15000 + 400 * k,
  cache_creation: { ephemeral_5m_input_tokens: 300 * (k % 4), ephemeral_1h_input_tokens: 0 },
});

let written = 0;
for (let i = 0; i < n; i++) {
  const sid = sessionId(i);
  const f = i % FAMILIES;
  const cwd = `/work/gen-project-${i % PROJECTS}`;
  const projDir = join(dir, cwd.replace(/[^A-Za-z0-9]/g, '-'));
  mkdirSync(projDir, { recursive: true });
  const model = MODELS[f % MODELS.length];
  const start = now - Math.floor(rand() * (WEEK_MS - TURNS * STEP_MS - 60_000)) - TURNS * STEP_MS - 60_000;
  const ts = k => new Date(start + k * STEP_MS).toISOString();
  const base = extra => ({ sessionId: sid, cwd, version: '2.1.0', gitBranch: 'main', ...extra });

  const lines = [JSON.stringify(base({
    type: 'user', uuid: `${sid}-p`, parentUuid: null, timestamp: ts(0), isSidechain: false,
    message: { role: 'user', content: `${family(f)} for bead gen-${hex(i, 5)}: keep the tests green.` },
  }))];
  let parent = `${sid}-p`;
  for (let k = 0; k < TURNS; k++) {
    const uuid = `${sid}-a${k}`;
    const content = [{ type: 'text', text: `Step ${k + 1} of ${family(f).toLowerCase()}.` }];
    const toolId = `toolu_${hex(i, 6)}_${k}`;
    const calls = k % 2 === 0;
    if (calls) content.push({ type: 'tool_use', id: toolId, name: 'Bash', input: { command: `go test ./pkg/p${k % 13}/...` } });
    lines.push(JSON.stringify(base({
      type: 'assistant', uuid, parentUuid: parent, timestamp: ts(k + 1), isSidechain: false,
      message: { id: `msg_${hex(i, 6)}_${k}`, model, role: 'assistant', type: 'message', content, usage: usage(i, k) },
    })));
    parent = uuid;
    if (calls) {
      const isError = (i + k) % 37 === 0;
      const r = `${sid}-r${k}`;
      lines.push(JSON.stringify(base({
        type: 'user', uuid: r, parentUuid: uuid, timestamp: new Date(Date.parse(ts(k + 1)) + 1500).toISOString(), isSidechain: false,
        message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: toolId, content: isError ? 'FAIL\texit status 1' : 'ok', is_error: isError }] },
      })));
      parent = r;
    }
  }
  writeFileSync(join(projDir, `${sid}.jsonl`), lines.join('\n') + '\n');
  written++;
}
console.log(`wrote ${written} sessions of ${TURNS} turns across ${FAMILIES} prompt families and ${PROJECTS} projects under ${dir}`);
