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
import { buildRows, wireViewFilters } from './table.js';

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

// The four numeric columns shared by every breakdown table. Each
// data-key matches a field on the row objects so table.js can sort.
const TOK_NUM_HEADS = `
  <th data-key="input" class="num" title="Fresh, non-cached prompt tokens (cache miss, full input price).">Input</th>
  <th data-key="output" class="num" title="Tokens the model generated. Most expensive category per token.">Output</th>
  <th data-key="cache_write" class="num" title="Context first written to the cache (cache_create, ~1.25× input price).">Cache write</th>
  <th data-key="cache_read" class="num" title="Context served from cache (~10% of fresh input price).">Cache read</th>
  <th data-key="total" class="num" title="Sum of all four categories for this row.">Total</th>
  <th data-key="pct" class="num" title="Share of the grand total token count.">%</th>`;

// tokTable renders one breakdown table: a label column + the shared
// numeric columns. labelKey must match the row field the label sits on
// (model rows → "model", breakdown rows → "label") so sorting works.
function tokTable(dataTable, labelKey, labelHead, labelTitle) {
  return `<table data-table="${dataTable}">
    <thead><tr>
      <th data-key="${labelKey}" title="${labelTitle}">${labelHead}</th>
      ${TOK_NUM_HEADS}
    </tr></thead>
    <tbody></tbody>
  </table>`;
}

const SHELL = `
  <header class="view-head"><h1>${labelIcon('tokens')}Tokens</h1></header>
  <div class="controls view-filter" role="search">
    <label>Filter rows: <input class="vf-text" type="search" placeholder="model, path, command…"></label>
    <label>Min cost: <input class="vf-cost" type="number" value="0" step="0.01" min="0"></label>
  </div>

  <details class="guide">
    <summary>How to read this section</summary>
    <div class="body">
      <ul>
        <li><strong>Total tokens</strong> is every token across all five categories — the number people usually mean by "tokens burned." It is dominated by <strong>cache read</strong>, the conversation history re-read from cache on every turn, which bills at ~10% of fresh input.</li>
        <li><strong>Composition</strong> demystifies that headline: a 90%-cache-read total is mostly the same context counted over and over, not 90% of real work. <strong>Output</strong> is the most expensive category per token; <strong>cache write</strong> is context first sent (cache miss); <strong>input</strong> is fresh non-cached prompt tokens.</li>
        <li><strong>Volume over time</strong> stacks the four categories per period so you can spot a day where output spiked or cache reads ballooned.</li>
        <li><strong>Breakdown tabs</strong> slice the same tokens five ways — <strong>by model</strong>, <strong>by project</strong>, <strong>by subagents</strong>, <strong>by skill &amp; slash command</strong>, and <strong>by top prompts</strong> — each row split into input / output / cache write / cache read. These are the token-centric twins of the Cost tab's tables.</li>
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

  <nav class="subtabs" aria-label="Token breakdown sections">
    <a class="subtab is-active" href="#tokens/model"   data-subtab="model">By model</a>
    <a class="subtab"           href="#tokens/project" data-subtab="project">By project</a>
    <a class="subtab"           href="#tokens/subagents" data-subtab="subagents">By subagents</a>
    <a class="subtab"           href="#tokens/skill"   data-subtab="skill">By skill &amp; slash command</a>
    <a class="subtab"           href="#tokens/prompt"  data-subtab="prompt">By top prompts</a>
  </nav>

  <div class="subview is-active" data-subview="model">
    ${tokTable('tokmodel', 'model', 'Model', 'Claude model ID, e.g. claude-sonnet-4-6.')}
  </div>
  <div class="subview" data-subview="project">
    ${tokTable('tokproject', 'label', 'Project', 'Working directory the session ran in (its CWD when Claude Code was launched).')}
  </div>
  <div class="subview" data-subview="subagents">
    ${tokTable('toksubagent', 'label', 'Subagent', 'Subagent type (the agent name from ~/.claude/agents or the built-in Agent tool); unknown types fold into one row.')}
  </div>
  <div class="subview" data-subview="skill">
    ${tokTable('tokskill', 'label', 'Key', 'Skill name or slash-command (e.g. /review, skill:tdd) that was invoked.')}
  </div>
  <div class="subview" data-subview="prompt">
    <div class="small">User prompts ranked by the total tokens of the assistant turn chain each one kicked off. Hover a row's snippet for the full prompt.</div>
    ${tokTable('tokprompt', 'label', 'Snippet', 'First ~120 characters of the user prompt. Hover the cell for full text.')}
  </div>
`;

// numCells maps a token row's six numeric columns. Shared by every
// breakdown table so the columns format identically; total carries a
// sort value so the column sorts numerically, not lexically.
const numCells = r => [
  [fmtNum(r.input), true],
  [fmtNum(r.output), true],
  [fmtNum(r.cache_write), true],
  [fmtNum(r.cache_read), true],
  [fmtNum(r.total), true, r.total],
  [(r.pct || 0).toFixed(1) + '%', true],
];

// paintTokTable fills one breakdown table: a per-table label cell
// (returned by labelCell) followed by the shared numeric columns.
function paintTokTable(container, dataTable, rows, labelCell) {
  const table = container.querySelector(`[data-table="${dataTable}"]`);
  if (!table) return;
  buildRows(table, rows, r => [labelCell(r), ...numCells(r)]);
}

// activateSubview toggles the active subtab + subview, mirroring
// view-cost.js. Falls back to the first tab when the route names none
// or an unknown one.
function activateSubview(container, sub) {
  const subs = container.querySelectorAll('.subtab[data-subtab]');
  if (!subs.length) return;
  const wanted = sub && container.querySelector(`.subtab[data-subtab="${sub}"]`)
    ? sub
    : subs[0].dataset.subtab;
  subs.forEach(t => t.classList.toggle('is-active', t.dataset.subtab === wanted));
  container.querySelectorAll('.subview').forEach(s =>
    s.classList.toggle('is-active', s.dataset.subview === wanted));
}

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

export async function paint(route) {
  const container = document.getElementById('view-tokens');
  if (!container) return;
  if (painted) {
    activateSubview(container, route && route.sub);
    return;
  }

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
  const byProject = data.by_project || [];
  const bySubagent = data.by_subagent || [];
  const bySkill = data.by_skill || [];
  const byPrompt = data.by_prompt || [];
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

  paintTokTable(container, 'tokmodel', byModel, r => [escHtml(r.model), false]);
  paintTokTable(container, 'tokproject', byProject, r =>
    [`<span class="truncate path" title="${escHtml(r.label)}">${escHtml(r.label)}</span>`, false]);
  paintTokTable(container, 'toksubagent', bySubagent, r => [escHtml(r.label), false]);
  paintTokTable(container, 'tokskill', bySkill, r => [`<code>${escHtml(r.label)}</code>`, false]);
  paintTokTable(container, 'tokprompt', byPrompt, r => {
    const full = r.sample || r.label || '';
    const head = full.length > 80 ? full.slice(0, 79) + '…' : full;
    return [`<span title="${escHtml(full)}">${escHtml(head)}</span>`, false];
  });

  // Tokens filters tables only — its composition bar and volume trend
  // are corpus-headline charts, so no onApply (they must not respond to
  // the row filter). wireViewFilters re-renders the breakdown tables.
  wireViewFilters(container);
  activateSubview(container, route && route.sub);

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
