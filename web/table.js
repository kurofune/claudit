// Paginated/sortable/filtered table state machine. Ported from the
// legacy buildRows/renderTable/ensurePager block in
// internal/render/report.html.tmpl (lines 2740-2925) so the SPA's
// Cost/Cache/Tools/Subagents tabs render rows the same way the
// fat-HTML report does.
//
// Each call to buildRows() captures the full row data and a mapper in
// per-table state; the renderer filters → sorts → slices to current
// page → builds tbody and updates the pager. Sort, filter, and pager
// events all call renderTable() rather than mutating DOM directly.
//
// Per-table state lives in a WeakMap keyed by the <table> element so
// detached tables (view module re-paint) drop state automatically.

import { fmtNum } from './format.js';

const PAGE_SIZE = 25;
const tableStates = new WeakMap();

// rowSearchText builds a single lowercased haystack from a row's
// string fields so the global filter can match any of them without
// per-call re-stringifying. Numbers/objects are skipped to avoid
// matching coincidental digits in token counts.
export function rowSearchText(r) {
  let s = '';
  for (const k in r) {
    const v = r[k];
    if (typeof v === 'string') s += ' ' + v;
  }
  return s.toLowerCase();
}

// getFilterParams reads the global filter + min-cost inputs (rendered
// in the SPA shell at #panel > .controls). Centralized here so
// filterRows() and the legacy bare-anchor handlers stay in sync if
// the input ids ever change.
export function getFilterParams() {
  const fEl = document.getElementById('filter');
  const mEl = document.getElementById('mincost');
  const q = (fEl ? fEl.value : '').trim().toLowerCase();
  const minCost = parseFloat((mEl ? mEl.value : '0') || '0') || 0;
  return { q, minCost };
}

function filterRows(st) {
  const { q, minCost } = getFilterParams();
  return st.rows.filter(r => {
    if (minCost > 0 && (r.CostUSD || 0) < minCost) return false;
    if (q && !rowSearchText(r).includes(q)) return false;
    return true;
  });
}

// sortRows returns a stable-ish sorted shallow copy. null/undefined
// values sink to the end of an asc sort (top of desc). Mixed numeric
// vs string columns sort by JS .localeCompare on the stringified pair
// — the legacy code's contract.
export function sortRows(rows, key, dir) {
  if (!key || !rows.length || !(key in rows[0])) return rows;
  const sign = dir === 'asc' ? 1 : -1;
  return [...rows].sort((a, b) => {
    const av = a[key], bv = b[key];
    if (av == null && bv == null) return 0;
    if (av == null) return sign;
    if (bv == null) return -sign;
    if (typeof av === 'number' && typeof bv === 'number') {
      return sign * (av - bv);
    }
    return sign * String(av).localeCompare(String(bv));
  });
}

function attachHeat(td, value, max) {
  if (max <= 0 || value == null) return;
  td.classList.add('heat');
  td.style.setProperty('--heat',
    Math.min(100, 100 * value / max).toFixed(1) + '%');
}

function ensurePager(table) {
  let pager = table.parentNode.querySelector(
    `:scope > .pager[data-pager-for="${table.dataset.table}"]`);
  if (pager) return pager;
  pager = document.createElement('div');
  pager.className = 'pager';
  pager.dataset.pagerFor = table.dataset.table;
  pager.innerHTML = `
    <button class="pager-btn" data-pager-action="prev" aria-label="Previous page">‹ Prev</button>
    <span class="pager-info">
      <span class="pager-page">Page <strong class="pager-cur">1</strong> / <strong class="pager-total">1</strong></span>
      <span class="pager-count">0 entries</span>
    </span>
    <button class="pager-btn" data-pager-action="next" aria-label="Next page">Next ›</button>
  `;
  table.parentNode.insertBefore(pager, table.nextSibling);
  pager.querySelector('[data-pager-action="prev"]').addEventListener('click', () => {
    const st = tableStates.get(table);
    if (st && st.page > 0) { st.page--; renderTable(table); }
  });
  pager.querySelector('[data-pager-action="next"]').addEventListener('click', () => {
    const st = tableStates.get(table);
    if (!st) return;
    const totalPages = Math.max(1, Math.ceil(filterRows(st).length / st.pageSize));
    if (st.page < totalPages - 1) { st.page++; renderTable(table); }
  });
  return pager;
}

