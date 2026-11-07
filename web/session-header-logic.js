// @ts-check
// The session header's numbers, strip bars, Worth a look notes and the
// explain-this-session prompt, computed over one session's summary (the
// /sessions/summary payload: every turn's numbers and calls, no text).
// Every sentence here is a fixed template over counted fields: claudit
// generates no prose.

import { detectRetries, detectSignals, inputIdentity, nearestRank, percentiles, retryTarget } from './agents-insights-logic.js';
import { agentLabel, parseRefKey, parseTime } from './agents-model.js';
import { clock, fmtDuration, fmtUSD, toolLabel, unwrapExit } from './session-story-logic.js';
import { familyStats, TOP_TENTH_MIN_RUNS } from './sessions-list-logic.js';
import { fmtMoney, fmtNum } from './format.js';

/** @import { StoryStep, StoryTool, StoryChild, Tokens, SessionIndexRow, SessionSummary as WholeStory } from './api-types.js' */
/** @import { FamilyStats } from './sessions-list-logic.js' */
/** @import { Signal } from './agents-insights-logic.js' */

/**
 * One model turn in session order.
 * @typedef {Object} Turn
 * @property {number} n                   1-based position in session turn order
 * @property {StoryStep} step
 * @property {number} agentIndex          0 = main, else 1 + the child's index
 * @property {number} stepIndex
 * @property {string} label               '' for the main agent, else the sub-agent's label
 * @property {string} link                the turn uuid a deep link lands on: its own, else
 *                                        the latest main-agent turn's at or before it
 */

export const MAX_BARS = 120;
export const MAX_NOTES = 3;
const CMD_RUNES = 120;
// NO_PROMPT_KEY is the family every prompt-less session shares; its runs
// are not runs of one prompt.
const NO_PROMPT_KEY = '(no prompt)';

/** @param {string} s @param {number} n */
const clip = (s, n) => (s.length > n ? s.slice(0, n - 1) + '…' : s);

/** @param {StoryTool|undefined} t */
const commandOf = t => (t ? clip(toolLabel(t), CMD_RUNES) : '');

/** @param {StoryStep} s */
const firstCommand = s => commandOf((s.tools || [])[0]);

const firstLine = (/** @type {string} */ s) => String(s || '').split('\n').map(l => l.trim()).find(Boolean) || '';

// agentsOf lists the story's agents in flattenSession order: the main agent
// first, then each sub-agent, as {label, steps}.
/** @param {WholeStory} story */
function agentsOf(story) {
  return [
    { label: '', steps: story.steps || [] },
    ...(story.children || []).map(c => ({ label: agentLabel(/** @type {any} */ (c)) || 'subagent', steps: c.steps || [] })),
  ];
}

// sessionOf is the story as the one-session graph detectSignals reads:
// main and children in flattenSession order.
/** @param {WholeStory} story */
export function sessionOf(story) {
  return {
    session_id: story.session_id,
    main: { kind: 'main', steps: story.steps || [] },
    children: (story.children || []).map(c => ({ ...c, kind: c.kind || 'subagent' })),
  };
}

// sessionTurns orders every turn of the main agent and its sub-agents by
// timestamp (main first on a tie). Each turn links to its own uuid; one
// with none links to the latest main turn at or before it.
/** @param {WholeStory} story @returns {Turn[]} */
export function sessionTurns(story) {
  /** @type {Omit<Turn, 'n'|'link'>[]} */
  const all = [];
  agentsOf(story).forEach((a, agentIndex) => {
    a.steps.forEach((step, stepIndex) => all.push({ step, agentIndex, stepIndex, label: a.label }));
  });
  const ts = new Map(all.map(t => [t, parseTime(t.step.timestamp)]));
  const key = (/** @type {Omit<Turn, 'n'|'link'>} */ t) => { const v = ts.get(t); return Number.isNaN(v) ? Infinity : v; };
  all.sort((a, b) => (key(a) - key(b)) || (a.agentIndex - b.agentIndex) || (a.stepIndex - b.stepIndex));
  const firstMain = all.find(t => t.agentIndex === 0 && t.step.uuid);
  let mainLink = firstMain ? firstMain.step.uuid || '' : '';
  return all.map((t, i) => {
    if (t.agentIndex === 0 && t.step.uuid) mainLink = t.step.uuid;
    return { ...t, n: i + 1, link: t.step.uuid || mainLink };
  });
}

