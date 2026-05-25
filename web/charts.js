// SVG chart builders ported from the inline JS in
// internal/render/report.html.tmpl (lines 2150-2550). Kept as plain
// template-string emitters so the rendered SVG is byte-for-byte
// equivalent to the legacy surface — the static report can swap
// between SSR-only and SPA-hosted chrome without users seeing a
// rendering shift.

import { fmtMoney, escHtml, bucketLabel, pointHitRatio, fmtCompact } from './format.js';

// TOKEN_BANDS — the stacked-area layers, bottom→top. Largest category
// (cache read) sits on the bottom so the baseline stays visually
// stable; output (smallest, most expensive) rides on top where it's
// easiest to read. Each band's CSS class supplies its fill color.
const TOKEN_BANDS = [
  { key: 'cacheRead',  label: 'Cache read',  cls: 'tok-area-cread'  },
  { key: 'cacheWrite', label: 'Cache write', cls: 'tok-area-cwrite' },
  { key: 'input',      label: 'Input',       cls: 'tok-area-input'  },
  { key: 'output',     label: 'Output',      cls: 'tok-area-output' },
];

// normalizeTokenPoint flattens a TrendPoint's five raw token fields
// into the four chart bands (cache_create_5m + _1h collapse into one
// "cache write" band) plus the per-period total.
function normalizeTokenPoint(p, period) {
  const input = p.InputTokens || 0;
  const output = p.OutputTokens || 0;
  const cacheWrite = (p.CacheCreate5mTokens || 0) + (p.CacheCreate1hTokens || 0);
  const cacheRead = p.CacheReadTokens || 0;
  return {
    label: bucketLabel(p.time, period),
    input, output, cacheWrite, cacheRead,
    total: input + output + cacheWrite + cacheRead,
  };
}

// trendSpark — tiny inline SVG bar sparkline for a table cell, cost-
// normalized (bar heights scale to the largest cost in the series).
// Bars (not a polyline) make sparse/single-cell series readable.
// Ported from report.html.tmpl:2163.
export function trendSpark(points, period, w, h) {
  w = w || 90; h = h || 18;
  if (!points || points.length === 0) return '<span class="small">—</span>';
  let max = 0;
  for (const p of points) { if ((p.cost_usd || 0) > max) max = p.cost_usd; }
  const n = points.length;
  const bw = w / n;
  let bars = '';
  for (let i = 0; i < n; i++) {
    const v = points[i].cost_usd || 0;
    const bh = max > 0 ? (v / max) * (h - 1) : 0;
    const x = i * bw;
    const y = h - bh;
    const label = `${escHtml(bucketLabel(points[i].time, period))}: ${fmtMoney(v)}`;
    bars += `<rect x="${x.toFixed(2)}" y="${y.toFixed(2)}" width="${(bw*0.85).toFixed(2)}" height="${bh.toFixed(2)}" data-tooltip="${label}"></rect>`;
  }
  return `<svg class="trend-spark" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">${bars}</svg>`;
}

// trendSparkHit — hit-ratio variant: bars from 0..1 (axis-fixed) so a
// 30%-hit row reads visibly shorter than a 90%-hit row even when
// their cacheable volume is identical. Ported from
// report.html.tmpl:2770.
export function trendSparkHit(points, period, w, h) {
  w = w || 90; h = h || 18;
  if (!points || points.length === 0) return '<span class="small">—</span>';
  const n = points.length;
  const bw = w / n;
  let bars = '';
  for (let i = 0; i < n; i++) {
    const r = pointHitRatio(points[i]);
    const bh = r * (h - 1);
    const x = i * bw;
    const y = h - bh;
    const label = `${escHtml(bucketLabel(points[i].time, period))}: ${(r * 100).toFixed(1)}% hit`;
    bars += `<rect x="${x.toFixed(2)}" y="${y.toFixed(2)}" width="${(bw*0.85).toFixed(2)}" height="${bh.toFixed(2)}" data-tooltip="${label}"></rect>`;
  }
  return `<svg class="trend-spark" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">${bars}</svg>`;
}

