// Show all tool patterns — the Overview section under the hotspot list.
// The hotspots rank the top context costs and skip zero-cost rows and the
// Skill, SlashCommand and Agent tools; this table answers "what about
// everything else?" with every (tool, pattern) row of /_claudit/api/tools,
// each tool a parent row with its patterns beneath. /tools is fetched on
// the first open only. Grouping, sorting and filtering live in
// overview-tools-logic.js.

import { fetchTools } from './api.js';
import { fmtNum, fmtMoney, fmtPct1, escHtml } from './format.js';
import { tableBodySkeleton } from './skeleton.js';
import { providerFromSearch, emptyProviderText } from './provider-filter.js';
import { SHOW_ALL, hashShowAll } from './overview-logic.js';
import {
  groupedRows, filterGroups, startExpanded, headingText, nextSort,
  contextCostTip, errorTip, fmtDuration, DEFAULT_SORT,
} from './overview-tools-logic.js';

const EMPTY = 'No tool calls in this window';
const LABEL = 'Show all tool patterns';

const COLUMNS = [
  { key: 'tool', label: 'Tool / Pattern', tip: 'Tool name (Read, Bash, Edit, Grep, Agent, …), then its argument patterns beneath: the command for Bash, the file extension for Read, the host for WebFetch, …' },
  { key: 'calls', label: 'Calls', num: true, tip: 'Times the tool was invoked.' },
  { key: 'error_pct', label: 'Error %', num: true, tip: 'Errored results as a share of calls whose result was recorded. Calls with no recorded result are left out.' },
  { key: 'median_ms', label: 'Median', num: true, tip: "Median time from the end of the calling turn to the tool's result." },
  { key: 'p95_ms', label: 'p95', num: true, tip: "95th-percentile time from the end of the calling turn to the tool's result." },
  { key: 'context_tokens', label: 'Context added', num: true, tip: "Cache tokens the tool's results added to context." },
  { key: 'context_cost_usd', label: 'Context cost', num: true, tip: 'What those tokens cost: the cache write, plus every later turn that re-read them before the session ended or compacted.' },
];

const chevron = '<svg class="chev" viewBox="0 0 16 16" aria-hidden="true"><path d="M6 3.5 10.5 8 6 12.5" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round"/></svg>';

const headHTML = () => `<tr>${COLUMNS.map(c =>
  `<th scope="col" data-key="${c.key}"${c.num ? ' class="num"' : ''} data-tooltip="${escHtml(c.tip)}" aria-sort="none"><button type="button" class="th-sort">${escHtml(c.label)}</button></th>`,
).join('')}</tr>`;

const tableHTML = body => `<table class="tool-patterns">
    <thead>${headHTML()}</thead>
    <tbody>${body}</tbody>
  </table>`;

// statCells are the six numeric cells after the name. Each tooltip cell is
// focusable so the tooltip opens from the keyboard too.
function statCells(r) {
  return `<td class="num">${escHtml(fmtNum(r.calls))}</td>
    <td class="num" tabindex="0" data-tooltip="${escHtml(errorTip(r))}">${r.error_pct == null ? '—' : escHtml(fmtPct1(r.error_pct))}</td>
    <td class="num">${escHtml(fmtDuration(r.median_ms))}</td>
    <td class="num">${escHtml(fmtDuration(r.p95_ms))}</td>
    <td class="num">${escHtml(fmtNum(r.context_tokens))}</td>
    <td class="num" tabindex="0" data-tooltip="${escHtml(contextCostTip(r))}">${escHtml(fmtMoney(r.context_cost_usd))}</td>`;
}