// callKey is a call's identity for matching a failure to a later success,
// the same identity detectRetries groups a retry storm by.
/** @param {StoryTool} t */
const callKey = t => `${t.kind || ''}\u0000${t.name || ''}\u0000${inputIdentity(t)}`;

// succeeded lists every call that returned ok or has a later call of the
// same callKey that returned ok in the same agent.
/** @param {WholeStory} story @returns {Set<StoryTool>} */
function succeeded(story) {
  /** @type {Set<StoryTool>} */
  const out = new Set();
  for (const a of agentsOf(story)) {
    const ok = new Set();
    const tools = a.steps.flatMap(s => s.tools || []);
    for (let i = tools.length - 1; i >= 0; i--) {
      const t = tools[i];
      const k = callKey(t);
      if (t.status === 'ok') ok.add(k);
      if (ok.has(k)) out.add(t);
    }
  }
  return out;
}

// unresolvedFailures lists every errored call with no later call of the
// same name and whole input (input_key; detail when it had no input) that
// returned ok in the same agent, as {turn, tool}, in session turn order.
/** @param {WholeStory} story @param {Turn[]} [turns] @param {Set<StoryTool>} [ok] */
export function unresolvedFailures(story, turns = sessionTurns(story), ok = succeeded(story)) {
  /** @type {{turn: Turn, tool: StoryTool}[]} */
  const out = [];
  for (const turn of turns) {
    for (const tool of turn.step.tools || []) if (tool.status === 'error' && !ok.has(tool)) out.push({ turn, tool });
  }
  return out;
}

/** @param {string} s @param {number} n */
const clipRunes = (s, n) => { const r = Array.from(s); return r.length > n ? r.slice(0, n - 1).join('') + '…' : s; };

/**
 * One row of the failure block.
 * @typedef {Object} FailureRow
 * @property {string} check  the failing check's name as text holds it, set as code; '' on an edit or other row
 * @property {string} name   the row's short name, as the Failing tile's sub-line lists it
 * @property {string} text   what failed, as the row reads
 * @property {string} link   turn uuid the row links to
 */

/**
 * The failure block's rows in its three groups.
 * @typedef {Object} FailureGroups
 * @property {FailureRow[]} checks  "Last <check> failed · <command>", one per failing_checks entry
 * @property {FailureRow[]} edits   "<tool> failed and never applied · <input>", one per edit never applied
 * @property {FailureRow[]} other   "<tool> · <input>", every other errored call not retried
 */

// EDIT_TOOLS are the tools that change a file, as the index's failed_edits
// counts them.
const EDIT_TOOLS = new Set(['Edit', 'Write', 'MultiEdit', 'apply_patch']);

// failureRows splits the session's failures into the block's three groups:
// one row per check whose last run failed (story.failing_checks, the index
// row's), linked to that run's turn; one row per unresolvedFailures edit;
// and every other unresolvedFailures call, the runs of a failing check's
// own command (the same input_key) excepted since its row stands for them;
// a check with no input_key excepts none. A call reads by its
// tool name and the first line of its input, at most CMD_RUNES runes,
// else its detail; all in session turn order.
/** @param {WholeStory} story @returns {FailureGroups} */
export function failureRows(story) {
  const failing = story.failing_checks || [];
  const checkKeys = new Set(failing.map(c => c.input_key).filter(Boolean));
  /** @type {FailureGroups} */
  const out = {
    checks: failing.map(c => ({
      check: c.check, name: c.check, text: `Last ${c.check} failed · ${c.command}`, link: c.turn,
    })),
    edits: [],
    other: [],
  };
  for (const { turn, tool } of unresolvedFailures(story)) {
    const what = clipRunes(firstLine(tool.input || ''), CMD_RUNES) || tool.detail || '';
    if (EDIT_TOOLS.has(tool.name || '')) {
      out.edits.push({
        check: '', name: [tool.name, tool.detail || what].filter(Boolean).join(' '),
        text: [`${tool.name} failed and never applied`, what].filter(Boolean).join(' · '), link: turn.link,
      });
    } else if (!(tool.input_key && checkKeys.has(tool.input_key))) {
      out.other.push({
        check: '', name: commandOf(tool), text: [tool.name || '', what].filter(Boolean).join(' · '), link: turn.link,
      });
    }
  }
  return out;
}

