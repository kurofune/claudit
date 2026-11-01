// Overview view — landing tab. Fetches /_claudit/api/overview, paints
// totals tiles, the ranked hotspot list, and the headline trend chart into
// the #view-overview section; under the hotspots, Show all opens every tool
// pattern (overview-tools.js).
//
// SPA equivalent of the legacy SSR'd Overview chrome
// (internal/render/totals_html.go + hotspots_html.go +
// the inline trendSection() IIFE in report.html.tmpl).

import { fetchOverview, fetchAnomalies } from './api.js';
import { fmtMoney, fmtNum, hitRatioPill, escHtml } from './format.js';
import { trendLineChart, hitRatioChart, forecastChart, wireChartInteractivity } from './charts.js';
import { overviewSkeleton, skeletonResetIfPending } from './skeleton.js';
import { sessionHash } from './router.js';
import { hotspotTrend, showForecastTab, barWidth, restoreExpanded, hashRank, hashShowAll, promptPreview } from './overview-logic.js';
import { toolPatternsHTML, wireToolPatterns } from './overview-tools.js';
import {
  providerFromSearch, providerTiles, providerLabel, shareText,
  emptyProviderText, API_EQUIVALENT_TIP,
} from './provider-filter.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

// apiEquivalentHTML marks every dollar figure on the Overview: claudit prices
// each provider's tokens at API list rates while the plans bill by
// subscription. Focusable so the tooltip opens from the keyboard too.
const apiEquivalentHTML = () =>
  `<span class="api-eq" tabindex="0" data-tooltip="${escHtml(API_EQUIVALENT_TIP)}">API-equivalent</span>`;

function totalsHTML(totals, overallHitRatio, totalTokens) {
  const cost = totals.CostUSD || 0;
  const sessions = totals.Sessions || 0;
  const turns = totals.Turns || 0;
  return `
    <div class="headline">
      <div class="label">${labelIcon('cost')}Total cost ${apiEquivalentHTML()}</div>
      <div class="value">${escHtml(fmtMoney(cost))}</div>
    </div>
    <div class="metric">
      <div class="label">${labelIcon('sessions')}Sessions</div>
      <div class="value">${escHtml(fmtNum(sessions))}</div>
    </div>
    <div class="metric">
      <div class="label">${labelIcon('turns')}Assistant turns</div>
      <div class="value">${escHtml(fmtNum(turns))}</div>
    </div>
    <div class="metric">
      <div class="label">${labelIcon('tokens')}Total tokens</div>
      <div class="value">${escHtml(fmtNum(totalTokens))}</div>
    </div>
    <div class="metric">
      <div class="label">${labelIcon('gauge')}Cache hit ratio</div>
      <div class="value">${hitRatioPill(overallHitRatio)}</div>
    </div>`;
}

// providerTilesHTML draws one tile per provider with data: its name,
// sessions, turns, dollars and share of the window's dollars.
function providerTilesHTML(rows, provider) {
  const tiles = providerTiles(rows, provider);
  if (tiles.length === 0) return '';
  return `<div class="provider-tiles" aria-label="Spend by provider">${tiles.map(r => `
    <div class="provider-tile" data-provider="${escHtml(r.provider)}">
      <div class="provider-tile-head">
        <span class="provider-tile-name">${escHtml(providerLabel(r.provider))}</span>
        ${apiEquivalentHTML()}
        <span class="provider-tile-share" title="Share of this window's dollars">${escHtml(shareText(r.share))}</span>
      </div>
      <div class="provider-tile-cost">${escHtml(fmtMoney(r.cost_usd || 0))}</div>
      <div class="provider-tile-meta">${escHtml(fmtNum(r.sessions || 0))} sessions · ${escHtml(fmtNum(r.turns || 0))} turns</div>
      <div class="provider-tile-bar" aria-hidden="true"><span style="width:${Math.max(0, Math.min(100, r.share || 0)).toFixed(1)}%"></span></div>
    </div>`).join('')}</div>`;
}

const kindLabel = kind => String(kind).replace(/_/g, ' ');

