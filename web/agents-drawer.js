// Shared detail drawer for the Agents tab, split out of view-agents.js.
// Renders the audit payload (buildDrawerPayload) for the shared selection and
// handles the serve-only "show full" loads; the core wires the drawer's click
// delegate and calls renderDrawer / loadFull / loadFullTurn.

import { fetchAgentToolFull, fetchAgentTurnFull } from './api.js';
import { fmtMoney, fmtNum, fmtCompact, escHtml } from './format.js';
import {
  buildDrawerPayload, resolveRef, parseRefKey, detectRetries,
  formatElapsed, looksTruncated,
} from './agents-logic.js';
import {
  lastGraph, selectedRef, fullCache, fullTurnCache,
  labelIcon, kindBadge, isServeMode, shortModel,
} from './agents-shared.js';

// ── shared detail drawer ────────────────────────────────────────────────────

export function renderDrawer(container) {
  const drawer = container.querySelector('.agents-drawer');
  if (!drawer) return;
  drawer.innerHTML = drawerHTML(buildDrawerPayload(lastGraph, selectedRef, fullCache, fullTurnCache), retryInfoFor(selectedRef));
}

// retryInfoFor reports whether the selected tool is a retry of an earlier
// errored call of the same (kind,name,detail) — { attempt, total, ofRefKey } —
// or null. `total` is the chain length (max attempt sharing the same first
// call); ofRefKey is the full refKey of attempt 1 so the drawer can link back.
// Pure lookup over detectRetries for the selected tool's agent.
function retryInfoFor(ref) {
  const parsed = parseRefKey(ref);
  if (!parsed || parsed.type !== 'tool') return null;
  const r = resolveRef(lastGraph, ref);
  if (!r || r.type !== 'tool') return null;
  const retries = detectRetries(r.agent);
  const entry = retries.get(`${parsed.stepIndex}:${parsed.toolIndex}`);
  if (!entry) return null;
  let total = entry.attempt;
  for (const v of retries.values()) {
    if (v.ofRef === entry.ofRef && v.attempt > total) total = v.attempt;
  }
  return { attempt: entry.attempt, total, ofRefKey: `${r.session.session_id || ''}#${r.agentIndex}.${entry.ofRef}` };
}

function drawerHTML(p, retry = null) {
  if (!p) return `<div class="dr-empty-state">Select an agent, turn, or tool to inspect it here.</div>`;

  // A retry chain: this tool repeats an earlier call that errored. Link back to
  // the first attempt so the whole chain is one click apart.
  const retryRow = retry
    ? `<button type="button" class="dr-retry" data-ref="${escHtml(retry.ofRefKey)}" title="Jump to the first attempt">
         <span class="dr-retry-icon" aria-hidden="true">↻</span> attempt ${retry.attempt} of ${retry.total}
       </button>`
    : '';

  const typeLabel = p.type === 'tool' ? 'tool' : p.type === 'step' ? 'turn' : (p.agentKind === 'main' ? 'main agent' : 'sub-agent');
  const desc = p.description ? `<p class="dr-desc">${escHtml(p.description)}</p>` : '';

  // An Agent call links straight to the sub-agent it launched, with that
  // sub-agent's rolled-up cost/errors — one decision's blast radius, one click
  // away. Only a navigable child (in this window) gets a link.
  const spawnRow = (p.spawned && p.spawned.childRef)
    ? `<button type="button" class="dr-spawn" data-ref="${escHtml(p.spawned.childRef)}" title="Jump to the sub-agent this call launched">
         <span class="dr-spawn-icon" aria-hidden="true">↳</span>
         <span class="dr-spawn-label">sub-agent</span>
         <span class="dr-spawn-stats">+${escHtml(fmtMoney(p.spawned.cost_usd || 0))}${p.spawned.error_count ? ` · ${fmtNum(p.spawned.error_count)} ${p.spawned.error_count === 1 ? 'error' : 'errors'}` : ''}</span>
       </button>`
    : '';

  // Compact metric chips — only the ones that apply to this kind.
  const metrics = [
    drMetric('cost', p.cost_usd ? fmtMoney(p.cost_usd) : ''),
    drMetric('dur', p.durationMs ? formatElapsed(p.durationMs) : ''),
    drMetric('model', p.model ? shortModel(p.model) : ''),
    drMetric('tokens', p.tokens && p.tokens.total ? `${fmtCompact(p.tokens.total)}` : ''),
    drMetric('steps', p.type === 'agent' && p.stepCount ? fmtNum(p.stepCount) : ''),
  ].filter(Boolean).join('');

  // Sections vary by level so no row is dead weight. "Reasoning" is the turn's
  // extended-thinking; "Message" is the assistant's prose for that turn (what it
  // says between tool calls) — both inherited from the parent step.
  //  - tool: Reasoning, Message, then its own Input/Output (the only level that
  //    has real tool I/O).
  //  - step (turn): Reasoning, Message, a Tools list (each row clicks through to
  //    that tool's I/O), and the per-turn token breakdown — never the
  //    always-empty I/O rows a turn would otherwise show.
  //  - agent: only the rolled-up tokens. An agent has no turn text of its own,
  //    so Reasoning/Message would always be empty placeholders — omit them.
  // The skeleton order is stable so the layout never jumps between selections.
  let sections;
  if (p.type === 'tool') {
    sections = [
      drTurnSection('Reasoning', p.thinking, 'thinking', p),
      drTurnSection('Message', p.text, 'text', p),
      drIOSection('Input', p.input, 'input', p),
      drIOSection('Output', p.output, 'output', p),
      // A tool inherits its turn's tokens; drTokens self-collapses at 0.
      drTokens(p.tokens),
    ].join('');
  } else if (p.type === 'step') {
    sections = [
      drTurnSection('Reasoning', p.thinking, 'thinking', p),
      drTurnSection('Message', p.text, 'text', p),
      drToolList(p.tools),
      drTokens(p.tokens),
    ].join('');
  } else {
    sections = drTokens(p.tokens);
  }

  return `<div class="dr">
    <div class="dr-head">
      ${kindBadge(p.kind)}
      <span class="dr-title" title="${escHtml(p.title)}">${escHtml(p.title)}</span>
      <span class="dr-type">${escHtml(typeLabel)}</span>
      ${statusPill(p.status)}
    </div>
    ${retryRow}
    ${spawnRow}
    <div class="dr-project" title="${escHtml(p.cwd)}">${labelIcon('overview')}<span class="dr-proj-name">${escHtml(p.project || '—')}</span></div>
    <div class="dr-sid" title="${escHtml(p.sessionId)}"><span class="dr-sid-id">${escHtml(p.sessionId || '—')}</span></div>
    <div class="dr-agentline"><span class="dr-agent">${escHtml(p.agentLabel)}</span>${p.detail ? ` <span class="dr-detail">${escHtml(p.detail)}</span>` : ''}</div>
    ${desc}
    ${metrics ? `<div class="dr-metrics">${metrics}</div>` : ''}
    ${sections}
  </div>`;
}

