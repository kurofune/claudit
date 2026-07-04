// Tree lens rendering for the Agents tab, split out of view-agents.js.
// The compact session→agent navigator rail with lazy agent bodies; the core
// wires delegation and calls renderInspector / onTreeToggle /
// ensureAgentExpanded.

import { fmtMoney, fmtNum, fmtCompact, escHtml } from './format.js';
import {
  flattenSession, agentLabel, agentTokens, agentElapsedMs, formatElapsed,
  parseTime, refKey, parseRefKey, resolveRef, spawnTargetIndex, baseName,
} from './agents-logic.js';
import {
  lastGraph, selectedRef, treeLimit, TREE_PAGE, openAgentBodies,
  colorSlot, kindBadge, cssEsc, originBadgeHTML, clockTime, shortId,
  shortModel,
} from './agents-shared.js';

// elapsedSpan renders a live-updating elapsed timer for one agent.
function elapsedSpan(agent, extraCls = '') {
  const start = parseTime(agent && agent.started_at);
  const end = parseTime(agent && agent.ended_at);
  const running = agent && agent.status === 'running';
  const ms = agentElapsedMs(agent, Date.now());
  return `<span class="${extraCls}" data-elapsed data-start="${Number.isFinite(start) ? start : ''}" data-end="${Number.isFinite(end) ? end : ''}" data-running="${running ? '1' : '0'}">${escHtml(formatElapsed(ms))}</span>`;
}

// ── Tree lens (formerly Inspector) ──────────────────────────────────────────

// The Tree lens is ONE compact, collapsible navigator rail (no more split
// list|log): each session is an expandable group, each agent an expandable node
// whose summary is its headline row and whose body is a tight step→tool log.
// Clicking any summary, turn, or tool sets the shared selection and fills the
// wide detail drawer on the right (where input/output/reasoning live); the drag
// handle between the rail and the drawer resizes the split. Native <details>
// carries the expand/collapse state, snapshotted across live re-renders by
// captureState/restoreState (session groups keyed by data-skey; agent bodies by
// openAgentBodies).
export function renderInspector(sessions) {
  const sel = resolveRef(lastGraph, selectedRef);
  if (!sessions.length) {
    return `<div class="itree" data-no-tooltip><div class="ac-idle">No agents in this window.</div></div>`;
  }
  // Render only the newest `treeLimit` sessions, but always far enough to include
  // the selected one (a ‹ › filter step can target a session past the cap).
  const selIdx = sel ? sessions.findIndex(s => (s.session_id || '') === sel.session.session_id) : -1;
  const shown = Math.min(sessions.length, Math.max(treeLimit, selIdx + 1));
  const tree = sessions.slice(0, shown).map((s, si) => itreeSessionHTML(s, si, sel)).join('');
  const more = sessions.length > shown
    ? `<button type="button" class="itree-more" data-tree-more>Show ${Math.min(TREE_PAGE, sessions.length - shown)} more · ${fmtNum(sessions.length - shown)} older ${sessions.length - shown === 1 ? 'session' : 'sessions'} hidden</button>`
    : '';
  return `<div class="itree" role="tree" aria-label="Agents" data-no-tooltip>${tree}${more}</div>`;
}

function itreeSessionHTML(session, si, sel) {
  const sid = session.session_id || '';
  const c = colorSlot(si);
  const agents = flattenSession(session);
  const rows = agents.map((a, i) => itreeAgentHTML(session, a, i, sel)).join('');
  return `<details class="itree-sess" data-skey="${escHtml(sid)}" open>
    <summary class="itree-sess-head" data-c="${c}" title="${escHtml(session.cwd || '')}">
      <span class="itree-caret" aria-hidden="true">▸</span>
      <span class="insp-sess-proj">${escHtml(baseName(session.cwd) || '—')}</span>
      <span class="insp-sess-sid" title="${escHtml(sid)}">${escHtml(shortId(sid))}</span>
      ${originBadgeHTML(session)}
    </summary>
    <div class="itree-sess-body">${rows}</div>
  </details>`;
}