// The detail table's Command cell keeps the full input in its title and
// renders it on one line; the JSONL path stays in the copied prompt only.
function exampleRowHTML(e) {
  const sid = e.session_id || '';
  const full = e.input || '';
  const command = full
    ? `<td class="ex-cmd" title="${escHtml(full)}"><code>${escHtml(full.replace(/\s+/g, ' ').trim())}</code></td>`
    : '<td class="ex-cmd is-empty">(no input recorded)</td>';
  const session = sid
    ? `<a class="ex-session" href="${escHtml(sessionHash(sid))}" title="Open session ${escHtml(sid)}">${escHtml(sid.slice(0, 8))}</a>`
    : '<span class="ex-session is-empty">none</span>';
  return `<tr>
      <td class="num ex-tokens">${escHtml(fmtNum(e.context_tokens || 0))}</td>
      <td class="ex-sess">${session}</td>
      ${command}
    </tr>`;
}

function hotspotDetailHTML(h, rank) {
  const examples = h.examples || [];
  const table = examples.length
    ? `<table class="hotspot-detail" aria-label="Biggest invocations by context added">
        <colgroup><col class="c-tokens"><col class="c-session"><col class="c-command"></colgroup>
        <thead><tr><th scope="col" class="num">Tokens added</th><th scope="col">Session</th><th scope="col">Command</th></tr></thead>
        <tbody>${examples.map(exampleRowHTML).join('')}</tbody>
      </table>`
    : '<p class="small empty-state">No example invocations recorded for this hotspot.</p>';
  return `${table}
    <div class="actions">
      <span class="small">Copy prompt hands the coding agent in this repo the evidence, one change to make, and a target.</span>
      <a class="anchor-link" href="#overview/hotspot-${rank}" aria-label="Link to hotspot ${rank}">Link</a>
    </div>
    <details class="hotspot-prompt">
      <summary>Show prompt</summary>
      <p class="small">JSONL paths are left out here; Copy prompt includes them.</p>
      <pre></pre>
    </details>`;
}

const chevron = '<svg class="chev" viewBox="0 0 16 16" aria-hidden="true"><path d="M6 3.5 10.5 8 6 12.5" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"/></svg>';

function hotspotRowHTML(h, idx, hotspots) {
  const rank = idx + 1;
  const anchorID = `hotspot-${rank}`;
  const trend = hotspotTrend(h);
  const trendHTML = trend
    ? `<span class="h-trend is-${trend.dir}" title="${escHtml(trend.title)}">${escHtml(trend.text)}</span>`
    : '<span class="h-notrend" aria-hidden="true"></span>';
  const pct = (h.pct_of_total || 0).toFixed(1);
  const basis = h.pct_basis || 'context cost';
  return `<article class="hotspot" id="${anchorID}" aria-labelledby="${anchorID}-title">
    <div class="hotspot-row">
      <button type="button" class="hotspot-toggle" aria-expanded="false" aria-controls="${anchorID}-detail">
        ${chevron}
        <span class="rank">#${rank}</span>
        <span class="hotspot-kind">${escHtml(kindLabel(h.kind))}</span>
        <span class="h-title" id="${anchorID}-title">${escHtml(h.title)}</span>
        <span class="h-meta">
          <span class="h-share" title="${escHtml(`${pct}% of ${basis}`)}"><span style="width:${barWidth(h, hotspots).toFixed(1)}%"></span></span>
          <span class="h-pct">${pct}%</span>
          <span class="h-cost" title="Context cost: writing this output into context and re-reading it on later turns">${escHtml(fmtMoney(h.cost_usd))}</span>
          ${trendHTML}
        </span>
      </button>
      <button type="button" class="copy" data-hotspot="${idx}">Copy prompt</button>
    </div>
    <div class="hotspot-body" id="${anchorID}-detail" role="region" aria-labelledby="${anchorID}-title" hidden></div>
  </article>`;
}

function hotspotsHTML(hotspots) {
  if (!hotspots || hotspots.length === 0) {
    return `<p class="small empty-state">No hotspots in this window. Widen the date range to take in sessions whose tool calls put output into context.</p>`;
  }
  return hotspots.map((h, i) => hotspotRowHTML(h, i, hotspots)).join('');
}

