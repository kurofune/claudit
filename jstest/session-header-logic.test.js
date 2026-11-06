process.env.TZ = 'UTC';

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  headerNumbers, stripBars, worthALook, explainPrompt, sessionOf, sessionTurns, unresolvedFailures, MAX_BARS,
  failureRows, failuresHeading, headerFamilyStats, comparisonLine,
} from '../web/session-header-logic.js';

const T0 = Date.parse('2026-05-01T10:00:00Z');
const at = sec => new Date(T0 + sec * 1000).toISOString();

// A story tool in the /sessions/story shape (agentflow.StoryTool).
const tool = (input, status, extra = {}) => ({
  name: 'Bash', detail: input.split(' ')[0], kind: 'exec', input, status, count: 1, output: '',
  input_key: '', exit_code: null, ...extra,
});
const tokens = (i = 0, o = 0, c5 = 0, c1 = 0, r = 0) =>
  ({ InputTokens: i, OutputTokens: o, CacheCreate5mTokens: c5, CacheCreate1hTokens: c1, CacheReadTokens: r });
const step = (uuid, sec, tools = [], extra = {}) => ({
  uuid, timestamp: at(sec), model: 'claude-opus-4-7', cost_usd: 0.01, tokens: tokens(), context_tokens: 0,
  duration_ms: 1000, gen_ms: 0, tools, context_cost_usd: 0, ...extra,
});
const child = (agentType, steps) => ({
  kind: 'subagent', agent_type: agentType, description: '', parent_tool_use_id: '', started_at: steps[0].timestamp,
  ended_at: steps[steps.length - 1].timestamp, cost_usd: 0, error_count: 0, steps,
});
const story = (steps, children = [], extra = {}) => ({
  session_id: 's1', title: 'fix the login bug', project: 'claudit', provider: 'claude-code', model: 'claude-opus-4-7',
  started_at: steps[0].timestamp, last_activity: steps[steps.length - 1].timestamp,
  steps, children, source_files: ['/logs/s1.jsonl'], ...extra,
});

test('headerNumbers sums cost, turns, commands and tokens across main and sub-agents', () => {
  const st = story([
    step('a1', 0, [tool('go build ./...', 'error'), tool('rg foo', 'ok', { count: 3 })],
      { cost_usd: 0.25, tokens: tokens(100, 50, 1000, 200, 3000) }),
    step('a2', 30, [tool('go build ./...', 'ok')], { cost_usd: 0.5, tokens: tokens(10, 5, 0, 0, 4000) }),
    step('a3', 90, [tool('npm test', 'error'), tool('make lint', 'error'), tool('go vet', 'error')],
      { cost_usd: 1, tokens: tokens(1, 1, 0, 0, 0) }),
  ], [child('Explore', [step('k1', 10, [tool('go build ./...', 'error')], { cost_usd: 0.125, tokens: tokens(4, 4, 0, 0, 0) })])],
  { started_at: at(-5), last_activity: at(115) });

  const n = headerNumbers(st);
  assert.equal(n.costUSD, 1.875);
  assert.equal(n.durationMs, 120000);
  assert.equal(n.turns, 4);
  assert.equal(n.commands, 9, 'count 3 on rg counts three commands');
  assert.equal(n.durationSub, '4 turns · 9 commands');
  assert.equal(n.tokens, (100 + 50 + 1000 + 200 + 3000) + (10 + 5 + 4000) + 2 + 8);
  // re-read = 7000 over input+writes+reads = 115 + 1200 + 7000.
  assert.equal(n.cachePct, Math.round(100 * 7000 / (100 + 10 + 1 + 4 + 1200 + 7000)));
  assert.equal(n.tokensSub, '84% re-read from cache');
  // The main agent's go build recovered; the sub-agent's did not (same
  // agent only), and the last turn's three failures never recovered.
  assert.equal(n.failed, 4);
  assert.equal(n.failedSub, 'go build ./..., npm test +2 more');
});

test('headerNumbers of a session with no tool calls reads 0 failed', () => {
  const n = headerNumbers(story([step('a1', 0), step('a2', 5)]));
  assert.equal(n.failed, 0);
  assert.equal(n.failedSub, 'none');
  assert.equal(n.commands, 0);
  assert.equal(n.cachePct, 0);
});