function drMetric(label, value) {
  return value ? `<span class="dr-metric"><span class="dr-m-k">${escHtml(label)}</span><span class="dr-m-v">${escHtml(value)}</span></span>` : '';
}

function statusPill(status) {
  if (status === 'running') return `<span class="ag-pill ag-running">running</span>`;
  if (status === 'done') return `<span class="ag-pill ag-done">done</span>`;
  if (status === 'ok') return `<span class="ag-pill ag-ok">✓ ok</span>`;
  if (status === 'error') return `<span class="ag-pill ag-err">✗ error</span>`;
  return '';
}

function drSection(label, content, pre) {
  const empty = content == null || content === '';
  if (empty) return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">${escHtml(label)} <span class="dr-none">—</span></h4></section>`;
  const body = pre
    ? `<pre class="dr-pre">${escHtml(content)}</pre>`
    : `<div class="dr-text">${escHtml(content)}</div>`;
  return `<section class="dr-sec"><h4 class="dr-sec-h">${escHtml(label)}</h4>${body}</section>`;
}

// drIOSection renders a tool's Input/Output as a <pre>, with a "show full"
// affordance when the snippet was truncated (looksTruncated). In serve mode
// that's a button that loads the untruncated content from disk; in static
// mode there's no disk, so it degrades to a clear "snippet only" label rather
// than a dead button.
function drIOSection(label, content, field, p) {
  const empty = content == null || content === '';
  if (empty) {
    return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">${escHtml(label)} <span class="dr-none">—</span></h4></section>`;
  }
  let affordance = '';
  if (p.type === 'tool' && p.toolId && looksTruncated(content)) {
    affordance = isServeMode()
      ? `<button type="button" class="dr-full-btn" data-loadfull="${escHtml(field)}" data-session="${escHtml(p.sessionId)}" data-tool="${escHtml(p.toolId)}">show full</button>`
      : `<span class="dr-full-note" title="Run claudit serve to load the full content">snippet only</span>`;
  }
  return `<section class="dr-sec"><h4 class="dr-sec-h">${escHtml(label)}${affordance}</h4><pre class="dr-pre">${escHtml(content)}</pre></section>`;
}

