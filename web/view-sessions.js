// Sessions view — the biggest payload-reduction tab. Fetches the
// lightweight session list (/api/sessions, ~50KB even on a busy
// corpus) and lazy-fetches each session's full timeline only when
// the user expands a card. Each timeline request is keyed per
// session ID and the response is cached client-side so a re-open
// doesn't refetch.
//
// SPA equivalent of the legacy SSR'd Sessions view in
// internal/render/sessions_html.go — the markup, class names, and
// color slots all mirror that file so web/app.css (lifted in
// Phase 5) renders without changes.
//
// Deep-link contract: #sessions/{id} or #sessions/session-{id}
// triggers the same expand + scroll behavior as a click. Matches
// the legacy fat-HTML report's URL contract — old bookmarks keep
// working after Phase 8 flips / to the SPA.

import { fetchSessions, fetchSessionTimeline } from './api.js';
import { fmtMoney, escHtml } from './format.js';
import { sessionListSkeleton, timelineSkeleton } from './skeleton.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

const SHELL = `
  <header class="view-head"><h1>${labelIcon('sessions')}Sessions</h1></header>
  <details class="guide">
    <summary>Drilling into a session</summary>
    <div class="body">
      <p>Each card below is one Claude Code session, ranked by total cost. Open a session to load and view its user prompts in order; open a prompt to see the assistant turns it produced. Per-session timelines load on demand — clicking a closed card is what fetches the data, so unused sessions never touch the wire.</p>
      <ul>
        <li><strong>Read top-down.</strong> The first session is your most expensive in this window — it's usually the most informative.</li>
        <li><strong>Look for prompt cost spikes.</strong> A single prompt that ran 30 turns and cost $5 is a prime target for tightening — fewer tool calls, narrower context, or a custom skill.</li>
        <li><strong>Each turn row</strong> packs: timestamp + the gap to the next turn within this prompt, the model, <code>in · out · cache</code> token counts, the dollar cost, and the tool chips that fired. Tool chips are color-coded by name — the same tool reads the same color across the whole report.</li>
        <li><strong>Sidechain turns</strong> carry a <em>sidechain</em> label — those are subagent runs nested inside the parent session.</li>
        <li>The small <code>#</code> link in each session's summary copies a shareable URL that re-opens this exact card.</li>
      </ul>
    </div>
  </details>

  <div id="session-list" class="session-list"></div>
  <div id="session-empty" class="empty-note" hidden>No sessions in this window. Try widening <code>--since</code>/<code>--until</code>.</div>
`;

// Tracks which sessions have already had their timelines fetched
// so a repeated open doesn't re-fire the network request. Keyed by
// session ID; the value is the resolved timeline or a Promise (the
// latter handles the race where the user clicks twice quickly).
const timelineCache = new Map();

let painted = false;
let navPainted = false;
// Tracks the most-recently-applied deep-link sub so a hashchange
// from the same value (no-op) doesn't re-trigger scroll/expand.
let appliedSub = null;

// paintNav fetches /sessions just to derive the sidebar metric (count
// · top cost). Called at startup so the metric resolves before the
// user clicks the tab. Cheap on cache hit.
export async function paintNav() {
  if (navPainted || painted) return;
  let payload;
  try { payload = await fetchSessions(); } catch { return; }
  const sessions = (payload && payload.sessions) || [];
  updateNavMetric(sessions, payload && payload.total_sessions);
  navPainted = true;
}

export async function paint(route) {
  const container = document.getElementById('view-sessions');
  if (!container) return;

  if (painted) {
    applyDeepLink(container, route.sub);
    return;
  }

  container.innerHTML = SHELL;
  const listEl = container.querySelector('#session-list');
  if (listEl) listEl.innerHTML = sessionListSkeleton(8);

  let payload;
  try {
    payload = await fetchSessions();
  } catch (err) {
    container.innerHTML = `<header class="view-head"><h1>${labelIcon('sessions')}Sessions</h1></header>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load sessions:</strong> ${escHtml(err.message)}</div>`;
    return;
  }

  const sessions = (payload && payload.sessions) || [];
  const list = container.querySelector('#session-list');
  const empty = container.querySelector('#session-empty');
  if (sessions.length === 0) {
    if (empty) empty.hidden = false;
  } else {
    list.innerHTML = sessions.map((s, i) => sessionCardHTML(s, (i % 5) + 1)).join('');
    wireCardOpens(list);
  }

  updateNavMetric(sessions, payload && payload.total_sessions);

  painted = true;
  navPainted = true;
  applyDeepLink(container, route.sub);
}

