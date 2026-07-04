// Feed lens rendering for the Agents tab, split out of view-agents.js.
// Pure HTML-string builders over agents-logic derivations; the core wires
// delegation/selection and calls renderControl.

import { fmtMoney, fmtNum, fmtCompact, escHtml } from './format.js';
import {
  buildEventFeed, buildLiveFeed, refKey, formatElapsed, baseName, originClass,
} from './agents-logic.js';
import {
  lastGraph, selectedRef, colorSlot, kindBadge, kindFamily,
  clockTime, shortId, clip,
} from './agents-shared.js';

// ── Feed lens (formerly Mission Control) ────────────────────────────────────

// renderControl draws the Feed lens: one scrolling .agent-feed box whose top
// stratum is a STICKY band of "live rows" — one per currently-running agent —
// pinned above the reverse-chronological event history that scrolls beneath it.
// (This replaces the old full-width Active-now card band; the live roster now
// lives inside the feed so the lens and detail drawer align at the top edge.)
// The live rows share the feed's iconography/hues; a left-rail green pulse +
// live-ticking timer marks them as alive against the static history rows.
export function renderControl(sessions) {
  const feed = buildEventFeed(lastGraph, { limit: 250 });
  const feedHTML = feed.length === 0
    ? `<div class="ac-idle">No activity yet.</div>`
    : feed.map(feedRowHTML).join('');

  return `
    <section class="mc-feed">
      <div class="mc-section-head">Live feed <span class="mc-count">${feed.length}</span></div>
      <div class="agent-feed" tabindex="0">${liveStratumHTML()}${feedHTML}</div>
    </section>`;
}

// liveStratumHTML is the sticky top band of the feed: a "RUNNING · n" header
// plus one live row per running agent. Renders nothing when nothing is running,
// so the feed collapses to pure history. It's a single grid item spanning all
// the feed's columns, with position:sticky pinning it as the history scrolls.
function liveStratumHTML() {
  const live = buildLiveFeed(lastGraph);
  if (!live.length) return '';
  return `<div class="feed-live">
    <div class="feed-live-head"><span class="mc-dot-live"></span>Running <span class="mc-count">${live.length}</span></div>
    ${live.map(liveRowHTML).join('')}
  </div>`;
}

// A live row mirrors a history feedRowHTML cell-for-cell — same six subgrid
// columns (time · ctx · agent · glyph · body · metric) — so the running roster
// lines up with the history beneath it. The differences that mark it "live": a
// green pulse in the time column instead of a timestamp, and a metric whose
// duration is a live-ticking elapsed timer (cost · elapsed · tokens, same hues
// and token figure as a history row).
function liveRowHTML(d) {
  const ref = refKey({ sessionId: d.sessionId, agentIndex: d.agentIndex });
  const c = colorSlot(d.agentIndex);
  const sel = ref === selectedRef ? ' is-selected' : '';
  const tool = d.currentTool
    ? `${kindBadge(d.currentToolKind)}<span class="fe-tool kind-${kindFamily(d.currentToolKind)}">${escHtml(d.currentTool)}</span>`
    : `<span class="live-idle">working…</span>`;
  const desc = d.kind !== 'main' && d.description
    ? ` <span class="fe-arg" title="${escHtml(d.description)}">${escHtml(clip(d.description, 72))}</span>` : '';
  // Metric mirrors feMetric (cost · dur · tok), but "dur" is a live-ticking
  // elapsed timer fed by the same data-elapsed contract tickTimers rewrites
  // against the wall clock (running → counts up from start).
  const parts = [];
  if (d.cost_usd) parts.push(`<span class="fe-m-cost">${escHtml(fmtMoney(d.cost_usd))}</span>`);
  parts.push(`<span class="fe-m-dur" data-elapsed data-start="${Number.isFinite(d.startedAt) ? d.startedAt : ''}" data-end="" data-running="1">${escHtml(formatElapsed(d.elapsedMs))}</span>`);
  if (d.tokens) parts.push(`<span class="fe-m-tok">${escHtml(fmtCompact(d.tokens))} tok</span>`);
  const metric = `<span class="fe-metric">${parts.join('<span class="fe-m-sep">·</span>')}</span>`;
  return `<div class="live-row${sel}" data-c="${c}" data-ref="${escHtml(ref)}" tabindex="0" role="button">
    <span class="fe-time live-pulse-cell"><span class="live-pulse" aria-label="running"></span></span>
    ${feCtx(d)}
    <span class="fe-agent" title="${escHtml(d.agentLabel)}">${escHtml(clip(d.agentLabel, 24))}</span>
    <span class="fe-glyph"></span>
    <span class="fe-body">${tool}${desc}</span>
    ${metric}
  </div>`;
}