test('unresolvedFailures keys on name and input, else detail', () => {
  const st = story([
    step('a1', 0, [tool('', 'error', { name: 'Read', detail: '.go' })]),
    step('a2', 1, [tool('', 'ok', { name: 'Read', detail: '.go' }), tool('ls', 'error')]),
    step('a3', 2, [tool('ls -la', 'ok')]),
  ]);
  assert.deepEqual(unresolvedFailures(st).map(f => f.tool.input), ['ls']);
});

test('stripBars: one bar per turn in timestamp order, heights by context cost or duration, failed turns marked', () => {
  const st = story([
    step('a1', 0, [tool('go test ./...', 'ok')], { context_cost_usd: 0.2, duration_ms: 4000 }),
    step('a2', 20, [tool('npm test', 'error')], { context_cost_usd: 0.05, duration_ms: 1000 }),
  ], [child('Explore', [step('k1', 10, [tool('rg login', 'ok')], { context_cost_usd: 0.1, duration_ms: 8000 })])]);

  const cost = stripBars(st, 'cost');
  assert.deepEqual(cost.map(b => [b.first, b.value, b.heightPct, b.failed, b.link]), [
    [1, 0.2, 100, false, 'a1'],
    [2, 0.1, 50, false, 'k1'],
    [3, 0.05, 25, true, 'a2'],
  ]);
  assert.equal(cost[0].tip, 'Turn 1 · 10:00:00 · $0.20 added to context · go test ./...');
  assert.equal(cost[1].tip, 'Turn 2 · 10:00:10 · Explore · $0.10 added to context · rg login');

  const time = stripBars(st, 'time');
  assert.deepEqual(time.map(b => b.heightPct), [50, 100, 12.5]);
  assert.equal(time[1].tip, 'Turn 2 · 10:00:10 · Explore · 8s · rg login');
  assert.ok(!cost.some(b => / · \$.*\$/.test(b.tip)), 'one dollar figure per turn, never per command');
});

test('stripBars bins a 5,000-turn story into 120 bars of 42 turns', () => {
  const steps = [];
  for (let i = 0; i < 5000; i++) {
    steps.push(step(`t${i + 1}`, i, [tool(`echo ${i}`, 'ok')], { context_cost_usd: 0.001, duration_ms: 500 }));
  }
  steps[44].tools = [tool('make deploy', 'error')];
  const st = story(steps);
  const bars = stripBars(st, 'cost');
  assert.equal(bars.length, MAX_BARS);
  assert.equal(bars[0].first, 1);
  assert.equal(bars[0].last, 42);
  assert.equal(bars[119].first, 4999);
  assert.equal(bars[119].last, 5000);
  assert.ok(Math.abs(bars[1].value - 0.042) < 1e-9);
  assert.equal(bars[1].tip, 'turns 43–84 · $0.04 added to context');
  assert.equal(bars[1].link, 't43', 'a bin jumps to its first turn');
  assert.equal(bars[1].failed, true);
  assert.equal(bars[0].failed, false);
  const time = stripBars(st, 'time');
  assert.equal(time[1].value, 42 * 500);
  assert.equal(time[1].tip, 'turns 43–84 · 21s');
});

const sessionFrom = st => sessionOf(st);

test('worthALook: clean session has no notes', () => {
  const st = story([
    step('a1', 0, [tool('git status', 'ok')]),
    step('a2', 5, [tool('go test ./...', 'ok')]),
    step('a3', 10),
  ]);
  assert.deepEqual(worthALook(sessionFrom(st), T0 + 3600e3), []);
});

test('worthALook: a session that ends on failed commands puts that note first', () => {
  const st = story([
    step('a1', 0, [tool('rg login', 'ok')], { cost_usd: 2 }),
    step('a2', 5, [tool('git diff', 'ok')]),
    step('a3', 10, [tool('npm test', 'error'), tool('make lint', 'error'), tool('go vet ./...', 'error')]),
  ]);
  const notes = worthALook(sessionFrom(st), T0 + 15e3);
  assert.deepEqual(notes.map(n => n.kind), ['error-cascade', 'cost-whale'],
    'the cascade sorts below the whale by severity, but the session ended on it');
  assert.equal(notes[0].head, '3 tools errored back-to-back, starting with Bash');
  assert.equal(notes[0].body, 'At 10:00:10 · npm test · 3 consecutive tool errors starting with Bash — threshold 3');
  assert.equal(notes[0].turn, 3);
  assert.equal(notes[0].link, 'a3');
  assert.equal(notes[1].turn, 1);
});

