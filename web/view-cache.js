// Cache view — paints the four cache-efficiency drill-downs
// (projects, sessions, subagent types, invocations) plus the overall-
// hit-ratio summary band. Per-row sparklines use trendSparkHit (axis-
// fixed 0..1) so a 30%-hit row reads visibly shorter than a 90%-hit
// row even when their cacheable volume is identical.

import { fetchCache, fetchTrends } from './api.js';
import { fmtNum, fmtMoney, hitRatioPill, escHtml, truncate } from './format.js';
import { trendSparkHit } from './charts.js';
import {
  buildRows, withTrend, injectTrendColumn, wireGlobalFilters,
} from './table.js';

const labelIcon = id => `<svg class="icon" aria-hidden="true"><use href="#icon-${id}"/></svg>`;

const SHELL = `
  <header class="view-head"><h1>${labelIcon('cache')}Cache efficiency</h1></header>

  <details class="guide">
    <summary>Why miss tokens matter — and what you can do about them</summary>
    <div class="body">
      <p><strong>Cached input tokens cost roughly a tenth of fresh input.</strong> Every time the cache misses, you pay ~10× more for the same context. Getting your hit ratio up is usually the single biggest cost lever in this report.</p>
      <ul>
        <li><strong>Hit ratio</strong> = <code>cache_read / (cache_read + input + cache_create_5m + cache_create_1h)</code>. Higher is better.</li>
        <li><strong>Miss tokens</strong> = the volume that paid full price (<code>input</code>) or cache-write price (<code>cache_create</code>) instead of the cheap <code>cache_read</code> price. Rows in the tables below are ranked by this number — these are your cost levers.</li>
      </ul>
      <p><strong>What high miss tokens usually mean:</strong></p>
      <ul>
        <li><em>Many short sessions, low hit ratio</em> → you keep starting fresh sessions before the cache warms up. Try staying in one session longer; restarts are expensive.</li>
        <li><em>Long sessions, still high miss</em> → context keeps getting invalidated. Common causes: editing very large files (each edit invalidates downstream cache), running <code>/compact</code> often, or jumping between unrelated tasks in one session.</li>
        <li><em>Subagents dominating miss tokens</em> → expected. Every subagent invocation starts with an empty cache, so their miss volume is a structural tax (see the <strong>Subagent types</strong> sub-tab). The only lever is invoking subagents less, or with smaller starting context.</li>
      </ul>
    </div>
  </details>

  <div id="cache-summary" class="trend-summary"></div>

  <nav class="subtabs" aria-label="Cache breakdowns">
    <a class="subtab is-active" href="#cache/projects"    data-subtab="projects">Projects</a>
    <a class="subtab"           href="#cache/sessions"    data-subtab="sessions">Sessions</a>
    <a class="subtab"           href="#cache/subagents"   data-subtab="subagents">Subagent types</a>
    <a class="subtab"           href="#cache/invocations" data-subtab="invocations">Invocations</a>
  </nav>

  <div class="subview is-active" data-subview="projects">
    <table data-table="cache-project">
      <thead><tr>
        <th data-key="Key" title="Working directory the session ran in.">Project</th>
        <th data-key="HitRatio" class="num" title="cache_read / (cache_read + input + cache_create_5m + cache_create_1h). Higher is better.">Hit ratio</th>
        <th data-key="Miss" class="num" title="Tokens paid full price (input) or cache-write price (cache_create) instead of the cheap cache-read price. Lower is better — this is your cost lever.">Miss tokens</th>
        <th data-key="CacheReadTokens" class="num" title="Tokens served from cache. Cheapest input price (~10% of fresh).">Cache read</th>
        <th data-key="Turns" class="num" title="Number of assistant turns across this project's sessions.">Turns</th>
        <th data-key="CostUSD" class="num" title="Dollar cost of those turns.">Cost</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>

  <div class="subview" data-subview="sessions">
    <table data-table="cache-session">
      <thead><tr>
        <th data-key="Key" title="Session ID (matches the .jsonl filename in ~/.claude/projects/…).">Session</th>
        <th data-key="Subtitle" title="Working directory of this session.">Project</th>
        <th data-key="HitRatio" class="num" title="cache_read / (cache_read + input + cache_create_5m + cache_create_1h). Higher is better.">Hit ratio</th>
        <th data-key="Miss" class="num" title="Tokens paid full price or cache-write price in this session. High in long sessions = cache invalidated often (large file edits, /compact, jumping tasks).">Miss tokens</th>
        <th data-key="CacheReadTokens" class="num" title="Tokens served from cache in this session.">Cache read</th>
        <th data-key="Turns" class="num" title="Assistant turns in this session.">Turns</th>
        <th data-key="CostUSD" class="num" title="Dollar cost of this session.">Cost</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>

  <div class="subview" data-subview="subagents">
    <div class="small">Subagents start with a cold cache (each invocation is a fresh context). The miss-token volume below is the structural tax for using each subagent type.</div>
    <table data-table="cache-subagent">
      <thead><tr>
        <th data-key="Key" title="Subagent type (the agent name from ~/.claude/agents or the built-in Agent tool).">Subagent type</th>
        <th data-key="HitRatio" class="num" title="Hit ratio across all invocations of this subagent. Structurally low — every invocation starts cold.">Hit ratio</th>
        <th data-key="Miss" class="num" title="Total miss tokens across all invocations of this subagent type. The structural tax for using it; lever is invoke less or with smaller starting context.">Miss tokens</th>
        <th data-key="CacheReadTokens" class="num" title="Tokens served from cache (only possible within a single invocation, after the first turn).">Cache read</th>
        <th data-key="Turns" class="num" title="Total assistant turns across all invocations of this subagent type.">Turns</th>
        <th data-key="CostUSD" class="num" title="Total dollar cost of all invocations of this subagent type.">Cost</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>

  <div class="subview" data-subview="invocations">
    <table data-table="cache-invocation">
      <thead><tr>
        <th data-key="Key" title="The description argument passed when this subagent was invoked.">Description</th>
        <th data-key="Subtitle" title="Subagent type for this invocation.">Type</th>
        <th data-key="HitRatio" class="num" title="Hit ratio for this single invocation.">Hit ratio</th>
        <th data-key="Miss" class="num" title="Miss tokens for this single invocation. Useful for spotting individual runs that paid an unusually large cold-cache tax.">Miss tokens</th>
        <th data-key="CacheReadTokens" class="num" title="Tokens served from cache within this invocation (only after the first turn).">Cache read</th>
        <th data-key="Turns" class="num" title="Assistant turns in this invocation.">Turns</th>
        <th data-key="CostUSD" class="num" title="Dollar cost of this invocation.">Cost</th>
      </tr></thead>
      <tbody></tbody>
    </table>
  </div>
`;

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