function feedRowHTML(e) {
  const c = colorSlot(e.agentIndex);
  const time = clockTime(e.t);
  const ref = e.kind === 'tool'
    ? refKey({ sessionId: e.sessionId, agentIndex: e.agentIndex, stepIndex: e.stepIndex, toolIndex: e.toolIndex })
    : refKey({ sessionId: e.sessionId, agentIndex: e.agentIndex });
  const sel = ref === selectedRef ? ' is-selected' : '';
  let glyph = '<span class="fe-glyph fe-arrow">→</span>';
  let body;
  let metric = '';
  if (e.kind === 'spawn') {
    glyph = '<span class="fe-glyph fe-spawn">↳</span>';
    body = `<span class="fe-verb">spawn</span> <span class="fe-strong">${escHtml(e.agentLabel)}</span>${e.description ? ` <span class="fe-dim">${escHtml(e.description)}</span>` : ''}`;
  } else if (e.kind === 'done') {
    glyph = '<span class="fe-glyph fe-done">✓</span>';
    body = `<span class="fe-verb">done</span> <span class="fe-dim">${fmtNum(e.steps)} step${e.steps === 1 ? '' : 's'}</span>`;
    metric = feMetric(e.cost_usd, 0, e.tokens);
  } else {
    if (e.status === 'error') glyph = '<span class="fe-glyph fe-err">✗</span>';
    else if (e.status === 'ok') glyph = '<span class="fe-glyph fe-ok">✓</span>';
    const arg = e.input || e.detail;
    body = `${kindBadge(e.toolKind)}<span class="fe-tool kind-${kindFamily(e.toolKind)}">${escHtml(e.tool)}</span>${arg ? ` <span class="fe-arg" title="${escHtml(e.input || e.detail)}">${escHtml(clip(arg, 72))}</span>` : ''}`;
    metric = feMetric(e.cost_usd, e.durationMs, e.tokens);
  }
  return `<div class="fe-row fe-${e.kind}${sel}" data-c="${c}" data-ref="${escHtml(ref)}" tabindex="0" role="button">
    <span class="fe-time">${escHtml(time)}</span>
    ${feCtx(e)}
    <span class="fe-agent" title="${escHtml(e.agentLabel)}">${escHtml(clip(e.agentLabel, 24))}</span>
    ${glyph}
    <span class="fe-body">${body}</span>
    ${metric}
  </div>`;
}

// feCtx names the project→thread a feed row belongs to — the cross-session feed
// interleaves rows from every open session, so each one shows where it came
// from: the cwd's project folder and the short session id. The full path and
// id ride on the title for disambiguation when basenames collide.
function feCtx(e) {
  const proj = baseName(e.cwd) || '—';
  const thread = shortId(e.sessionId || '');
  const title = `${e.cwd || ''}${thread ? ` · ${e.sessionId}` : ''}`;
  // A headless (SDK) origin is marked with a compact accent tag, not the full
  // pill — the feed's ctx column is a single ellipsizing line, so the marker
  // leads (survives the clip) while the project/thread truncate behind it.
  const origin = originClass(e.entrypoint) === 'sdk'
    ? `<span class="fe-origin" title="Headless run (claude -p / Agent SDK)">sdk</span>`
    : '';
  return `<span class="fe-ctx" title="${escHtml(title)}">${origin}<span class="fe-ctx-proj">${escHtml(proj)}</span><span class="fe-ctx-sep">›</span><span class="fe-ctx-thread">${escHtml(thread)}</span></span>`;
}

// feMetric is the compact per-row cost·duration·tokens chip — the feed doubles
// as a spend/latency/token heat-map. Each figure gets its own hue (money /
// time / tokens) so the eye can pick one dimension out of the stream. Renders
// nothing when all are empty.
function feMetric(cost, ms, tokens) {
  const parts = [];
  if (cost) parts.push(`<span class="fe-m-cost">${escHtml(fmtMoney(cost))}</span>`);
  if (ms) parts.push(`<span class="fe-m-dur">${escHtml(formatElapsed(ms))}</span>`);
  if (tokens) parts.push(`<span class="fe-m-tok">${escHtml(fmtCompact(tokens))} tok</span>`);
  return parts.length ? `<span class="fe-metric">${parts.join('<span class="fe-m-sep">·</span>')}</span>` : '';
}