// anomalyIndex narrows D.anomalies to a single kind, indexed by
// bucket timestamp string. The string comes from json.Marshal of
// time.Time so it matches TrendPoint.time bytes-for-bytes.
export function anomalyIndex(anomalies, kind) {
  const out = new Map();
  if (!anomalies) return out;
  for (const a of anomalies) {
    if (a.kind === kind) out.set(a.time, a);
  }
  return out;
}

// Catmull-Rom → cubic-bezier path. Tension 0.5 (the standard).
export function smoothPath(pts) {
  if (!pts || pts.length === 0) return '';
  if (pts.length === 1) return `M ${pts[0][0].toFixed(2)} ${pts[0][1].toFixed(2)}`;
  let d = `M ${pts[0][0].toFixed(2)} ${pts[0][1].toFixed(2)}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[Math.max(0, i - 1)];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[Math.min(pts.length - 1, i + 2)];
    const c1x = p1[0] + (p2[0] - p0[0]) / 6;
    const c1y = p1[1] + (p2[1] - p0[1]) / 6;
    const c2x = p2[0] - (p3[0] - p1[0]) / 6;
    const c2y = p2[1] - (p3[1] - p1[1]) / 6;
    d += ` C ${c1x.toFixed(2)} ${c1y.toFixed(2)}, ${c2x.toFixed(2)} ${c2y.toFixed(2)}, ${p2[0].toFixed(2)} ${p2[1].toFixed(2)}`;
  }
  return d;
}

// HTML-attribute-safe JSON encoder for embedding chart points in
// data-chart-points. Escapes the five characters that matter inside a
// single-quoted attribute (& ' " < >).
export function encodeChartData(arr) {
  return JSON.stringify(arr).replace(/[&'"<>]/g, c => `&#${c.charCodeAt(0)};`);
}

// chartViewboxWidth reads the panel's interior width once at chart-
// render time so the viewBox matches the rendered SVG. Falls back to
// 1100 if the panel isn't measurable yet.
export function chartViewboxWidth() {
  const p = document.querySelector('.panel');
  if (!p) return 1100;
  const r = p.getBoundingClientRect();
  const cs = window.getComputedStyle(p);
  const inner = r.width - parseFloat(cs.paddingLeft || 0) - parseFloat(cs.paddingRight || 0);
  return Math.max(800, Math.round(inner - 24));
}

