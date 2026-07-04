// Agents-local trace filter bar, split out of view-agents.js. Builds the
// bar, reads/applies the spec, dims non-matching rows, and steps the shared
// selection through matches (via coreHooks.select — the filter can't import
// the core, which imports it). The core wires the bar's input/click
// delegates and calls ensureFilterBar / applyFilter / the Insights bridges.

import { escHtml } from './format.js';
import {
  flattenSession, filterTrace, specActive, deepestRefs, parseRefKey,
} from './agents-logic.js';
import {
  lastGraph, activeSub, filterSpec, setFilterSpec,
  filterBarBuilt, setFilterBarBuilt, hitIndex, setHitIndex,
  filterMatchSet, setFilterMatchSet, dimNodes,
  kindBadge, kindFamily, coreHooks,
} from './agents-shared.js';

let currentHits = [];

// ── trace filter (Phase 2) ──────────────────────────────────────────────────
// One bar above the lenses turns the trace from something you read into
// something you interrogate: free text + kind chips + an errors toggle + slow/
// expensive thresholds. Matching (filterTrace) is pure and runs against the
// already-loaded graph, so it works in the offline static report with no
// round-trip. Non-matching rows dim across ALL lenses via a post-render DOM
// pass (no lens renderer needs to know about the filter); ‹ › step the shared
// selection through the matches.

const FILTER_KIND_ORDER = ['agent', 'edit', 'exec', 'read', 'web', 'skill', 'mcp', 'command', 'todo', 'other'];

// The trace filter dims + steps through per-ref rows. Feed and Tree lay them out
// directly; the Timeline's agent bars and turn segments also carry data-ref, so
// the same dim-walk collapses the Gantt to matches (e.g. failures only). Only
// Conversation (a thread) has no such rows, so the bar is hidden — and filtering
// not applied — there.
const FILTER_LENSES = new Set(['feed', 'tree', 'timeline']);
export function lensHasFilter(sub) { return FILTER_LENSES.has(sub); }

// presentKinds lists the kinds that actually occur in the graph, in canonical
// order — the chip row only offers kinds the user can act on.
function presentKinds(graph) {
  const seen = new Set();
  for (const s of (graph && graph.sessions) || []) {
    for (const a of flattenSession(s)) {
      for (const st of a.steps || []) {
        for (const t of st.tools || []) if (t && t.kind) seen.add(t.kind);
      }
    }
  }
  return FILTER_KIND_ORDER.filter(k => seen.has(k));
}

// ensureFilterBar fills the (persistent) bar exactly once. Kind chips reflect
// the loaded graph; rebuilding on every live update would wipe the user's
// focus/typing, so this is strictly idempotent.
export function ensureFilterBar(container) {
  if (filterBarBuilt) return;
  const bar = container.querySelector('[data-trace-filter]');
  if (!bar) return;
  const chips = presentKinds(lastGraph).map(k =>
    `<button type="button" class="tf-chip kind-${kindFamily(k)}" data-tf-kind="${k}" aria-pressed="false" title="Show only ${escHtml(k)} calls">${kindBadge(k)}<span class="tf-chip-label">${escHtml(k)}</span></button>`).join('');
  bar.innerHTML = `
    <input type="search" class="tf-text" data-tf-text placeholder="filter trace — name, path, input, reasoning…" aria-label="Filter trace by text">
    <div class="tf-chips" role="group" aria-label="Tool kinds">${chips}</div>
    <button type="button" class="tf-toggle" data-tf-errors aria-pressed="false" title="Only tool calls that errored">✗ errors</button>
    <label class="tf-num" title="Turns at least this slow"><span aria-hidden="true">⏱</span> ≥<input type="number" data-tf-dur min="0" step="500" placeholder="0"><span class="tf-unit">ms</span></label>
    <label class="tf-num" title="Turns or agents at least this costly"><span aria-hidden="true">$</span> ≥<input type="number" data-tf-cost min="0" step="0.01" placeholder="0"></label>
    <div class="tf-result" data-tf-result hidden>
      <span class="tf-count" data-tf-count></span>
      <button type="button" class="tf-nav" data-tf-prev title="Previous match" aria-label="Previous match">‹</button>
      <button type="button" class="tf-nav" data-tf-next title="Next match" aria-label="Next match">›</button>
      <button type="button" class="tf-clear" data-tf-clear title="Clear the filter">clear</button>
    </div>`;
  setFilterBarBuilt(true);
}

