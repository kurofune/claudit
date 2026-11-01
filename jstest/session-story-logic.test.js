process.env.TZ = 'UTC';

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  storyRows, groupCommands, unresolvedTools, unresolvedCount, idleGaps, idleText,
  nestChildren, parallelHeader, parallelClusters, childHeader, pageMath, moreTurns,
  searchSteps, searchEntries, hiddenNotice, splitWrappers, unwrapExit, fmtDuration,
} from '../web/session-story-logic.js';

const T0 = Date.parse('2026-11-01T09:00:00Z');
const at = sec => new Date(T0 + sec * 1000).toISOString();

const tool = (id, input, status, startSec, endSec, extra = {}) => ({
  id, name: 'Bash', detail: input.split(' ')[0], kind: 'exec', input, status, output: `out of ${input}`, count: 1,
  started_at: at(startSec), ended_at: at(endSec), exit_code: null, ...extra,
});
const step = (uuid, sec, f = {}) => ({
  uuid, timestamp: at(sec), model: 'claude-opus-5-5', cost_usd: 0, tokens: {}, context_tokens: 0,
  duration_ms: 0, gen_ms: 0, tools: [], ...f,
});

test('storyRows: prompt, agent messages and groups in timestamp order, text verbatim', () => {
  const long = 'x'.repeat(3000);
  const steps = [
    step('a1', 5, { text: 'First I will look.', tools: [tool('t1', 'ls', 'ok', 5, 6)] }),
    step('a2', 10, { text: long }),
    step('a3', 20, { text: 'Done.\nSecond line.' }),
  ];
  const rows = storyRows({ uuid: 'u1', text: 'fix the bug\nplease', timestamp: at(0), turns: 3 }, steps);
  assert.deepEqual(rows.map(r => r.kind), ['prompt', 'agent', 'group', 'agent', 'agent']);
  assert.equal(rows[0].clock, '09:00:00');
  assert.equal(rows[0].text, 'fix the bug\nplease');
  assert.equal(rows[1].clock, '09:00:05');
  assert.equal(rows[1].text, 'First I will look.');
  assert.equal(rows[3].text, long);
  assert.equal(rows[4].text, 'Done.\nSecond line.');
});

test('storyRows: two prompts are two pages, each its own row sequence', () => {
  const p1 = storyRows({ uuid: 'u1', text: 'one', timestamp: at(0), turns: 1 }, [step('a1', 1, { text: 'A' })]);
  const p2 = storyRows({ uuid: 'u2', text: 'two', timestamp: at(100), turns: 2 },
    [step('b1', 101, { text: 'B' }), step('b2', 102, { text: 'C' })]);
  assert.deepEqual(p1.map(r => `${r.kind}:${r.clock}:${r.text}`), ['prompt:09:00:00:one', 'agent:09:00:01:A']);
  assert.deepEqual(p2.map(r => `${r.kind}:${r.clock}:${r.text}`),
    ['prompt:09:01:40:two', 'agent:09:01:41:B', 'agent:09:01:42:C']);
});

test('splitWrappers keeps the written text and the raw wrapper blocks apart', () => {
  const raw = '# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>\nbe nice\n</INSTRUCTIONS>\n<environment_context>\n  <cwd>/repo</cwd>\n</environment_context>\nmake it faster';
  const { visible, hidden } = splitWrappers(raw);
  assert.equal(visible, 'make it faster');
  assert.equal(hidden, '# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>\nbe nice\n</INSTRUCTIONS>\n\n<environment_context>\n  <cwd>/repo</cwd>\n</environment_context>');
  assert.deepEqual(splitWrappers('plain'), { visible: 'plain', hidden: '' });
  const caveat = splitWrappers('<local-command-caveat>Caveat: x</local-command-caveat>\n<system-reminder>r</system-reminder>hi');
  assert.equal(caveat.visible, 'hi');
  assert.equal(caveat.hidden, '<local-command-caveat>Caveat: x</local-command-caveat>\n\n<system-reminder>r</system-reminder>');
  const prose = 'FamilyKey strips <local-command-caveat>, <system-reminder> and <command-name> blocks.\nSecond line.';
  assert.deepEqual(splitWrappers(prose), { visible: prose, hidden: '' });
  assert.deepEqual(splitWrappers('hi\n<system-reminder>unclosed\nmore'), { visible: 'hi', hidden: '<system-reminder>unclosed\nmore' });
});