// hitRatioChart — mini line chart for hit ratio over time. Y axis
// fixed 0..100% so chart shape is comparable across reports.
export function hitRatioChart(points, period, anomalies) {
  if (!points || points.length < 2) return '';
  const w = chartViewboxWidth(), h = 110;
  const pad = { l: 60, r: 16, t: 8, b: 22 };
  const innerW = w - pad.l - pad.r;
  const innerH = h - pad.t - pad.b;
  const n = points.length;
  const xAt = i => pad.l + (i / (n - 1)) * innerW;
  const yAt = r => pad.t + innerH - r * innerH;
  const anomIdx = anomalyIndex(anomalies, 'hitratio_drop');

  const coords = points.map((p, i) => [xAt(i), yAt(pointHitRatio(p))]);
  const linePath = smoothPath(coords);
  const areaPath = linePath +
    ` L ${xAt(n-1).toFixed(2)} ${(pad.t + innerH).toFixed(2)}` +
    ` L ${xAt(0).toFixed(2)} ${(pad.t + innerH).toFixed(2)} Z`;

  let yAxis = '';
  for (const r of [0, 0.5, 1.0]) {
    yAxis += `<line class="trend-axis" x1="${pad.l}" x2="${w - pad.r}" y1="${yAt(r).toFixed(2)}" y2="${yAt(r).toFixed(2)}" stroke-dasharray="2,3"/>`;
    yAxis += `<text class="trend-tick" x="${pad.l - 6}" y="${(yAt(r) + 4).toFixed(2)}" text-anchor="end">${(r*100).toFixed(0)}%</text>`;
  }
  let dots = '', markers = '';
  for (let i = 0; i < n; i++) {
    const r = pointHitRatio(points[i]);
    const a = anomIdx.get(points[i].time);
    const cls = a ? 'trend-pt trend-pt-anomaly' : 'trend-pt';
    const rad = a ? 5 : 3;
    dots += `<circle class="${cls}" cx="${xAt(i).toFixed(2)}" cy="${yAt(r).toFixed(2)}" r="${rad}" data-i="${i}"/>`;
    if (a) {
      const ppGap = (a.ratio * 100).toFixed(0);
      markers += `<text class="trend-anomaly-marker" x="${xAt(i).toFixed(2)}" y="${(yAt(r) + 16).toFixed(2)}" text-anchor="middle">▼ −${ppGap}pp</text>`;
    }
  }

  const data = points.map(p => ({
    label: bucketLabel(p.time, period),
    ratio: pointHitRatio(p),
    anomaly: anomIdx.get(p.time) || null,
  }));

  return `<div class="trend-subtitle">Cache hit ratio over ${escHtml(period)}</div>
    <div class="chart-host" data-chart-type="hitratio"
         data-chart-points='${encodeChartData(data)}'
         data-pad-l="${pad.l}" data-pad-r="${pad.r}"
         data-pad-t="${pad.t}" data-pad-b="${pad.b}"
         data-vb-w="${w}" data-vb-h="${h}">
      <svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="xMidYMid meet" class="trend-hitratio">
        <defs>
          <linearGradient id="grad-hit" x1="0" x2="0" y1="0" y2="1">
            <stop class="grad-stop-hit-top" offset="0%"/>
            <stop class="grad-stop-hit-bot" offset="100%"/>
          </linearGradient>
        </defs>
        ${yAxis}
        <path class="trend-area" d="${areaPath}" fill="url(#grad-hit)"/>
        <path class="trend-line trend-line-2" d="${linePath}"/>
        ${dots}
        ${markers}
        <g class="crosshair" style="opacity:0">
          <line class="crosshair-line" x1="0" x2="0" y1="${pad.t}" y2="${(pad.t + innerH).toFixed(2)}"/>
          <circle class="crosshair-dot" cx="0" cy="0" r="5"/>
        </g>
      </svg>
      <div class="chart-tooltip" role="tooltip" style="opacity:0; left:0; top:0;"></div>
    </div>`;
}