// readSpec pulls the current spec out of the bar's controls.
function readSpec(container) {
  const bar = container.querySelector('[data-trace-filter]');
  if (!bar) return filterSpec;
  const val = sel => { const el = bar.querySelector(sel); return el ? el.value : ''; };
  const pressed = sel => { const el = bar.querySelector(sel); return !!el && el.getAttribute('aria-pressed') === 'true'; };
  return {
    text: val('[data-tf-text]'),
    kinds: [...bar.querySelectorAll('[data-tf-kind][aria-pressed="true"]')].map(b => b.dataset.tfKind),
    errorsOnly: pressed('[data-tf-errors]'),
    minDurationMs: Number(val('[data-tf-dur]')) || 0,
    minCostUSD: Number(val('[data-tf-cost]')) || 0,
  };
}

// applyFilter recomputes the match set and dims every non-matching lens row.
// Called after each (re)render so live updates and lens switches keep dimming.
export function applyFilter(container) {
  const active = specActive(filterSpec) && lensHasFilter(activeSub);
  setFilterMatchSet(active ? filterTrace(lastGraph, filterSpec) : null);
  const lens = container.querySelector('.agents-lens');
  if (lens) {
    lens.classList.toggle('is-filtering', active);
    dimNodes(lens, filterMatchSet);
  }
  currentHits = filterMatchSet ? matchHits(filterMatchSet) : [];
  if (hitIndex >= currentHits.length) setHitIndex(-1);
  updateFilterResult(container);
}

// matchHits is the ordered list of "deepest" matched refs — a matched ref with
// no matched descendant — which is what ‹ › steps through. Sorted in reading
// order (session order, then agent/step/tool index).
function matchHits(matchSet) {
  const order = new Map(((lastGraph && lastGraph.sessions) || []).map((s, i) => [(s && s.session_id) || '', i]));
  const leaves = deepestRefs(matchSet);
  const rank = r => {
    const p = parseRefKey(r) || {};
    return [order.has(p.sessionId) ? order.get(p.sessionId) : 1e9, p.agentIndex ?? 1e9, p.stepIndex ?? -1, p.toolIndex ?? -1];
  };
  return leaves.sort((a, b) => {
    const ra = rank(a), rb = rank(b);
    for (let i = 0; i < ra.length; i++) if (ra[i] !== rb[i]) return ra[i] - rb[i];
    return 0;
  });
}

// updateFilterResult refreshes the "N matches" readout and prev/next state.
function updateFilterResult(container) {
  const bar = container.querySelector('[data-trace-filter]');
  if (!bar) return;
  const result = bar.querySelector('[data-tf-result]');
  const countEl = bar.querySelector('[data-tf-count]');
  if (!specActive(filterSpec)) { if (result) result.hidden = true; return; }
  if (result) result.hidden = false;
  const n = currentHits.length;
  if (countEl) {
    countEl.textContent = hitIndex >= 0 && n ? `${hitIndex + 1} / ${n} matches` : `${n} match${n === 1 ? '' : 'es'}`;
    countEl.classList.toggle('is-empty', n === 0);
  }
  bar.querySelectorAll('[data-tf-prev],[data-tf-next]').forEach(b => { b.disabled = n === 0; });
}

// stepHit advances the shared selection to the next/prev match and scrolls it
// into view in the current lens (the drawer still updates even if the active
// lens has no row for that ref — e.g. a tool hit while on the Timeline).
function stepHit(container, dir) {
  if (!currentHits.length) return;
  setHitIndex((hitIndex + dir + currentHits.length) % currentHits.length);
  const ref = currentHits[hitIndex];
  coreHooks.select(container, ref);
  const el = container.querySelector(`.agents-lens [data-ref="${ref}"]`);
  if (el && el.scrollIntoView) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
  updateFilterResult(container);
}

// applyBucketFilter is the bridge from an Insights latency bucket to the trace
// filter: it sets the spec to the bucket's tool kinds, mirrors that into the
// (shared) filter bar, and routes to the Timeline — a filter-bearing lens — so
// the dimming the Insights lens can't show actually appears. Empty kinds clears
// nothing useful, so it's a no-op (the bucket wasn't clickable anyway).
export function applyBucketFilter(container, kinds) {
  if (!kinds || kinds.length === 0) return;
  setFilterSpec({ text: '', kinds: kinds.slice(), errorsOnly: false, minDurationMs: 0, minCostUSD: 0 });
  setHitIndex(-1);
  syncFilterBar(container);
  location.hash = '#agents/timeline';
}