test('worthALook: a failure the same command later fixed leaves no failure note', () => {
  const st = story([
    step('a1', 0, [tool('npm test', 'error'), tool('make lint', 'error'), tool('go vet ./...', 'error')]),
    step('a2', 5, [tool('npm test', 'ok'), tool('make lint', 'ok'), tool('go vet ./...', 'ok')]),
  ]);
  assert.deepEqual(worthALook(sessionFrom(st), T0 + 10e3), []);
});

test('worthALook: a TDD loop (go test errors four times, then passes) has no note at all', () => {
  const st = story([
    step('a1', 0, [tool('go test ./...', 'error')]),
    step('a2', 5, [tool('go test ./...', 'error')]),
    step('a3', 10, [tool('go test ./...', 'error')]),
    step('a4', 15, [tool('go test ./...', 'error')]),
    step('a5', 20, [tool('go test ./...', 'ok')]),
  ]);
  assert.deepEqual(worthALook(sessionFrom(st), T0 + 25e3), []);
});

test('worthALook: an incomplete session idle 10 minutes past its unanswered call has one idle note', () => {
  const st = story([
    step('a1', 0, [tool('git status', 'ok')]),
    step('a2', 5, [tool('npm run e2e', undefined)]),
  ]);
  const notes = worthALook(sessionFrom(st), T0 + 5e3 + 10 * 60e3);
  assert.equal(notes.length, 1);
  assert.equal(notes[0].kind, 'idle-stall');
  assert.equal(notes[0].head, 'Idle 10.0 min after the last activity');
  assert.equal(notes[0].body, 'At 10:00:05 · npm run e2e · idle 10.0 min after the last activity, Bash has no result — threshold 2.0 min');
  assert.equal(notes[0].link, 'a2');
});

test('worthALook ranks an unresolved error cascade above three slow calls, even when a slow call is on the last turn', () => {
  // 63 timed calls: 60 at 30 s hold the p95 at 30 s; three at 70-90 s are each
  // over 2× it and over 60 s, so four signals compete for three notes. The
  // cascade (three errors, untimed) sits in turn 1.
  const timedCall = (i, sec) => tool(`sleep ${i}`, 'ok', { started_at: at(0), ended_at: at(sec) });
  const st = story([
    step('a1', 0, [tool('npm test', 'error'), tool('make lint', 'error'), tool('go vet ./...', 'error')]),
    step('a2', 5, Array.from({ length: 60 }, (_, i) => timedCall(i, 30))),
    step('a3', 10, [timedCall(60, 70), timedCall(61, 80)]),
    step('a4', 15, [timedCall(62, 90)]),
  ]);
  const notes = worthALook(sessionFrom(st), T0 + 20e3);
  assert.deepEqual(notes.map(n => n.kind), ['error-cascade', 'slow-tool', 'slow-tool']);
  assert.equal(notes[0].link, 'a1');
  assert.equal(notes[1].link, 'a4', 'within the slow notes, the one on the last turn first');
  assert.equal(notes[2].head, 'Bash took 80.0s — 2.7× the session p95');
});

test('worthALook keeps at most three notes', () => {
  const st = story([1, 2, 3, 4].map(i => step(`a${i}`, i, [tool(`echo ${i}`, 'ok')], { cost_usd: 1 })));
  assert.equal(worthALook(sessionFrom(st), T0).length, 3);
});

test('worthALook: a session with no tool calls has no notes, even with a turn over the cost-whale floor', () => {
  const st = story([step('a1', 0, [], { cost_usd: 1 })]);
  assert.deepEqual(worthALook(sessionFrom(st), T0 + 3600e3), []);
});

