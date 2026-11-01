#!/usr/bin/env node
// gen-long-session.mjs <dir> [turns] [subagents] — writes a synthetic
// Claude Code projects root holding one single-prompt session of `turns`
// assistant turns (default 5000), each running one Bash call, with
// `subagents` sub-agents (default 40) launched by Agent calls spread
// evenly through it. Each sub-agent runs 12 turns in its own
// <session>/subagents/agent-<id>.jsonl beside a .meta.json naming the
// spawning tool_use (toolUseId). Timestamps end an hour before now.
// Serve it with:
//   go run ./cmd/claudit serve --root <dir> --open=false --last=
// to load the Sessions list, header and story at scale.
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const [dir, turnsArg, subsArg] = process.argv.slice(2);
if (!dir) {
  console.error('usage: gen-long-session.mjs <dir> [turns] [subagents]');
  process.exit(2);
}
const turns = Number(turnsArg || 5000);
const subs = Number(subsArg || 40);
if (!Number.isInteger(turns) || turns < 1 || !Number.isInteger(subs) || subs < 0 || subs > turns) {
  console.error(`turns must be a positive integer and subagents 0..turns, got ${turnsArg} ${subsArg}`);
  process.exit(2);
}

const SESSION = '5000aaaa-0000-4000-8000-000000000001';
const CWD = '/work/long-session';
const MODEL = 'claude-opus-4-7';
const SUB_TURNS = 12;
const STEP_MS = 2000;
const start = Date.now() - 60 * 60 * 1000 - turns * STEP_MS;
const ts = i => new Date(start + i * STEP_MS).toISOString();

const usage = i => ({
  input_tokens: 40 + (i % 50),
  output_tokens: 120 + (i % 300),
  cache_creation_input_tokens: 200 * (i % 5),
  cache_read_input_tokens: 20000 + 10 * i,
  cache_creation: { ephemeral_5m_input_tokens: 200 * (i % 5), ephemeral_1h_input_tokens: 0 },
});

const base = (extra) => ({ sessionId: SESSION, cwd: CWD, version: '2.1.0', gitBranch: 'main', ...extra });

function assistant(uuid, parentUuid, t, msgId, content, u, sidechain) {
  return base({
    type: 'assistant', uuid, parentUuid, timestamp: t, isSidechain: sidechain,
    message: { id: msgId, model: MODEL, role: 'assistant', type: 'message', content, usage: u },
  });
}

function toolResult(uuid, parentUuid, t, toolId, text, isError, sidechain) {
  return base({
    type: 'user', uuid, parentUuid, timestamp: t, isSidechain: sidechain,
    message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: toolId, content: text, is_error: isError }] },
  });
}

const encoded = CWD.replace(/[^A-Za-z0-9]/g, '-');
const projDir = join(dir, encoded);
const subDir = join(projDir, SESSION, 'subagents');
mkdirSync(subDir, { recursive: true });

const every = subs ? Math.floor(turns / subs) : Infinity;
const main = [];
main.push(JSON.stringify(base({
  type: 'user', uuid: 'p-0', parentUuid: null, timestamp: ts(0), isSidechain: false,
  message: { role: 'user', content: 'Port the whole test suite to the new runner, file by file, and keep it green.' },
})));
let parent = 'p-0';
let spawned = 0;
for (let i = 0; i < turns; i++) {
  const spawn = spawned < subs && i % every === every - 1;
  const toolId = `toolu_main_${i}`;
  const t = ts(i + 1);
  const call = spawn
    ? { type: 'tool_use', id: toolId, name: 'Agent', input: { subagent_type: 'Explore', description: `survey batch ${spawned + 1}`, prompt: `Survey test batch ${spawned + 1} and list every file that still imports the old runner.` } }
    : { type: 'tool_use', id: toolId, name: 'Bash', input: { command: `go test ./pkg/batch${i % 97}/...`, description: `run batch ${i % 97}` } };
  const uuid = `a-${i}`;
  main.push(JSON.stringify(assistant(uuid, parent, t, `msg_main_${i}`, [
    { type: 'text', text: `Step ${i + 1}: ${spawn ? 'hand a batch survey to a sub-agent' : 'run the next batch'}.` },
    call,
  ], usage(i), false)));
  const isError = !spawn && i % 211 === 17;
  const rUuid = `r-${i}`;
  main.push(JSON.stringify(toolResult(rUuid, uuid, ts(i + 1.5), toolId, isError ? 'FAIL\texit status 1' : 'ok', isError, false)));
  parent = rUuid;

  if (spawn) {
    const agentId = `a${String(spawned).padStart(16, '0')}`;
    const lines = [];
    lines.push(JSON.stringify(base({
      type: 'user', uuid: `s${spawned}-p`, parentUuid: null, timestamp: t, isSidechain: true, agentId,
      message: { role: 'user', content: `Survey test batch ${spawned + 1} and list every file that still imports the old runner.` },
    })));
    let sp = `s${spawned}-p`;
    for (let k = 0; k < SUB_TURNS; k++) {
      const st = new Date(Date.parse(t) + (k + 1) * 100).toISOString();
      const sid = `toolu_sub_${spawned}_${k}`;
      const su = `s${spawned}-a${k}`;
      lines.push(JSON.stringify({ ...assistant(su, sp, st, `msg_sub_${spawned}_${k}`, [
        { type: 'tool_use', id: sid, name: 'Grep', input: { pattern: 'oldrunner', path: `pkg/batch${k}` } },
      ], usage(k), true), agentId }));
      const sr = `s${spawned}-r${k}`;
      lines.push(JSON.stringify({ ...toolResult(sr, su, st, sid, `pkg/batch${k}/x_test.go`, false, true), agentId }));
      sp = sr;
    }
    writeFileSync(join(subDir, `agent-${agentId}.jsonl`), lines.join('\n') + '\n');
    writeFileSync(join(subDir, `agent-${agentId}.meta.json`), JSON.stringify({
      agentType: 'Explore', description: `survey batch ${spawned + 1}`, toolUseId: toolId,
    }));
    spawned++;
  }
}
writeFileSync(join(projDir, `${SESSION}.jsonl`), main.join('\n') + '\n');
console.log(`wrote session ${SESSION}: ${turns} turns, ${spawned} sub-agents under ${projDir}`);
