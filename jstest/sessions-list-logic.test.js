process.env.TZ = 'UTC';

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  familyLabel, groupByFamily, applyFilter, applySort, searchRows, activeRows, pageGroup,
  filterCounts, listView, rowLabel, elapsedText, windowText, GROUP_PAGE,
  topSentence, worthOpening, familyStats, modelShort, worthRank, rowTitle, agoText,
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

test('pageGroup shows the 3 rows most worth opening, in the given order, and hides the rest until expanded', () => {
  const rows = Array.from({ length: 7 }, (_, i) => row(`r${i}`, { cost_usd: i + 1 }));
  rows[1].failed = 1;
  rows[4].compactions = 2;
  const p = pageGroup(rows, false);
  assert.equal(GROUP_PAGE, 3);
  assert.deepEqual(p.visible.map(r => r.session_id), ['r1', 'r4', 'r6'], 'failed, then compacted, then costliest; input order kept');
  assert.equal(p.hidden, 4);
  assert.deepEqual(pageGroup(rows, true), { visible: rows, hidden: 0 });
  assert.deepEqual(pageGroup(rows.slice(0, 3), false), { visible: rows.slice(0, 3), hidden: 0 });
});

test('worthRank: failed by cost, then compactions, then cost', () => {
  const rows = [
    row('cheap-failed', { failed: 1, cost_usd: 1 }),
    row('pricey', { cost_usd: 50 }),
    row('compacted', { compactions: 3, cost_usd: 2 }),
    row('dear-failed', { failed: 4, cost_usd: 9 }),
    row('compacted-once', { compactions: 1, cost_usd: 80 }),
  ];
  assert.deepEqual([...rows].sort(worthRank).map(r => r.session_id),
    ['dear-failed', 'cheap-failed', 'compacted', 'compacted-once', 'pricey']);
});

test('a 3,000-row index in 88 families yields the sentence, 8 Worth opening rows and every family collapsed to 3 behind "Show all N"', () => {
  const rows = Array.from({ length: 3000 }, (_, i) => row(`s${i}`, {
    family_key: `family _ number ${i % 88}`, last_activity: at(-i), cost_usd: (i % 37) + 0.5,
    failed: i % 101 === 0 ? 1 : 0, compactions: i % 211 === 0 ? 2 : 0,
  }));
  const v = listView(rows, { expanded: new Set(['family _ number 3']) });
  assert.match(v.sentence, /^3,000 sessions in the last 7 days, \$[\d,.]+\. \d+ ended with unfixed failed commands/);
  assert.equal(v.worth.length, 8);
  assert.equal(v.groups.length, 88);
  assert.equal(v.groups.reduce((n, g) => n + g.count, 0), 3000);
  for (const g of v.groups) {
    if (g.key === 'family _ number 3') {
      assert.equal(g.rows.length, g.count);
      assert.equal(g.hidden, 0);
    } else {
      assert.equal(g.rows.length, 3);
      assert.equal(g.hidden, g.count - 3);
    }
  }
  assert.equal(v.groups[0].key, 'family _ number 0');
});

// SIXC: six rows, $1–$6, two with unfixed failures ($2 and $5).
const SIXC = codex => [1, 2, 3, 4, 5, 6].map(i => row(`c${i}`, {
  cost_usd: i, failed: i === 2 || i === 5 ? 1 : 0, provider: codex && i === 3 ? 'codex' : 'claude-code',
}));

test('topSentence counts the window, its cost and the sessions that ended with unfixed failed commands', () => {
  assert.equal(topSentence(SIXC(false), 'the last 7 days'),
    '6 sessions in the last 7 days, $21.00. 2 ended with unfixed failed commands ($7.00).');
  assert.equal(topSentence(SIXC(true), 'the last 7 days'),
    '6 sessions in the last 7 days, $21.00 API-equivalent. 2 ended with unfixed failed commands ($7.00).');
  assert.equal(topSentence(SIXC(false).map(r => ({ ...r, failed: 0 })), 'the last 7 days'),
    '6 sessions in the last 7 days, $21.00.');
  assert.equal(topSentence([row('one', { cost_usd: 1234.5 })], 'since 2026-10-25'), '1 session since 2026-10-25, $1,235.');
});