export function reset() {
  painted = false;
  navPainted = false;
  appliedSub = null;
  timelineCache.clear();
}

// sessionCardHTML mirrors renderSessionCard in
// internal/render/sessions_html.go. The session-body element starts
// empty — populated only when the user opens the card.
function sessionCardHTML(s, colorSlot) {
  const sid = escHtml(s.session_id || '');
  const cwd = s.cwd || '';
  const cwdEsc = escHtml(cwd);
  const turns = s.turns || 0;
  return `<details class="session-card" id="session-${sid}" data-session="${sid}">
    <summary>
      <span class="s-id" data-c="${colorSlot}" title="${sid}">${sid}</span>
      <span class="s-cwd" title="${cwdEsc}">${cwd === '' ? '&mdash;' : cwdEsc}</span>
      <span class="s-stats">
        <span>${turns} turn${turns === 1 ? '' : 's'}</span>
        <span class="s-cost">${escHtml(fmtMoney(s.cost_usd || 0))}</span>
        <a class="anchor-link" href="#sessions/session-${sid}" title="Copy link to this session" aria-label="Copy link to session">#</a>
      </span>
      <span class="s-time">${escHtml(formatTimeRange(s.started_at, s.ended_at))}</span>
    </summary>
    <div class="session-body" data-loaded="0">
      ${timelineSkeleton(3)}
    </div>
  </details>`;
}

// wireCardOpens binds one 'toggle' listener per session-card. The
// first open triggers fetchSessionTimeline + renderSessionBody;
// subsequent opens are no-ops because data-loaded flips to "1".
function wireCardOpens(list) {
  list.querySelectorAll('details.session-card').forEach(card => {
    card.addEventListener('toggle', () => {
      if (!card.open) return;
      const sid = card.dataset.session;
      const body = card.querySelector('.session-body');
      if (!body || body.dataset.loaded === '1') return;
      loadTimeline(sid, body);
    });
  });
}

async function loadTimeline(sid, body) {
  // Mark loaded immediately so concurrent re-opens during the fetch
  // don't fire a second request. data-loaded "1" gates the toggle
  // listener above.
  body.dataset.loaded = '1';
  let tl = timelineCache.get(sid);
  if (!tl) {
    const promise = fetchSessionTimeline(sid).catch(err => {
      timelineCache.delete(sid); // allow retry on a future open
      throw err;
    });
    timelineCache.set(sid, promise);
    try {
      tl = await promise;
      timelineCache.set(sid, tl);
    } catch (err) {
      body.innerHTML = `<div class="warning-card" role="alert"><strong class="danger">Failed to load timeline:</strong> ${escHtml(err.message)}</div>`;
      body.dataset.loaded = '0';
      return;
    }
  } else if (typeof tl.then === 'function') {
    // Already in-flight — wait for the same promise.
    try {
      tl = await tl;
    } catch (err) {
      body.innerHTML = `<div class="warning-card" role="alert"><strong class="danger">Failed to load timeline:</strong> ${escHtml(err.message)}</div>`;
      body.dataset.loaded = '0';
      return;
    }
  }
  body.innerHTML = renderSessionBody(tl);
}

// renderSessionBody mirrors the inner ".session-body" children that
// renderSessionCard in sessions_html.go writes after the summary.
function renderSessionBody(tl) {
  const prompts = (tl && tl.prompts) || [];
  if (prompts.length === 0) {
    return `<div class="small empty-state">(no prompts in this session — turns may be sidechain-only)</div>`;
  }
  return prompts.map(p => promptBlockHTML(p)).join('');
}