function costPanelHTML(points, period, anomalies) {
  let peak = 0, sum = 0;
  for (const p of points) { if (p.cost_usd > peak) peak = p.cost_usd; sum += p.cost_usd; }
  const mean = sum / points.length;
  let deltaHtml = '';
  if (points.length >= 2) {
    const prev = points[points.length - 2].cost_usd || 0;
    const cur = points[points.length - 1].cost_usd || 0;
    if (prev > 0) {
      const d = 100 * (cur - prev) / prev;
      const cls = d >= 0 ? 'trend-delta-up' : 'trend-delta-down';
      const sign = d >= 0 ? '+' : '';
      deltaHtml = `<span>Latest vs prior: <strong class="${cls}">${sign}${d.toFixed(1)}%</strong></span>`;
    }
  }
  const latestHit = points[points.length - 1].hit_ratio || 0;
  return `
    <div class="trend-chart" data-trend-panel="cost">
      ${trendLineChart(points, period, anomalies)}
      ${hitRatioChart(points, period, anomalies)}
      <div class="trend-summary">
        <span>Buckets: <strong>${points.length}</strong></span>
        <span>Peak: <strong>${escHtml(fmtMoney(peak))}</strong></span>
        <span>Mean: <strong>${escHtml(fmtMoney(mean))}</strong></span>
        <span>Latest hit ratio: <strong>${(latestHit*100).toFixed(1)}%</strong></span>
        ${deltaHtml}
      </div>
    </div>`;
}

function trendSectionHTML(data, anomalies) {
  const points = data.trend_totals || [];
  // The API ships the bucket granularity on the payload; a same-day
  // window comes back as "hour" so the axis reads HH:MM. Fall back to
  // day for older payloads / the static report.
  const period = data.period || 'day';
  const forecast = data.forecast || null;
  const forecastTab = showForecastTab(forecast, period);
  if (points.length === 0 && !forecastTab) return '';

  const costPanel = points.length === 0
    ? `<div class="trend-chart" data-trend-panel="cost">
        <div class="small empty-state">No spend in this window yet.</div>
      </div>`
    : costPanelHTML(points, period, anomalies);

  if (!forecastTab) {
    return `<h2>Cost by ${escHtml(period)}</h2>
      <div class="small">Spend bucketed by ${escHtml(period)}.</div>
      ${costPanel}`;
  }

  return `<h2>Spend over time</h2>
    <div class="small">Spend bucketed by day, with a linear month-end projection.</div>
    <nav class="subtabs" aria-label="Trend views">
      <a class="subtab is-active" href="#" data-trend-tab="cost">Cost by day</a>
      <a class="subtab" href="#" data-trend-tab="forecast">Forecast</a>
    </nav>
    ${costPanel}
    <div class="trend-chart" data-trend-panel="forecast" hidden>
      ${forecastChart(forecast, points)}
    </div>`;
}

// Unpriced turns are counted as $0, so every total on this page is low by
// however much they would have cost. Show the volume behind each missing
// model — the name alone doesn't say whether the gap is worth acting on.
export function warningsHTML(unknownModels) {
  if (!unknownModels || unknownModels.length === 0) return '';
  const items = unknownModels.map(m =>
    `<li><code>${escHtml(m.provider ? `${m.provider}/${m.model}` : m.model)}</code> — ${fmtNum(m.tokens)} tokens across ${fmtNum(m.turns)} ${m.turns === 1 ? 'turn' : 'turns'}</li>`
  ).join('');
  return `<div class="warning-card" role="alert"><strong class="danger">Unpriced models:</strong> these turns count as $0, so the totals below are understated. Add them to <code>~/.config/claudit/prices.yaml</code>.<ul class="warning-list">${items}</ul></div>`;
}

// OPEN_KEY holds the open hotspot ranks across the SSE refresh, which
// reloads the whole page.
const OPEN_KEY = 'claudit.overview.openHotspots';

function readOpen() {
  try { return sessionStorage.getItem(OPEN_KEY); } catch { return null; }
}

function writeOpen(container) {
  const ranks = Array.from(container.querySelectorAll('.hotspot-toggle[aria-expanded="true"]'))
    .map(b => Number(b.closest('.hotspot').id.replace('hotspot-', '')));
  try { sessionStorage.setItem(OPEN_KEY, JSON.stringify(ranks)); } catch { /* storage off */ }
}

// setOpen renders the detail only while the row is open, so a collapsed
// row carries none of the example evidence.
function setOpen(card, h, open) {
  const toggle = card.querySelector('.hotspot-toggle');
  const body = card.querySelector('.hotspot-body');
  if (!toggle || !body) return;
  const rank = Number(card.id.replace('hotspot-', ''));
  toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  card.classList.toggle('is-open', open);
  body.innerHTML = open && h ? hotspotDetailHTML(h, rank) : '';
  body.hidden = !open;
  const details = body.querySelector('details.hotspot-prompt');
  if (details) {
    details.addEventListener('toggle', () => { if (details.open) fillPrompt(details, h); });
  }
}

