process.env.TZ = 'UTC';

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  familyLabel, groupByFamily, applyFilter, applySort, searchRows, activeRows, pageGroup,
  filterCounts, listView, rowLabel, elapsedText, windowText, GROUP_PAGE,
} from '../web/sessions-list-logic.js';

const at = min => new Date(Date.parse('2026-11-01T09:00:00Z') + min * 60_000).toISOString();

const row = (id, f = {}) => ({
  session_id: id, family_key: 'fix the bug', remainder: '', title: 'fix the bug', outcome: 'Done.',
  project: 'claudit', provider: 'claude-code', model: 'claude-opus-5-5', cost_usd: 1, duration_ms: 60_000,
  turns: 3, commands: 2, failed: 0, last_activity: at(0), active: false, started_at: at(-10), ...f,
});

// Six rows across three families; costs, durations and recency each order
// them differently so a sort that reads the wrong field fails.
const SIX = [
  row('a', { family_key: 'work bead _ next', remainder: 'claudit-9yx', last_activity: at(50), cost_usd: 2, duration_ms: 10_000, active: true }),
  row('b', { family_key: 'work bead _ next', remainder: 'claudit-4eb', last_activity: at(40), cost_usd: 9, duration_ms: 5_000, failed: 2 }),
  row('c', { family_key: 'work bead _ next', remainder: 'claudit-7lt', last_activity: at(30), cost_usd: 1, duration_ms: 90_000 }),
  row('d', { family_key: 'review the diff', last_activity: at(60), cost_usd: 3, duration_ms: 1_000, outcome: 'Ship it.' }),
  row('e', { family_key: 'review the diff', last_activity: at(10), cost_usd: 4, duration_ms: 2_000, failed: 1, active: true }),
  row('f', { family_key: 'retry _ times with a _ms timeout', remainder: '3 · 500', last_activity: at(20), project: 'djinn' }),
];

test('familyLabel draws every "_" placeholder as "…"', () => {
  assert.equal(familyLabel('work bead _ next'), 'work bead … next');
  assert.equal(familyLabel('retry _ times with a _ms timeout'), 'retry … times with a …ms timeout');
  assert.equal(familyLabel('(no prompt)'), '(no prompt)');
});

test('groupByFamily makes one group per family_key, ordered by its newest member', () => {
  const groups = groupByFamily(SIX);
  assert.deepEqual(groups.map(g => g.key), ['review the diff', 'work bead _ next', 'retry _ times with a _ms timeout']);
  assert.deepEqual(groups.map(g => g.label), ['review the diff', 'work bead … next', 'retry … times with a …ms timeout']);
  assert.deepEqual(groups.map(g => g.rows.length), [2, 3, 1]);
  assert.deepEqual(groups[1].rows.map(r => r.session_id), ['a', 'b', 'c']);
  assert.equal(groups[0].newest, at(60));
});

test('rowLabel is the remainder, or the title when the remainder is empty', () => {
  assert.equal(rowLabel(SIX[0]), 'claudit-9yx');
  assert.equal(rowLabel(SIX[3]), 'fix the bug');
});

test('applyFilter narrows to active rows or rows with unresolved failures', () => {
  assert.deepEqual(applyFilter(SIX, 'all').map(r => r.session_id), ['a', 'b', 'c', 'd', 'e', 'f']);
  assert.deepEqual(applyFilter(SIX, 'active').map(r => r.session_id), ['a', 'e']);
  assert.deepEqual(applyFilter(SIX, 'failed').map(r => r.session_id), ['b', 'e']);
  assert.deepEqual(filterCounts(SIX), { all: 6, active: 2, failed: 2 });
});

test('applySort orders by last_activity, cost_usd or duration_ms, descending', () => {
  assert.deepEqual(applySort(SIX, 'newest').map(r => r.session_id), ['d', 'a', 'b', 'c', 'f', 'e']);
  assert.deepEqual(applySort(SIX, 'cost').map(r => r.session_id), ['b', 'e', 'd', 'a', 'c', 'f']);
  assert.deepEqual(applySort(SIX, 'duration').map(r => r.session_id), ['c', 'f', 'a', 'b', 'e', 'd']);
  assert.deepEqual(SIX.map(r => r.session_id), ['a', 'b', 'c', 'd', 'e', 'f'], 'the input is not reordered');
});

test('the sort orders rows within each group while groups keep newest-member order', () => {
  const v = listView(SIX, { sort: 'cost' });
  assert.deepEqual(v.groups.map(g => g.key), ['review the diff', 'work bead _ next', 'retry _ times with a _ms timeout']);
  assert.deepEqual(v.groups[1].rows.map(r => r.session_id), ['b', 'a', 'c']);
  assert.deepEqual(v.groups[0].rows.map(r => r.session_id), ['e', 'd']);
  const d = listView(SIX, { sort: 'duration' });
  assert.deepEqual(d.groups[1].rows.map(r => r.session_id), ['c', 'a', 'b']);
});