// failuresHeading counts what the block leads with: the failing checks and
// the edits never applied, a zero dropped; '' when both are zero.
/** @param {FailureGroups} g */
export function failuresHeading(g) {
  const parts = [];
  if (g.checks.length) parts.push(`${fmtNum(g.checks.length)} failing check${g.checks.length === 1 ? '' : 's'}`);
  if (g.edits.length) parts.push(`${fmtNum(g.edits.length)} edit${g.edits.length === 1 ? '' : 's'} never applied`);
  return parts.join(' · ');
}

// otherFailuresLabel is the collapsed button over the other errored calls.
/** @param {number} n */
export const otherFailuresLabel = n =>
  `${fmtNum(n)} other command${n === 1 ? '' : 's'} errored and ${n === 1 ? 'was' : 'were'} not retried`;

/**
 * A family's numbers as the header compares one run against them.
 * @typedef {FamilyStats & {costP90: number, durationP90: number}} HeaderFamilyStats
 */

// headerFamilyStats is familyStats over the index rows of familyKey with
// the nearest-rank 90th percentile of their cost_usd and duration_ms; null
// when there is no index, no key or the prompt-less family.
/** @param {SessionIndexRow[]|undefined|null} rows @param {string} familyKey @returns {HeaderFamilyStats|null} */
export function headerFamilyStats(rows, familyKey) {
  if (!Array.isArray(rows) || !familyKey || familyKey === NO_PROMPT_KEY) return null;
  const fam = rows.filter(r => r && r.family_key === familyKey);
  if (!fam.length) return null;
  return {
    ...familyStats(fam),
    costP90: nearestRank(fam.map(r => r.cost_usd || 0), 90),
    durationP90: nearestRank(fam.map(r => r.duration_ms || 0), 90),
  };
}

// comparisonLine places this run among its prompt's runs once the family
// has TOP_TENTH_MIN_RUNS of them: its cost, whether its cost or duration
// is at or above the family's 90th percentile, the run count, median cost
// and model mix. '' below that many runs.
/** @param {{cost_usd?: number, duration_ms?: number}} summary @param {HeaderFamilyStats|null} stats */
export function comparisonLine(summary, stats) {
  if (!stats || stats.runs < TOP_TENTH_MIN_RUNS) return '';
  const cost = summary.cost_usd || 0, dur = summary.duration_ms || 0;
  const costly = cost >= stats.costP90;
  const long = dur >= stats.durationP90;
  const where = costly && long ? 'longest, most expensive 10% of'
    : costly ? 'most expensive 10% of' : long ? 'longest 10% of' : 'one of';
  const aside = [stats.median != null ? `median ${fmtMoney(stats.median)}` : '',
    stats.models.map(([m, n]) => `${fmtNum(n)} ${m}`).join(', ')].filter(Boolean).join('; ');
  return `${fmtMoney(cost)} — ${where} ${fmtNum(stats.runs)} runs of this prompt${aside ? ` (${aside})` : ''}`;
}

/** @param {Tokens|undefined} t */
const tokenParts = t => ({
  input: (t && t.InputTokens) || 0,
  output: (t && t.OutputTokens) || 0,
  c5: (t && t.CacheCreate5mTokens) || 0,
  c1: (t && t.CacheCreate1hTokens) || 0,
  read: (t && t.CacheReadTokens) || 0,
});