test('worthALook keeps a retry storm whose peak attempt has no result yet, and drops it once that attempt succeeds', () => {
  const tries = last => story([
    step('a1', 0, [tool('go test ./...', 'error')]),
    step('a2', 5, [tool('go test ./...', 'error')]),
    step('a3', 10, [tool('go test ./...', 'error')]),
    step('a4', 15, [tool('go test ./...', last)]),
  ]);
  const pending = worthALook(sessionFrom(tries(undefined)), T0 + 16e3);
  assert.deepEqual(pending.map(n => [n.kind, n.link]), [['retry-storm', 'a4'], ['error-cascade', 'a1']],
    'three unresolved failures and an unanswered fourth attempt are no recovery');
  assert.equal(headerNumbers(tries(undefined)).failed, 3);
  assert.deepEqual(worthALook(sessionFrom(tries('ok')), T0 + 16e3), []);
  assert.equal(headerNumbers(tries('ok')).failed, 0);
});

test('worthALook keeps a retry storm a different input_key succeeds after, and drops it once the same input_key does', () => {
  const capped = 'go test ./internal/aggregate/... -run TestContextCost'.padEnd(500, 'x');
  const tries = key => story([
    step('a1', 0, [tool(capped, 'error', { input_key: 'aaaa' })]),
    step('a2', 5, [tool(capped, 'error', { input_key: 'aaaa' })]),
    step('a3', 10, [tool(capped, 'error', { input_key: 'aaaa' })]),
    step('a4', 15, [tool(capped, 'ok', { input_key: key })]),
  ]);
  const other = worthALook(sessionFrom(tries('bbbb')), T0 + 16e3);
  assert.deepEqual(other.map(n => [n.kind, n.link]), [['retry-storm', 'a3'], ['error-cascade', 'a1']],
    'a success of another whole input is no recovery of the storm');
  assert.match(other[0].body, /go test \.\/internal\/aggregate/, 'the note still names the readable command');
  assert.equal(headerNumbers(tries('bbbb')).failed, 3);
  assert.deepEqual(worthALook(sessionFrom(tries('aaaa')), T0 + 16e3), []);
  assert.equal(headerNumbers(tries('aaaa')).failed, 0);
});

test('unresolvedFailures matches a later success by input_key, not the capped input', () => {
  const capped = 'go test ./internal/aggregate/... -run TestContextCost'.padEnd(500, 'x');
  const distinct = story([
    step('a1', 0, [tool(capped, 'error', { input_key: 'aaaa' })]),
    step('a2', 5, [tool(capped, 'ok', { input_key: 'bbbb' })]),
  ]);
  assert.deepEqual(unresolvedFailures(distinct).map(f => f.tool.input_key), ['aaaa'],
    'equal snippets with distinct input_key are two commands');
  const n = headerNumbers(distinct);
  assert.equal(n.failed, 1);
  assert.match(explainPrompt(distinct), /Unresolved failures:\n10:00:00  go test /);
  assert.deepEqual(stripBars(distinct, 'cost').map(b => b.failed), [true, false]);

  const same = story([
    step('a1', 0, [tool(capped, 'error', { input_key: 'aaaa' })]),
    step('a2', 5, [tool(capped, 'ok', { input_key: 'aaaa' })]),
  ]);
  assert.deepEqual(unresolvedFailures(same), [], 'the same input_key later ok recovers it');
  assert.equal(headerNumbers(same).failed, 0);
});

test('sessionTurns links each turn to its own uuid, a uuid-less one to the latest main turn before it', () => {
  const st = story([step('a1', 0), step('a2', 20)], [child('Plan', [step('k1', 5), step('k2', 25), step('', 30)])]);
  assert.deepEqual(sessionTurns(st).map(t => [t.n, t.step.uuid, t.label, t.link]), [
    [1, 'a1', '', 'a1'], [2, 'k1', 'Plan', 'k1'], [3, 'a2', '', 'a2'], [4, 'k2', 'Plan', 'k2'], [5, '', 'Plan', 'a2'],
  ]);
});

test('a sub-agent bar and note land on the sub-agent turn they describe', () => {
  const st = story([step('a1', 0, [tool('git status', 'ok')]), step('a2', 30, [tool('git diff', 'ok')])], [child('Explore', [
    step('k1', 5, [tool('npm test', 'error')]),
    step('k2', 6, [tool('make lint', 'error')]),
    step('k3', 7, [tool('go vet ./...', 'error')]),
  ])]);
  const bars = stripBars(st, 'cost');
  assert.deepEqual(bars.map(b => b.link), ['a1', 'k1', 'k2', 'k3', 'a2']);
  const cascade = worthALook(sessionFrom(st), T0 + 31e3).find(n => n.kind === 'error-cascade');
  assert.ok(cascade, 'the sub-agent cascade is a note');
  assert.equal(cascade.link, 'k1');
  assert.equal(cascade.turn, 2);
});