test('groupCommands: tool-only steps join the group of the message before them', () => {
  const steps = [
    step('a0', 0, { tools: [tool('t0', 'pwd', 'ok', 0, 1)] }),
    step('a1', 2, { text: 'Look', tools: [tool('t1', 'ls', 'ok', 2, 3)] }),
    step('a2', 4, { tools: [tool('t2', 'cat x', 'ok', 4, 5)] }),
    step('a3', 6, { text: 'Now' }),
    step('a4', 7, { tools: [tool('t4', 'rg y', 'ok', 7, 8)] }),
  ];
  assert.deepEqual(groupCommands(steps), [
    { lead: -1, steps: [0] }, { lead: 1, steps: [1, 2] }, { lead: 3, steps: [3, 4] },
  ]);
});

test('group header: 7 commands, 2 unresolved, 1m 12s', () => {
  const steps = [
    step('a1', 0, { text: 'Running checks', cost_usd: 0.1, tools: [
      tool('t1', 'go test ./a', 'error', 0, 10), tool('t2', 'go vet', 'ok', 10, 12)] }),
    step('a2', 14, { cost_usd: 0.2, tools: [tool('t3', 'go test ./a', 'error', 14, 20)] }),
    step('a3', 22, { cost_usd: 0.3, tools: [tool('t4', 'go test ./a', 'ok', 22, 30), tool('t5', 'rg foo', 'error', 30, 31)] }),
    step('a4', 40, { cost_usd: 0.4, tools: [tool('t6', 'cat y', 'error', 40, 41), tool('t7', 'ls', undefined, 41, 72)] }),
  ];
  const unresolved = unresolvedTools(steps);
  assert.deepEqual([...unresolved].map(t => t.id), ['t6', 't5']);
  assert.equal(unresolvedCount(steps.flatMap(s => s.tools), unresolved), 2);
  const rows = storyRows(null, steps);
  assert.deepEqual(rows.map(r => r.kind), ['agent', 'group']);
  const g = rows[1];
  assert.equal(g.header.text, '7 commands · 2 unresolved · 1m 12s');
  assert.equal(g.header.cost.toFixed(2), '1.00');
  const marks = g.items.map(i => i.mark).join('');
  assert.equal(marks, '✕✓✕✓✕✕…');
  const first = g.items[0];
  assert.equal(first.clock, '09:00:00');
  assert.equal(first.label, 'go test ./a');
  assert.equal(first.durationMs, 10000);
});

test('group header omits unresolved when every error later succeeded', () => {
  const steps = [step('a1', 0, { text: 'go', tools: [tool('t1', 'make', 'error', 0, 2), tool('t2', 'make', 'ok', 3, 5)] })];
  assert.equal(storyRows(null, steps)[1].header.text, '2 commands · 5s');
});

test('step row: one-line command, full input in the title', () => {
  const t = tool('t1', 'cd /x &&\n  go test\t./...', 'ok', 0, 1);
  const item = storyRows(null, [step('a1', 0, { text: 'hi', tools: [t] })])[1].items[0];
  assert.equal(item.label, 'cd /x && go test ./...');
  assert.equal(item.title, 'cd /x &&\n  go test\t./...');
});

test('idle gaps: 5m01s and 42m render, 4m59s does not', () => {
  assert.equal(idleText(5 * 60000 + 1000), '5 min idle');
  assert.equal(idleText(4 * 60000 + 59000), '');
  assert.equal(idleText(42 * 60000), '42 min idle');
  const steps = [step('a1', 0, { text: 'a' }), step('a2', 301, { text: 'b' }), step('a3', 600, { text: 'c' }), step('a4', 600 + 42 * 60, { text: 'd' })];
  assert.deepEqual(idleGaps(steps), ['', '5 min idle', '', '42 min idle']);
  const rows = storyRows(null, steps);
  assert.deepEqual(rows.map(r => r.kind === 'idle' ? r.text : r.kind), ['agent', '5 min idle', 'agent', 'agent', '42 min idle', 'agent']);
});

test('idle gap counts from the end of the previous turn\'s last call', () => {
  const steps = [step('a1', 0, { text: 'run', tools: [tool('t1', 'sleep', 'ok', 0, 200)] }), step('a2', 200 + 299, { text: 'b' })];
  assert.deepEqual(idleGaps(steps), ['', '']);
});