// headerNumbers is the four tiles: what the session cost, how long it ran
// with how many turns and commands, how many tokens it moved with the share
// re-read from cache, and what it ended failing: the failure block's
// failing checks and edits never applied, with up to two of their names.
// The other errored calls count only in the block's collapsed disclosure.
/** @param {WholeStory} story @param {FailureGroups} [groups] the story's failureRows, when the caller has them */
export function headerNumbers(story, groups = failureRows(story)) {
  const turns = sessionTurns(story);
  let cost = 0, commands = 0, input = 0, output = 0, c5 = 0, c1 = 0, read = 0;
  for (const { step } of turns) {
    cost += step.cost_usd || 0;
    for (const t of step.tools || []) commands += t.count || 1;
    const p = tokenParts(step.tokens);
    input += p.input; output += p.output; c5 += p.c5; c1 += p.c1; read += p.read;
  }
  const start = parseTime(story.started_at), end = parseTime(story.last_activity);
  const durationMs = !Number.isNaN(start) && !Number.isNaN(end) && end > start ? end - start : 0;
  const tokens = input + output + c5 + c1 + read;
  const prompt = input + c5 + c1 + read;
  const cachePct = prompt > 0 ? Math.round(100 * read / prompt) : 0;
  const failing = [...groups.checks, ...groups.edits];
  const names = [...new Set(failing.map(f => f.name).filter(Boolean))];
  const shown = names.slice(0, 2).map(c => clip(c, 40));
  const more = names.length > 2 ? ` +${names.length - 2} more` : '';
  return {
    costUSD: cost,
    durationMs,
    turns: turns.length,
    commands,
    durationSub: `${turns.length} turn${turns.length === 1 ? '' : 's'} · ${commands} command${commands === 1 ? '' : 's'}`,
    tokens,
    cachePct,
    tokensSub: `${cachePct}% re-read from cache`,
    failed: failing.length,
    failedSub: shown.length ? shown.join(', ') + more : 'none',
  };
}

/**
 * One strip bar: a turn, or a bin of consecutive turns.
 * @typedef {Object} Bar
 * @property {number} first               1-based first turn number
 * @property {number} last
 * @property {number} value               context cost in USD, or duration in ms
 * @property {number} heightPct           value against the tallest bar, 0–100
 * @property {boolean} failed             holds an unresolved failure
 * @property {string} link                turn uuid the bar's click lands on
 * @property {string} tip                 the bar's tooltip text
 */

// stripBars is one bar per model turn in session order, or, past maxBars
// turns, one per bin of ceil(n / maxBars) consecutive turns summing its
// turns' values. mode 'cost' measures context_cost_usd (what the turn added
// to context), 'time' duration_ms.
/** @param {WholeStory} story @param {'cost'|'time'} mode @param {number} [maxBars] @returns {Bar[]} */
export function stripBars(story, mode, maxBars = MAX_BARS) {
  const turns = sessionTurns(story);
  const failedTurns = new Set(unresolvedFailures(story, turns).map(f => f.turn));
  const value = (/** @type {Turn} */ t) => (mode === 'time' ? t.step.duration_ms || 0 : t.step.context_cost_usd || 0);
  const fmt = (/** @type {number} */ v) => (mode === 'time' ? fmtDuration(v) : `${fmtUSD(v)} added to context`);
  const size = turns.length > maxBars ? Math.ceil(turns.length / maxBars) : 1;
  /** @type {Bar[]} */
  const bars = [];
  for (let i = 0; i < turns.length; i += size) {
    const bin = turns.slice(i, i + size);
    const head = bin[0];
    const v = bin.reduce((s, t) => s + value(t), 0);
    let tip;
    if (size === 1) {
      const parts = [`Turn ${head.n}`, clock(head.step.timestamp)];
      if (head.label) parts.push(head.label);
      parts.push(fmt(v));
      const cmd = firstCommand(head.step);
      if (cmd) parts.push(cmd);
      tip = parts.filter(Boolean).join(' · ');
    } else {
      tip = `turns ${head.n}–${bin[bin.length - 1].n} · ${fmt(v)}`;
    }
    bars.push({
      first: head.n, last: bin[bin.length - 1].n, value: v, heightPct: 0,
      failed: bin.some(t => failedTurns.has(t)), link: head.link, tip,
    });
  }
  const max = bars.reduce((m, b) => Math.max(m, b.value), 0);
  for (const b of bars) b.heightPct = max > 0 ? Math.round(1000 * b.value / max) / 10 : 0;
  return bars;
}

/**
 * One Worth a look note.
 * @typedef {Object} Note
 * @property {string} kind
 * @property {string} head                bold head sentence: the signal's summary
 * @property {string} body                time, command and the signal's reason
 * @property {number} turn                1-based turn number the note points at
 * @property {string} link                turn uuid its click lands on
 */