test('explainPrompt is the fixed template over logs, unresolved failures and the costliest turns', () => {
  const st = story([
    step('a1', 0, [tool('rg login', 'ok')], { cost_usd: 0.4 }),
    step('a2', 61, [tool('go test ./internal/...', 'error', { output: '\n--- FAIL: TestLogin (0.01s)\nlogin_test.go:12: want 200' })], { cost_usd: 1.25, duration_ms: 90000 }),
    step('a3', 122, [], { cost_usd: 0.05 }),
    step('a4', 183, [tool('make lint', 'error', { output: '' })], { cost_usd: 0.9 }),
  ], [], { source_files: ['/u/.claude/projects/p/s1.jsonl', '/u/.claude/projects/p/s1/subagents/agent-k.jsonl'] });
  assert.equal(explainPrompt(st), [
    'You are the coding agent that ran this session. Explain what went wrong and what to change.',
    '',
    'Session log: /u/.claude/projects/p/s1.jsonl',
    'Session log: /u/.claude/projects/p/s1/subagents/agent-k.jsonl',
    '',
    'Unresolved failures:',
    '10:01:01  go test ./internal/...  → --- FAIL: TestLogin (0.01s)',
    '10:03:03  make lint  → no output',
    '',
    'Retried calls: none',
    '',
    'Costliest turns:',
    '10:01:01  $1.25  go test ./internal/...',
    '10:03:03  $0.90  make lint',
    '10:00:00  $0.40  rg login',
    '',
    'Slowest turns (median 1s, p95 1m 16s):',
    '10:01:01  1m 30s  go test ./internal/...',
  ].join('\n'));
});

test('explainPrompt lists each retried call once, in session order, with its attempts and errors', () => {
  const st = story([
    step('a1', 0, [tool('go test ./...', 'error'), tool('rg foo', 'ok')]),
    step('a2', 10, [tool('go test ./...', 'error')]),
    step('a3', 20, [tool('go test ./...', 'ok'), tool('rg foo', 'ok')]),
    step('a4', 40, [tool('', 'error', { name: 'Read', kind: 'read', detail: 'main.go' })]),
    step('a5', 50, [tool('', 'error', { name: 'Read', kind: 'read', detail: 'main.go' })]),
  ], [child('Explore', [
    step('k1', 5, [tool('npm test', 'error')]),
    step('k2', 6, [tool('npm test', 'error')]),
  ])]);
  const lines = explainPrompt(st).split('\n');
  const at = lines.indexOf('Retried calls:');
  assert.ok(at > 0, 'the retried section is there');
  assert.deepEqual(lines.slice(at + 1, lines.indexOf('', at)), [
    '10:00:00  go test ./...  → 3 attempts, 2 errored',
    '10:00:05  npm test  → 2 attempts, 2 errored',
    '10:00:40  main.go  → 2 attempts, 2 errored',
  ], 'a clean repeat (rg foo) is no retry; a call with no input names its detail');
});

test('explainPrompt lists no slowest turn when every turn took as long as the median', () => {
  const st = story([step('a1', 0, [tool('ls', 'ok')]), step('a2', 5), step('a3', 9)]);
  assert.match(explainPrompt(st), /\nSlowest turns: none$/);
});

test('explainPrompt of a clean session says none for each list', () => {
  const st = story([step('a1', 0, [], { cost_usd: 0 })], [], { source_files: [] });
  assert.equal(explainPrompt(st), [
    'You are the coding agent that ran this session. Explain what went wrong and what to change.',
    '',
    'Session log: none recorded',
    '',
    'Unresolved failures: none',
    '',
    'Retried calls: none',
    '',
    'Costliest turns: none',
    '',
    'Slowest turns: none',
  ].join('\n'));
});

