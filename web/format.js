// Formatting helpers ported verbatim from the legacy inline JS in
// internal/render/report.html.tmpl. Keeping the API surface identical
// makes the chart and view modules drop-in equivalent to their
// inline-IIFE ancestors — no per-call transformation lookup needed.

export const fmtMoney = v => {
  if (v == null) return '—';
  if (v >= 1000) return '$' + Math.round(v).toLocaleString();
  return '$' + v.toFixed(2);
};

export const fmtPct = (part, total) => total > 0 ? (100 * part / total).toFixed(1) + '%' : '—';

// pct is the table-mapper-friendly alias used by table cell rows. Same
// math as fmtPct, named to match the legacy inline JS where it sits
// alongside fmtMoney / fmtNum.
export const pct = fmtPct;

// truncate clips a long string by dropping middle characters and
// prepending an ellipsis — keeps the tail visible (file basename,
// session-id suffix). Mirrors the Go renderer's truncate() in
// internal/render/markdown.go so a project path looks the same in
// the SPA's hbar list and in the markdown report.
export function truncate(s, max) {
  if (!s) return '';
  const str = String(s);
  if (str.length <= max) return str;
  if (max <= 1) return '…';
  return '…' + str.slice(str.length - (max - 1));
}

export const fmtPct1 = v => (v == null || isNaN(v)) ? '—' : (100 * v).toFixed(1) + '%';

export const fmtNum = v => v == null ? '0' : Number(v).toLocaleString();

// fmtCompact abbreviates large counts for chart axes and tooltips
// where a full comma-grouped integer is too wide: 1234567 → "1.2M",
// 34000 → "34k". Full precision still lives in fmtNum for table cells.
export const fmtCompact = v => {
  v = Number(v) || 0;
  const abs = Math.abs(v);
  if (abs >= 1e9) return (v / 1e9).toFixed(abs >= 1e10 ? 0 : 1) + 'B';
  if (abs >= 1e6) return (v / 1e6).toFixed(abs >= 1e7 ? 0 : 1) + 'M';
  if (abs >= 1e3) return (v / 1e3).toFixed(abs >= 1e4 ? 0 : 1) + 'k';
  return String(Math.round(v));
};

export const fmtDate = ts => {
  if (!ts) return '—';
  try { return new Date(ts).toISOString().slice(0, 16).replace('T', ' '); }
  catch { return ts; }
};

export const escHtml = s => String(s == null ? '' : s)
  .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

// Semantic pill for hit-ratio values. Tier thresholds match the
// rules-of-thumb in the cache guide — kept in sync with the legacy JS.
// Pass extraClass="tier-sm" for the compact totals/nav-metric variant.
export function hitRatioPill(v, extraClass) {
  if (v == null || isNaN(v)) return '—';
  const tier = v >= 0.70 ? 'good' : v >= 0.40 ? 'ok' : 'bad';
  const cls = `tier tier-${tier}` + (extraClass ? ' ' + extraClass : '');
  return `<span class="${cls}">${fmtPct1(v)}</span>`;
}

// bucketLabel formats a TrendPoint.time for X-axis ticks. Period is
// the report's bucket granularity — "day" | "week" | "month" | "".
export function bucketLabel(ts, period) {
  if (!ts) return '';
  const s = String(ts).slice(0, 10);
  if (period === 'month') return String(ts).slice(0, 7);
  if (period === 'week') return 'wk ' + s.slice(5);
  return s.slice(5);
}