function itreeAgentHTML(session, agent, agentIndex, sel) {
  const sid = session.session_id || '';
  const running = agent.status === 'running';
  const tokens = agentTokens(agent).total;
  const agentRef = refKey({ sessionId: sid, agentIndex });
  // The agent (or any step/tool inside it) being selected lights the row and
  // opens the node — so a spawn jump or the default selection lands expanded.
  const holdsSel = !!(sel && sel.session.session_id === sid && sel.agentIndex === agentIndex);
  if (holdsSel) openAgentBodies.add(agentRef);
  // Lazy body: only render the log for an expanded agent (selected or toggled
  // open earlier). Collapsed agents ship a bare placeholder, filled on expand.
  const open = holdsSel || openAgentBodies.has(agentRef);
  // Tight summary for the rail: name, an error flag if any, then duration + cost
  // pinned right. Step/token totals (and everything else) ride in the drawer.
  return `<details class="itree-agent" data-akey="${escHtml(agentRef)}"${open ? ' open' : ''} title="${escHtml(agentLabel(agent))}${tokens ? ` · ${fmtCompact(tokens)} tok` : ''}">
    <summary class="itree-agent-row${holdsSel ? ' is-selected' : ''}" data-ref="${escHtml(agentRef)}">
      <span class="itree-caret" aria-hidden="true">▸</span>
      <span class="insp-dot ${running ? 'is-running' : 'is-done'}"></span>
      <span class="insp-d-name">${escHtml(agentLabel(agent))}</span>
      ${agent.error_count ? `<span class="insp-d-stat insp-d-err" title="tool calls that errored">✗${fmtNum(agent.error_count)}</span>` : ''}
      <span class="insp-d-spacer"></span>
      <span class="insp-d-stat">${elapsedSpan(agent)}</span>
      <span class="insp-d-stat insp-d-cost">${escHtml(fmtMoney(agent.cost_usd || 0))}</span>
    </summary>
    <div class="itree-agent-body"${open ? ' data-rendered="1"' : ''}>${open ? itreeAgentBodyHTML(session, agent, agentIndex) : ''}</div>
  </details>`;
}

// itreeAgentBodyHTML is the inner of an agent node's lazy body: its description
// (sub-agents only) plus the step→tool log. Rendered inline for agents open at
// render time, and injected by fillAgentBody when one is expanded interactively.
function itreeAgentBodyHTML(session, agent, agentIndex) {
  const sid = session.session_id || '';
  const desc = agent.kind !== 'main' && agent.description
    ? `<p class="insp-d-desc">${escHtml(agent.description)}</p>` : '';
  const steps = (agent.steps || []);
  const stepHTML = steps.length === 0
    ? `<div class="ac-idle">No assistant turns recorded.</div>`
    : steps.map((st, i) => inspectorStepHTML(st, i, steps.length, sid, agentIndex, session)).join('');
  return `${desc}<div class="insp-steps">${stepHTML}</div>`;
}

// fillAgentBody syncs one agent node's lazy body to its open state: an expanded
// node gets its log rendered (once), a collapsed one is emptied to free the DOM.
// Shared by the toggle handler and ensureAgentExpanded.
function fillAgentBody(node) {
  const akey = node.dataset.akey;
  const body = node.querySelector('.itree-agent-body');
  if (!body) return;
  if (node.open) {
    openAgentBodies.add(akey);
    if (!body.dataset.rendered) {
      const r = resolveRef(lastGraph, akey);
      if (r) { body.innerHTML = itreeAgentBodyHTML(r.session, r.agent, r.agentIndex); body.dataset.rendered = '1'; }
    }
  } else {
    openAgentBodies.delete(akey);
    body.innerHTML = '';
    delete body.dataset.rendered;
  }
}

// onTreeToggle catches a user expanding/collapsing an agent node. The native
// `toggle` event doesn't bubble, so this is wired in the capture phase.
export function onTreeToggle(e) {
  const node = e.target;
  if (node instanceof Element && node.classList && node.classList.contains('itree-agent')) {
    fillAgentBody(node);
  }
}

// ensureAgentExpanded opens the node holding `ref` and renders its body, so a
// selection that lands inside a collapsed agent (a spawn jump, a ‹ › filter
// step) reveals the row instead of highlighting a node that isn't there.
export function ensureAgentExpanded(container, ref) {
  const p = parseRefKey(ref);
  if (!p) return;
  const akey = refKey({ sessionId: p.sessionId, agentIndex: p.agentIndex });
  const node = container.querySelector(`details.itree-agent[data-akey="${cssEsc(akey)}"]`);
  if (!node) return;
  node.open = true;
  fillAgentBody(node);
}