test('explainPrompt over a summary, whose calls carry no output, names the failure without claiming no output', () => {
  const bare = (input, status) => {
    const t = tool(input, status);
    delete t.output;
    return t;
  };
  const st = story([step('a1', 0, [bare('npm test', 'error')]), step('a2', 30)]);
  const lines = explainPrompt(st).split('\n');
  const i = lines.indexOf('Unresolved failures:');
  assert.equal(lines[i + 1], '10:00:00  npm test');
});

test('failureRows: one row per unfixed failure, tool name and the input\'s first line (≤120 runes) else its detail, linked to its turn', () => {
  const long = 'x'.repeat(130);
  const st = story([
    step('a1', 0, [tool('npm test\n  --watch', 'error'), tool('', 'error', { name: 'Read', detail: 'main.go' })]),
    step('a2', 30, [tool(long, 'error')]),
  ], [child('Explore', [step('', 10, [tool('rg auth', 'error')])])]);
  const rows = failureRows(st);
  assert.deepEqual(rows.map(r => r.text), [
    'Bash · npm test',
    'Read · main.go',
    'Bash · rg auth',
    'Bash · ' + 'x'.repeat(119) + '…',
  ]);
  assert.deepEqual(rows.map(r => r.link), ['a1', 'a1', 'a1', 'a2'], 'a sub-agent turn with no uuid links to the main turn before it');
  assert.equal(Array.from(rows[3].text.split(' · ')[1]).length, 120);
  assert.equal(failuresHeading(1), '1 failed command never fixed');
  assert.equal(failuresHeading(25), '25 failed commands never fixed');
});

// family builds index rows of one prompt: cost_usd and duration_ms per
// run, Fable for the first `fable` rows and Opus for the rest.
const family = (costs, durations, fable) => costs.map((c, i) => ({
  session_id: `r${i}`, family_key: 'fix the login bug', cost_usd: c, duration_ms: durations[i],
  model: i < fable ? 'claude-fable-5-1' : 'claude-opus-4-7', provider: 'claude-code',
}));
const COSTS = [1, 1, 1, 1, 1.06, 1.06, 2, 2, 3, 8.28];
const DURS = [10, 10, 10, 10, 20, 20, 30, 30, 40, 90].map(m => m * 60e3);
const OTHER = { session_id: 'z', family_key: 'another prompt', cost_usd: 100, duration_ms: 1e9, model: 'claude-opus-4-7' };

test('comparisonLine: most expensive, longest, both, neither, and no line under 5 runs', () => {
  const stats = headerFamilyStats([...family(COSTS, DURS, 6), OTHER], 'fix the login bug');
  assert.equal(stats.runs, 10, 'only rows of the summary\'s family_key count');
  const mix = '(median $1.06; 6 Fable, 4 Opus)';
  const sum = (cost_usd, duration_ms) => ({ family_key: 'fix the login bug', cost_usd, duration_ms });
  assert.equal(comparisonLine(sum(8.28, 10 * 60e3), stats), `$8.28 — most expensive 10% of 10 runs of this prompt ${mix}`);
  assert.equal(comparisonLine(sum(1, 90 * 60e3), stats), `$1.00 — longest 10% of 10 runs of this prompt ${mix}`);
  assert.equal(comparisonLine(sum(8.28, 90 * 60e3), stats), `$8.28 — longest, most expensive 10% of 10 runs of this prompt ${mix}`);
  assert.equal(comparisonLine(sum(1.06, 20 * 60e3), stats), `$1.06 — one of 10 runs of this prompt ${mix}`);
  const four = headerFamilyStats(family(COSTS.slice(0, 4), DURS.slice(0, 4), 2), 'fix the login bug');
  assert.equal(comparisonLine(sum(8.28, 90 * 60e3), four), '');
  assert.equal(comparisonLine(sum(8.28, 90 * 60e3), null), '');
});

test('headerFamilyStats: no stats for the prompt-less family, an empty key or a missing index', () => {
  assert.equal(headerFamilyStats(family(COSTS, DURS, 6).map(r => ({ ...r, family_key: '(no prompt)' })), '(no prompt)'), null);
  assert.equal(headerFamilyStats(family(COSTS, DURS, 6), ''), null);
  assert.equal(headerFamilyStats(undefined, 'fix the login bug'), null);
});