// drTurnSection renders a turn's Reasoning/Message as a <pre>, with the same
// "show full" affordance drIOSection gives tool I/O: when the snippet was
// truncated (looksTruncated) and the turn has a uuid to fetch by, serve mode
// gets a button that loads the untruncated content from disk; static mode
// degrades to a "snippet only" label rather than a dead button.
function drTurnSection(label, content, field, p) {
  const empty = content == null || content === '';
  if (empty) {
    return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">${escHtml(label)} <span class="dr-none">—</span></h4></section>`;
  }
  let affordance = '';
  if (p.turnUuid && looksTruncated(content)) {
    affordance = isServeMode()
      ? `<button type="button" class="dr-full-btn" data-loadfullturn="${escHtml(field)}" data-session="${escHtml(p.sessionId)}" data-turn="${escHtml(p.turnUuid)}">show full</button>`
      : `<span class="dr-full-note" title="Run claudit serve to load the full content">snippet only</span>`;
  }
  return `<section class="dr-sec"><h4 class="dr-sec-h">${escHtml(label)}${affordance}</h4><pre class="dr-pre">${escHtml(content)}</pre></section>`;
}

// loadFull handles a "show full" click: fetch the untruncated tool I/O from
// the server and swap it into the section's <pre>. The button is removed once
// the full content is in (there's nothing more to load); a failure re-enables
// it so the user can retry.
export async function loadFull(btn) {
  const sec = btn.closest('.dr-sec');
  const pre = sec && sec.querySelector('.dr-pre');
  const { session, tool, loadfull: field } = btn.dataset;
  if (!pre || !session || !tool) return;
  btn.disabled = true;
  btn.textContent = 'loading…';
  try {
    const d = await fetchAgentToolFull(session, tool);
    const fullText = (field === 'output' ? d.output : d.input) || '';
    // Cache by tool_use id so the next drawer paint (e.g. a live SSE tick)
    // re-applies the full content instead of reverting to the snippet.
    fullCache[tool] = { ...(fullCache[tool] || {}), [field]: fullText };
    pre.textContent = fullText;
    btn.remove();
  } catch {
    btn.textContent = 'failed — retry';
    btn.disabled = false;
  }
}

// loadFullTurn is loadFull's turn-level twin: fetch the untruncated
// thinking/text for the turn and swap it into the section's <pre>. Cached by
// turn uuid so the next drawer paint re-applies the full content instead of
// reverting to the snippet; a failure re-enables the button for retry.
export async function loadFullTurn(btn) {
  const sec = btn.closest('.dr-sec');
  const pre = sec && sec.querySelector('.dr-pre');
  const { session, turn, loadfullturn: field } = btn.dataset;
  if (!pre || !session || !turn) return;
  btn.disabled = true;
  btn.textContent = 'loading…';
  try {
    const d = await fetchAgentTurnFull(session, turn);
    const fullText = (field === 'text' ? d.text : d.thinking) || '';
    fullTurnCache[turn] = { ...(fullTurnCache[turn] || {}), [field]: fullText };
    pre.textContent = fullText;
    btn.remove();
  } catch {
    btn.textContent = 'failed — retry';
    btn.disabled = false;
  }
}

// drToolList renders a turn's tool calls as a list of click-through rows
// (kind badge · name · detail · status pill). Each row carries the tool's
// data-ref so the shared drawer delegate jumps the selection straight to that
// tool's Input/Output. Empty turns collapse to a dim "—" header like any other
// section, so a tool-only turn never renders as an empty slab.
function drToolList(tools) {
  if (!tools || !tools.length) {
    return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">Tools <span class="dr-none">—</span></h4></section>`;
  }
  const rows = tools.map(t => {
    const detail = t.detail ? `<span class="dr-tool-detail" title="${escHtml(t.detail)}">${escHtml(t.detail)}</span>` : '';
    const pill = t.status ? statusPill(t.status) : '';
    return `<button type="button" class="dr-tool-row" data-ref="${escHtml(t.refKey)}" title="${escHtml(t.name)}">
      ${kindBadge(t.kind)}<span class="dr-tool-name">${escHtml(t.name)}</span>${detail}${pill}
    </button>`;
  }).join('');
  return `<section class="dr-sec"><h4 class="dr-sec-h">Tools <span class="dr-sec-sum">${tools.length}</span></h4>
    <div class="dr-tools">${rows}</div></section>`;
}

function drTokens(t) {
  if (!t || !t.total) return `<section class="dr-sec is-empty"><h4 class="dr-sec-h">Tokens <span class="dr-none">—</span></h4></section>`;
  const cell = (k, v) => `<div class="dr-tok"><span class="dr-tok-k">${k}</span><span class="dr-tok-v">${escHtml(fmtNum(v))}</span></div>`;
  return `<section class="dr-sec"><h4 class="dr-sec-h">Tokens <span class="dr-sec-sum">${escHtml(fmtCompact(t.total))} total</span></h4>
    <div class="dr-toks">${cell('input', t.input)}${cell('output', t.output)}${cell('cache write', t.cacheWrite)}${cell('cache read', t.cacheRead)}</div>
  </section>`;
}