function parentHTML(g, open) {
  const t = g.tool;
  const n = g.patterns.length;
  if (n === 0) {
    return `<tr class="tool-parent" data-tool="${escHtml(t.tool)}">
      <td class="tp-name"><span class="chev-space" aria-hidden="true"></span>${escHtml(t.tool)}</td>${statCells(t)}</tr>`;
  }
  return `<tr class="tool-parent" data-tool="${escHtml(t.tool)}" tabindex="0" aria-expanded="${open}">
      <td class="tp-name">${chevron}${escHtml(t.tool)} <span class="small">${escHtml(fmtNum(n))} ${n === 1 ? 'pattern' : 'patterns'}</span></td>${statCells(t)}</tr>`;
}

function patternHTML(r, open) {
  const name = r.pattern ? `<code>${escHtml(r.pattern)}</code>` : '<span class="small">(no pattern)</span>';
  return `<tr class="tool-pattern" data-tool="${escHtml(r.tool)}"${open ? '' : ' hidden'}>
      <td class="tp-name">${name}</td>${statCells(r)}</tr>`;
}

function errorReason(err) {
  if (err && err.status) return `HTTP ${err.status}${err.statusText ? ' ' + err.statusText : ''}`;
  return (err && err.message) || 'network error';
}

// onActivate runs fn on click, and on Enter or Space; cancelling the key
// stops a button's own synthesized click from running fn twice.
function onActivate(el, fn) {
  el.addEventListener('click', fn);
  el.addEventListener('keydown', (e) => {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    if (!e.repeat) fn(e);
  });
  el.addEventListener('keyup', (e) => { if (e.key === ' ') e.preventDefault(); });
}

export function toolPatternsHTML() {
  return `<div class="tool-patterns-section" id="tool-patterns">
      <button type="button" class="show-all" aria-expanded="false" aria-controls="tool-patterns-body">${chevron}${LABEL}</button>
      <div class="tool-patterns-body" id="tool-patterns-body" hidden></div>
    </div>`;
}

