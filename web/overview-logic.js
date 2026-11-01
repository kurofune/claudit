// Pure helpers for the Overview view (view-overview.js), unit-tested in
// jstest/overview-logic.test.js.

import { fmtMoney } from './format.js';

// hotspotTrend is a card's change against the previous window of equal
// length: ▲/▼ with a whole percent, "new" when the hotspot cost nothing
// there, or null when the server compared no previous window.
export function hotspotTrend(h) {
  if (!h || h.prev_cost_usd == null) return null;
  const prev = `Previous window: ${fmtMoney(h.prev_cost_usd)}`;
  if (h.trend_pct == null) {
    return { text: 'new', dir: 'new', title: `${prev} — no context cost from this hotspot in the previous window of equal length` };
  }
  const n = Math.round(Math.abs(h.trend_pct));
  if (h.trend_pct >= 0) return { text: `▲ +${n}%`, dir: 'up', title: prev };
  return { text: `▼ −${n}%`, dir: 'down', title: prev };
}

// showForecastTab decides whether the trend section offers a Forecast
// subtab. It stays on daily bucketing even before the month has two days
// of cost (reason too_few_days) so the panel can say why there is no
// projection yet; a historical or non-daily window has nothing to project.
// Payloads from before the reason field fall back to "has a projection".
export function showForecastTab(forecast, period) {
  if (period !== 'day' || !forecast) return false;
  if (typeof forecast.reason === 'string') {
    return forecast.reason === '' || forecast.reason === 'too_few_days';
  }
  return (forecast.projected_month_end_usd || 0) > 0;
}

// barWidth is a hotspot's share-bar width in percent: its pct_of_total
// against the largest hotspot's, so the top row fills its track.
export function barWidth(h, hotspots) {
  const pct = h && h.pct_of_total > 0 ? h.pct_of_total : 0;
  const max = Math.max(0, ...(hotspots || []).map(x => (x && x.pct_of_total > 0 ? x.pct_of_total : 0)));
  if (pct === 0 || max === 0) return 0;
  return Math.min(100, (100 * pct) / max);
}

const HOTSPOT_HASH = /^#?overview\/hotspot-([1-9]\d*)$/;

// hashRank is the N of a #overview/hotspot-N deep link, or null.
export function hashRank(hash) {
  const m = HOTSPOT_HASH.exec(String(hash || ''));
  return m ? Number(m[1]) : null;
}

const isRank = n => Number.isInteger(n) && n > 0;

// restoreExpanded is the sorted set of hotspot ranks to open on paint:
// the ranks stored (a JSON array, as written on every toggle) plus the
// deep-linked rank. Malformed input contributes nothing.
export function restoreExpanded({ hash, stored }) {
  const ranks = new Set();
  try {
    const parsed = JSON.parse(stored);
    if (Array.isArray(parsed)) parsed.filter(isRank).forEach(n => ranks.add(n));
  } catch { /* nothing stored, or not JSON */ }
  const linked = hashRank(hash);
  if (linked != null) ranks.add(linked);
  return [...ranks].sort((a, b) => a - b);
}

// promptPreview is h.prompt as the Show-prompt preview displays it: every
// JSONL path removed, with the separator that introduced it. The copied
// prompt keeps them, since the agent opens those files.
export function promptPreview(h) {
  if (!h || !h.prompt) return '';
  const files = [...new Set((h.examples || []).map(e => e && e.source_file).filter(Boolean))]
    .sort((a, b) => b.length - a.length);
  let text = h.prompt;
  for (const f of files) text = text.split(f).join('');
  return text
    .replace(/\S*\.jsonl\b/g, '')
    .replace(/[ \t]*·[ \t]*$/gm, '')
    .replace(/[ \t]+$/gm, '');
}
