// @ts-check
// Session story derivations: the "What happened" section's rows, built from
// one prompt's loaded story steps. Every header is a count over the payload;
// nothing here writes prose of its own.

/** @import { SessionStoryPage, StoryStep, StoryTool, StoryChild, StoryPrompt } from './api-types.js' */

export const PAGE_TURNS = 200;
export const IDLE_MS = 5 * 60 * 1000;

// The blocks a harness writes into a user message on the person's behalf;
// the same list internal/agentflow/family.go strips for the family key. A
// block opens at the start of a line, so a prompt that names a tag in its
// prose keeps that prose; an unclosed block runs to the end of the text.
const WRAPPERS = [
  /^[ \t]*<local-command-[a-z-]+>[\s\S]*?(?:<\/local-command-[a-z-]+>|$(?![\s\S]))/gm,
  /^[ \t]*<system-reminder>[\s\S]*?(?:<\/system-reminder>|$(?![\s\S]))/gm,
  /^[ \t]*<command-[a-z-]+>[\s\S]*?(?:<\/command-[a-z-]+>|$(?![\s\S]))/gm,
  /^# AGENTS\.md instructions[^\n]*[\s\S]*?(?:<\/INSTRUCTIONS>|$(?![\s\S]))/gm,
  /^[ \t]*<environment_context>[\s\S]*?(?:<\/environment_context>|$(?![\s\S]))/gm,
  /^[ \t]*<codex_internal_context[^>]*>[\s\S]*?(?:<\/codex_internal_context>|$(?![\s\S]))/gm,
  /^[ \t]*<guardian_tool_descriptions>[\s\S]*?(?:<\/guardian_tool_descriptions>|$(?![\s\S]))/gm,
];

export const WRAPPER_LABEL = 'AGENTS.md and environment context hidden';

// splitWrappers separates a prompt into the text the person wrote and the
// raw wrapper blocks, both in the order they appeared. The written text
// keeps its whitespace; only the blank lines at a seam with a wrapper go,
// and the pieces a wrapper split apart rejoin on a line break.
/** @param {string} text @returns {{visible: string, hidden: string}} */
export function splitWrappers(text) {
  const src = String(text || '');
  /** @type {{start: number, end: number}[]} */
  const found = [];
  for (const re of WRAPPERS) {
    for (const m of src.matchAll(re)) found.push({ start: m.index || 0, end: (m.index || 0) + m[0].length });
  }
  found.sort((a, b) => a.start - b.start || b.end - a.end);
  const blocks = [];
  let at = 0;
  for (const b of found) {
    if (b.start < at || b.end === b.start) continue;
    blocks.push(b);
    at = b.end;
  }
  const pieces = [];
  let from = 0;
  blocks.forEach((b, k) => {
    pieces.push({ text: src.slice(from, b.start), afterWrap: k > 0, beforeWrap: true });
    from = b.end;
  });
  pieces.push({ text: src.slice(from), afterWrap: blocks.length > 0, beforeWrap: false });
  const visible = pieces.map(p => {
    let t = p.text;
    if (p.afterWrap) t = t.replace(/^(?:[ \t]*\r?\n)+/, '');
    if (p.beforeWrap) t = t.replace(/(?:\r?\n[ \t]*)+$/, '');
    return t;
  }).filter(t => t.trim() !== '');
  return { visible: visible.join('\n'), hidden: blocks.map(b => src.slice(b.start, b.end)).join('\n\n') };
}

const pad = n => String(n).padStart(2, '0');