// wireToolPatterns binds the section toolPatternsHTML rendered and returns
// its controller: open({ scroll }) shows the table (fetching /tools the
// first time) and close() hides it.
export function wireToolPatterns(section) {
  const button = section && section.querySelector('button.show-all');
  const body = section && section.querySelector('.tool-patterns-body');
  if (!button || !body) return { open() {}, close() {} };

  let tools = null;
  let loading = false;
  let scrollPending = false;
  let sort = { ...DEFAULT_SORT };
  let query = '';
  let openByDefault = true;
  const expanded = new Map();
  // A tool the user has not toggled follows the default, except that a
  // filter opens it so its matching patterns show.
  const isExpanded = tool => (expanded.has(tool) ? expanded.get(tool) : (query.trim() !== '' || openByDefault));

  function renderRows() {
    const tbody = body.querySelector('table.tool-patterns tbody');
    if (!tbody) return;
    const groups = filterGroups(groupedRows(tools, sort.key, sort.dir), query);
    tbody.innerHTML = groups.length === 0
      ? `<tr><td colspan="${COLUMNS.length}" class="empty-state-row">No matching rows.</td></tr>`
      : groups.map(g => {
        const open = isExpanded(g.tool.tool);
        return parentHTML(g, open) + g.patterns.map(r => patternHTML(r, open)).join('');
      }).join('');
    body.querySelectorAll('thead th[data-key]').forEach(th => {
      const on = th.dataset.key === sort.key;
      th.classList.toggle('sorted-desc', on && sort.dir === 'desc');
      th.classList.toggle('sorted-asc', on && sort.dir === 'asc');
      th.setAttribute('aria-sort', on ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none');
    });
  }

  function toggleParent(tr) {
    const tool = tr.dataset.tool;
    const open = tr.getAttribute('aria-expanded') !== 'true';
    expanded.set(tool, open);
    tr.setAttribute('aria-expanded', String(open));
    tr.parentNode.querySelectorAll('tr.tool-pattern').forEach(p => {
      if (p.dataset.tool === tool) p.hidden = !open;
    });
  }

  function paintTable() {
    const all = groupedRows(tools);
    openByDefault = startExpanded(all);
    body.innerHTML = `<h3 class="tool-patterns-head"><svg class="icon" aria-hidden="true"><use href="#icon-tools"/></svg>${escHtml(headingText(all))}</h3>
      <div class="small">Every tool and argument pattern in this window, including the ones the hotspots leave out: tools whose results cost no context, and the Skill, SlashCommand and Agent tools. Context cost is the cache write that put a tool's results into context, plus each later turn that re-read them; each call counts once. Open or close a tool's row for its patterns.</div>
      <div class="controls tp-controls" role="search">
        <label>Filter rows <input class="tp-filter" type="search" placeholder="tool or pattern…" aria-label="Filter rows by tool or pattern"></label>
      </div>
      ${tableHTML('')}`;
    renderRows();

    const input = body.querySelector('input.tp-filter');
    input.addEventListener('input', () => { query = input.value; renderRows(); });
    body.querySelectorAll('thead th[data-key]').forEach(th => {
      onActivate(th, () => { sort = nextSort(sort, th.dataset.key); renderRows(); });
    });
    const tbody = body.querySelector('tbody');
    const parentOf = e => {
      const tr = e.target && e.target.closest ? e.target.closest('tr.tool-parent') : null;
      return tr && tr.hasAttribute('aria-expanded') ? tr : null;
    };
    tbody.addEventListener('click', (e) => { const tr = parentOf(e); if (tr) toggleParent(tr); });
    tbody.addEventListener('keydown', (e) => {
      if (e.key !== 'Enter' && e.key !== ' ') return;
      const tr = parentOf(e);
      if (!tr) return;
      e.preventDefault();
      if (!e.repeat) toggleParent(tr);
    });
  }

  const scrollToSection = () => {
    if (typeof section.scrollIntoView === 'function') section.scrollIntoView({ block: 'start' });
  };

  // settle scrolls a deep-linked section into view once its content is in:
  // scrolling while the table is a few skeleton rows stops short of it.
  function settle() {
    if (!scrollPending) return;
    scrollPending = false;
    scrollToSection();
  }

  async function load() {
    loading = true;
    body.innerHTML = tableHTML(tableBodySkeleton(COLUMNS.length, 6));
    let payload;
    try {
      payload = await fetchTools();
    } catch (err) {
      loading = false;
      body.innerHTML = `<div class="warning-card" role="alert"><strong class="danger">Failed to load tool patterns:</strong> ${escHtml(errorReason(err))}
        <button type="button" class="retry" data-retry>Retry</button></div>`;
      body.querySelector('[data-retry]').addEventListener('click', () => { load(); });
      settle();
      return;
    }
    loading = false;
    tools = payload || {};
    const provider = providerFromSearch(window.location.search);
    if (provider !== 'all' && !(tools.sessions > 0)) {
      body.innerHTML = `<p class="empty-state">${escHtml(emptyProviderText(provider))}</p>`;
    } else if (!tools.by_tool || tools.by_tool.length === 0) {
      body.innerHTML = `<p class="small empty-state">${EMPTY}</p>`;
    } else {
      paintTable();
    }
    settle();
  }

  function open({ scroll = false } = {}) {
    button.setAttribute('aria-expanded', 'true');
    section.classList.add('is-open');
    body.hidden = false;
    if (!hashShowAll(window.location.hash)) history.replaceState(history.state, '', SHOW_ALL);
    if (scroll && tools) scrollToSection();
    else if (scroll) scrollPending = true;
    if (!tools && !loading) load();
  }

  function close() {
    button.setAttribute('aria-expanded', 'false');
    section.classList.remove('is-open');
    body.hidden = true;
    if (hashShowAll(window.location.hash)) history.replaceState(history.state, '', '#overview');
  }

  onActivate(button, () => {
    if (button.getAttribute('aria-expanded') === 'true') close(); else open();
  });

  return { open, close };
}