const child = (f) => ({ kind: 'subagent', agent_type: 'Explore', description: 'find callers', parent_tool_use_id: '',
  started_at: at(0), ended_at: at(83), cost_usd: 0.42, error_count: 0, steps: [step('c1', 1), step('c2', 2)], ...f });

test('sub-agent: nests under the step whose tool spawned it', () => {
  const steps = [step('a1', 0, { text: 'spawn', tools: [tool('ag1', 'Agent', 'ok', 0, 90, { name: 'Agent' })] }), step('a2', 100, { text: 'after' })];
  const c = child({ parent_tool_use_id: 'ag1', started_at: at(1), ended_at: at(84) });
  const { byStep, before } = nestChildren(steps, [c]);
  assert.deepEqual([...byStep.keys()], [0]);
  assert.equal(before.size, 0);
  assert.equal(childHeader(c), 'Explore · find callers · $0.42 · 1m 23s · 2 steps');
  const g = storyRows(null, steps, [c])[1];
  assert.deepEqual(g.items.map(i => i.kind), ['step', 'child']);
  assert.equal(g.items[1].header, 'Explore · find callers · $0.42 · 1m 23s · 2 steps');
  assert.equal(g.items[1].note, '');
});

test('sub-agent: an empty parent id places it by started_at, labelled parent step unknown', () => {
  const steps = [step('a1', 0, { text: 'one' }), step('a2', 50, { text: 'two' }), step('a3', 100, { text: 'three' })];
  const c = child({ started_at: at(60), ended_at: at(70) });
  const rows = storyRows(null, steps, [c]);
  assert.deepEqual(rows.map(r => r.kind), ['agent', 'agent', 'child', 'agent']);
  assert.equal(rows[2].note, 'parent step unknown');
});

test('sub-agents: overlapping spans share a parallel header', () => {
  const steps = [step('a1', 0, { text: 'fan out', tools: [
    tool('ag1', 'Agent one', 'ok', 0, 100, { name: 'Agent' }), tool('ag2', 'Agent two', 'ok', 0, 100, { name: 'Agent' })] })];
  const a = child({ parent_tool_use_id: 'ag1', description: 'scan web', started_at: at(1), ended_at: at(40) });
  const b = child({ parent_tool_use_id: 'ag2', description: 'scan api', started_at: at(2), ended_at: at(2 + 3 * 60 + 5) });
  assert.equal(parallelClusters([a, b]).length, 1);
  assert.equal(parallelHeader([a, b]), '2 agents in parallel · longest: scan api 3m 5s');
  const items = storyRows(null, steps, [a, b])[1].items;
  assert.deepEqual(items.map(i => i.kind), ['step', 'step', 'parallel']);
  assert.equal(items[2].header, '2 agents in parallel · longest: scan api 3m 5s');
  const c = child({ description: 'later', started_at: at(500), ended_at: at(510) });
  assert.equal(parallelClusters([a, b, c]).length, 2);
});

test('sub-agents spawned by one tool whose spans overlap share a parallel header', () => {
  const steps = [step('a1', 0, { text: 'fan out', tools: [
    tool('ag1', 'Agent one', 'ok', 0, 100, { name: 'Agent' }), tool('ag2', 'Agent two', 'ok', 0, 100, { name: 'Agent' })] })];
  const a = child({ parent_tool_use_id: 'ag1', description: 'scan web', started_at: at(1), ended_at: at(40) });
  const b = child({ parent_tool_use_id: 'ag1', description: 'scan api', started_at: at(2), ended_at: at(2 + 3 * 60 + 5) });
  const items = storyRows(null, steps, [a, b])[1].items;
  assert.deepEqual(items.map(i => i.kind), ['step', 'step', 'parallel']);
  assert.equal(items[2].header, '2 agents in parallel · longest: scan api 3m 5s');
  assert.deepEqual(items[2].children.map(x => x.child.description), ['scan web', 'scan api']);
});

test('pageMath: a 5,000-turn prompt is 25 pages of 200', () => {
  const pages = pageMath(5000);
  assert.equal(pages.length, 25);
  assert.ok(pages.every(p => p.limit === 200));
  assert.deepEqual(pages.map(p => p.offset), Array.from({ length: 25 }, (_, i) => i * 200));
  assert.deepEqual(pageMath(450).map(p => [p.offset, p.limit]), [[0, 200], [200, 200], [400, 50]]);
  assert.deepEqual(pageMath(0), []);
});