export async function paint(route) {
  const container = document.getElementById('view-cache');
  if (!container) return;

  if (painted) {
    activateSubview(container, route.sub);
    return;
  }

  container.innerHTML = SHELL;

  let cache;
  try {
    cache = await fetchCache();
  } catch (err) {
    container.innerHTML = `<header class="view-head"><h1>${labelIcon('cache')}Cache efficiency</h1></header>
      <div class="warning-card" role="alert"><strong class="danger">Failed to load cache data:</strong> ${escHtml(err.message)}</div>`;
    return;
  }

  const proj = cache.cache_by_project || [];
  const sess = cache.cache_by_session || [];
  const subs = cache.cache_by_subagent || [];
  const invs = cache.cache_by_invocation || [];
  let totalMiss = 0;
  for (const r of proj) totalMiss += r.Miss || 0;

  const summary = container.querySelector('#cache-summary');
  if (summary) {
    summary.innerHTML = `
      <span>Overall hit ratio: ${hitRatioPill(cache.overall_hit_ratio)}</span>
      <span>Total miss tokens: <strong>${fmtNum(totalMiss)}</strong></span>
      <span>Projects with cacheable traffic: <strong>${proj.length}</strong></span>
      <span>Sessions with cacheable traffic: <strong>${sess.length}</strong></span>
    `;
  }

  // Trends for the three sparkline-bearing cache subviews. Cache
  // invocations have no per-bucket series — each one is a single run.
  let projectTrends = null, sessionTrends = null, subagentTrends = null, period = '';
  try {
    const [pT, sT, aT] = await Promise.all([
      fetchTrends('project').catch(() => null),
      fetchTrends('session').catch(() => null),
      fetchTrends('subagent').catch(() => null),
    ]);
    projectTrends = pT;
    sessionTrends = sT;
    subagentTrends = aT;
    period = (pT && pT.period) || (sT && sT.period) || (aT && aT.period) || '';
  } catch { /* graceful */ }

  const tProj = container.querySelector('[data-table="cache-project"]');
  const tSess = container.querySelector('[data-table="cache-session"]');
  const tSub  = container.querySelector('[data-table="cache-subagent"]');
  const tInv  = container.querySelector('[data-table="cache-invocation"]');

  if (period) {
    injectTrendColumn(tProj, 'Miss', period);
    injectTrendColumn(tSess, 'Miss', period);
    injectTrendColumn(tSub,  'Miss', period);
  }

  buildRows(tProj, proj, r => withTrend([
    [`<span class="truncate path" title="${escHtml(r.Key)}">${escHtml(r.Key)}</span>`, false],
    [hitRatioPill(r.HitRatio), true],
    [fmtNum(r.Miss), true, r.Miss],
    [fmtNum(r.CacheReadTokens), true],
    [fmtNum(r.Turns), true],
    [escHtml(fmtMoney(r.CostUSD)), true],
  ], 2, projectTrends && projectTrends.series ? projectTrends.series[r.Key] : null, period, trendSparkHit));

  buildRows(tSess, sess, r => withTrend([
    [`<code>${escHtml((r.Key || '').slice(0, 8))}…</code>`, false],
    [`<span class="truncate path" title="${escHtml(r.Subtitle)}">${escHtml(truncate(r.Subtitle || '', 80))}</span>`, false],
    [hitRatioPill(r.HitRatio), true],
    [fmtNum(r.Miss), true, r.Miss],
    [fmtNum(r.CacheReadTokens), true],
    [fmtNum(r.Turns), true],
    [escHtml(fmtMoney(r.CostUSD)), true],
  ], 3, sessionTrends && sessionTrends.series ? sessionTrends.series[r.Key] : null, period, trendSparkHit));

  buildRows(tSub, subs, r => withTrend([
    [escHtml(r.Key), false],
    [hitRatioPill(r.HitRatio), true],
    [fmtNum(r.Miss), true, r.Miss],
    [fmtNum(r.CacheReadTokens), true],
    [fmtNum(r.Turns), true],
    [escHtml(fmtMoney(r.CostUSD)), true],
  ], 2, subagentTrends && subagentTrends.series ? subagentTrends.series[r.Key] : null, period, trendSparkHit));

  buildRows(tInv, invs, r => [
    [`<span class="truncate" title="${escHtml(r.Key)}">${escHtml(truncate(r.Key, 80))}</span>`, false],
    [escHtml(r.Subtitle || '(unknown)'), false],
    [hitRatioPill(r.HitRatio), true],
    [fmtNum(r.Miss), true, r.Miss],
    [fmtNum(r.CacheReadTokens), true],
    [fmtNum(r.Turns), true],
    [escHtml(fmtMoney(r.CostUSD)), true],
  ]);

  wireGlobalFilters();
  activateSubview(container, route.sub);

  // Sidebar metric — overall hit-ratio tier pill.
  const cacheNav = document.getElementById('nav-metric-cache');
  if (cacheNav) {
    cacheNav.innerHTML = cache.overall_hit_ratio != null
      ? hitRatioPill(cache.overall_hit_ratio, 'tier-sm') + ' <span class="small">hit</span>'
      : '—';
  }

  painted = true;
}

export function reset() { painted = false; }