function promptBlockHTML(p) {
  const text = p.text || '';
  const isOrphan = !p.uuid;
  const isRedacted = text.startsWith('[redacted ');
  const turnN = (p.turns || []).length;
  const keyAttr = p.key ? ` data-prompt-key="${escHtml(p.key)}"` : '';
  const orphanAttr = isOrphan ? ` data-orphan="1"` : '';

  let textSpan;
  if (isOrphan) {
    textSpan = `<span class="p-text p-redacted">(orphan turns — no recognized originating prompt)</span>`;
  } else if (isRedacted) {
    textSpan = `<span class="p-text p-redacted">${escHtml(text)}</span>`;
  } else {
    textSpan = `<span class="p-text">${escHtml(text)}</span>`;
  }

  const truncatedNote = p.truncated
    ? `<div class="p-truncated">(prompt truncated — re-run with a higher --sessions cap or check the raw JSONL for the full text)</div>`
    : '';

  const turnList = (p.turns || []).map(turnRowHTML).join('');

  return `<details class="prompt-block"${orphanAttr}${keyAttr}>
    <summary>
      ${textSpan}
      <span class="p-stats">
        <span>${turnN} turn${turnN === 1 ? '' : 's'}</span>
        <span class="p-cost">${escHtml(fmtMoney(p.cost_usd || 0))}</span>
      </span>
    </summary>
    ${truncatedNote}
    <ul class="turn-list">${turnList}</ul>
  </details>`;
}

function turnRowHTML(t) {
  const model = t.model || '';
  const modelEsc = escHtml(model);
  const tokens = t.tokens || {};
  const tools = t.tools || [];
  const dur = formatDuration(t.duration_ms || 0);
  const durChip = dur
    ? ` <span class="t-dur" title="Time to next turn within this prompt">${escHtml(dur)}</span>`
    : '';
  const cacheChip = formatCacheChip(tokens);
  const cachePart = cacheChip ? ` · ${cacheChip}` : '';
  const sideChip = t.sidechain ? `<span class="t-side">sidechain</span>` : '';
  const toolChips = tools.map(toolChipHTML).join('');

  return `<li class="turn-row">
    <span class="t-time">${escHtml(formatHMS(t.timestamp))}${durChip}</span>
    <span class="t-model" title="${modelEsc}">${model === '' ? '&mdash;' : modelEsc}</span>
    <span class="t-tokens">${formatTokens(tokens)}${cachePart}</span>
    <span class="t-cost">${escHtml(fmtMoney(t.cost_usd || 0))}</span>
    <span class="t-tools">${toolChips}${sideChip}</span>
  </li>`;
}

function toolChipHTML(t) {
  const name = t.name || '';
  const detail = t.detail || '';
  const slot = toolColorSlot(name);
  const nameEsc = escHtml(name);
  const detailEsc = escHtml(detail);
  const title = detail ? ` title="${nameEsc} · ${detailEsc}"` : '';
  const detailSpan = detail ? ` · <span class="t-tool-detail">${detailEsc}</span>` : '';
  return `<span class="t-tool" data-c="${slot}"${title}>${nameEsc}${detailSpan}</span>`;
}

