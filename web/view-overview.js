// Overview view — landing tab. Fetches /_claudit/api/overview, paints
// totals tiles, hotspot card stack, and the headline trend chart into
// the #view-overview section.
//
// SPA equivalent of the legacy SSR'd Overview chrome
// (internal/render/totals_html.go + hotspots_html.go +
// the inline trendSection() IIFE in report.html.tmpl).

import { fetchOverview, fetchAnomalies } from './api.js';
import { fmtMoney, fmtNum, hitRatioPill, escHtml, pointHitRatio } from './format.js';
import { trendLineChart, hitRatioChart, forecastChart, wireChartInteractivity } from './charts.js';
import { overviewSkeleton, skeletonResetIfPending } from './skeleton.js';

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
    <a class="metric metric-link" href="#tokens" title="Open the Tokens breakdown">
      <div class="label">${labelIcon('tokens')}Total tokens</div>
      <div class="value">${escHtml(fmtNum(totalTokens))}</div>
    </a>
    <div class="metric">
      <div class="label">${labelIcon('gauge')}Cache hit ratio</div>
      <div class="value">${hitRatioPill(overallHitRatio)}</div>
    </div>`;
}

function hotspotCard(h, idx) {
  const rank = idx + 1;
  const anchorID = `hotspot-${rank}`;
  const kindFmt = escHtml(String(h.kind).replace(/_/g, ' '));
  const titleEsc = escHtml(h.title);
  const promptEsc = escHtml(h.prompt);
  return `<details class="hotspot" id="${anchorID}">
    <summary>
      <span class="rank">#${rank}</span>
      <span class="hotspot-kind">${kindFmt}</span>
      <span class="h-title" title="${titleEsc}">${titleEsc}</span>
      <span class="h-cost">${escHtml(fmtMoney(h.cost_usd))}</span>
      <span class="h-pct">(${(h.pct_of_total || 0).toFixed(1)}%)</span>
      <a class="anchor-link" href="#overview/${anchorID}" title="Copy link to this hotspot" aria-label="Copy link to hotspot ${rank}">#</a>
    </summary>
    <div class="body">
      <div class="actions">
        <button class="copy" data-hotspot="${idx}">Copy prompt</button>
        <span class="small">Paste this into Claude or another LLM for specific advice.</span>
      </div>
      <pre>${promptEsc}</pre>
    </div>
  </details>`;
}

function hotspotsHTML(hotspots) {
  if (!hotspots || hotspots.length === 0) {
    return `<p class="small empty-state">No hotspots in this window. Try widening the time range with <code>--since</code>, or lowering <code>-hotspots</code>.</p>`;
  }
  return hotspots.map(hotspotCard).join('');
}

function trendSectionHTML(data, anomalies) {
  const points = data.trend_totals;
  // The API doesn't ship the period on /overview; infer from data
  // shape (we'll add it explicitly in a later phase if needed). For
  // now, day-bucketed is the dominant case in serve mode.
  const period = 'day';
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

  const latestHit = pointHitRatio(points[points.length - 1]);
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

function warningsHTML(unknownModels) {
  if (!unknownModels || unknownModels.length === 0) return '';
  return `<div class="warning-card" role="alert"><strong class="danger">Unpriced models:</strong> ${unknownModels.map(escHtml).join(', ')} — add them to <code>~/.config/claudit/prices.yaml</code>.</div>`;
}

function wireHotspots(container, hotspots) {
  container.querySelectorAll('button.copy').forEach(btn => {
    btn.addEventListener('click', async (e) => {
      e.preventDefault();
      const pre = btn.closest('.body').querySelector('pre');
      const text = pre ? pre.textContent : '';
      let ok = false;
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) {
          await navigator.clipboard.writeText(text);
          ok = true;
        }
      } catch { ok = false; }
      if (!ok && pre) {
        const range = document.createRange();
        range.selectNodeContents(pre);
        const sel = window.getSelection();
        sel.removeAllRanges(); sel.addRange(range);
        try { ok = document.execCommand('copy'); } catch { ok = false; }
      }
      const old = btn.textContent;
      btn.textContent = ok ? 'Copied — paste into your LLM' : 'Select & copy manually';
      btn.classList.add('copied');
      setTimeout(() => { btn.textContent = old; btn.classList.remove('copied'); }, 2200);
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
    container.innerHTML = `<div class="view-head"><h1>${labelIcon('overview')}Overview</h1></div>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load overview:</strong> ${escHtml(err.message)}</div>`;
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
    <h2>${labelIcon('flame')}Top cost hotspots</h2>
    <div class="small">Each card opens to reveal a tailored prompt — click <strong>Copy prompt</strong> and paste into Claude for specific advice.</div>
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
