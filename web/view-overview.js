// Overview view — landing tab. Fetches /_claudit/api/overview, paints
// totals tiles, hotspot card stack, and the headline trend chart into
// the #view-overview section.
//
// SPA equivalent of the legacy SSR'd Overview chrome
// (internal/render/totals_html.go + hotspots_html.go +
// the inline trendSection() IIFE in report.html.tmpl).

import { fetchOverview, fetchAnomalies } from './api.js';
import { fmtMoney, fmtNum, hitRatioPill, escHtml } from './format.js';
import { trendLineChart, hitRatioChart, forecastChart, wireChartInteractivity } from './charts.js';
import { overviewSkeleton, skeletonResetIfPending } from './skeleton.js';
import { agentsSessionHash } from './router.js';
import { hotspotTrend } from './overview-logic.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

function totalsHTML(totals, overallHitRatio, totalTokens) {
  const cost = totals.CostUSD || 0;
  const sessions = totals.Sessions || 0;
  const turns = totals.Turns || 0;
  return `
    <div class="headline">
      <div class="label">${labelIcon('cost')}Total cost</div>
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

const kindLabel = kind => String(kind).replace(/_/g, ' ');

function exampleHTML(e) {
  const sid = e.session_id || '';
  const input = e.input
    ? `<code class="ex-input" title="${escHtml(e.input)}">${escHtml(e.input.replace(/\s+/g, ' ').trim())}</code>`
    : '<span class="ex-input is-empty">(no input recorded)</span>';
  const session = sid
    ? `<a class="ex-session" href="${escHtml(agentsSessionHash(sid))}" title="Open session ${escHtml(sid)} in the Agents tree">session ${escHtml(sid.length > 8 ? sid.slice(0, 8) : sid)}</a>`
    : '<span class="ex-session">no session</span>';
  return `<li class="hotspot-example">
      ${input}
      <span class="ex-meta"><span class="ex-tokens">${escHtml(fmtNum(e.context_tokens || 0))} tokens</span> · ${session} · <span class="ex-file">${escHtml(e.source_file || '')}</span></span>
    </li>`;
}

function hotspotCard(h, idx) {
  const rank = idx + 1;
  const anchorID = `hotspot-${rank}`;
  const trend = hotspotTrend(h);
  const trendHTML = trend
    ? `<span class="h-trend is-${trend.dir}" title="${escHtml(trend.title)}">${escHtml(trend.text)}</span>`
    : '';
  const examples = (h.examples || []);
  const examplesHTML = examples.length
    ? `<ol class="hotspot-examples" aria-label="Biggest invocations by context added">${examples.map(exampleHTML).join('')}</ol>`
    : '';
  return `<article class="hotspot" id="${anchorID}" aria-labelledby="${anchorID}-title">
    <header class="hotspot-head">
      <span class="rank">#${rank}</span>
      <span class="hotspot-kind">${escHtml(kindLabel(h.kind))}</span>
      <h3 class="h-title" id="${anchorID}-title">${escHtml(h.title)}</h3>
      <span class="h-cost" title="Context cost: writing this output into context and re-reading it on later turns">${escHtml(fmtMoney(h.cost_usd))}</span>
      <span class="h-pct">${(h.pct_of_total || 0).toFixed(1)}% of ${escHtml(h.pct_basis || 'context cost')}</span>
      ${trendHTML}
      <a class="anchor-link" href="#overview/${anchorID}" title="Copy link to this hotspot" aria-label="Copy link to hotspot ${rank}">#</a>
    </header>
    ${examplesHTML}
    <div class="actions">
      <button type="button" class="copy" data-hotspot="${idx}">Copy prompt</button>
      <span class="small">For the coding agent in this repo: the evidence, one change to make, and a target.</span>
    </div>
    <details class="hotspot-prompt">
      <summary>Show prompt</summary>
      <pre>${escHtml(h.prompt)}</pre>
    </details>
  </article>`;
}

function hotspotsHTML(hotspots) {
  if (!hotspots || hotspots.length === 0) {
    return `<p class="small empty-state">No hotspots in this window. Widen the date range to take in sessions whose tool calls put output into context.</p>`;
  }
  return hotspots.map(hotspotCard).join('');
}

function trendSectionHTML(data, anomalies) {
  const points = data.trend_totals;
  // The API ships the bucket granularity on the payload; a same-day
  // window comes back as "hour" so the axis reads HH:MM. Fall back to
  // day for older payloads / the static report.
  const period = data.period || 'day';
  if (!points || points.length === 0) return '';

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
  const forecast = data.forecast || null;
  const showForecastTab = !!forecast && (forecast.projected_month_end_usd || 0) > 0;

  const costPanel = `
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

  if (!showForecastTab) {
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

function wireHotspots(container, hotspots) {
  container.querySelectorAll('button.copy').forEach(btn => {
    btn.addEventListener('click', async (e) => {
      e.preventDefault();
      const card = btn.closest('.hotspot');
      const pre = card ? card.querySelector('pre') : null;
      const h = hotspots[Number(btn.dataset.hotspot)];
      const text = h ? h.prompt : (pre ? pre.textContent : '');
      let ok = false;
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) {
          await navigator.clipboard.writeText(text);
          ok = true;
        }
      } catch { ok = false; }
      if (!ok && pre) {
        const details = pre.closest('details');
        if (details) details.open = true;
        const range = document.createRange();
        range.selectNodeContents(pre);
        const sel = window.getSelection();
        sel.removeAllRanges(); sel.addRange(range);
        try { ok = document.execCommand('copy'); } catch { ok = false; }
      }
      btn.textContent = ok ? 'Copied' : 'Copy failed — select the prompt below';
      btn.classList.toggle('copied', ok);
      clearTimeout(btn._resetTimer);
      btn._resetTimer = setTimeout(() => { btn.textContent = 'Copy prompt'; btn.classList.remove('copied'); }, 2200);
    });
  });
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

export async function paint() {
  const container = document.getElementById('view-overview');
  if (!container) return;
  if (painted) return; // first-render only for Phase 5 — re-fetch comes with SSE-driven invalidation in a later phase.

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
  // OverallHitRatio is computed server-side (aggregate.OverallHitRatio)
  // and shipped on the payload — read it rather than reimplementing the
  // formula here. Single source of truth lives in Go.
  const overallHitRatio = overview.overall_hit_ratio || 0;

  container.innerHTML = `
    <div class="view-head"><h1>${labelIcon('overview')}Overview</h1></div>
    <div class="totals">${totalsHTML(totals, overallHitRatio, overview.total_tokens || 0)}</div>
    ${warningsHTML(overview.unknown_models)}
    <div id="trend-section">${trendSectionHTML(overview, anomalies.anomalies || [])}</div>
    <h2>${labelIcon('flame')}Top context-cost hotspots</h2>
    <div class="small">Ranked by context cost: what each pattern's output cost to write into context and re-read on later turns. Each card's change is against the previous window of equal length. <strong>Copy prompt</strong> hands the evidence to the coding agent working in this repo (Claude Code or Codex).</div>
    <div class="hotspots">${hotspotsHTML(overview.hotspots)}</div>`;

  wireHotspots(container, overview.hotspots || []);
  wireTrendTabs(container);
  wireChartInteractivity(container);

  updateNavMetric('overview', fmtMoney(totals.CostUSD || 0));
  updateDateRange(totals.First, totals.Last);

  painted = true;
}

export function reset() {
  painted = false;
}