// fillPrompt writes the path-free preview only once the user asks to see it.
function fillPrompt(details, h) {
  const pre = details.querySelector('pre');
  if (pre && !pre.textContent && h) pre.textContent = promptPreview(h);
}

// openDeepLink opens the #overview/hotspot-N row, or the #overview/all
// tool-patterns table, and scrolls it into view.
function openDeepLink(container, hotspots) {
  if (hashShowAll(window.location.hash)) { toolPatterns.open({ scroll: true }); return; }
  const rank = hashRank(window.location.hash);
  if (rank == null) return;
  const card = container.querySelector(`#hotspot-${rank}`);
  if (!card) return;
  if (!card.classList.contains('is-open')) setOpen(card, hotspots[rank - 1], true);
  if (typeof card.scrollIntoView === 'function') card.scrollIntoView({ block: 'start' });
}

// copyText falls back to a selected off-screen textarea when the Clipboard
// API is missing or refuses, so the row stays as the user left it.
function copyText(text) {
  const ta = document.createElement('textarea');
  ta.setAttribute('readonly', '');
  ta.setAttribute('aria-hidden', 'true');
  ta.style.position = 'fixed';
  ta.style.top = '-1000px';
  ta.style.opacity = '0';
  ta.value = text;
  ta.textContent = text;
  document.body.appendChild(ta);
  let ok = false;
  try {
    if (typeof ta.select === 'function') ta.select();
    ok = document.execCommand('copy') === true;
  } catch { ok = false; }
  ta.remove();
  return ok;
}

async function copyPrompt(btn, h) {
  const text = h ? h.prompt : '';
  let ok = false;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      ok = true;
    }
  } catch { ok = false; }
  if (!ok) ok = copyText(text);
  btn.textContent = ok ? 'Copied' : 'Copy failed — the browser blocked the clipboard';
  btn.classList.toggle('copied', ok);
  clearTimeout(btn._resetTimer);
  btn._resetTimer = setTimeout(() => { btn.textContent = 'Copy prompt'; btn.classList.remove('copied'); }, 2200);
}

function wireHotspots(container, hotspots) {
  const list = container.querySelector('.hotspots');
  if (!list) return;
  container.querySelectorAll('button.copy').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.preventDefault();
      copyPrompt(btn, hotspots[Number(btn.dataset.hotspot)]);
    });
  });
  const toggle = btn => {
    const card = btn.closest('.hotspot');
    const rank = Number(card.id.replace('hotspot-', ''));
    const open = btn.getAttribute('aria-expanded') !== 'true';
    setOpen(card, hotspots[rank - 1], open);
    writeOpen(list);
    if (!open && hashRank(window.location.hash) === rank) {
      history.replaceState(history.state, '', '#overview');
    }
  };
  container.querySelectorAll('button.hotspot-toggle').forEach(btn => {
    btn.addEventListener('click', () => toggle(btn));
    // Enter and Space toggle on keydown; cancelling the key stops the
    // button's own synthesized click from toggling it back.
    btn.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      e.preventDefault();
      if (!e.repeat) toggle(btn);
    });
    btn.addEventListener('keyup', (e) => { if (e.key === ' ') e.preventDefault(); });
  });
  for (const rank of restoreExpanded({ hash: window.location.hash, stored: readOpen() })) {
    const card = list.querySelector(`#hotspot-${rank}`);
    if (card) setOpen(card, hotspots[rank - 1], true);
  }
}

function wireTrendTabs(container) {
  container.querySelectorAll('.subtab[data-trend-tab]').forEach(tab => {
    tab.addEventListener('click', (e) => {
      e.preventDefault();
      const wanted = tab.dataset.trendTab;
      container.querySelectorAll('.subtab[data-trend-tab]').forEach(t =>
        t.classList.toggle('is-active', t.dataset.trendTab === wanted));
      container.querySelectorAll('[data-trend-panel]').forEach(p => {
        p.hidden = (p.dataset.trendPanel !== wanted);
      });
    });
  });
}

