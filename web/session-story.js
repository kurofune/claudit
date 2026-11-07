// @ts-check
// The session page's "What happened" section: one user prompt's turns at a
// time, verbatim and in order, each agent message followed by its command
// group, with idle gaps, nested sub-agents and step rows that expand to the
// turn's thinking and the call's exact input and output.

import { fetchSessionStory, fetchAgentToolFull, fetchAgentTurnFull } from './api.js';
import { escHtml } from './format.js';
import { skelLine } from './skeleton.js';
import {
  storyRows, searchEntries, searchSteps, hiddenNotice, moreTurns, fmtDuration, fmtUSD,
  looksTruncated, unwrapExit, clock, firstLineOf, fullInput, WRAPPER_LABEL, PAGE_TURNS,
} from './session-story-logic.js';
/** @import { ChildEntry } from './session-story-logic.js' */

/** @import { SessionStoryPage, StoryStep, StoryTool, StoryChild, StoryPrompt } from './api-types.js' */
/** @import { StoryRow, StoryItem, SearchEntry } from './session-story-logic.js' */

const FLASH_MS = 1900;

/**
 * @typedef {Object} StoryState
 * @property {string} sessionId
 * @property {number} gen                 bumps on every load and page change, so a stale fetch drops
 * @property {StoryPrompt[]} prompts
 * @property {number} promptIndex
 * @property {number} from                first loaded turn offset into the prompt
 * @property {number} to                  offset after the last loaded turn
 * @property {StoryStep[]} steps
 * @property {StoryChild[]} children
 * @property {{replays: number, outside_filter: number}} hidden
 * @property {Set<string>} open           expanded step rows, children and disclosures by key
 * @property {Map<string, StoryItem|StoryRow>} byKey
 * @property {SearchEntry[]} entries
 * @property {number} cursor              search position in entries
 * @property {string} pageError           a failed "more"/"earlier" load, shown on its row
 */

/** @type {WeakMap<Element, StoryState>} */
const states = new WeakMap();
/** @type {WeakSet<Element>} */
const wired = new WeakSet();

// Untruncated text fetched on "Show full", by tool id and by turn uuid; kept
// across repaints so an expanded row never reverts to its snippet.
/** @type {Map<string, {input: string, output: string}>} */
const fullTool = new Map();
/** @type {Map<string, {thinking: string, text: string}>} */
const fullTurn = new Map();

// paintStory mounts the story of sessionId in host, replacing whatever
// story host showed: every request the previous one still has in flight is
// dropped when it lands. With turnUuid it opens the page that carries that
// turn, opens the sub-agent that ran it if a sub-agent did, scrolls its row
// into view and flashes it. Resolves once the first
// page (or its error state) has painted.
/** @param {Element} host @param {string} sessionId @param {string} [turnUuid] */
export async function paintStory(host, sessionId, turnUuid = '') {
  wire(host);
  /** @type {StoryState} */
  const st = {
    sessionId, gen: 0, prompts: [], promptIndex: 0, from: 0, to: 0, steps: [], children: [],
    hidden: { replays: 0, outside_filter: 0 }, open: new Set(), byKey: new Map(), entries: [], cursor: -1, pageError: '',
  };
  states.set(host, st);
  await load(host, st, turnUuid ? { turn: turnUuid } : { prompt: 0, offset: 0 }, turnUuid);
}

// current reports whether st is still the story host shows and gen still
// its latest page, so an answer that arrives later may paint.
/** @param {Element} host @param {StoryState} st @param {number} gen */
const current = (host, st, gen) => states.get(host) === st && st.gen === gen;

/** @param {any} err */
const reasonOf = err => (err && err.status
  ? `HTTP ${err.status}${err.statusText ? ' ' + err.statusText : ''}`
  : (err && err.message) || 'network error');

/**
 * @param {Element} host @param {StoryState} st
 * @param {{prompt?: number, offset?: number, turn?: string}} req
 * @param {string} [flashTurn]
 */