// trendLineChart — headline cost trend: line + gradient area, hover
// crosshair, smooth bezier interpolation. Placeholder for <2 points.
export function trendLineChart(points, period, anomalies) {
  if (!points || points.length === 0) return '<div class="small">(no trend data)</div>';
  if (points.length === 1) {
    return `<div class="small">Only one ${escHtml(period)} of data — chart hidden.</div>`;
  }
  const w = chartViewboxWidth(), h = 320;
  const pad = { l: 60, r: 16, t: 12, b: 32 };
  const innerW = w - pad.l - pad.r;
  const innerH = h - pad.t - pad.b;
  let max = 0;
  for (const p of points) { if ((p.cost_usd || 0) > max) max = p.cost_usd; }
  if (max <= 0) max = 1;
  const n = points.length;
  const xAt = i => pad.l + (i / (n - 1)) * innerW;
  const yAt = v => pad.t + innerH - (v / max) * innerH;

  const coords = points.map((p, i) => [xAt(i), yAt(p.cost_usd || 0)]);
  const linePath = smoothPath(coords);
  const areaPath = linePath +
    ` L ${xAt(n-1).toFixed(2)} ${(pad.t + innerH).toFixed(2)}` +
    ` L ${xAt(0).toFixed(2)} ${(pad.t + innerH).toFixed(2)} Z`;

  let yAxis = '';
  for (const v of [0, max / 2, max]) {
    yAxis += `<line class="trend-axis" x1="${pad.l}" x2="${w - pad.r}" y1="${yAt(v).toFixed(2)}" y2="${yAt(v).toFixed(2)}" stroke-dasharray="2,3"/>`;
    yAxis += `<text class="trend-tick" x="${pad.l - 6}" y="${(yAt(v) + 4).toFixed(2)}" text-anchor="end">${escHtml(fmtMoney(v))}</text>`;
  }

  const tickCount = Math.min(n, 8);
  const tickIdx = [];
  {
    const seen = new Set();
    for (let k = 0; k < tickCount; k++) {
      const i = tickCount === 1 ? 0 : Math.round(k * (n - 1) / (tickCount - 1));
      if (!seen.has(i)) { seen.add(i); tickIdx.push(i); }
    }
  }
  let xTicks = '';
  for (const i of tickIdx) {
    xTicks += `<text class="trend-tick" x="${xAt(i).toFixed(2)}" y="${h - pad.b + 16}" text-anchor="middle">${escHtml(bucketLabel(points[i].time, period))}</text>`;
  }

  const anomIdx = anomalyIndex(anomalies, 'cost_spike');
  let dots = '', markers = '';
  for (let i = 0; i < n; i++) {
    const v = points[i].cost_usd || 0;
    const a = anomIdx.get(points[i].time);
    const cls = a ? 'trend-pt trend-pt-anomaly' : 'trend-pt';
    const rad = a ? 5 : 3;
    dots += `<circle class="${cls}" cx="${xAt(i).toFixed(2)}" cy="${yAt(v).toFixed(2)}" r="${rad}" data-i="${i}"/>`;
    if (a) {
      const above = yAt(v) - 10;
      const y = above < pad.t + 4 ? yAt(v) + 16 : above;
      markers += `<text class="trend-anomaly-marker" x="${xAt(i).toFixed(2)}" y="${y.toFixed(2)}" text-anchor="middle">▲ ${a.ratio.toFixed(1)}×</text>`;
    }
  }

  const data = points.map(p => ({
    label: bucketLabel(p.time, period),
    cost: p.cost_usd || 0,
    turns: p.turns || 0,
    anomaly: anomIdx.get(p.time) || null,
  }));

  return `
    <div class="chart-host" data-chart-type="cost"
         data-chart-points='${encodeChartData(data)}'
         data-pad-l="${pad.l}" data-pad-r="${pad.r}"
         data-pad-t="${pad.t}" data-pad-b="${pad.b}"
         data-vb-w="${w}" data-vb-h="${h}">
      <svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="xMidYMid meet">
        <defs>
          <linearGradient id="grad-cost" x1="0" x2="0" y1="0" y2="1">
            <stop class="grad-stop-cost-top" offset="0%"/>
            <stop class="grad-stop-cost-mid" offset="60%"/>
            <stop class="grad-stop-cost-bot" offset="100%"/>
          </linearGradient>
        </defs>
        ${yAxis}
        <path class="trend-area" d="${areaPath}" fill="url(#grad-cost)"/>
        <path class="trend-line" d="${linePath}"/>
        ${dots}
        ${markers}
        ${xTicks}
        <g class="crosshair" style="opacity:0">
          <line class="crosshair-line" x1="0" x2="0" y1="${pad.t}" y2="${(pad.t + innerH).toFixed(2)}"/>
          <circle class="crosshair-dot" cx="0" cy="0" r="5"/>
        </g>
      </svg>
      <div class="chart-tooltip" role="tooltip" style="opacity:0; left:0; top:0;"></div>
    </div>`;
}