function inspectorStepHTML(step, i, total, sid, agentIndex, session) {
  const time = clockTime(parseTime(step.timestamp));
  const ref = refKey({ sessionId: sid, agentIndex, stepIndex: i });
  const sel = ref === selectedRef ? ' is-selected' : '';
  const tools = (step.tools || []);
  const toolHTML = tools.map((t, j) => toolRowHTML(t, sid, agentIndex, i, j, session)).join('');
  const model = step.model ? `<span class="insp-step-model">${escHtml(shortModel(step.model))}</span>` : '';
  const reasoned = (step.thinking || step.text)
    ? `<span class="insp-step-reason" title="this turn has reasoning — click to read it">✦ reasoned</span>` : '';
  return `<div class="insp-step">
    <div class="insp-step-head${sel}" data-ref="${escHtml(ref)}" tabindex="0" role="button">
      <span class="insp-step-n">${i + 1}/${total}</span>
      <span class="insp-step-time">${escHtml(time)}</span>
      ${model}
      ${reasoned}
      ${step.cost_usd ? `<span class="insp-step-cost">${escHtml(fmtMoney(step.cost_usd))}</span>` : ''}
      ${(() => { const tk = agentTokens(step).total; return tk ? `<span class="insp-step-toks" title="tokens this turn">${escHtml(fmtCompact(tk))}</span>` : ''; })()}
    </div>
    ${tools.length ? `<div class="insp-tools">${toolHTML}</div>` : ''}
  </div>`;
}

// In the compact tree a tool is ALWAYS one tight, clickable row (kind · name ·
// detail · status · cost); its full input/output lives in the shared drawer,
// which the click opens — so the rail stays a navigator, not a content dump.
function toolRowHTML(tool, sid, agentIndex, stepIndex, toolIndex, session) {
  const name = tool.name || '';
  const ref = refKey({ sessionId: sid, agentIndex, stepIndex, toolIndex });
  const sel = ref === selectedRef ? ' is-selected' : '';
  const detail = tool.detail ? `<span class="tr-detail">${escHtml(tool.detail)}</span>` : '';
  const status = tool.status === 'error'
    ? '<span class="tr-status tr-err" title="errored">✗</span>'
    : tool.status === 'ok' ? '<span class="tr-status tr-ok" title="ok">✓</span>' : '';
  // A spawning Agent call shows its sub-agent's cost inline (the blast radius
  // of one decision), with the full clickable rollup nested directly beneath.
  const costBadge = tool.spawned
    ? `<span class="tr-spawn-cost" title="cost of the sub-agent this call launched">+${escHtml(fmtMoney(tool.spawned.cost_usd || 0))}</span>` : '';
  const spawnRow = tool.spawned ? spawnRowHTML(tool, session) : '';
  return `<div class="tr${sel}" data-ref="${escHtml(ref)}" tabindex="0" role="button"><span class="tr-row">${kindBadge(tool.kind)}<span class="tr-name">${escHtml(name)}</span>${detail}${status}${costBadge}</span></div>${spawnRow}`;
}

// spawnRowHTML renders the nested sub-agent affordance under a spawning Agent
// call: the child's label plus the rolled-up "+$X · N tools · M errors across
// sub-agent". When the sub-agent is in this window it's a button that jumps the
// shared selection to it (data-ref = the child's agent refKey); otherwise it's
// a static badge. This is the Tree lens's nesting — each sub-agent shown under
// the exact step/tool that launched it.
function spawnRowHTML(tool, session) {
  const sp = tool.spawned;
  if (!sp || !session) return '';
  const idx = spawnTargetIndex(session, sp.agent_ref);
  const child = idx == null ? null : flattenSession(session)[idx];
  const parts = [`+${fmtMoney(sp.cost_usd || 0)}`];
  if (child) {
    const n = (child.steps || []).reduce((sum, st) => sum + (st.tools || []).length, 0);
    parts.push(`${fmtNum(n)} ${n === 1 ? 'tool' : 'tools'}`);
  }
  const errs = sp.error_count || 0;
  if (errs) parts.push(`${fmtNum(errs)} ${errs === 1 ? 'error' : 'errors'}`);
  const stats = `${parts.join(' · ')} across sub-agent`;
  const label = child ? agentLabel(child) : 'sub-agent';
  if (idx == null) {
    return `<div class="tr-spawn" title="This sub-agent isn't in the current window">
      <span class="tr-spawn-arrow" aria-hidden="true">↳</span>
      <span class="tr-spawn-label">${escHtml(label)}</span>
      <span class="tr-spawn-stats">${escHtml(stats)}</span>
    </div>`;
  }
  const childRef = refKey({ sessionId: session.session_id || '', agentIndex: idx });
  const selCls = childRef === selectedRef ? ' is-selected' : '';
  return `<button type="button" class="tr-spawn is-link${selCls}" data-ref="${escHtml(childRef)}" title="Jump to this sub-agent">
    <span class="tr-spawn-arrow" aria-hidden="true">↳</span>
    <span class="tr-spawn-label">${escHtml(label)}</span>
    <span class="tr-spawn-stats">${escHtml(stats)}</span>
  </button>`;
}