test('moreTurns: turns left past the loaded ones and the loaded cost', () => {
  const loaded = Array.from({ length: 200 }, (_, i) => step(`a${i}`, i, { cost_usd: 0.01 }));
  assert.deepEqual(moreTurns(5000, 200, loaded), { n: 4800, cost: loaded.reduce((a, s) => a + s.cost_usd, 0), text: '4800 more turns · $2.00 so far' });
  assert.equal(moreTurns(200, 200, loaded).text, '');
});

test('hiddenNotice discloses the filter and replay counts, nothing at zero', () => {
  assert.deepEqual(hiddenNotice({ outside_filter: 12, replays: 3 }), [
    '12 earlier turns outside the date filter', '3 replayed turns from a resumed session folded']);
  assert.deepEqual(hiddenNotice({ outside_filter: 0, replays: 0 }), []);
  assert.deepEqual(hiddenNotice(null), []);
});

test('searchSteps: next match after the cursor, case-insensitive, wrapping; -1 for none', () => {
  const steps = [
    step('a1', 1, { text: 'Look around', tools: [tool('t1', 'go test ./...', 'ok', 1, 2), tool('t2', 'rg Foo', 'ok', 2, 3)] }),
    step('a2', 4, { tools: [tool('t3', 'GO TEST ./x', 'error', 4, 5)] }),
  ];
  const entries = searchEntries(storyRows({ uuid: 'u', text: 'please run go test', timestamp: at(0), turns: 2 }, steps));
  assert.deepEqual(entries.map(e => e.key), ['p', 'a0', 's0.0', 's0.1', 's1.0']);
  assert.equal(searchSteps(entries, 'go test'), 0);
  assert.equal(searchSteps(entries, 'go test', 0), 2);
  assert.equal(searchSteps(entries, 'go test', 2), 4);
  assert.equal(searchSteps(entries, 'go test', 4), 0);
  assert.equal(searchSteps(entries, 'foo'), 3);
  assert.equal(searchSteps(entries, 'nothing here'), -1);
  assert.equal(searchSteps(entries, '  '), -1);
});

test('unwrapExit drops the Codex exit-code header lines from full output', () => {
  assert.equal(unwrapExit('Exit code: 1\nWall time: 0.2 seconds\nOutput:\nboom\n'), 'boom\n');
  assert.equal(unwrapExit('Process exited with code 2\nOutput:\nx'), 'x');
  assert.equal(unwrapExit('plain output\nOutput:\nmore'), 'plain output\nOutput:\nmore');
  assert.equal(unwrapExit('no envelope'), 'no envelope');
});

test('fmtDuration reads spans', () => {
  assert.equal(fmtDuration(72000), '1m 12s');
  assert.equal(fmtDuration(42000), '42s');
  assert.equal(fmtDuration(250), '250ms');
  assert.equal(fmtDuration(0), '0s');
  assert.equal(fmtDuration(2 * 3600000 + 5 * 60000), '2h 5m');
});

test('a call stamped as ending before it started reads 0s; one with no stamps reads its turn\'s gap', () => {
  const early = tool('t1', 'ls', 'ok', 10, 9.97);
  const unstamped = { ...tool('t2', 'pwd', 'ok', 0, 0), started_at: undefined, ended_at: undefined };
  const g = storyRows(null, [step('a1', 10, { text: 'x', duration_ms: 11000, tools: [early, unstamped] })])[1];
  assert.deepEqual(g.items.map(i => i.durationMs), [0, 11000]);
  assert.equal(g.header.text, '2 commands · 11s');
});

test('sub-agents with unknown parents that overlap share one header even when they start around different steps', () => {
  const steps = [step('a1', 0, { text: 'one' }), step('a2', 10, { text: 'two' }), step('a3', 200, { text: 'three' })];
  const a = child({ description: 'a', started_at: at(5), ended_at: at(500) });
  const b = child({ description: 'b', started_at: at(20), ended_at: at(30) });
  const rows = storyRows(null, steps, [a, b]);
  assert.deepEqual(rows.map(r => r.kind), ['agent', 'parallel', 'agent', 'agent']);
  assert.equal(rows[1].header, '2 agents in parallel · longest: a 8m 15s');
  assert.deepEqual(rows[1].children.map(x => x.note), ['parent step unknown', 'parent step unknown']);
});