// tokensStackedChart — stacked-area chart of token volume per period,
// one band per category (TOKEN_BANDS, largest at the bottom). Y scales
// to the tallest per-period total. Shares the crosshair system via
// data-chart-type "tokens": the dot rides the stack top (the period
// total) and the tooltip lists every category for that period. Bands
// are straight-segment polygons (not smoothed) so stacked layers never
// bezier-overshoot and cross. Mirrors trendLineChart's axis/tick chrome.
export function tokensStackedChart(points, period) {
  if (!points || points.length === 0) return '<div class="small">(no token trend data)</div>';
  if (points.length === 1) {
    return `<div class="small">Only one ${escHtml(period)} of data — chart hidden.</div>`;
  }
  const w = chartViewboxWidth(), h = 320;
  const pad = { l: 64, r: 16, t: 12, b: 32 };
  const innerW = w - pad.l - pad.r;
  const innerH = h - pad.t - pad.b;

  const data = points.map(p => normalizeTokenPoint(p, period));
  let max = 0;
  for (const d of data) { if (d.total > max) max = d.total; }
  if (max <= 0) max = 1;
  const n = data.length;
  const xAt = i => pad.l + (i / (n - 1)) * innerW;
  const yAt = v => pad.t + innerH - (v / max) * innerH;

  // Stack bottom→top, carrying a running cumulative per point. Each
  // band's polygon = its top boundary (cumulative, left→right) joined
  // to its bottom boundary (previous cumulative, right→left).
  const cum = new Array(n).fill(0);
  let bands = '';
  for (const band of TOKEN_BANDS) {
    const lower = cum.slice();
    for (let i = 0; i < n; i++) cum[i] += data[i][band.key];
    const top = [], bot = [];
    for (let i = 0; i < n; i++) top.push(`${xAt(i).toFixed(2)},${yAt(cum[i]).toFixed(2)}`);
    for (let i = n - 1; i >= 0; i--) bot.push(`${xAt(i).toFixed(2)},${yAt(lower[i]).toFixed(2)}`);
    bands += `<polygon class="tok-area ${band.cls}" points="${top.join(' ')} ${bot.join(' ')}"/>`;
  }

  let yAxis = '';
  for (const v of [0, max / 2, max]) {
    yAxis += `<line class="trend-axis" x1="${pad.l}" x2="${w - pad.r}" y1="${yAt(v).toFixed(2)}" y2="${yAt(v).toFixed(2)}" stroke-dasharray="2,3"/>`;
    yAxis += `<text class="trend-tick" x="${pad.l - 6}" y="${(yAt(v) + 4).toFixed(2)}" text-anchor="end">${escHtml(fmtCompact(v))}</text>`;
  }

  const tickCount = Math.min(n, 8);
  const tickIdx = [];
  {
    const seen = new Set();
    for (let k = 0; k < tickCount; k++) {
      const i = tickCount === 1 ? 0 : Math.round(k * (n - 1) / (tickCount - 1));
      if (!seen.has(i)) { seen.add(i); tickIdx.push(i); }
    }
  }
  let xTicks = '';
  for (const i of tickIdx) {
    xTicks += `<text class="trend-tick" x="${xAt(i).toFixed(2)}" y="${h - pad.b + 16}" text-anchor="middle">${escHtml(data[i].label)}</text>`;
  }

  return `
    <div class="chart-host" data-chart-type="tokens"
         data-chart-points='${encodeChartData(data)}'
         data-pad-l="${pad.l}" data-pad-r="${pad.r}"
         data-pad-t="${pad.t}" data-pad-b="${pad.b}"
         data-vb-w="${w}" data-vb-h="${h}" data-y-max="${max}">
      <svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="xMidYMid meet">
        ${yAxis}
        ${bands}
        ${xTicks}
        <g class="crosshair" style="opacity:0">
          <line class="crosshair-line" x1="0" x2="0" y1="${pad.t}" y2="${(pad.t + innerH).toFixed(2)}"/>
          <circle class="crosshair-dot" cx="0" cy="0" r="5"/>
        </g>
      </svg>
      <div class="chart-tooltip" role="tooltip" style="opacity:0; left:0; top:0;"></div>
    </div>`;
}

