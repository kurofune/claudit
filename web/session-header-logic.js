// @ts-check
// The session header's numbers, strip bars, Worth a look notes and the
// explain-this-session prompt, computed over the whole story of one session
// (the /sessions/story?whole=1 payload). Every sentence here is a fixed
// template over counted fields: claudit generates no prose.

import { detectSignals, retryTarget } from './agents-insights-logic.js';
import { agentLabel, parseRefKey, parseTime } from './agents-model.js';
import { clock, fmtDuration, fmtUSD, toolLabel, unwrapExit } from './session-story-logic.js';

/** @import { StoryStep, StoryTool, StoryChild, Tokens, SessionStoryWhole as WholeStory } from './api-types.js' */
/** @import { Signal } from './agents-insights-logic.js' */

/**
 * One model turn in session order.
 * @typedef {Object} Turn
 * @property {number} n                   1-based position in session turn order
 * @property {StoryStep} step
 * @property {number} agentIndex          0 = main, else 1 + the child's index
 * @property {number} stepIndex
 * @property {string} label               '' for the main agent, else the sub-agent's label
 * @property {string} link                the main-agent turn uuid a deep link lands on
 */

export const MAX_BARS = 120;
export const MAX_NOTES = 3;
const CMD_RUNES = 120;

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
// timestamp (main first on a tie). A sub-agent turn links to the latest
// main turn at or before it, since the story pages main-agent turns.
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
  let link = firstMain ? firstMain.step.uuid || '' : '';
  return all.map((t, i) => {
    if (t.agentIndex === 0 && t.step.uuid) link = t.step.uuid;
    return { ...t, n: i + 1, link };
  });
}

// unresolvedFailures lists every errored call with no later call of the
// same name and input (or detail, lacking one) that returned ok in the same
// agent, as {turn, tool}, in session turn order.
/** @param {WholeStory} story @param {Turn[]} [turns] */
export function unresolvedFailures(story, turns = sessionTurns(story)) {
  /** @type {Set<StoryTool>} */
  const failed = new Set();
  for (const a of agentsOf(story)) {
    const later = new Set();
    const tools = a.steps.flatMap(s => s.tools || []);
    for (let i = tools.length - 1; i >= 0; i--) {
      const t = tools[i];
      const k = `${t.name}\u0000${retryTarget(t)}`;
      if (t.status === 'ok') later.add(k);
      else if (t.status === 'error' && !later.has(k)) failed.add(t);
    }
  }
  /** @type {{turn: Turn, tool: StoryTool}[]} */
  const out = [];
  for (const turn of turns) {
    for (const tool of turn.step.tools || []) if (failed.has(tool)) out.push({ turn, tool });
  }
  return out;
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
// re-read from cache, and its unresolved failures with up to two of their
// commands.
/** @param {WholeStory} story */
export function headerNumbers(story) {
  const turns = sessionTurns(story);
  let cost = 0, commands = 0, input = 0, output = 0, c5 = 0, c1 = 0, read = 0;
  for (const { step } of turns) {
    cost += step.cost_usd || 0;
    for (const t of step.tools || []) commands += t.count || 1;
    const p = tokenParts(step.tokens);
    input += p.input; output += p.output; c5 += p.c5; c1 += p.c1; read += p.read;
  }
  const start = parseTime(story.started_at), end = parseTime(story.ended_at);
  const durationMs = !Number.isNaN(start) && !Number.isNaN(end) && end > start ? end - start : 0;
  const tokens = input + output + c5 + c1 + read;
  const prompt = input + c5 + c1 + read;
  const cachePct = prompt > 0 ? Math.round(100 * read / prompt) : 0;
  const failures = unresolvedFailures(story, turns);
  const names = [...new Set(failures.map(f => commandOf(f.tool)).filter(Boolean))];
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
    failed: failures.length,
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
// graph with their default thresholds, drops a retry storm or error
// cascade whose failing command later returned ok in the same agent, ranks
// first a note on the main agent's last turn (the session ended on it) and
// keeps at most MAX_NOTES.
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
  const turns = sessionTurns(story);
  const unresolved = new Set(unresolvedFailures(story, turns).map(f => f.tool));
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
    if (s.kind === 'retry-storm') return !unresolved.has(tool);
    // error-cascade: the run of consecutive errors starting at ref.
    const flat = steps.flatMap(st => st.tools || []);
    let i = flat.indexOf(tool);
    for (; i >= 0 && i < flat.length && flat[i].status === 'error'; i++) {
      if (unresolved.has(flat[i])) return false;
    }
    return true;
  };

  const kept = signals.filter(s => !((s.kind === 'retry-storm' || s.kind === 'error-cascade') && resolved(s)));
  const endedOn = (/** @type {Signal} */ s) => {
    const r = parseRefKey(s.ref);
    return !!r && r.agentIndex === 0 && r.stepIndex === mainLast && mainLast >= 0;
  };
  const ranked = [...kept.filter(endedOn), ...kept.filter(s => !endedOn(s))].slice(0, MAX_NOTES);
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

// explainPrompt is the text the "Copy explain-this-session prompt" button
// copies: addressed to the coding agent that ran the session, naming its
// logs, each unresolved failure (time, command, first output line) and the
// three costliest turns (time, cost, first command).
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
      const out = firstLine(unwrapExit(tool.output || ''));
      lines.push(`${clock(turn.step.timestamp)}  ${commandOf(tool)}  → ${out ? clip(out, CMD_RUNES) : 'no output'}`);
    }
  } else {
    lines.push('Unresolved failures: none');
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
  return lines.join('\n');
}