async function load(host, st, req, flashTurn = '') {
  if (states.get(host) !== st) return;
  const gen = ++st.gen;
  host.innerHTML = shellHTML(`<div class="story-skel" aria-busy="true" aria-label="Loading the session">
      ${skelLine('story-skel-a')}${skelLine('story-skel-b')}${skelLine('story-skel-c')}${skelLine('story-skel-b')}</div>`);
  /** @type {SessionStoryPage} */
  let page;
  try {
    page = await fetchSessionStory(st.sessionId, req);
  } catch (err) {
    if (!current(host, st, gen)) return;
    if (err && err.status === 404 && req.turn) {
      await load(host, st, { prompt: 0, offset: 0 });
      return;
    }
    if (err && err.status === 404) {
      host.innerHTML = shellHTML(`<div class="story-empty" role="status"><p>Session not in this window</p>
        <a class="story-back" href="#agents">Back to sessions</a></div>`);
      return;
    }
    host.innerHTML = shellHTML(`<div class="warning-card story-error" role="alert"><strong class="danger">Could not load this session:</strong> ${escHtml(reasonOf(err))}
        <button type="button" class="retry" data-retry>Retry</button></div>`);
    const retry = host.querySelector('[data-retry]');
    if (retry) retry.addEventListener('click', () => { load(host, st, req, flashTurn); });
    return;
  }
  if (!current(host, st, gen)) return;
  st.prompts = page.prompts || [];
  st.promptIndex = page.page ? page.page.prompt_index : 0;
  st.from = page.page ? page.page.turn_offset : 0;
  st.steps = page.steps || [];
  st.to = st.from + st.steps.length;
  st.children = page.children || [];
  st.hidden = page.hidden || { replays: 0, outside_filter: 0 };
  st.open = new Set();
  st.cursor = -1;
  st.pageError = '';
  paintPage(host, st);
  if (flashTurn) revealTurn(host, st, flashTurn);
}

// revealTurn flashes the row of turn uuid, first opening the sub-agent that
// ran it when it is a sub-agent's turn; a sub-agent turn with no row of its
// own (no text, no call) flashes its sub-agent.
/** @param {Element} host @param {StoryState} st @param {string} uuid */
function revealTurn(host, st, uuid) {
  const sel = `[data-turn="${cssEsc(uuid)}"]`;
  const key = host.querySelector(sel) ? '' : childKeyOf(st, uuid);
  if (key) {
    st.open.add(key);
    paintRows(host, st);
    if (!host.querySelector(sel)) { jumpTo(host, `.story-rows [data-key="${cssEsc(key)}"]`); return; }
  }
  jumpTo(host, sel);
}

// childKeyOf is the key of the painted sub-agent whose steps hold turn
// uuid, '' when none does.
/** @param {StoryState} st @param {string} uuid */
function childKeyOf(st, uuid) {
  const holds = (/** @type {StoryChild} */ c) => (c.steps || []).some(s => s.uuid === uuid);
  for (const r of st.byKey.values()) {
    if (r.kind === 'child' && holds(r.child)) return r.key;
    if (r.kind === 'parallel') {
      const k = r.children.findIndex(e => holds(e.child));
      if (k >= 0) return `${r.key}.${k}`;
    }
  }
  return '';
}

/** @param {string} body */
const shellHTML = body => `<section class="story" aria-label="What happened">
  <h2 class="story-title">What happened</h2>${body}</section>`;