// wireChartInteractivity — bind hover crosshair + tooltip to every
// .chart-host under `root`. Each host carries its data + viewBox
// padding as data-* attrs; we bind one mousemove listener per host,
// find the closest x-bucket, and reposition the in-SVG crosshair plus
// an HTML tooltip anchored to the host. Pure DOM, no chart lib.
//
// Ported verbatim from the chartInteractivity() IIFE that lived in the
// legacy report.html.tmpl (removed in the Phase 10 cleanup); the SPA
// cutover ported the chart *emitters* but dropped this wiring, so the
// crosshair markup rendered inert until a view called this. Must be
// invoked after the chart markup is injected into the DOM.
export function wireChartInteractivity(root) {
  (root || document).querySelectorAll('.chart-host').forEach(host => {
    let pts;
    try { pts = JSON.parse(host.dataset.chartPoints); }
    catch { return; }
    if (!pts || pts.length < 2) return;

    const svg     = host.querySelector('svg');
    const cross   = host.querySelector('.crosshair');
    const cline   = cross && cross.querySelector('.crosshair-line');
    const cdot    = cross && cross.querySelector('.crosshair-dot');
    const tip     = host.querySelector('.chart-tooltip');
    if (!svg || !cross || !tip) return;

    const padL  = parseFloat(host.dataset.padL);
    const padR  = parseFloat(host.dataset.padR);
    const padT  = parseFloat(host.dataset.padT);
    const padB  = parseFloat(host.dataset.padB);
    const vbW   = parseFloat(host.dataset.vbW);
    const vbH   = parseFloat(host.dataset.vbH);
    const innerW = vbW - padL - padR;
    const innerH = vbH - padT - padB;
    const n = pts.length;
    const xAt = i => padL + (i / (n - 1)) * innerW;
    const type = host.dataset.chartType;

    // Pre-compute Y for each point. Cost scales by max in the data;
    // forecast scales by data-y-max (so the dashed projection line
    // matches the rendered ymax exactly); hitratio is 0..1.
    let yAt;
    if (type === 'cost') {
      let max = 0;
      for (const p of pts) { if ((p.cost || 0) > max) max = p.cost; }
      if (max <= 0) max = 1;
      yAt = i => padT + innerH - ((pts[i].cost || 0) / max) * innerH;
    } else if (type === 'forecast') {
      const yMax = parseFloat(host.dataset.yMax) || 1;
      yAt = i => padT + innerH - ((pts[i].cum || 0) / yMax) * innerH;
    } else if (type === 'tokens') {
      // Dot rides the stack top — the period total — against the
      // chart's data-y-max (the tallest total across periods).
      const yMax = parseFloat(host.dataset.yMax) || 1;
      yAt = i => padT + innerH - ((pts[i].total || 0) / yMax) * innerH;
    } else {
      yAt = i => padT + innerH - (pts[i].ratio || 0) * innerH;
    }

    function fmtCostLocal(v) {
      if (v >= 1000) return '$' + Math.round(v).toLocaleString();
      return '$' + v.toFixed(2);
    }
    function anomalyLine(a) {
      if (!a) return '';
      if (a.kind === 'cost_spike') {
        return `<div class="ct-anomaly">▲ spike: ${a.ratio.toFixed(1)}× rolling 7-bucket median</div>`;
      }
      if (a.kind === 'hitratio_drop') {
        return `<div class="ct-anomaly">▼ hit-ratio drop: −${(a.ratio * 100).toFixed(0)} pp vs rolling median</div>`;
      }
      return '';
    }
    function tooltipHTML(i) {
      const p = pts[i];
      if (type === 'cost') {
        return `<div class="ct-label">${escHtml(p.label)}</div>
                <div class="ct-primary">${fmtCostLocal(p.cost)}</div>
                <div class="ct-meta">${Number(p.turns).toLocaleString()} turns</div>
                ${anomalyLine(p.anomaly)}`;
      }
      if (type === 'forecast') {
        return `<div class="ct-label">${escHtml(p.label)}</div>
                <div class="ct-primary">${fmtCostLocal(p.cum || 0)}</div>
                <div class="ct-meta">${p.projected ? 'projected cumulative' : 'cumulative MTD'}</div>`;
      }
      if (type === 'tokens') {
        // Order matches the stack top→bottom so the tooltip reads in the
        // same order the eye scans the bands.
        return `<div class="ct-label">${escHtml(p.label)}</div>
                <div class="ct-primary">${fmtCompact(p.total || 0)} tokens</div>
                <div class="ct-breakdown">
                  <span><i class="sw tok-area-output"></i>Output ${fmtCompact(p.output || 0)}</span>
                  <span><i class="sw tok-area-input"></i>Input ${fmtCompact(p.input || 0)}</span>
                  <span><i class="sw tok-area-cwrite"></i>Cache write ${fmtCompact(p.cacheWrite || 0)}</span>
                  <span><i class="sw tok-area-cread"></i>Cache read ${fmtCompact(p.cacheRead || 0)}</span>
                </div>`;
      }
      return `<div class="ct-label">${escHtml(p.label)}</div>
              <div class="ct-primary">${(p.ratio * 100).toFixed(1)}% hit</div>
              ${anomalyLine(p.anomaly)}`;
    }

    function show() { cross.style.opacity = '1'; tip.style.opacity = '1'; }
    function hide() { cross.style.opacity = '0'; tip.style.opacity = '0'; }
    hide();

    svg.addEventListener('mousemove', (e) => {
      // Convert the cursor's screen position into the SVG's viewBox
      // coordinate space. getScreenCTM gives the inverse mapping for free.
      const ctm = svg.getScreenCTM();
      if (!ctm) return;
      const inv = ctm.inverse();
      const ept = svg.createSVGPoint();
      ept.x = e.clientX; ept.y = e.clientY;
      const sp = ept.matrixTransform(inv);
      // Convert SVG x to nearest data index.
      const t = (sp.x - padL) / innerW;
      let i = Math.round(t * (n - 1));
      if (i < 0) i = 0;
      if (i > n - 1) i = n - 1;
      // Crosshair position in SVG coords.
      const cx = xAt(i);
      const cy = yAt(i);
      cline.setAttribute('x1', cx);
      cline.setAttribute('x2', cx);
      cdot.setAttribute('cx', cx);
      cdot.setAttribute('cy', cy);
      // Tooltip in DOM coords — map the SVG point back to screen, then
      // subtract host's offset to get host-relative pixels.
      const sptOut = svg.createSVGPoint();
      sptOut.x = cx; sptOut.y = cy;
      const dom = sptOut.matrixTransform(ctm);
      const hostRect = host.getBoundingClientRect();
      tip.style.left = `${dom.x - hostRect.left}px`;
      tip.style.top  = `${dom.y - hostRect.top}px`;
      tip.innerHTML  = tooltipHTML(i);
      show();
    });
    svg.addEventListener('mouseleave', hide);
  });
}

