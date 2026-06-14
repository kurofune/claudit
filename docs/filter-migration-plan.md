# Plan: replace the global filter bar with per-view local filters

Status: **not started** (design approved, no implementation landed). Written 2026-06-14.

## Why

The top "Filter rows / Min cost" bar (`web/index.html:172-176`) claims "Filter and
min-cost apply across every section" — but it doesn't. Verified mechanics:

- The inputs `#filter` + `#mincost` do nothing until a view calls `wireGlobalFilters()`
  (`web/table.js:239`), which binds one global `input` listener running
  `applyFiltersAll()` (`web/table.js:218`).
- `applyFiltersAll` only re-renders **registered sortable tables** (`table[data-table]`
  built via `buildRows`) and toggles `.hidden` on **drill-down `details tbody tr`** rows.
  Nothing else responds.

### Truth table (where the global bar actually works)

| View | wires it? | effect |
|---|---|---|
| Cost | yes (`view-cost.js:375`) | filters the 5 tables — **not** the model/project bar charts above them |
| Tokens | yes (`view-tokens.js:230`) | filters tables — not the composition bar / trend chart |
| Cache | yes (`view-cache.js:254`) | filters tables |
| Tools | yes (`view-tools.js:210`) | filters tables — not the by-tool hbar list |
| Overview | no | inert (tiles/charts only) |
| Sessions | no | inert (cards are `<details>` with div bodies, no tables) |
| Agents | no | inert — and it has its **own** rich filter ("filter trace", `view-agents.js:874`) |

Problems: the hint lies; even where it "works" the chart above the table ignores it;
and it competes with Agents' own (better) in-view filter.

## Chosen direction (user-approved)

**Per-view local filters.** Remove the global top bar. Each view that supports filtering
gets its own small filter row that filters that view's tables **and its row-derived bar
charts**. Overview/Sessions get no bar (nothing to filter). Agents keeps its own bar.

Which charts filter: only **row-derived** ones — Cost's model+project hbar lists and
Tools' by-tool hbar list. The corpus "headline" charts (Tokens composition bar + volume
trend, Cache hit-ratio summary band, Cost main/side stacked bar) represent whole-corpus
totals and should **not** respond to a row filter — leave them.

## Design

### Shared infra — `web/table.js`

1. Make `getFilterParams(scope)` scope-aware with a global fallback (keeps un-migrated
   views working mid-migration):
   ```js
   export function getFilterParams(scope) {
     let fEl = scope && scope.querySelector('.vf-text');
     let mEl = scope && scope.querySelector('.vf-cost');
     if (!fEl) fEl = document.getElementById('filter');   // legacy global bar
     if (!mEl) mEl = document.getElementById('mincost');
     const q = (fEl ? fEl.value : '').trim().toLowerCase();
     const minCost = parseFloat((mEl ? mEl.value : '0') || '0') || 0;
     return { q, minCost };
   }
   ```
2. Extract a **pure** predicate (TDD this — see test below) and use it in `filterRows`:
   ```js
   export function rowPassesFilter(r, q, minCost) {
     if (minCost > 0 && (r.CostUSD || 0) < minCost) return false;
     if (q && !rowSearchText(r).includes(q)) return false;
     return true;
   }
   ```
3. `filterRows(st, table)` derives scope from `table.closest('[data-own-filter]')` and
   calls `rowPassesFilter`. Thread `table` through `renderTable` (line ~113) and the two
   pager `filterRows(st)` calls (lines ~103-104).
4. Add `wireViewFilters(scope, onApply)`: binds the scope's `.vf-text`/`.vf-cost`
   (guard with a `dataset.vfWired` sentinel); on input re-renders the scope's tables
   (reset `st.page=0`), toggles its `details tbody tr` rows, then calls
   `onApply({q,minCost})` for view-specific chart re-render.
5. Keep `wireGlobalFilters`/`applyFiltersAll` until the last step (un-migrated views
   still use them), then delete.

### Router scaffold — `web/router.js:24` `activate()`

Hide the global bar on views that own their filter, so no two bars show mid-migration:
```js
const active = [...views].find(v => v.dataset.view === route.view);
const globalBar = document.querySelector('.controls:not(.view-filter)');
if (globalBar) globalBar.hidden = !!(active && active.dataset.ownFilter);
```
Remove this line in the final step once the global bar is gone.

### Per-view changes (repeat for each)

- Mark the view section in `web/index.html` with `data-own-filter` (this is the scope
  marker AND the router-hide flag).
- Add a local bar at the top of the view's SHELL — reuse `.controls` styling, add
  `view-filter` class so it's distinguishable from the global bar, and class the inputs
  `vf-text` / `vf-cost` (NOT ids, to avoid duplicate `#filter`/`#mincost`):
  ```html
  <div class="controls view-filter" role="search">
    <label>Filter rows: <input class="vf-text" type="search" placeholder="model, path, command…"></label>
    <label>Min cost: <input class="vf-cost" type="number" value="0" step="0.01" min="0"></label>
  </div>
  ```
