#!/usr/bin/env node
// gen-live-write.mjs <session.jsonl> [intervalMs] [count] — plays a live
// session: appends one assistant turn to the given Claude Code log every
// intervalMs (default 1000) until killed, or count turns when given. Each
// turn takes its sessionId and cwd from the log's last line that has them
// and chains its parentUuid to the line before, so the server's poller sees
// the log grow and bumps the corpus generation every tick. Pair it with a
// `claudit serve` over a copy of the corpus to measure requests while
// logs write:
//   node jstest/fixtures/gen-live-write.mjs "$d/projects/<dir>/<id>.jsonl"
import { appendFileSync, readFileSync } from 'node:fs';

const [file, intervalArg, countArg] = process.argv.slice(2);
if (!file) {
  console.error('usage: gen-live-write.mjs <session.jsonl> [intervalMs] [count]');
  process.exit(2);
}
const interval = Number(intervalArg || 1000);
const count = countArg === undefined ? Infinity : Number(countArg);
if (!(interval > 0) || !(count > 0)) {
  console.error(`intervalMs and count must be positive, got ${intervalArg} ${countArg}`);
  process.exit(2);
}

let sessionId = '', cwd = '', parent = null;
for (const line of readFileSync(file, 'utf8').split('\n')) {
  if (!line.trim()) continue;
  try {
    const v = JSON.parse(line);
    if (v.sessionId) sessionId = v.sessionId;
    if (v.cwd) cwd = v.cwd;
    if (v.uuid) parent = v.uuid;
  } catch {
    // A torn or foreign line: keep what earlier lines said.
  }
}
if (!sessionId) {
  console.error(`${file} carries no sessionId to continue`);
  process.exit(1);
}

const run = `live-${process.pid}-${Date.now()}`;
let n = 0;
/** @type {ReturnType<typeof setInterval> | undefined} */
let timer;
const tick = () => {
  const uuid = `${run}-${n}`;
  appendFileSync(file, JSON.stringify({
    type: 'assistant', uuid, parentUuid: parent, timestamp: new Date().toISOString(), sessionId, cwd,
    isSidechain: false, version: '2.1.0',
    message: {
      id: `msg_${run}_${n}`, model: 'claude-opus-4-7', role: 'assistant', type: 'message',
      content: [{ type: 'text', text: `Live turn ${n + 1}.` }],
      usage: { input_tokens: 20, output_tokens: 40, cache_creation_input_tokens: 0, cache_read_input_tokens: 1000,
        cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 0 } },
    },
  }) + '\n');
  parent = uuid;
  n++;
  if (n >= count) {
    clearInterval(timer);
    console.log(`appended ${n} turns to ${file}`);
  }
};
tick();
if (n < count) timer = setInterval(tick, interval);