// forecastChart — cumulative cost burn-up with a dashed projection to
// month-end. X spans [window_start, month_end), Y is cumulative $.
export function forecastChart(forecast, points) {
  if (!forecast || (forecast.projected_month_end_usd || 0) <= 0) {
    return '<div class="small empty-state">Forecast unavailable — needs at least two days of cost in the current calendar month and daily bucketing.</div>';
  }
  const ws = new Date(forecast.window_start).getTime();
  const me = new Date(forecast.month_end).getTime();
  const projected = forecast.projected_month_end_usd;
  const N = Math.round((me - ws) / 86400000);
  if (N < 1) {
    return '<div class="small empty-state">Forecast window is empty.</div>';
  }

  const cum = new Array(N).fill(null);
  let running = 0, lastDay = -1;
  const inWindow = (points || [])
    .filter(p => { const t = new Date(p.time).getTime(); return t >= ws && t < me; })
    .sort((a, b) => new Date(a.time) - new Date(b.time));
  for (const p of inWindow) {
    const d = Math.floor((new Date(p.time).getTime() - ws) / 86400000);
    if (d < 0 || d >= N) continue;
    running += p.cost_usd || 0;
    cum[d] = running;
    if (d > lastDay) lastDay = d;
  }
  if (lastDay < 0) {
    return '<div class="small empty-state">No data in the forecast window.</div>';
  }
  let prev = 0;
  for (let i = 0; i <= lastDay; i++) {
    if (cum[i] === null) cum[i] = prev; else prev = cum[i];
  }
  if (lastDay < N - 1) {
    const startCum = cum[lastDay];
    const span = N - 1 - lastDay;
    for (let i = lastDay + 1; i < N; i++) {
      cum[i] = startCum + ((i - lastDay) / span) * (projected - startCum);
    }
  }

  const w = chartViewboxWidth(), h = 320;
  const pad = { l: 70, r: 24, t: 28, b: 32 };
  const innerW = w - pad.l - pad.r;
  const innerH = h - pad.t - pad.b;
  const ymax = Math.max(projected, cum[lastDay]) * 1.05 || 1;
  const xAt = i => pad.l + (i / Math.max(1, N - 1)) * innerW;
  const yAt = v => pad.t + innerH - (v / ymax) * innerH;

  let solid = '';
  for (let i = 0; i <= lastDay; i++) {
    solid += `${i === 0 ? 'M' : 'L'} ${xAt(i).toFixed(2)} ${yAt(cum[i]).toFixed(2)} `;
  }
  const lastX = xAt(lastDay), lastY = yAt(cum[lastDay]);
  const endX = xAt(N - 1), endY = yAt(projected);

  let yAxis = '';
  for (const v of [0, projected / 2, projected]) {
    yAxis += `<line class="trend-axis" x1="${pad.l}" x2="${w - pad.r}" y1="${yAt(v).toFixed(2)}" y2="${yAt(v).toFixed(2)}" stroke-dasharray="2,3"/>`;
    yAxis += `<text class="trend-tick" x="${pad.l - 6}" y="${(yAt(v) + 4).toFixed(2)}" text-anchor="end">${escHtml(fmtMoney(v))}</text>`;
  }

  const tickDays = N >= 3 ? [0, Math.floor((N - 1) / 2), N - 1] : [0, N - 1];
  let xTicks = '';
  for (const d of tickDays) {
    const dt = new Date(ws + d * 86400000);
    xTicks += `<text class="trend-tick" x="${xAt(d).toFixed(2)}" y="${h - pad.b + 16}" text-anchor="middle">${(dt.getUTCMonth() + 1)}/${dt.getUTCDate()}</text>`;
  }

  const todayLabel = `<text class="trend-tick" x="${lastX.toFixed(2)}" y="${(pad.t - 8).toFixed(2)}" text-anchor="middle">today</text>`;
  const projLabel = `<text class="trend-forecast-label" x="${endX.toFixed(2)}" y="${(endY - 12).toFixed(2)}" text-anchor="end">${escHtml(fmtMoney(projected))}</text>`;

  const lastDt = new Date(me - 86400000);
  const lastDateStr = `${lastDt.getUTCFullYear()}-${String(lastDt.getUTCMonth() + 1).padStart(2,'0')}-${String(lastDt.getUTCDate()).padStart(2,'0')}`;

  const monthNames = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
  const data = [];
  for (let i = 0; i < N; i++) {
    const dt = new Date(ws + i * 86400000);
    data.push({
      label: `${monthNames[dt.getUTCMonth()]} ${dt.getUTCDate()}`,
      cum: cum[i],
      projected: i > lastDay,
    });
  }

  return `
    <div class="chart-host" data-chart-type="forecast"
         data-chart-points='${encodeChartData(data)}'
         data-pad-l="${pad.l}" data-pad-r="${pad.r}"
         data-pad-t="${pad.t}" data-pad-b="${pad.b}"
         data-vb-w="${w}" data-vb-h="${h}"
         data-y-max="${ymax}">
      <svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="xMidYMid meet">
        ${yAxis}
        <path class="trend-line" d="${solid}" fill="none"/>
        <line class="trend-forecast" x1="${lastX.toFixed(2)}" y1="${lastY.toFixed(2)}" x2="${endX.toFixed(2)}" y2="${endY.toFixed(2)}" stroke-dasharray="5,4"/>
        <circle class="trend-pt" cx="${lastX.toFixed(2)}" cy="${lastY.toFixed(2)}" r="4"/>
        <circle class="trend-pt-forecast" cx="${endX.toFixed(2)}" cy="${endY.toFixed(2)}" r="4"/>
        ${todayLabel}
        ${projLabel}
        ${xTicks}
        <g class="crosshair" style="opacity:0">
          <line class="crosshair-line" x1="0" x2="0" y1="${pad.t}" y2="${(pad.t + innerH).toFixed(2)}"/>
          <circle class="crosshair-dot" cx="0" cy="0" r="5"/>
        </g>
      </svg>
      <div class="chart-tooltip" role="tooltip" style="opacity:0; left:0; top:0;"></div>
    </div>
    <div class="trend-summary">
      <span>Spent in window: <strong>${escHtml(fmtMoney(forecast.mtd_cost_usd))}</strong> over <strong>${(forecast.days_elapsed || 0).toFixed(1)}</strong> days</span>
      <span>Daily rate: <strong>${escHtml(fmtMoney(forecast.daily_rate_usd))}</strong></span>
      <span>Projected by ${escHtml(lastDateStr)}: <strong>${escHtml(fmtMoney(projected))}</strong></span>
    </div>`;
}