// worthALook runs the five per-session detectors over the one-session
// graph with their default thresholds, drops a retry storm whose peak
// attempt or a later one of the same call returned ok in the same agent
// and an error cascade every one of whose errors did, ranks the remaining
// failures (retry storms, error cascades) before every cost-whale,
// slow-tool and idle-stall note, within each group first a note on the main
// agent's last turn (the session ended on it) then detectSignals' severity
// order, and keeps at most MAX_NOTES. A session with no tool calls has no
// notes.
/**
 * @param {{session_id: string, main: {steps: StoryStep[]}, children: StoryChild[]}} session
 * @param {number} [nowMs]
 * @returns {Note[]}
 */
export function worthALook(session, nowMs) {
  const story = /** @type {WholeStory} */ (/** @type {unknown} */ ({
    session_id: session.session_id, steps: (session.main && session.main.steps) || [], children: session.children || [],
  }));
  const agents = agentsOf(story);
  if (!agents.some(a => a.steps.some(s => (s.tools || []).length > 0))) return [];
  const turns = sessionTurns(story);
  const ok = succeeded(story);
  const turnAt = new Map(turns.map(t => [`${t.agentIndex}.${t.stepIndex}`, t]));
  const mainLast = agents[0].steps.length - 1;
  const opts = typeof nowMs === 'number' && !Number.isNaN(nowMs) ? { nowMs } : {};
  const signals = detectSignals(/** @type {any} */ ({ sessions: [sessionOf(story)] }), opts);

  /** @param {Signal} s */
  const resolved = s => {
    const r = parseRefKey(s.ref);
    if (!r || r.stepIndex == null || r.toolIndex == null) return false;
    const steps = (agents[r.agentIndex] || { steps: [] }).steps;
    const tool = ((steps[r.stepIndex] || { tools: [] }).tools || [])[r.toolIndex];
    if (!tool) return false;
    if (s.kind === 'retry-storm') return ok.has(tool);
    // error-cascade: the run of consecutive errors starting at ref.
    const flat = steps.flatMap(st => st.tools || []);
    let i = flat.indexOf(tool);
    for (; i >= 0 && i < flat.length && flat[i].status === 'error'; i++) {
      if (!ok.has(flat[i])) return false;
    }
    return true;
  };

  const isFailure = (/** @type {Signal} */ s) => s.kind === 'retry-storm' || s.kind === 'error-cascade';
  const kept = signals.filter(s => !(isFailure(s) && resolved(s)));
  const endedOn = (/** @type {Signal} */ s) => {
    const r = parseRefKey(s.ref);
    return !!r && r.agentIndex === 0 && r.stepIndex === mainLast && mainLast >= 0;
  };
  const endedFirst = (/** @type {Signal[]} */ group) => [...group.filter(endedOn), ...group.filter(s => !endedOn(s))];
  const ranked = [
    ...endedFirst(kept.filter(isFailure)), ...endedFirst(kept.filter(s => !isFailure(s))),
  ].slice(0, MAX_NOTES);
  return ranked.map(s => {
    const r = parseRefKey(s.ref);
    const turn = r && r.stepIndex != null ? turnAt.get(`${r.agentIndex}.${r.stepIndex}`) : undefined;
    const step = turn ? turn.step : undefined;
    const tool = step && r && r.toolIndex != null ? (step.tools || [])[r.toolIndex] : undefined;
    const cmd = tool ? commandOf(tool) : step ? firstCommand(step) : '';
    const time = step ? clock(step.timestamp) : '';
    const body = [time ? `At ${time}` : '', cmd, s.reason].filter(Boolean).join(' · ');
    return { kind: s.kind, head: s.summary, body, turn: turn ? turn.n : 0, link: turn ? turn.link : '' };
  });
}