test('worthOpening lists each session once under its first reason, in reason order, capped at 8', () => {
  const big = n => Array.from({ length: 6 }, (_, i) => row(`${n}${i}`, { family_key: n, cost_usd: i + 1 }));
  const rows = [
    row('f-cheap', { family_key: 'x', failed: 1, cost_usd: 3, first_failed_turn: 'u-1', compactions: 5 }),
    row('f-dear', { family_key: 'y', failed: 3, cost_usd: 30, first_failed_turn: 'u-9' }),
    row('k-two', { family_key: 'z', compactions: 2, cost_usd: 2 }),
    row('k-four', { family_key: 'w', compactions: 4, cost_usd: 1 }),
    ...big('fam'),
    ...Array.from({ length: 4 }, (_, i) => row(`small${i}`, { family_key: 'small', cost_usd: 10 + i })),
    row('top', { family_key: 'solo', cost_usd: 99 }),
  ];
  const w = worthOpening(rows);
  assert.deepEqual(w.map(x => [x.row.session_id, x.reason]), [
    ['f-dear', '3 failed commands never fixed'],
    ['f-cheap', '1 failed command never fixed'],
    ['k-four', 'Context compacted 4 times'],
    ['k-two', 'Context compacted 2 times'],
    ['fam5', 'In the most expensive 10% of 6 runs of this prompt'],
    ['top', 'Most expensive session this week'],
  ]);
  assert.equal(w[0].turn, 'u-9');
  assert.equal(w[2].turn, '');
  assert.ok(!w.some(x => x.row.session_id.startsWith('small')), 'no "10%" row from a 4-row family');
  const ids = w.map(x => x.row.session_id);
  assert.equal(new Set(ids).size, ids.length, 'no session twice');

  const many = Array.from({ length: 12 }, (_, i) => row(`m${i}`, { failed: 1, cost_usd: i }));
  assert.equal(worthOpening(many).length, 8, 'capped at 8');
  assert.equal(worthOpening(many)[0].row.session_id, 'm11', 'costliest failure first');
  assert.deepEqual(worthOpening([row('a', { cost_usd: 5 })]).map(x => x.reason), ['Most expensive session this week']);
  assert.deepEqual(worthOpening([]), []);
});

test('the window\'s most expensive row already listed under an earlier reason is not listed again', () => {
  const rows = [row('a', { failed: 2, cost_usd: 50 }), row('b', { cost_usd: 3 })];
  assert.deepEqual(worthOpening(rows).map(x => x.row.session_id), ['a']);
});

test('modelShort names Claude models by family and leaves any other id as it is', () => {
  assert.equal(modelShort('claude-opus-4-7'), 'Opus');
  assert.equal(modelShort('claude-fable-5-1'), 'Fable');
  assert.equal(modelShort('claude-sonnet-4-6'), 'Sonnet');
  assert.equal(modelShort('claude-haiku-4-5'), 'Haiku');
  assert.equal(modelShort('gpt-5.1-codex'), 'gpt-5.1-codex');
  assert.equal(modelShort(''), '');
});