export function renderTable(table) {
  const st = tableStates.get(table);
  if (!st) return;

  const filtered = filterRows(st);
  const sorted = sortRows(filtered, st.sortKey, st.sortDir);
  const totalRows = sorted.length;
  const totalPages = Math.max(1, Math.ceil(totalRows / st.pageSize));
  if (st.page >= totalPages) st.page = totalPages - 1;
  if (st.page < 0) st.page = 0;
  const pageRows = sorted.slice(st.page * st.pageSize, (st.page + 1) * st.pageSize);

  // Heat scales per-column: a column's overlay maxes at the largest
  // heatVal seen in that column among filtered rows. This keeps the
  // Miss-tokens column from being scaled against CostUSD (which would
  // always clip to 100%).
  const heatMax = {};
  filtered.forEach(r => {
    const cells = st.mapper(r);
    cells.forEach((c, i) => {
      const v = c[2];
      if (v != null && v > 0) {
        heatMax[i] = Math.max(heatMax[i] || 0, v);
      }
    });
  });

  const tbody = table.querySelector('tbody');
  tbody.innerHTML = '';

  if (totalRows === 0) {
    const colCount = table.querySelectorAll('thead th').length;
    tbody.innerHTML = `<tr><td colspan="${colCount}" class="empty-state-row">No matching rows.</td></tr>`;
  } else {
    pageRows.forEach(r => {
      const tr = document.createElement('tr');
      tr.dataset.cost = r.CostUSD || 0;
      const cells = st.mapper(r);
      cells.forEach(([html, isNum, heatVal], i) => {
        const td = document.createElement('td');
        if (isNum) td.classList.add('num');
        td.innerHTML = html;
        if (heatVal != null) attachHeat(td, heatVal, heatMax[i] || 0);
        tr.appendChild(td);
      });
      tbody.appendChild(tr);
    });
  }

  // Pager: hide entirely when total fits on a single page.
  const pager = ensurePager(table);
  pager.querySelector('.pager-cur').textContent = totalRows === 0 ? 0 : st.page + 1;
  pager.querySelector('.pager-total').textContent = totalRows === 0 ? 0 : totalPages;
  pager.querySelector('.pager-count').textContent =
    fmtNum(totalRows) + (totalRows === 1 ? ' entry' : ' entries');
  pager.querySelector('[data-pager-action="prev"]').disabled = st.page <= 0 || totalRows === 0;
  pager.querySelector('[data-pager-action="next"]').disabled =
    st.page >= totalPages - 1 || totalRows === 0;
  pager.style.display = totalRows > st.pageSize ? '' : 'none';

  // Sort indicators on header cells.
  table.querySelectorAll('thead th').forEach(h =>
    h.classList.remove('sorted-asc', 'sorted-desc'));
  if (st.sortKey) {
    const th = table.querySelector(`thead th[data-key="${st.sortKey}"]`);
    if (th) th.classList.add(st.sortDir === 'asc' ? 'sorted-asc' : 'sorted-desc');
  }
}