test('searchRows matches family key, remainder, title, outcome or project, case-insensitively', () => {
  const ids = q => searchRows(SIX, q).map(r => r.session_id);
  assert.deepEqual(ids('WORK BEAD'), ['a', 'b', 'c']);
  assert.deepEqual(ids('4EB'), ['b']);
  assert.deepEqual(ids('ship IT'), ['d']);
  assert.deepEqual(ids('djinn'), ['f']);
  assert.deepEqual(ids('FIX THE'), ['a', 'b', 'c', 'd', 'e', 'f'], 'title');
  assert.deepEqual(ids('   '), ['a', 'b', 'c', 'd', 'e', 'f'], 'blank query keeps every row');
  assert.deepEqual(ids('nothing like this'), []);
});

test('listView keeps a group only while it has a matching row and reports no match', () => {
  const v = listView(SIX, { query: 'claudit-4eb' });
  assert.deepEqual(v.groups.map(g => g.key), ['work bead _ next']);
  assert.equal(v.groups[0].count, 1);
  assert.equal(v.empty, '');
  const none = listView(SIX, { query: 'zzz' });
  assert.deepEqual(none.groups, []);
  assert.equal(none.empty, 'No sessions match');
  assert.deepEqual(listView([], {}).empty, 'none');
});

test('listView filters after the search and counts each chip over the searched rows', () => {
  const v = listView(SIX, { filter: 'failed' });
  assert.deepEqual(v.groups.map(g => g.key), ['work bead _ next', 'review the diff']);
  assert.deepEqual(v.counts, { all: 6, active: 2, failed: 2 });
  const s = listView(SIX, { filter: 'active', query: 'review' });
  assert.deepEqual(s.counts, { all: 2, active: 1, failed: 1 });
  assert.deepEqual(s.groups.map(g => g.rows.map(r => r.session_id)), [['e']]);
});

test('activeRows lists active sessions, newest first, and listView pins them', () => {
  assert.deepEqual(activeRows(SIX).map(r => r.session_id), ['a', 'e']);
  assert.deepEqual(listView(SIX, {}).active.map(r => r.session_id), ['a', 'e']);
  assert.deepEqual(listView(SIX.map(r => ({ ...r, active: false })), {}).active, []);
});

test('elapsedText is the time since started_at', () => {
  const now = Date.parse(at(0));
  assert.equal(elapsedText(at(-10), now), '10m');
  assert.equal(elapsedText(at(-1.5), now), '1m 30s');
  assert.equal(elapsedText('', now), '');
});

test('pageGroup shows 20 rows and hides the rest until expanded', () => {
  const rows = Array.from({ length: 47 }, (_, i) => row(`r${i}`));
  const p = pageGroup(rows, false);
  assert.equal(GROUP_PAGE, 20);
  assert.equal(p.visible.length, 20);
  assert.equal(p.hidden, 27);
  assert.deepEqual(pageGroup(rows, true), { visible: rows, hidden: 0 });
  assert.deepEqual(pageGroup(rows.slice(0, 5), false), { visible: rows.slice(0, 5), hidden: 0 });
});

test('a 3,000-row index in 88 families collapses each group to 20 rows behind "Show N more"', () => {
  const rows = Array.from({ length: 3000 }, (_, i) => row(`s${i}`, {
    family_key: `family _ number ${i % 88}`, last_activity: at(-i),
  }));
  const v = listView(rows, { expanded: new Set(['family _ number 3']) });
  assert.equal(v.groups.length, 88);
  assert.equal(v.groups.reduce((n, g) => n + g.count, 0), 3000);
  for (const g of v.groups) {
    if (g.key === 'family _ number 3') {
      assert.equal(g.rows.length, g.count);
      assert.equal(g.hidden, 0);
    } else {
      assert.equal(g.rows.length, 20);
      assert.equal(g.hidden, g.count - 20);
    }
  }
  assert.equal(v.groups[0].key, 'family _ number 0');
});

test('windowText names the since/until filter, or the default window', () => {
  assert.equal(windowText('?since=2026-10-25&until=2026-11-01'), 'since 2026-10-25, until 2026-11-01');
  assert.equal(windowText('?since=2026-10-25'), 'since 2026-10-25');
  assert.equal(windowText('?until=2026-11-01&project=x'), 'until 2026-11-01');
  assert.equal(windowText(''), 'the last 7 days');
});
