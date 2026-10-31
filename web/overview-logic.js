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