// toolColorSlot mirrors the FNV-1a hash → 1..5 mapping in
// internal/render/sessions_html.go's toolColorSlot. Same tool name
// gets the same color across both the SPA and the static report.
function toolColorSlot(name) {
  if (!name) return 0;
  let h = 0x811c9dc5 >>> 0;
  for (let i = 0; i < name.length; i++) {
    h ^= name.charCodeAt(i) & 0xff;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return (h % 5) + 1;
}

// formatDuration mirrors the rules in sessions_html.go's
// formatDuration: ms / "N.Ns" / "Ns" / "MmSs" depending on size.
function formatDuration(ms) {
  if (!ms || ms <= 0) return '';
  if (ms < 1000) return `${ms}ms`;
  const sec = ms / 1000;
  if (sec < 60) {
    if (sec < 10) return `${sec.toFixed(1)}s`;
    return `${Math.round(sec)}s`;
  }
  const m = Math.floor(sec / 60);
  const s = Math.round(sec) - m * 60;
  if (s === 0) return `${m}m`;
  return `${m}m${s}s`;
}

function formatHMS(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleTimeString([], { hour12: false });
}

function formatTime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  const pad = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// formatTimeRange mirrors sessions_html.go's formatTimeRange:
// "{start} → {end}" with an optional "(span)" suffix.
function formatTimeRange(startTs, endTs) {
  const left = formatTime(startTs);
  const right = formatTime(endTs);
  const span = formatSpan(startTs, endTs);
  if (!span) return `${left} → ${right}`;
  return `${left} → ${right} (${span})`;
}

function formatSpan(startTs, endTs) {
  if (!startTs || !endTs) return '';
  const start = new Date(startTs);
  const end = new Date(endTs);
  if (isNaN(start.getTime()) || isNaN(end.getTime())) return '';
  let secs = Math.round((end.getTime() - start.getTime()) / 1000);
  if (secs <= 0) return '';
  if (secs < 60) return `${secs}s`;
  const d = Math.floor(secs / 86400); secs -= d * 86400;
  const h = Math.floor(secs / 3600); secs -= h * 3600;
  const m = Math.floor(secs / 60); secs -= m * 60;
  const parts = [];
  if (d > 0) parts.push(`${d}d`);
  if (h > 0) parts.push(`${h}h`);
  if (h > 0 || m > 0) parts.push(`${m}m`);
  if (h > 0 && secs > 0) parts.push(`${secs}s`);
  return parts.join(' ');
}

function formatCount(n) {
  if (n == null) return '0';
  const v = Number(n);
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return String(v);
}

function formatTokens(tk) {
  const inp = tk.InputTokens || 0;
  const out = tk.OutputTokens || 0;
  return `${formatCount(inp)} in · ${formatCount(out)} out`;
}

function formatCacheChip(tk) {
  const r = tk.CacheReadTokens || 0;
  const c5 = tk.CacheCreate5mTokens || 0;
  const c1 = tk.CacheCreate1hTokens || 0;
  const total = r + c5 + c1;
  if (total === 0) return '';
  const parts = [];
  if (r > 0) parts.push(`${formatCount(r)} read`);
  if (c5 > 0) parts.push(`${formatCount(c5)} create 5m`);
  if (c1 > 0) parts.push(`${formatCount(c1)} create 1h`);
  return `<span class="t-cache" title="${parts.join(', ')}">${formatCount(total)} cache</span>`;
}

// applyDeepLink handles #sessions/{id} or #sessions/session-{id}.
// Finds the matching card, opens it (which triggers the toggle
// listener → lazy-fetch), and scrolls into view. Idempotent within
// the same sub value so a no-op hashchange doesn't re-scroll.
function applyDeepLink(container, sub) {
  if (!sub) {
    appliedSub = null;
    return;
  }
  if (sub === appliedSub) return;
  appliedSub = sub;

  // Accept both forms. The legacy anchor copy-link writes
  // "#sessions/session-{id}" — keep that working — but the plan
  // also calls for bare "#sessions/{id}". Try the explicit form
  // first; fall back to prefixing "session-" if the bare id was
  // passed in.
  const targetId = sub.startsWith('session-') ? sub : `session-${sub}`;
  const card = container.querySelector(`#${cssEscape(targetId)}`);
  if (!card) return;
  if (!card.open) card.open = true; // triggers loadTimeline
  // Defer scroll a tick so the open animation paints first.
  requestAnimationFrame(() => {
    card.scrollIntoView({ behavior: 'smooth', block: 'start' });
  });
}

// cssEscape wraps CSS.escape so the selector works on session IDs
// containing dashes (UUIDs always do). Falls back to a manual
// passthrough on the rare browser that lacks CSS.escape.
function cssEscape(s) {
  if (typeof CSS !== 'undefined' && typeof CSS.escape === 'function') {
    return CSS.escape(s);
  }
  return String(s).replace(/[^a-zA-Z0-9_-]/g, '\\$&');
}

function updateNavMetric(sessions, total) {
  const el = document.getElementById('nav-metric-sessions');
  if (!el) return;
  if (!sessions || sessions.length === 0) {
    el.textContent = '—';
    el.removeAttribute('title');
    return;
  }
  // Server-side sort is by cost desc, so sessions[0] is the top.
  const top = sessions[0];
  const shown = sessions.length;
  const cost = `top ${fmtMoney(top.cost_usd || 0)}`;
  if (total != null && total > shown) {
    // The list is capped (server --sessions / ?sessions). Show "N of M"
    // so this metric doesn't read as contradicting the Overview tile,
    // which counts ALL M sessions in the window. total_sessions ships
    // from Go (aggregate.Totals().Sessions) — the same source the tile
    // reads, so the two numbers reconcile.
    el.textContent = `${shown} of ${total} · ${cost}`;
    el.title = `Showing the top ${shown} sessions by cost; ${total} sessions ran in this window. Raise the --sessions cap (or open with ?scope=all) to list more.`;
  } else {
    // Not capped — the shown count is the full count.
    el.textContent = `${total != null ? total : shown} · ${cost}`;
    el.removeAttribute('title');
  }
}