// clock is a timestamp's local wall-clock time, HH:MM:SS.
/** @param {string} ts */
export function clock(ts) {
  const d = new Date(ts);
  if (!ts || isNaN(d.getTime())) return '';
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// fmtDuration reads a span as "42s", "1m 12s" or "2h 5m"; under a second
// reads in milliseconds.
/** @param {number} ms */
export function fmtDuration(ms) {
  if (!(ms > 0)) return '0s';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return sec % 60 ? `${m}m ${sec % 60}s` : `${m}m`;
  const h = Math.floor(m / 60);
  return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
}

/** @param {number} v */
export const fmtUSD = v => '$' + (v >= 1000 ? Math.round(v).toLocaleString() : (v || 0).toFixed(2));

const ms = ts => { const t = Date.parse(ts || ''); return isNaN(t) ? NaN : t; };

// toolSpanMs is a tool call's own run time from its start and end stamps.
/** @param {StoryTool} t @returns {number} NaN when either stamp is missing */
export function toolSpanMs(t) {
  return ms(t.ended_at) - ms(t.started_at);
}

// stepEnd is when a model turn's work finished: its last tool's end, or
// its generation's end when it ran no timed tool.
/** @param {StoryStep} s */
export function stepEnd(s) {
  let end = ms(s.timestamp) + (s.gen_ms || 0);
  for (const t of s.tools || []) {
    const e = ms(t.ended_at);
    if (e > end) end = e;
  }
  return end;
}

// idleText reads the gap between one turn's end and the next turn's start
// as "42 min idle" when it runs over five minutes, else ''.
/** @param {number} gapMs */
export function idleText(gapMs) {
  if (!(gapMs > IDLE_MS)) return '';
  const min = Math.floor(gapMs / 60000);
  if (min < 60) return `${min} min idle`;
  return min % 60 ? `${Math.floor(min / 60)} h ${min % 60} min idle` : `${Math.floor(min / 60)} h idle`;
}

// idleGaps lists, for each step after the first, the idle text before it
// ('' when the gap is five minutes or less).
/** @param {StoryStep[]} steps @returns {string[]} */
export function idleGaps(steps) {
  return steps.map((s, i) => (i === 0 ? '' : idleText(ms(s.timestamp) - stepEnd(steps[i - 1]))));
}

// callKey is a call's identity: its name and its whole input, by the
// payload's input_key when it has one, else the whole input text.
/** @param {StoryTool} t */
const callKey = t => `${t.name}\u0000${t.input_key ? `#${t.input_key}` : (t.input_full || t.input || '')}`;

// unresolvedTools is the set of errored calls whose exact input never
// later succeeded among the loaded steps.
/** @param {StoryStep[]} steps @returns {Set<StoryTool>} */
export function unresolvedTools(steps) {
  const out = new Set();
  const succeededAfter = new Set();
  const all = steps.flatMap(s => s.tools || []);
  for (let i = all.length - 1; i >= 0; i--) {
    const t = all[i];
    if (t.status === 'ok') succeededAfter.add(callKey(t));
    else if (t.status === 'error' && !succeededAfter.has(callKey(t))) out.add(t);
  }
  return out;
}

// unresolvedCount counts a group's tools in the unresolved set.
/** @param {StoryTool[]} tools @param {Set<StoryTool>} unresolved */
export function unresolvedCount(tools, unresolved) {
  return tools.filter(t => unresolved.has(t)).length;
}

const firstLine = s => String(s || '').split('\n').map(l => l.trim()).find(Boolean) || '';

// toolLabel is a call's one-line command: its input when it has one, else
// its detail, else its name.
/** @param {StoryTool} t */
export function toolLabel(t) {
  const line = String(t.input || '').replace(/\s+/g, ' ').trim();
  return line || t.detail || t.name || '';
}

// fullInput is a call's whole input: input_full when the snippet was cut.
/** @param {StoryTool} t */
export function fullInput(t) {
  return t.input_full || t.input || '';
}

// statusMark is ✓ / ✕ / … for a call that succeeded, errored or has no result.
/** @param {StoryTool} t */
export function statusMark(t) {
  return t.status === 'ok' ? '✓' : t.status === 'error' ? '✕' : '…';
}

// groupCommands cuts the steps into model-message runs: each step with
// text (or with no tools at all) leads, and the tool-only steps after it,
// up to the next lead, join its command group. Tool-only steps before any
// lead form a group with no lead.
/** @param {StoryStep[]} steps @returns {{lead: number, steps: number[]}[]} */
export function groupCommands(steps) {
  const out = [];
  let cur = null;
  steps.forEach((s, i) => {
    const tools = (s.tools || []).length;
    const leads = !!String(s.text || '').trim() || tools === 0;
    if (leads || !cur) {
      cur = { lead: leads ? i : -1, steps: [i] };
      out.push(cur);
    } else {
      cur.steps.push(i);
    }
  });
  return out;
}

// groupHeader is a command group's counts over every turn of the group,
// its lead included: its commands (a counted row counts each run), how many
// stayed unresolved, its wall span from its first turn to its last call's
// end, and the model cost of its turns.
/** @param {StoryStep[]} steps @param {Set<StoryTool>} unresolved */
export function groupHeader(steps, unresolved) {
  const tools = steps.flatMap(s => s.tools || []);
  const start = ms(steps[0].timestamp);
  // A turn whose calls carry no end stamp ends where the next turn starts.
  const end = Math.max(...steps.map(s => ((s.tools || []).some(t => isNaN(ms(t.ended_at)))
    ? Math.max(stepEnd(s), ms(s.timestamp) + (s.duration_ms || 0)) : stepEnd(s))));
  const durationMs = end > start ? end - start : 0;
  const cost = steps.reduce((a, s) => a + (s.cost_usd || 0), 0);
  const commands = tools.reduce((a, t) => a + (t.count > 1 ? t.count : 1), 0);
  const bad = unresolvedCount(tools, unresolved);
  const parts = [`${commands} ${commands === 1 ? 'command' : 'commands'}`];
  if (bad) parts.push(`${bad} unresolved`);
  parts.push(fmtDuration(durationMs));
  return { commands, unresolved: bad, durationMs, cost, text: parts.join(' · ') };
}

/** @param {StoryChild} c */
const childSpan = c => Math.max(0, ms(c.ended_at) - ms(c.started_at)) || 0;

/** @param {StoryChild} c */
const childName = c => c.agent_type || (c.kind === 'main' ? 'rollout' : 'sub-agent');

// childHeader is a sub-agent's collapsed header.
/** @param {StoryChild} c */
export function childHeader(c) {
  const n = (c.steps || []).length;
  return [childName(c), c.description, fmtUSD(c.cost_usd || 0), fmtDuration(childSpan(c)), `${n} ${n === 1 ? 'step' : 'steps'}`]
    .filter(Boolean).join(' · ');
}

// parallelClusters splits children, by start time, into runs whose spans
// overlap one another.
/** @param {StoryChild[]} children @returns {StoryChild[][]} */
export function parallelClusters(children) {
  const sorted = [...children].sort((a, b) => ms(a.started_at) - ms(b.started_at));
  const out = [];
  let end = -Infinity;
  for (const c of sorted) {
    if (out.length && ms(c.started_at) < end) out[out.length - 1].push(c);
    else { out.push([c]); end = -Infinity; }
    end = Math.max(end, ms(c.ended_at));
  }
  return out;
}

// parallelHeader names a run of overlapping children by count and its
// longest member.
/** @param {StoryChild[]} cluster */
export function parallelHeader(cluster) {
  const longest = cluster.reduce((a, c) => (childSpan(c) > childSpan(a) ? c : a), cluster[0]);
  return `${cluster.length} agents in parallel · longest: ${longest.description || childName(longest)} ${fmtDuration(childSpan(longest))}`;
}

// nestChildren places each child: under the step whose tool spawned it,
// else (its parent unknown or not loaded) before the first step that
// started after it.
/**
 * @param {StoryStep[]} steps
 * @param {StoryChild[]} children
 * @returns {{byStep: Map<number, StoryChild[]>, before: Map<number, {child: StoryChild, note: string}[]>}}
 *   before's key steps.length means after the last step
 */
export function nestChildren(steps, children) {
  const owner = new Map();
  steps.forEach((s, i) => (s.tools || []).forEach(t => { if (t.id) owner.set(t.id, i); }));
  const byStep = new Map();
  const before = new Map();
  for (const c of children || []) {
    const at = c.parent_tool_use_id ? owner.get(c.parent_tool_use_id) : undefined;
    if (at !== undefined) {
      if (!byStep.has(at)) byStep.set(at, []);
      byStep.get(at).push(c);
      continue;
    }
    const start = ms(c.started_at);
    let k = steps.findIndex(s => ms(s.timestamp) > start);
    if (k < 0) k = steps.length;
    if (!before.has(k)) before.set(k, []);
    before.get(k).push({ child: c, note: c.parent_tool_use_id ? 'parent step not loaded' : 'parent step unknown' });
  }
  return { byStep, before };
}

/**
 * A placed child: its note, and when it sits away from the call that
 * spawned it, that call's step row key and clock.
 * @typedef {{child: StoryChild, header: string, note: string, spawnKey: string, spawnClock: string}} ChildEntry
 */

// clusterBlock is the render block of one run of overlapping children: a
// lone child, or a parallel header over two or more.
/** @param {Omit<ChildEntry, 'header'>[]} entries */
function clusterBlock(entries) {
  const list = entries.map(e => ({ ...e, header: childHeader(e.child) }));
  return list.length > 1
    ? { kind: 'parallel', header: parallelHeader(list.map(e => e.child)), children: list }
    : { kind: 'child', ...list[0] };
}

// hiddenNotice discloses what the window and replay dedup left out.
/** @param {{replays?: number, outside_filter?: number}|null|undefined} hidden @returns {string[]} */
export function hiddenNotice(hidden) {
  const out = [];
  const f = (hidden && hidden.outside_filter) || 0;
  const r = (hidden && hidden.replays) || 0;
  if (f > 0) out.push(`${f} earlier ${f === 1 ? 'turn' : 'turns'} outside the date filter`);
  if (r > 0) out.push(`${r} replayed ${r === 1 ? 'turn' : 'turns'} from a resumed session folded`);
  return out;
}

// pageMath cuts a prompt's turns into story pages of `size`.
/** @param {number} turns @param {number} [size] @returns {{offset: number, limit: number}[]} */
export function pageMath(turns, size = PAGE_TURNS) {
  const out = [];
  for (let o = 0; o < turns; o += size) out.push({ offset: o, limit: Math.min(size, turns - o) });
  return out;
}

// moreTurns is what the "N more turns" row reads once `from`..`to` of a
// prompt's `turns` are loaded: the turns left and the loaded turns' cost.
/** @param {number} turns @param {number} to @param {StoryStep[]} loaded */
export function moreTurns(turns, to, loaded) {
  const n = Math.max(0, turns - to);
  const cost = loaded.reduce((a, s) => a + (s.cost_usd || 0), 0);
  return { n, cost, text: n ? `${n} more ${n === 1 ? 'turn' : 'turns'} · ${fmtUSD(cost)} so far` : '' };
}

/**
 * A row of the story, in order.
 * @typedef {{kind: 'prompt', key: string, clock: string, text: string, hidden: string, timestamp: string}
 *   | {kind: 'agent', key: string, index: number, step: StoryStep, clock: string, text: string, cost: number}
 *   | {kind: 'group', key: string, header: ReturnType<typeof groupHeader>, items: StoryItem[]}
 *   | {kind: 'idle', key: string, text: string}
 *   | ({kind: 'child', key: string} & ChildEntry)
 *   | {kind: 'parallel', key: string, header: string, children: ChildEntry[]}} StoryRow
 * @typedef {{kind: 'step', key: string, index: number, step: StoryStep, tool: StoryTool, toolIndex: number,
 *   clock: string, mark: string, label: string, title: string, durationMs: number, count: number}
 *   | {kind: 'idle', key: string, text: string}
 *   | ({kind: 'child', key: string} & ChildEntry)
 *   | {kind: 'parallel', key: string, header: string, children: ChildEntry[]}} StoryItem
 */

// storyRows lays out a prompt's loaded steps: the prompt, then each agent
// message followed by its command group, with idle gaps and sub-agents in
// place. prompt is null for a sub-agent's own steps.
/**
 * @param {StoryPrompt|null} prompt
 * @param {StoryStep[]} steps
 * @param {StoryChild[]} [children]
 * @param {string} [keyPrefix]
 * @returns {StoryRow[]}
 */
export function storyRows(prompt, steps, children = [], keyPrefix = '') {
  /** @type {StoryRow[]} */
  const rows = [];
  if (prompt) {
    const { visible, hidden } = splitWrappers(prompt.text);
    rows.push({ kind: 'prompt', key: `${keyPrefix}p`, clock: clock(prompt.timestamp), text: visible, hidden, timestamp: prompt.timestamp });
  }
  const unresolved = unresolvedTools(steps);
  const idle = idleGaps(steps);
  const { byStep, before } = nestChildren(steps, children);
  let blockSeq = 0;
  /** @param {Omit<ChildEntry, 'header'>[]} entries @returns {StoryRow} */
  const block = entries => /** @type {StoryRow} */ ({ ...clusterBlock(entries), key: `${keyPrefix}c${blockSeq++}` });
  const stepKey = (i, ti) => `${keyPrefix}s${i}.${ti}`;

  // Children whose spawning call is on the page cluster across the whole
  // page, so overlapping ones share a header even when successive turns
  // spawned them. A run sits after the last of its spawning calls' steps;
  // a member spawned by an earlier step names that step's call.
  /** @type {Map<number, StoryRow[]>} */
  const linkedRows = new Map();
  const ownerOf = new Map();
  for (const [i, list] of byStep) for (const c of list) ownerOf.set(c, i);
  for (const cluster of parallelClusters([...ownerOf.keys()])) {
    const at = Math.max(...cluster.map(c => ownerOf.get(c)));
    const entries = cluster.map(c => {
      const i = ownerOf.get(c);
      const ti = (steps[i].tools || []).findIndex(t => t.id === c.parent_tool_use_id);
      const t = steps[i].tools[ti];
      return i === at
        ? { child: c, note: '', spawnKey: '', spawnClock: '' }
        : { child: c, note: '', spawnKey: stepKey(i, ti), spawnClock: clock(t.started_at || steps[i].timestamp) };
    });
    if (!linkedRows.has(at)) linkedRows.set(at, []);
    linkedRows.get(at).push(block(entries));
  }
  const linkedAt = i => linkedRows.get(i) || [];

  // Children with no loaded parent step cluster among themselves, and each
  // run sits before the first step that started after its earliest member.
  /** @type {Map<number, StoryRow[]>} */
  const orphanRows = new Map();
  const orphans = [...before.values()].flat();
  for (const cluster of parallelClusters(orphans.map(x => x.child))) {
    const start = ms(cluster[0].started_at);
    let k = steps.findIndex(s => ms(s.timestamp) > start);
    if (k < 0) k = steps.length;
    const notes = [...new Set(cluster.map(c => orphans.find(x => x.child === c).note))].join(' · ');
    if (!orphanRows.has(k)) orphanRows.set(k, []);
    orphanRows.get(k).push(block(cluster.map(c => ({ child: c, note: notes, spawnKey: '', spawnClock: '' }))));
  }
  const orphansAt = i => orphanRows.get(i) || [];

  for (const g of groupCommands(steps)) {
    const first = g.steps[0];
    const idleRow = idle[first] ? [{ kind: 'idle', key: `${keyPrefix}i${first}`, text: idle[first] }] : [];
    rows.push(...orphansAt(first), .../** @type {StoryRow[]} */ (idleRow));
    if (g.lead >= 0) {
      const s = steps[g.lead];
      rows.push({ kind: 'agent', key: `${keyPrefix}a${g.lead}`, index: g.lead, step: s, clock: clock(s.timestamp), text: s.text || '', cost: s.cost_usd || 0 });
    }
    const groupSteps = g.steps.filter(i => (steps[i].tools || []).length > 0);
    if (groupSteps.length === 0) {
      rows.push(...linkedAt(first));
      continue;
    }
    /** @type {StoryItem[]} */
    const items = [];
    for (const i of groupSteps) {
      // The group's first turn had its orphans and idle gap placed above
      // the lead; every later turn carries its own inside the group.
      if (i !== first) {
        items.push(.../** @type {StoryItem[]} */ (orphansAt(i)));
        if (idle[i]) items.push({ kind: 'idle', key: `${keyPrefix}i${i}`, text: idle[i] });
      }
      const s = steps[i];
      (s.tools || []).forEach((t, ti) => {
        const span = toolSpanMs(t);
        items.push({
          kind: 'step', key: stepKey(i, ti), index: i, step: s, tool: t, toolIndex: ti,
          clock: clock(t.started_at || s.timestamp), mark: statusMark(t), label: toolLabel(t),
          title: fullInput(t) || t.detail || t.name || '',
          durationMs: isNaN(span) ? (s.duration_ms || 0) : Math.max(0, span),
          count: t.count > 1 ? t.count : 1,
        });
      });
      items.push(.../** @type {StoryItem[]} */ (linkedAt(i)));
    }
    rows.push({ kind: 'group', key: `${keyPrefix}g${first}`, header: groupHeader(g.steps.map(i => steps[i]), unresolved), items });
  }
  rows.push(...orphansAt(steps.length));
  return rows;
}

// searchEntries flattens the rows a search can land on, in page order:
// the prompt by its text, agent messages by their text and step rows by
// the call's input and detail.
/** @param {StoryRow[]} rows @returns {{key: string, text: string}[]} */
export function searchEntries(rows) {
  const out = [];
  for (const r of rows) {
    if (r.kind === 'prompt') out.push({ key: r.key, text: `${r.text}\n${r.hidden}` });
    else if (r.kind === 'agent') out.push({ key: r.key, text: r.text });
    else if (r.kind === 'group') {
      for (const it of r.items) {
        if (it.kind === 'step') out.push({ key: it.key, text: `${fullInput(it.tool)}\n${it.tool.detail || ''}` });
      }
    }
  }
  return out;
}

// searchSteps finds the next entry after `after` whose text contains the
// query, case-insensitively, wrapping to the top; -1 when none does.
/** @param {{key: string, text: string}[]} entries @param {string} query @param {number} [after] */
export function searchSteps(entries, query, after = -1) {
  const q = String(query || '').trim().toLowerCase();
  if (!q || !entries.length) return -1;
  for (let n = 1; n <= entries.length; n++) {
    const i = (after + n + entries.length) % entries.length;
    if (entries[i].text.toLowerCase().includes(q)) return i;
  }
  return -1;
}

// looksTruncated reports whether a snippet ends in the cap's ellipsis.
/** @param {string} s */
export function looksTruncated(s) {
  return typeof s === 'string' && s.endsWith('…');
}

// unwrapExit removes the "Exit code: N" / "Output:" header lines Codex
// writes above a command's full output, as the story's snippets already do;
// under a "Script completed" header it returns the JSON chunk's output.
/** @param {string} text */
export function unwrapExit(text) {
  const s = String(text || '');
  const cut = s.indexOf('\nOutput:\n');
  const head = cut >= 0 ? s.slice(0, cut) : s.endsWith('\nOutput:') ? s.slice(0, -8) : null;
  if (head == null) return s;
  const body = cut >= 0 ? s.slice(cut + 9) : '';
  if (/^(?:Exit code: |Process exited with code )-?\d+[ \t]*$/m.test(head)) return body;
  if (head.startsWith('Script completed\n')) {
    const out = scriptChunkOutput(body);
    if (out != null) return out;
  }
  return s;
}

// scriptChunkOutput is the output of the JSON chunk a "Script completed"
// envelope carries, perhaps behind a "Warning: truncated output" block, or
// null when the body is not one such chunk.
/** @param {string} body */
function scriptChunkOutput(body) {
  let rest = body.replace(/^\n+/, '');
  if (rest.startsWith('Warning: truncated output')) {
    const gap = rest.indexOf('\n\n');
    if (gap < 0) return null;
    rest = rest.slice(gap + 2);
  }
  let chunk;
  try { chunk = JSON.parse(rest); } catch { return null; }
  if (!chunk || typeof chunk !== 'object' || !Number.isInteger(chunk.exit_code) || typeof chunk.output !== 'string') return null;
  return chunk.output;
}

// firstLineOf is a prompt's first non-empty line, for the jump list.
/** @param {string} text */
export function firstLineOf(text) {
  return firstLine(splitWrappers(text).visible);
}