// updateNavMetric paints the per-tab metric in the sidebar. For
// Overview that's total cost; the helper handles missing nodes so
// later phases can add metrics for Cost/Sessions/etc. without
// special-casing here.
function updateNavMetric(view, text) {
  const el = document.getElementById(`nav-metric-${view}`);
  if (el) el.textContent = text;
}

function updateDateRange(first, last) {
  const el = document.getElementById('date-range');
  if (!el) return;
  // Serve mode: the date picker owns this label and paints the SELECTED
  // WINDOW (matching the picker) from urlToRange. Don't clobber it with
  // the data span. Static-report mode has no picker button, so fall
  // through and show the corpus's actual first→last turn span.
  if (document.getElementById('date-range-button')) return;
  if (!first || !last) { el.textContent = '—'; return; }
  el.textContent = `${first.slice(0,10)} → ${last.slice(0,10)}`;
}

let painted = false;
let paintedHotspots = [];
const NO_TOOL_PATTERNS = { open() {}, close() {} };
let toolPatterns = NO_TOOL_PATTERNS;

export async function paint() {
  const container = document.getElementById('view-overview');
  if (!container) return;
  // First render only; a later #overview/hotspot-N or #overview/all route
  // opens its row or the tool-patterns table.
  if (painted) { openDeepLink(container, paintedHotspots); return; }

  toolPatterns = NO_TOOL_PATTERNS;
  container.innerHTML = `<div class="view-head"><h1>${labelIcon('overview')}Overview</h1></div>
    ${overviewSkeleton()}`;

  let overview, anomalies;
  try {
    [overview, anomalies] = await Promise.all([
      fetchOverview(),
      fetchAnomalies().catch(() => ({ anomalies: [] })),
    ]);
  } catch (err) {
    const reason = err && err.status ? `HTTP ${err.status}${err.statusText ? ' ' + err.statusText : ''}` : (err && err.message) || 'network error';
    container.innerHTML = `<div class="view-head"><h1>${labelIcon('overview')}Overview</h1></div>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load overview:</strong> ${escHtml(reason)}
        <button type="button" class="retry" data-retry>Retry</button></div>`;
    const retry = container.querySelector('[data-retry]');
    if (retry) retry.addEventListener('click', () => { paint(); });
    skeletonResetIfPending('nav-metric-overview');
    skeletonResetIfPending('date-range');
    return;
  }

  const totals = overview.totals || {};
  const provider = providerFromSearch(window.location.search);
  if (provider !== 'all' && !(totals.Sessions > 0)) {
    container.innerHTML = `
      <div class="view-head"><h1>${labelIcon('overview')}Overview</h1></div>
      <p class="empty-state">${escHtml(emptyProviderText(provider))}</p>`;
    updateNavMetric('overview', fmtMoney(0));
    painted = true;
    return;
  }
  // OverallHitRatio is computed server-side (aggregate.OverallHitRatio)
  // and shipped on the payload — read it rather than reimplementing the
  // formula here. Single source of truth lives in Go.
  const overallHitRatio = overview.overall_hit_ratio || 0;

  container.innerHTML = `
    <div class="view-head"><h1>${labelIcon('overview')}Overview</h1></div>
    <div class="totals">${totalsHTML(totals, overallHitRatio, overview.total_tokens || 0)}</div>
    ${providerTilesHTML(overview.providers, provider)}
    ${warningsHTML(overview.unknown_models)}
    <div id="trend-section">${trendSectionHTML(overview, anomalies.anomalies || [])}</div>
    <h2>${labelIcon('flame')}Top context-cost hotspots</h2>
    <div class="small">Ranked by context cost: what each pattern's output cost to write into context and re-read on later turns. Each row's change is against the previous window of equal length; open a row for its biggest invocations. <strong>Copy prompt</strong> hands the evidence to the coding agent working in this repo (Claude Code or Codex).</div>
    <div class="hotspots">${hotspotsHTML(overview.hotspots)}</div>
    ${toolPatternsHTML()}`;

  paintedHotspots = overview.hotspots || [];
  wireHotspots(container, paintedHotspots);
  toolPatterns = wireToolPatterns(container.querySelector('.tool-patterns-section'));
  openDeepLink(container, paintedHotspots);
  wireTrendTabs(container);
  wireChartInteractivity(container);

  updateNavMetric('overview', fmtMoney(totals.CostUSD || 0));
  updateDateRange(totals.First, totals.Last);

  painted = true;
}

export function reset() {
  painted = false;
}