/** @param {string} s */
const cssEsc = s => String(s).replace(/["\\]/g, '\\$&');

/** @param {Element} host @param {StoryState} st */
function paintPage(host, st) {
  const n = st.prompts.length;
  const i = st.promptIndex;
  const nav = n > 0 ? `<div class="story-bar">
      <button type="button" class="story-nav" data-prev ${i <= 0 ? 'disabled' : ''}>Previous prompt</button>
      <span class="story-pos">Prompt ${i + 1} of ${n}</span>
      <button type="button" class="story-nav" data-next ${i >= n - 1 ? 'disabled' : ''}>Next prompt</button>
      <label class="story-find">
        <input type="search" class="story-search" placeholder="Find a command or text, Enter for next" aria-label="Find a command or text in this prompt"></label>
      <span class="story-find-status" role="status" aria-live="polite"></span>
    </div>
    ${n > 1 ? jumpListHTML(st) : ''}` : '';
  const notices = hiddenNotice(st.hidden).map(t => `<div class="story-notice">${escHtml(t)}</div>`).join('');
  host.innerHTML = shellHTML(`${nav}${notices}<div class="story-rows"></div>`);
  paintRows(host, st);
}

/** @param {StoryState} st */
function jumpListHTML(st) {
  const items = st.prompts.map((p, k) => {
    const line = firstLineOf(p.text) || '(no text)';
    const cur = k === st.promptIndex ? ' aria-current="true"' : '';
    return `<li><button type="button" class="story-jump-item" data-jump="${k}"${cur} title="${escHtml(line)}">
      <span class="story-who">You · ${escHtml(clock(p.timestamp))}</span>
      <span class="story-jump-text">${escHtml(line)}</span>
      <span class="story-jump-turns">${p.turns} ${p.turns === 1 ? 'turn' : 'turns'}</span></button></li>`;
  }).join('');
  return `<details class="story-jump"><summary>All ${st.prompts.length} prompts</summary><ol class="story-jump-list">${items}</ol></details>`;
}

/** @param {Element} host @param {StoryState} st */
function paintRows(host, st) {
  const box = host.querySelector('.story-rows');
  if (!box) return;
  const prompt = st.prompts[st.promptIndex] || null;
  if (!prompt && st.steps.length === 0) {
    box.innerHTML = '<div class="story-empty" role="status"><p>No turns recorded</p></div>';
    st.entries = [];
    return;
  }
  const rows = storyRows(prompt, st.steps, st.children);
  st.byKey = new Map();
  index(st, rows);
  st.entries = searchEntries(rows);
  const parts = [];
  if (st.from > 0) {
    const n = Math.min(PAGE_TURNS, st.from);
    parts.push(`<button type="button" class="story-page-row" data-earlier>${st.from} earlier ${st.from === 1 ? 'turn' : 'turns'} in this prompt · show ${n}</button>`);
  }
  parts.push(...rows.map(r => rowHTML(r, st)));
  if (st.steps.length === 0) parts.push('<div class="story-empty" role="status"><p>No turns recorded</p></div>');
  const more = moreTurns(prompt ? prompt.turns : 0, st.to, st.steps);
  if (more.n > 0) {
    parts.push(`<button type="button" class="story-page-row" data-more title="${escHtml(`${fmtUSD(more.cost)} is the model cost of the ${st.steps.length} turns loaded so far`)}">${escHtml(more.text)}</button>`);
  }
  if (st.pageError) parts.push(`<div class="story-page-error" role="alert">${escHtml(st.pageError)}</div>`);
  box.innerHTML = parts.join('');
}

/** @param {StoryState} st @param {(StoryRow|StoryItem)[]} rows */
function index(st, rows) {
  for (const r of rows) {
    st.byKey.set(r.key, r);
    if (r.kind === 'group') index(st, r.items);
  }
}

/** @param {StoryRow|StoryItem} r @param {StoryState} st @returns {string} */
function rowHTML(r, st) {
  switch (r.kind) {
    case 'prompt': return promptHTML(r, st);
    case 'agent': return agentHTML(r, st);
    case 'group': return `<div class="story-group" data-key="${escHtml(r.key)}">
        <div class="story-group-h"><span class="story-group-count">${escHtml(r.header.text)}</span>
          <span class="story-cost" title="Model cost of the turns that ran these commands">${escHtml(fmtUSD(r.header.cost))}</span></div>
        <div class="story-items">${r.items.map(it => rowHTML(it, st)).join('')}</div></div>`;
    case 'idle': return `<div class="story-idle" data-key="${escHtml(r.key)}">${escHtml(r.text)}</div>`;
    case 'step': return stepHTML(r, st);
    case 'child': return childHTML(r.key, r, st);
    case 'parallel': return `<div class="story-parallel" data-key="${escHtml(r.key)}">
        <div class="story-parallel-h">${escHtml(r.header)}</div>
        ${r.children.map((c, k) => childHTML(`${r.key}.${k}`, c, st)).join('')}</div>`;
    default: return '';
  }
}

/** @param {Extract<StoryRow, {kind: 'prompt'}>} r @param {StoryState} st */
function promptHTML(r, st) {
  const open = st.open.has(`${r.key}:wrap`);
  const wrap = r.hidden
    ? `<button type="button" class="story-disclose" data-toggle="${escHtml(r.key)}:wrap" aria-expanded="${open}">${escHtml(WRAPPER_LABEL)} · ${open ? 'hide' : 'show'}</button>
       ${open ? `<pre class="story-pre story-wrapper-text">${escHtml(r.hidden)}</pre>` : ''}`
    : '';
  const text = r.text ? `<div class="story-text">${escHtml(r.text)}</div>` : '';
  return `<div class="story-row story-you" data-key="${escHtml(r.key)}">
    <div class="story-who">You · ${escHtml(r.clock)}</div>${text}${wrap}</div>`;
}

/** @param {Extract<StoryRow, {kind: 'agent'}>} r @param {StoryState} st */
function agentHTML(r, st) {
  const s = r.step;
  const full = s.uuid ? fullTurn.get(s.uuid) : undefined;
  const text = full && full.text ? full.text : r.text;
  const showFull = s.uuid && !(full && full.text) && looksTruncated(r.text)
    ? `<button type="button" class="story-full" data-full-turn="${escHtml(r.key)}">Show full</button>` : '';
  const thinkOpen = st.open.has(r.key);
  const think = s.thinking
    ? `<button type="button" class="story-disclose" data-toggle="${escHtml(r.key)}" aria-expanded="${thinkOpen}">Thinking · ${thinkOpen ? 'hide' : 'show'}</button>
       ${thinkOpen ? thinkingHTML(r.key, s) : ''}`
    : '';
  const body = text ? `<div class="story-text">${escHtml(text)}</div>` : '<div class="story-text is-quiet">(no message text)</div>';
  return `<div class="story-row story-agent" data-key="${escHtml(r.key)}"${s.uuid ? ` data-turn="${escHtml(s.uuid)}"` : ''}>
    <div class="story-who">Agent · ${escHtml(r.clock)} <span class="story-cost" title="Model cost of this turn">${escHtml(fmtUSD(r.cost))}</span></div>
    ${body}${showFull}${think}</div>`;
}

/** @param {string} key @param {StoryStep} s */
function thinkingHTML(key, s) {
  const full = s.uuid ? fullTurn.get(s.uuid) : undefined;
  const text = full && full.thinking ? full.thinking : s.thinking || '';
  const showFull = s.uuid && !(full && full.thinking) && looksTruncated(s.thinking || '')
    ? `<button type="button" class="story-full" data-full-turn="${escHtml(key)}">Show full</button>` : '';
  return `<div class="story-sec"><div class="story-sec-h">Thinking${showFull}</div><pre class="story-pre">${escHtml(text)}</pre></div>`;
}

/** @param {Extract<StoryItem, {kind: 'step'}>} r @param {StoryState} st */
function stepHTML(r, st) {
  const t = r.tool;
  const open = st.open.has(r.key);
  const cls = t.status === 'error' ? ' is-error' : t.status === 'ok' ? ' is-ok' : ' is-pending';
  const status = t.status === 'ok' ? 'succeeded' : t.status === 'error' ? 'failed' : 'no result';
  const turn = r.toolIndex === 0 && r.step.uuid ? ` data-turn="${escHtml(r.step.uuid)}"` : '';
  const row = `<div class="story-step${cls}" role="button" tabindex="0" aria-expanded="${open}" data-step="${escHtml(r.key)}" data-key="${escHtml(r.key)}"${turn}>
      <span class="story-clock">${escHtml(r.clock)}</span>
      <span class="story-mark" role="img" aria-label="${status}">${r.mark}</span>
      <span class="story-cmd-cell"><span class="story-cmd" title="${escHtml(r.title)}">${escHtml(r.label)}</span>${r.count > 1
        ? `<span class="story-chip story-runs" title="This call ran ${r.count} times with the same input; the log kept one outcome">×${r.count}</span>` : ''}</span>
      <span class="story-dur">${escHtml(fmtDuration(r.durationMs))}</span>
    </div>`;
  return open ? row + detailHTML(r) : row;
}

/** @param {Extract<StoryItem, {kind: 'step'}>} r */
function detailHTML(r) {
  const t = r.tool;
  const full = t.id ? fullTool.get(t.id) : undefined;
  const input = full ? full.input : fullInput(t);
  const output = full ? full.output : t.output || '';
  const canFull = t.id && !full;
  const btn = field => (canFull && looksTruncated(field === 'input' ? fullInput(t) : t.output || '')
    ? `<button type="button" class="story-full" data-full-tool="${escHtml(r.key)}">Show full</button>` : '');
  const chip = t.exit_code != null ? `<span class="story-chip story-exit">exit code ${t.exit_code}</span>` : '';
  const think = r.step.thinking ? thinkingHTML(r.key, r.step) : '';
  const outBody = output
    ? `<pre class="story-pre">${escHtml(output)}</pre>`
    : `<div class="story-none">${t.status ? 'No output' : 'No result recorded'}</div>`;
  return `<div class="story-detail" data-detail="${escHtml(r.key)}">
    ${think}
    <div class="story-sec"><div class="story-sec-h">Input · ${escHtml(t.name)}${btn('input')}</div><pre class="story-pre">${escHtml(input)}</pre></div>
    <div class="story-sec"><div class="story-sec-h">Output ${chip}${btn('output')}</div>${outBody}</div>
  </div>`;
}

/** @param {string} key @param {ChildEntry} e @param {StoryState} st */
function childHTML(key, e, st) {
  const { child: c, header, note } = e;
  const open = st.open.has(key);
  const noteChip = note ? `<span class="story-chip story-note">${escHtml(note)}</span>` : '';
  const spawn = e.spawnKey
    ? `<button type="button" class="story-spawn" data-goto="${escHtml(e.spawnKey)}" title="Go to the call that spawned this agent">spawned at ${escHtml(e.spawnClock)}</button>` : '';
  let body = '';
  if (open) {
    const rows = storyRows(null, c.steps || [], [], `${key}/`);
    index(st, rows);
    body = `<div class="story-child-body">${rows.length ? rows.map(r => rowHTML(r, st)).join('') : '<div class="story-empty"><p>No turns recorded</p></div>'}</div>`;
  }
  return `<div class="story-child" data-key="${escHtml(key)}">
    <button type="button" class="story-child-h" data-toggle="${escHtml(key)}" aria-expanded="${open}">
      <span class="story-caret" aria-hidden="true">${open ? '▾' : '▸'}</span>${escHtml(header)}</button>${noteChip}${spawn}${body}</div>`;
}

// ── interaction ──────────────────────────────────────────────────────────

/** @param {Element} host */
function wire(host) {
  if (wired.has(host)) return;
  wired.add(host);
  host.addEventListener('click', ev => {
    const st = states.get(host);
    const el = ev.target && /** @type {Element} */ (ev.target).closest
      ? /** @type {Element} */ (ev.target).closest('[data-step], [data-toggle], [data-full-tool], [data-full-turn], [data-more], [data-earlier], [data-prev], [data-next], [data-jump], [data-goto]')
      : null;
    if (!st || !el || !host.contains(el)) return;
    onAction(host, st, el);
  });
  host.addEventListener('keydown', ev => {
    const st = states.get(host);
    const target = /** @type {Element} */ (ev.target);
    if (!st || !target || !target.matches) return;
    const key = /** @type {KeyboardEvent} */ (ev).key;
    if (target.matches('.story-search')) {
      if (key === 'Enter') { ev.preventDefault(); find(host, st, /** @type {HTMLInputElement} */ (target)); }
      else if (key === 'Escape') { ev.preventDefault(); clearFind(host, st, /** @type {HTMLInputElement} */ (target)); }
      return;
    }
    if (target.matches('[data-step]') && (key === 'Enter' || key === ' ')) {
      ev.preventDefault();
      onAction(host, st, target);
    }
  });
  host.addEventListener('input', ev => {
    const st = states.get(host);
    if (st && ev.target && /** @type {Element} */ (ev.target).matches('.story-search')) {
      st.cursor = -1;
      setFindStatus(host, '');
    }
  });
}

/** @param {Element} host @param {StoryState} st @param {Element} el */
function onAction(host, st, el) {
  const d = /** @type {HTMLElement} */ (el).dataset;
  if (d.step !== undefined || d.toggle !== undefined) {
    const key = d.step !== undefined ? d.step : d.toggle || '';
    if (st.open.has(key)) st.open.delete(key); else st.open.add(key);
    repaint(host, st, el);
    return;
  }
  if (d.goto !== undefined) { jumpTo(host, `.story-rows [data-key="${cssEsc(d.goto)}"]`); return; }
  if (d.fullTool !== undefined) { showFullTool(host, st, el, d.fullTool); return; }
  if (d.fullTurn !== undefined) { showFullTurn(host, st, el, d.fullTurn); return; }
  if (d.more !== undefined) { loadMore(host, st, el); return; }
  if (d.earlier !== undefined) { loadEarlier(host, st, el); return; }
  if (d.prev !== undefined && st.promptIndex > 0) { goPrompt(host, st, st.promptIndex - 1); return; }
  if (d.next !== undefined && st.promptIndex < st.prompts.length - 1) { goPrompt(host, st, st.promptIndex + 1); return; }
  if (d.jump !== undefined) goPrompt(host, st, Number(d.jump));
}

/** @param {Element} host @param {StoryState} st @param {number} i */
function goPrompt(host, st, i) {
  if (!(i >= 0 && i < st.prompts.length)) return;
  load(host, st, { prompt: i, offset: 0 }).then(() => {
    const title = host.querySelector('.story-title');
    if (title && title.scrollIntoView) title.scrollIntoView({ block: 'start' });
  });
}

// repaint redraws the rows and puts focus back on the control that acted,
// found again by its key.
/** @param {Element} host @param {StoryState} st @param {Element} [el] */
function repaint(host, st, el) {
  const d = el ? /** @type {HTMLElement} */ (el).dataset : {};
  const sel = d.step !== undefined ? `[data-step="${cssEsc(d.step)}"]`
    : d.toggle !== undefined ? `[data-toggle="${cssEsc(d.toggle)}"]` : '';
  const hadFocus = el && el.ownerDocument && el.ownerDocument.activeElement === el;
  paintRows(host, st);
  if (sel && hadFocus) {
    const again = /** @type {HTMLElement|null} */ (host.querySelector(sel));
    if (again) again.focus();
  }
}

/** @param {Element} host @param {StoryState} st @param {Element} btn @param {string} key */
async function showFullTool(host, st, btn, key) {
  const r = st.byKey.get(key);
  if (!r || r.kind !== 'step' || !r.tool.id) return;
  const gen = st.gen;
  const b = /** @type {HTMLButtonElement} */ (btn);
  b.disabled = true;
  b.textContent = 'Loading…';
  try {
    const d = await fetchAgentToolFull(st.sessionId, r.tool.id);
    const output = r.tool.exit_code != null ? unwrapExit(d.output || '') : d.output || '';
    fullTool.set(r.tool.id, { input: d.input || '', output });
    if (current(host, st, gen)) paintRows(host, st);
  } catch (err) {
    if (!current(host, st, gen)) return;
    b.disabled = false;
    b.textContent = `Failed (${reasonOf(err)}) · retry`;
  }
}

/** @param {Element} host @param {StoryState} st @param {Element} btn @param {string} key */
async function showFullTurn(host, st, btn, key) {
  const r = st.byKey.get(key);
  const step = r && (r.kind === 'agent' || r.kind === 'step') ? r.step : null;
  if (!step || !step.uuid) return;
  const gen = st.gen;
  const b = /** @type {HTMLButtonElement} */ (btn);
  b.disabled = true;
  b.textContent = 'Loading…';
  try {
    const d = await fetchAgentTurnFull(st.sessionId, step.uuid);
    fullTurn.set(step.uuid, { thinking: d.thinking || '', text: d.text || '' });
    if (current(host, st, gen)) paintRows(host, st);
  } catch (err) {
    if (!current(host, st, gen)) return;
    b.disabled = false;
    b.textContent = `Failed (${reasonOf(err)}) · retry`;
  }
}

/** @param {StoryChild[]} have @param {StoryChild[]} more */
function mergeChildren(have, more) {
  const key = c => `${c.parent_tool_use_id}|${c.started_at}|${c.description}|${c.agent_type}`;
  const seen = new Set(have.map(key));
  return have.concat((more || []).filter(c => !seen.has(key(c))));
}

/** @param {Element} host @param {StoryState} st @param {Element} btn */
async function loadMore(host, st, btn) {
  const gen = ++st.gen;
  const b = /** @type {HTMLButtonElement} */ (btn);
  b.disabled = true;
  b.textContent = 'Loading…';
  try {
    const page = await fetchSessionStory(st.sessionId, { prompt: st.promptIndex, offset: st.to });
    if (!current(host, st, gen)) return;
    st.steps = st.steps.concat(page.steps || []);
    st.to += (page.steps || []).length;
    st.children = mergeChildren(st.children, page.children);
    st.pageError = '';
    paintRows(host, st);
  } catch (err) {
    if (!current(host, st, gen)) return;
    st.pageError = `Could not load more turns: ${reasonOf(err)}. Select the row to retry.`;
    paintRows(host, st);
  }
}

/** @param {Element} host @param {StoryState} st @param {Element} btn */
async function loadEarlier(host, st, btn) {
  const gen = ++st.gen;
  const offset = Math.max(0, st.from - PAGE_TURNS);
  const b = /** @type {HTMLButtonElement} */ (btn);
  b.disabled = true;
  b.textContent = 'Loading…';
  const anchor = host.querySelector('.story-rows [data-turn]');
  const anchorKey = anchor ? anchor.getAttribute('data-turn') : null;
  try {
    const page = await fetchSessionStory(st.sessionId, { prompt: st.promptIndex, offset });
    if (!current(host, st, gen)) return;
    const got = (page.steps || []).slice(0, st.from - offset);
    st.steps = got.concat(st.steps);
    st.from = offset;
    st.children = mergeChildren(page.children || [], st.children);
    st.pageError = '';
    // Every key shifts with the prepended steps; drop the expansions.
    st.open = new Set();
    paintRows(host, st);
    const again = anchorKey ? host.querySelector(`[data-turn="${cssEsc(anchorKey)}"]`) : null;
    if (again && again.scrollIntoView) again.scrollIntoView({ block: 'start' });
  } catch (err) {
    if (!current(host, st, gen)) return;
    st.pageError = `Could not load earlier turns: ${reasonOf(err)}. Select the row to retry.`;
    paintRows(host, st);
  }
}

// ── search ───────────────────────────────────────────────────────────────

/** @param {Element} host @param {string} text */
function setFindStatus(host, text) {
  const s = host.querySelector('.story-find-status');
  if (s) s.textContent = text;
}

/** @param {Element} host @param {StoryState} st @param {HTMLInputElement} input */
function find(host, st, input) {
  const q = input.value;
  if (!q.trim()) { setFindStatus(host, ''); return; }
  const i = searchSteps(st.entries, q, st.cursor);
  if (i < 0) { setFindStatus(host, 'No step matches'); return; }
  st.cursor = i;
  const total = st.entries.filter(e => e.text.toLowerCase().includes(q.trim().toLowerCase())).length;
  const rank = st.entries.slice(0, i + 1).filter(e => e.text.toLowerCase().includes(q.trim().toLowerCase())).length;
  setFindStatus(host, `${rank} of ${total}`);
  const { key, open } = st.entries[i];
  const shut = open.filter(k => !st.open.has(k));
  if (shut.length) {
    shut.forEach(k => st.open.add(k));
    paintRows(host, st);
  }
  jumpTo(host, `.story-rows [data-key="${cssEsc(key)}"]`);
}

/** @param {Element} host @param {StoryState} st @param {HTMLInputElement} input */
function clearFind(host, st, input) {
  input.value = '';
  st.cursor = -1;
  setFindStatus(host, '');
}

/** @type {WeakMap<Element, any>} */
const flashTimers = new WeakMap();

// jumpTo scrolls the first element matching sel into view and flashes it.
/** @param {Element} host @param {string} sel */
function jumpTo(host, sel) {
  const el = host.querySelector(sel);
  if (!el) return;
  host.querySelectorAll('.is-jumped').forEach(x => x.classList.remove('is-jumped'));
  if (el.scrollIntoView) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  el.classList.add('is-jumped');
  clearTimeout(flashTimers.get(host));
  flashTimers.set(host, setTimeout(() => el.classList.remove('is-jumped'), FLASH_MS));
}