test('familyStats reads runs, total, median and model mix; a 1-row family has no median', () => {
  const tri = [
    ...Array.from({ length: 3 }, (_, i) => row(`f${i}`, { model: 'claude-fable-5-1', cost_usd: [1, 2, 6][i] })),
    row('o1', { model: 'claude-opus-4-7', cost_usd: 3 }),
  ];
  const s = familyStats(tri);
  assert.equal(s.runs, 4);
  assert.equal(s.total, 12);
  assert.equal(s.median, 2.5, 'mean of the two middles');
  assert.deepEqual(s.models, [['Fable', 3], ['Opus', 1]]);
  assert.equal(s.text, '4 runs · $12.00 · median $2.50 · 3 Fable, 1 Opus');
  const odd = familyStats([row('a', { cost_usd: 1 }), row('b', { cost_usd: 9 }), row('c', { cost_usd: 4, model: 'gpt-5' })]);
  assert.equal(odd.median, 4);
  assert.equal(odd.text, '3 runs · $14.00 · median $4.00 · 2 Opus, 1 gpt-5');
  const big = familyStats([
    ...Array.from({ length: 102 }, (_, i) => row(`x${i}`, { model: 'claude-fable-5-1', cost_usd: 1 })),
    ...Array.from({ length: 79 }, (_, i) => row(`y${i}`, { model: 'claude-opus-4-7', cost_usd: 1 })),
  ]);
  assert.match(big.text, / · 102 Fable, 79 Opus$/);
  assert.equal(familyStats([row('one', { model: 'claude-opus-4-7', cost_usd: 2 })]).text, '1 run · $2.00 · Opus');
  assert.equal(familyStats([row('one', { model: 'claude-opus-4-7', cost_usd: 2 })]).median, null);
  const codex = familyStats([row('a', { cost_usd: 1 }), row('b', { cost_usd: 3, provider: 'codex', model: 'gpt-5' })]);
  assert.equal(codex.apiEquivalent, true);
  assert.equal(codex.text, '2 runs · $4.00 API-equivalent · median $2.00 · 1 gpt-5, 1 Opus', 'ties read alphabetically');
});

test('20 hand-typed sessions in 20 families of one read sensibly', () => {
  const rows = Array.from({ length: 20 }, (_, i) => row(`h${i}`, {
    family_key: `hand typed prompt ${i}`, title: `hand typed prompt ${i}`, cost_usd: i + 1,
    failed: i === 4 ? 2 : 0, first_failed_turn: i === 4 ? 'u4' : '', compactions: i === 7 ? 1 : 0, last_activity: at(-i),
  }));
  const v = listView(rows, {});
  assert.match(v.sentence, /^20 sessions in the last 7 days/);
  assert.deepEqual(v.worth.map(x => [x.row.session_id, x.reason]), [
    ['h4', '2 failed commands never fixed'],
    ['h7', 'Context compacted 1 time'],
    ['h19', 'Most expensive session this week'],
  ]);
  assert.ok(!v.worth.some(x => /10%/.test(x.reason)));
  assert.equal(v.groups.length, 20);
  assert.ok(v.groups.every(g => /^1 run · /.test(g.stats.text)), 'every header reads "1 run"');
  assert.ok(v.groups.every(g => g.hidden === 0), 'no family shows "Show all"');
});

test('rowTitle is the prompt\'s first line, else the remainder', () => {
  assert.equal(rowTitle(row('a', { title: 'fix the bug', remainder: 'x' })), 'fix the bug');
  assert.equal(rowTitle(row('a', { title: '', remainder: 'x' })), 'x');
  assert.equal(rowTitle(row('a', { title: '', remainder: '' })), '(no prompt)');
});

test('windowText names the since/until filter, or the default window', () => {
  assert.equal(windowText('?since=2026-10-25&until=2026-11-01'), 'since 2026-10-25, until 2026-11-01');
  assert.equal(windowText('?since=2026-10-25'), 'since 2026-10-25');
  assert.equal(windowText('?until=2026-11-01&project=x'), 'until 2026-11-01');
  assert.equal(windowText(''), 'the last 7 days');
});

test('agoText names the time since last activity in its coarsest unit', () => {
  const now = Date.parse(at(0));
  assert.equal(agoText(at(-0.5), now), 'just now');
  assert.equal(agoText(at(-12), now), '12m ago');
  assert.equal(agoText(at(-185), now), '3h ago');
  assert.equal(agoText(at(-3 * 1440), now), '3d ago');
  assert.equal(agoText('', now), '');
});