// retriedCalls lists each call detectRetries found repeated after an error,
// once per group at its first call, as {turn, tool, attempts, errors}
// (the group's peak attempt and its errored calls), in session turn order.
/** @param {WholeStory} story @param {Turn[]} turns */
function retriedCalls(story, turns) {
  const turnAt = new Map(turns.map(t => [`${t.agentIndex}.${t.stepIndex}`, t]));
  /** @type {{turn: Turn, tool: StoryTool, attempts: number, errors: number}[]} */
  const out = [];
  agentsOf(story).forEach((a, ai) => {
    /** @type {Map<string, {attempts: number, errors: number}>} */
    const groups = new Map();
    for (const { attempt, ofRef, errors } of detectRetries(/** @type {any} */ (a)).values()) {
      const g = groups.get(ofRef);
      if (!g || attempt > g.attempts) groups.set(ofRef, { attempts: attempt, errors });
    }
    for (const [ref, g] of groups) {
      const [si, ti] = ref.split(':').map(Number);
      const turn = turnAt.get(`${ai}.${si}`);
      const tool = (a.steps[si].tools || [])[ti];
      if (turn && tool) out.push({ turn, tool, ...g });
    }
  });
  return out.sort((x, y) => (x.turn.n - y.turn.n) || (x.turn.step.tools.indexOf(x.tool) - y.turn.step.tools.indexOf(y.tool)));
}

// slowestTurns is the median and p95 of the session's turn durations and
// up to three turns that took longer than the median and at least the p95,
// slowest first.
/** @param {Turn[]} turns */
function slowestTurns(turns) {
  const [median, p95] = percentiles(turns.map(t => t.step.duration_ms || 0), [50, 95]);
  const slow = turns
    .filter(t => (t.step.duration_ms || 0) > median && (t.step.duration_ms || 0) >= p95)
    .sort((a, b) => (b.step.duration_ms - a.step.duration_ms) || (a.n - b.n))
    .slice(0, 3);
  return { median, p95, slow };
}

// explainPrompt is the text the "Copy explain-this-session prompt" button
// copies: addressed to the coding agent that ran the session, naming its
// logs, each unresolved failure (time, command and, when the call carries
// its output, that output's first line; the summary carries none), each
// call retried after an error (time, target, attempts), the three costliest
// turns (time, cost, first command) and the turns at or over the session's
// p95 turn time.
/** @param {WholeStory} story */
export function explainPrompt(story) {
  const turns = sessionTurns(story);
  const lines = ['You are the coding agent that ran this session. Explain what went wrong and what to change.', ''];
  const files = story.source_files || [];
  if (files.length) for (const f of files) lines.push(`Session log: ${f}`);
  else lines.push('Session log: none recorded');
  lines.push('');
  const failures = unresolvedFailures(story, turns);
  if (failures.length) {
    lines.push('Unresolved failures:');
    for (const { turn, tool } of failures) {
      const head = `${clock(turn.step.timestamp)}  ${commandOf(tool)}`;
      if (tool.output === undefined) { lines.push(head); continue; }
      const out = firstLine(unwrapExit(tool.output));
      lines.push(`${head}  → ${out ? clip(out, CMD_RUNES) : 'no output'}`);
    }
  } else {
    lines.push('Unresolved failures: none');
  }
  lines.push('');
  const retried = retriedCalls(story, turns);
  if (retried.length) {
    lines.push('Retried calls:');
    for (const r of retried) {
      lines.push(`${clock(r.turn.step.timestamp)}  ${clip(retryTarget(r.tool) || r.tool.name || '', CMD_RUNES)}  → ${r.attempts} attempts, ${r.errors} errored`);
    }
  } else {
    lines.push('Retried calls: none');
  }
  lines.push('');
  const costliest = [...turns]
    .filter(t => (t.step.cost_usd || 0) > 0)
    .sort((a, b) => (b.step.cost_usd - a.step.cost_usd) || (a.n - b.n))
    .slice(0, 3);
  if (costliest.length) {
    lines.push('Costliest turns:');
    for (const t of costliest) {
      lines.push(`${clock(t.step.timestamp)}  ${fmtUSD(t.step.cost_usd)}  ${firstCommand(t.step) || 'no command'}`);
    }
  } else {
    lines.push('Costliest turns: none');
  }
  lines.push('');
  const { median, p95, slow } = slowestTurns(turns);
  if (slow.length) {
    lines.push(`Slowest turns (median ${fmtDuration(median)}, p95 ${fmtDuration(p95)}):`);
    for (const t of slow) {
      lines.push(`${clock(t.step.timestamp)}  ${fmtDuration(t.step.duration_ms)}  ${firstCommand(t.step) || 'no command'}`);
    }
  } else {
    lines.push('Slowest turns: none');
  }
  return lines.join('\n');
}