- Replace the view's `wireGlobalFilters()` with `wireViewFilters(container, onApply)`.
  - **Cost** (`view-cost.js`): `onApply` re-renders the model+project hbars from filtered
    rows: `barsModel.innerHTML = modelBarsHTML(byModel.filter(keep), totalCost)` etc.,
    where `keep = r => rowPassesFilter(r, q, minCost)`. Keep `totalCost` as the bar
    denominator. Leave the stacked main/side bar alone. The `byModel`/`byProject`/
    `totalCost` are captured in the paint closure (wireViewFilters is called once).
  - **Tools** (`view-tools.js`): `onApply` re-renders the by-tool hbar (`#tools-bars`).
  - **Tokens** / **Cache**: no `onApply` (tables only).

### Migration order (one reviewable step per turn, app stays working throughout)

1. **Cost** — infra (table.js scope-aware + `rowPassesFilter` + `wireViewFilters` + TDD)
   + router scaffold + Cost local bar (tables + model/project hbars). Verify: Cost local
   bar filters tables AND bars; global bar hidden on Cost; Tokens still filters via the
   still-present global bar.
2. **Tools** — local bar (tables + by-tool hbar).
3. **Tokens** — local bar (tables only).
4. **Cache** — local bar (tables only).
5. **Cleanup** — delete the global `.controls` bar from `index.html`, remove
   `wireGlobalFilters`/`applyFiltersAll` and the global fallback in `getFilterParams`,
   remove the router-hide line. Verify all four + that Overview/Sessions/Agents have no bar.

### TDD — recreate `jstest/table.test.js` (Red first)

```js
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { rowSearchText, rowPassesFilter } from '../web/table.js';

test('rowSearchText concatenates only string fields, lowercased', () => {
  const r = { Model: 'Claude-Opus', Project: '/p/Foo', CostUSD: 12.5, Turns: 9 };
  const s = rowSearchText(r);
  assert.ok(s.includes('claude-opus'));
  assert.ok(s.includes('/p/foo'));
  assert.ok(!s.includes('12.5')); // numbers skipped
});
test('rowPassesFilter passes everything when there is no filter', () => {
  assert.equal(rowPassesFilter({ Model: 'x', CostUSD: 0.01 }, '', 0), true);
});
test('rowPassesFilter rejects rows below the min cost', () => {
  assert.equal(rowPassesFilter({ Model: 'x', CostUSD: 0.5 }, '', 1), false);
  assert.equal(rowPassesFilter({ Model: 'x', CostUSD: 0.5 }, '', 0.5), true); // >= passes
});
test('rowPassesFilter treats a missing CostUSD as 0', () => {
  assert.equal(rowPassesFilter({ Model: 'x' }, '', 1), false);
  assert.equal(rowPassesFilter({ Model: 'x' }, '', 0), true);
});
test('rowPassesFilter matches the query against any string field', () => {
  const r = { Model: 'claude-opus', Project: '/p/foo', CostUSD: 5 };
  assert.equal(rowPassesFilter(r, 'opus', 0), true);
  assert.equal(rowPassesFilter(r, '/p/foo', 0), true);
  assert.equal(rowPassesFilter(r, 'sonnet', 0), false);
});
test('rowPassesFilter requires BOTH the query and the min cost to pass', () => {
  const r = { Model: 'claude-opus', CostUSD: 0.5 };
  assert.equal(rowPassesFilter(r, 'opus', 1), false);
  assert.equal(rowPassesFilter(r, 'sonnet', 0), false);
  assert.equal(rowPassesFilter(r, 'opus', 0.5), true);
});
```

## ⚠️ Before starting: commit the unrelated work first

When this plan was written, the tree had **completed, browser-verified, but uncommitted**
work from three prior tasks, all mixed in the same files. Commit these as separate
conventional commits before touching the filter migration:

- **Timeline tooltip off-screen fix** — `web/tooltip.js` (`placeTooltip` gained an optional
  `anchorX`; `position`/`show`/`onEnter` thread the cursor x; wide targets anchor to the
  cursor) + `jstest/tooltip.test.js` (3 new tests).
- **Agents-view tooltip scoping** — `web/tooltip.js` (`data-no-tooltip` opt-out in `show`)
  + `web/view-agents.js` (`data-no-tooltip` on `.itree`; moved cwd `title` onto the
  `.tl-sess-item-proj` / `.conv-sess-item-proj` spans) + `web/app.css` (those proj spans
  `align-self:flex-start; max-width:100%` to shrink-wrap so the tooltip only fires over
  the name text).
- **Feed agent column width** — `web/app.css` `.agent-feed` grid: agent track
  `minmax(0,1.3fr)` → `minmax(0,1fr)` (~18% narrower).

These three are independent of the filter migration and were each verified; the user just
hadn't said "commit" yet. Splitting them by file/hunk may be needed since they share
`tooltip.js` and `app.css`.