// buildRows initializes per-table state and triggers the first render.
// Re-calling buildRows() on the same table replaces state — view
// modules can repaint without leaking old row data.
export function buildRows(table, rows, mapper) {
  if (!table) return;
  tableStates.set(table, {
    rows, mapper,
    pageSize: PAGE_SIZE,
    page: 0,
    sortKey: null,
    sortDir: null,
  });
  // Wire header sort once per table. Re-binding on every buildRows()
  // would stack listeners; the dataset.tableWired sentinel avoids that.
  if (!table.dataset.tableWired) {
    table.querySelectorAll('thead th[data-key]').forEach(th => {
      th.addEventListener('click', () => {
        const st = tableStates.get(table);
        if (!st) return;
        const key = th.dataset.key;
        // Injected trend column has no underlying field — skip silently.
        if (st.rows.length && !(key in st.rows[0])) return;
        if (st.sortKey === key) {
          st.sortDir = st.sortDir === 'asc' ? 'desc' : 'asc';
        } else {
          st.sortKey = key;
          st.sortDir = 'desc';
        }
        st.page = 0;
        renderTable(table);
      });
    });
    table.dataset.tableWired = '1';
  }
  renderTable(table);
}

// applyFiltersAll re-renders every active paged table after a filter
// or min-cost input event. Called by the global filter wire-up in
// wireGlobalFilters().
export function applyFiltersAll() {
  document.querySelectorAll('table[data-table]').forEach(table => {
    const st = tableStates.get(table);
    if (!st) return;
    st.page = 0;
    renderTable(table);
  });
  // Render-once details rows (the drill-down tables) use a class-based
  // hide rather than the state machine. Mirrors the legacy code.
  const { q, minCost } = getFilterParams();
  document.querySelectorAll('details tbody tr').forEach(tr => {
    const cost = parseFloat(tr.dataset.cost || '0');
    const text = tr.textContent.toLowerCase();
    const matches = (!q || text.includes(q)) && (cost >= minCost);
    tr.classList.toggle('hidden', !matches);
  });
}

// wireGlobalFilters binds the #filter + #mincost inputs once. Safe to
// call repeatedly — the wired sentinel guards against double-binding
// when SSE-driven repaints re-import the module's effects.
export function wireGlobalFilters() {
  const fEl = document.getElementById('filter');
  const mEl = document.getElementById('mincost');
  if (fEl && !fEl.dataset.tableWired) {
    fEl.addEventListener('input', applyFiltersAll);
    fEl.dataset.tableWired = '1';
  }
  if (mEl && !mEl.dataset.tableWired) {
    mEl.addEventListener('input', applyFiltersAll);
    mEl.dataset.tableWired = '1';
  }
}

// injectTrendColumn adds a "Trend" header cell to a sortable table
// immediately before the column with the given data-key. PERIOD-
// conditional — when period is empty the column is skipped entirely
// (matching the legacy template's PERIOD guard).
export function injectTrendColumn(table, beforeKey, period) {
  if (!period || !table) return;
  const headRow = table.querySelector('thead tr');
  if (!headRow) return;
  const ths = Array.from(headRow.children);
  // Re-running with the same beforeKey would inject a duplicate; guard
  // by checking for an existing data-trend-injected sentinel.
  if (headRow.querySelector('th[data-trend-injected]')) return;
  const idx = ths.findIndex(th => th.dataset.key === beforeKey);
  const th = document.createElement('th');
  th.textContent = 'Trend';
  th.dataset.trendInjected = '1';
  th.title = 'Per-row sparkline of cost across the report buckets (day/week/month, set by --by). Bars are downsampled to ~30 cells when wider.';
  if (idx >= 0) headRow.insertBefore(th, ths[idx]);
  else headRow.appendChild(th);
}

// withTrend inserts a trend-sparkline cell at the index where
// injectTrendColumn() placed the header. PERIOD-conditional. Pass
// sparkBuilder = trendSparkHit for the hit-ratio variant (0..1 fixed
// axis); default builder is the cost-normalized one.
export function withTrend(cells, idx, series, period, sparkBuilder) {
  if (!period || !sparkBuilder) return cells;
  const spark = sparkBuilder(series, period);
  const cell = [`<span class="trend-cell">${spark}</span>`, false];
  return cells.slice(0, idx).concat([cell]).concat(cells.slice(idx));
}
