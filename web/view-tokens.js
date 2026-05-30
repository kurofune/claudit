// Tokens view — fetches /_claudit/api/tokens and paints the token-
// accounting story in one scroll: the headline grand total + a 4-way
// composition breakdown (input / output / cache write / cache read),
// the stacked token-volume trend over time, and the by-model table.
//
// All rollups (grand total, composition percentages, per-model totals)
// are computed server-side in render.BuildTokens — this view is purely
// presentational, mirroring the "logic in Go, dumb JS" split.

import { fetchTokens } from './api.js';
import { fmtNum, fmtCompact, escHtml } from './format.js';
import { tokensStackedChart, wireChartInteractivity } from './charts.js';
import { buildRows, wireGlobalFilters } from './table.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

// Category label → fill class. Shared with the chart bands and the
// crosshair tooltip swatches so a color means the same thing everywhere.
const COMP_CLASS = {
  'Input': 'tok-area-input',
  'Output': 'tok-area-output',
  'Cache write': 'tok-area-cwrite',
  'Cache read': 'tok-area-cread',
};

function compBarHTML(comp) {
  return comp.map(c => {
    const cls = COMP_CLASS[c.label] || '';
    const w = (c.pct || 0).toFixed(2);
    return `<div class="tok-comp-seg ${cls}" style="width:${w}%"
      title="${escHtml(c.label)}: ${fmtNum(c.tokens)} (${(c.pct || 0).toFixed(1)}%)"></div>`;
  }).join('');
}

function compRowsHTML(comp) {
  return comp.map(c => {
    const cls = COMP_CLASS[c.label] || '';
    return `<div class="tok-comp-row">
      <span class="tok-comp-key"><i class="sw ${cls}"></i>${escHtml(c.label)}</span>
      <span class="tok-comp-num">${fmtNum(c.tokens)}</span>
      <span class="tok-comp-pct">${(c.pct || 0).toFixed(1)}%</span>
    </div>`;
  }).join('');
}

// Legend for the stacked chart — ordered top→bottom of the visual stack.
const CHART_LEGEND = `
  <div class="tok-legend">
    <span><i class="sw tok-area-output"></i>Output</span>
    <span><i class="sw tok-area-input"></i>Input</span>
    <span><i class="sw tok-area-cwrite"></i>Cache write</span>
    <span><i class="sw tok-area-cread"></i>Cache read</span>
  </div>`;

const SHELL = `
  <header class="view-head"><h1>${labelIcon('tokens')}Tokens</h1></header>
  <details class="guide">
    <summary>How to read this section</summary>
    <div class="body">
      <ul>
        <li><strong>Total tokens</strong> is every token across all five categories — the number people usually mean by "tokens burned." It is dominated by <strong>cache read</strong>, the conversation history re-read from cache on every turn, which bills at ~10% of fresh input.</li>
        <li><strong>Composition</strong> demystifies that headline: a 90%-cache-read total is mostly the same context counted over and over, not 90% of real work. <strong>Output</strong> is the most expensive category per token; <strong>cache write</strong> is context first sent (cache miss); <strong>input</strong> is fresh non-cached prompt tokens.</li>
        <li><strong>Volume over time</strong> stacks the four categories per period so you can spot a day where output spiked or cache reads ballooned.</li>
        <li><strong>By model</strong> shows which models consumed which token categories — a token-centric cut of the Cost tab.</li>
      </ul>
    </div>
  </details>

  <section class="tok-composition">
    <div class="tok-headline">
      <div class="label">${labelIcon('tokens')}Total tokens</div>
      <div class="value" id="tok-grand-total">—</div>
    </div>
    <div class="tok-comp-bar" id="tok-comp-bar" role="img" aria-label="Token composition by category"></div>
    <div class="tok-comp-rows" id="tok-comp-rows"></div>
  </section>

  <h2>${labelIcon('turns')}Volume over time</h2>
  <div class="small">Stacked token volume per period. Hover for the per-period breakdown.</div>
  ${CHART_LEGEND}
  <div id="tok-trend-chart"></div>

  <h2>${labelIcon('cost')}By model</h2>
  <table data-table="tokmodel">
    <thead><tr>
      <th data-key="model" title="Claude model ID, e.g. claude-sonnet-4-6.">Model</th>
      <th data-key="input" class="num" title="Fresh, non-cached prompt tokens (cache miss, full input price).">Input</th>
      <th data-key="output" class="num" title="Tokens the model generated. Most expensive category per token.">Output</th>
      <th data-key="cache_write" class="num" title="Context first written to the cache (cache_create, ~1.25× input price).">Cache write</th>
      <th data-key="cache_read" class="num" title="Context served from cache (~10% of fresh input price).">Cache read</th>
      <th data-key="total" class="num" title="Sum of all four categories for this model.">Total</th>
      <th data-key="pct" class="num" title="Share of the grand total token count.">%</th>
    </tr></thead>
    <tbody></tbody>
  </table>
`;

let painted = false;
let navPainted = false;

// paintNav resolves the sidebar metric (grand total tokens) ahead of a
// click. Short-circuits if the full paint already ran.
export async function paintNav() {
  if (navPainted || painted) return;
  let data;
  try { data = await fetchTokens(); } catch { return; }
  const el = document.getElementById('nav-metric-tokens');
  if (el) el.textContent = fmtCompact(data.total || 0);
  navPainted = true;
}

export async function paint() {
  const container = document.getElementById('view-tokens');
  if (!container) return;
  if (painted) return;

  container.innerHTML = SHELL;

  let data;
  try {
    data = await fetchTokens();
  } catch (err) {
    container.innerHTML = `<header class="view-head"><h1>${labelIcon('tokens')}Tokens</h1></header>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load token data:</strong> ${escHtml(err.message)}</div>`;
    return;
  }

  const comp = data.composition || [];
  const byModel = data.by_model || [];
  const trend = data.trend || [];
  const period = inferPeriod(data);

  const grandEl = container.querySelector('#tok-grand-total');
  if (grandEl) grandEl.textContent = fmtNum(data.total || 0);
  const barEl = container.querySelector('#tok-comp-bar');
  if (barEl) barEl.innerHTML = compBarHTML(comp);
  const rowsEl = container.querySelector('#tok-comp-rows');
  if (rowsEl) rowsEl.innerHTML = compRowsHTML(comp);

  const chartEl = container.querySelector('#tok-trend-chart');
  if (chartEl) chartEl.innerHTML = tokensStackedChart(trend, period);
  wireChartInteractivity(container);

  buildRows(container.querySelector('[data-table="tokmodel"]'), byModel, r => [
    [escHtml(r.model), false],
    [fmtNum(r.input), true],
    [fmtNum(r.output), true],
    [fmtNum(r.cache_write), true],
    [fmtNum(r.cache_read), true],
    [fmtNum(r.total), true, r.total],
    [(r.pct || 0).toFixed(1) + '%', true],
  ]);

  wireGlobalFilters();

  const el = document.getElementById('nav-metric-tokens');
  if (el) el.textContent = fmtCompact(data.total || 0);

  painted = true;
  navPainted = true;
}

// inferPeriod reads the bucket granularity shipped on the /tokens
// payload (a same-day window comes back as "hour"). Falls back to day
// for older payloads / the static report that omit the field.
function inferPeriod(data) {
  return (data && data.period) || 'day';
}

export function reset() { painted = false; navPainted = false; }