// applyCostFilter is the Cost-Pareto sibling of applyBucketFilter: a row (or the
// headline) names a dollar threshold, and we set the trace's minCostUSD to it and
// route to the Timeline so the dimming Insights can't show lands on a
// filter-bearing lens — "show me every turn at least this expensive." A
// non-positive threshold is a no-op (those rows aren't clickable anyway).
export function applyCostFilter(container, minCostUSD) {
  // Floor (never round up) to sub-cent precision so the field shows a clean
  // threshold AND the `>=` filter still admits the clicked turn — rounding up
  // would push the boundary past it and exclude the very turn that was clicked.
  const v = Math.floor(Number(minCostUSD) * 1e4) / 1e4;
  if (!(v > 0)) return;
  setFilterSpec({ text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: v });
  setHitIndex(-1);
  syncFilterBar(container);
  location.hash = '#agents/timeline';
}

// applyErrorFilter is the Error-breakdown sibling of applyBucketFilter: it turns
// on errorsOnly (optionally pinned to one tool kind from a per-kind bar / failing
// tool) and routes to the Timeline so the dimming Insights can't show lands on a
// filter-bearing lens — "show me every errored tool (of this kind)." Empty kinds
// means "all errors", which is still a valid filter (unlike applyBucketFilter,
// whose empty-kinds case constrains nothing).
export function applyErrorFilter(container, kinds) {
  setFilterSpec({ text: '', kinds: (kinds || []).slice(), errorsOnly: true, minDurationMs: 0, minCostUSD: 0 });
  setHitIndex(-1);
  syncFilterBar(container);
  location.hash = '#agents/timeline';
}

// syncFilterBar pushes the current filterSpec into the bar's controls — the
// inverse of readSpec — so a programmatic filter (e.g. an Insights bucket click)
// shows up in the bar the user can then read and clear.
function syncFilterBar(container) {
  const bar = container.querySelector('[data-trace-filter]');
  if (!bar) return;
  const set = (sel, v) => { const el = bar.querySelector(sel); if (el) el.value = v; };
  set('[data-tf-text]', filterSpec.text || '');
  set('[data-tf-dur]', filterSpec.minDurationMs || '');
  set('[data-tf-cost]', filterSpec.minCostUSD || '');
  bar.querySelectorAll('[data-tf-kind]').forEach(b =>
    b.setAttribute('aria-pressed', filterSpec.kinds.includes(b.dataset.tfKind) ? 'true' : 'false'));
  const err = bar.querySelector('[data-tf-errors]');
  if (err) err.setAttribute('aria-pressed', filterSpec.errorsOnly ? 'true' : 'false');
}

// clearFilter resets the controls and removes all dimming.
function clearFilter(container) {
  const bar = container.querySelector('[data-trace-filter]');
  if (bar) {
    bar.querySelectorAll('[data-tf-text],[data-tf-dur],[data-tf-cost]').forEach(i => { i.value = ''; });
    bar.querySelectorAll('[data-tf-kind],[data-tf-errors]').forEach(b => b.setAttribute('aria-pressed', 'false'));
  }
  setFilterSpec({ text: '', kinds: [], errorsOnly: false, minDurationMs: 0, minCostUSD: 0 });
  setHitIndex(-1);
  applyFilter(container);
}

// onFilterInput/onFilterClick are the bar's delegated handlers (wired once).
export function onFilterInput(container, e) {
  if (!e.target.closest('[data-tf-text],[data-tf-dur],[data-tf-cost]')) return;
  setFilterSpec(readSpec(container));
  setHitIndex(-1);
  applyFilter(container);
}

export function onFilterClick(container, e) {
  const toggle = e.target.closest('[data-tf-kind],[data-tf-errors]');
  if (toggle) {
    toggle.setAttribute('aria-pressed', toggle.getAttribute('aria-pressed') === 'true' ? 'false' : 'true');
    setFilterSpec(readSpec(container));
    setHitIndex(-1);
    applyFilter(container);
    return;
  }
  if (e.target.closest('[data-tf-prev]')) { stepHit(container, -1); return; }
  if (e.target.closest('[data-tf-next]')) { stepHit(container, +1); return; }
  if (e.target.closest('[data-tf-clear]')) { clearFilter(container); }
}
