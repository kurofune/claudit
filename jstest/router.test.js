import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseHash, redirectHash, spendHash, REDIRECTS, agentsSessionHash, parseAgentsSub, onChange } from '../web/router.js';
import { installDOM } from './helpers/mini-dom.js';

test('parseHash reads the Spend tab and Tokens unit from the hash', () => {
  assert.deepEqual(parseHash('#spend/project/tokens'), { view: 'spend', sub: 'project', unit: 'tokens' });
});
test('parseHash defaults the Spend unit to dollars', () => {
  assert.deepEqual(parseHash('#spend/project'), { view: 'spend', sub: 'project', unit: 'dollars' });
  assert.deepEqual(parseHash('#spend'), { view: 'spend', sub: '', unit: 'dollars' });
});
test('parseHash keeps multi-segment subs for other views', () => {
  assert.deepEqual(parseHash('#agents/timeline'), { view: 'agents', sub: 'timeline', unit: 'dollars' });
});
test('parseHash falls back to overview for an empty or unknown hash', () => {
  assert.equal(parseHash('').view, 'overview');
  assert.equal(parseHash('#nope').view, 'overview');
  assert.equal(parseHash('#cost').view, 'overview');
  assert.equal(parseHash('#tokens').view, 'overview');
  assert.equal(parseHash('#cache').view, 'overview');
  assert.equal(parseHash('#tools').view, 'overview');
  assert.equal(parseHash('#tools/drill').view, 'overview');
});
test('parseHash reads #overview/all as the Overview with sub all', () => {
  assert.deepEqual(parseHash('#overview/all'), { view: 'overview', sub: 'all', unit: 'dollars' });
});
test('parseHash accepts a hash without the leading #', () => {
  assert.deepEqual(parseHash('spend/model/tokens'), { view: 'spend', sub: 'model', unit: 'tokens' });
});

test('spendHash builds the canonical Spend hash', () => {
  assert.equal(spendHash('project', 'dollars'), '#spend/project');
  assert.equal(spendHash('project', 'tokens'), '#spend/project/tokens');
  assert.equal(spendHash('', 'tokens'), '#spend/model/tokens');
  assert.equal(spendHash('', 'dollars'), '#spend');
});

const REDIRECT_CASES = [
  ['#cost', '#spend'],
  ['#cost/', '#spend'],
  ['#cost/model', '#spend/model'],
  ['#cost/project', '#spend/project'],
  ['#cost/subagents', '#spend/subagents'],
  ['#cost/skill', '#spend/skill'],
  ['#cost/prompt', '#spend/prompt'],
  ['#tokens', '#spend/model/tokens'],
  ['#tokens/model', '#spend/model/tokens'],
  ['#tokens/project', '#spend/project/tokens'],
  ['#tokens/subagents', '#spend/subagents/tokens'],
  ['#tokens/skill', '#spend/skill/tokens'],
  ['#tokens/prompt', '#spend/prompt/tokens'],
  ['#cache', '#spend'],
  ['#cache/projects', '#spend'],
  ['#cache/sessions', '#spend'],
  ['#cache/anything/deeper', '#spend'],
];
for (const [from, to] of REDIRECT_CASES) {
  test(`redirectHash ${from} → ${to}`, () => {
    assert.equal(redirectHash(from), to);
    const target = parseHash(to);
    assert.equal(target.view, 'spend');
  });
}
test('redirectHash leaves current hashes alone', () => {
  for (const h of ['', '#overview', '#overview/all', '#spend', '#spend/project/tokens', '#agents/timeline', '#costly', '#toolsy']) {
    assert.equal(redirectHash(h), null, h);
  }
});
test('the redirect table names exactly the four retired views', () => {
  assert.deepEqual(REDIRECTS.map(r => r.from).sort(), ['cache', 'cost', 'tokens', 'tools']);
});

for (const from of ['#tools', '#tools/by-tool', '#tools/drill']) {
  test(`redirectHash ${from} → #overview/all`, () => {
    assert.equal(redirectHash(from), '#overview/all');
    assert.deepEqual(parseHash('#overview/all'), { view: 'overview', sub: 'all', unit: 'dollars' });
  });
}

// onChange rewrites a retired hash with replaceState before the route
// callback runs, so the callback already sees the new hash.
for (const [from, to, view, sub] of [
  ['#tools', '#overview/all', 'overview', 'all'],
  ['#tools/by-tool', '#overview/all', 'overview', 'all'],
  ['#tools/drill', '#overview/all', 'overview', 'all'],
  ['#cost/model', '#spend/model', 'spend', 'model'],
  ['#tokens', '#spend/model/tokens', 'spend', 'model'],
  ['#cache', '#spend', 'spend', ''],
]) {
  test(`onChange rewrites ${from} to ${to} with replaceState before the callback fires`, () => {
    const { location, replaceCalls, window } = installDOM({ hash: from });
    const seen = [];
    onChange(route => seen.push({ route, hash: location.hash, replaced: [...replaceCalls] }));
    assert.equal(seen.length, 1);
    assert.deepEqual(seen[0].replaced, [to]);
    assert.equal(seen[0].hash, to);
    assert.equal(seen[0].route.view, view);
    assert.equal(seen[0].route.sub, sub);

    location.hash = from;
    window.dispatchEvent(new Event('hashchange'));
    assert.equal(seen.length, 2);
    assert.deepEqual(seen[1].replaced, [to, to], 'a later hashchange to the retired hash redirects too');
    assert.equal(seen[1].hash, to);
  });
}

test('onChange leaves a current hash alone', () => {
  const { replaceCalls } = installDOM({ hash: '#overview/hotspot-2' });
  const seen = [];
  onChange(route => seen.push(route));
  assert.deepEqual(replaceCalls, []);
  assert.deepEqual(seen, [{ view: 'overview', sub: 'hotspot-2', unit: 'dollars' }]);
});

test('agentsSessionHash deep-links a session into the Tree lens', () => {
  const h = agentsSessionHash('3f2a-session');
  assert.equal(h, '#agents/tree/3f2a-session');
  const route = parseHash(h);
  assert.equal(route.view, 'agents');
  assert.deepEqual(parseAgentsSub(route.sub), { lens: 'tree', sessionId: '3f2a-session' });
});
test('parseAgentsSub reads a bare lens and survives a malformed session id', () => {
  assert.deepEqual(parseAgentsSub('timeline'), { lens: 'timeline', sessionId: '' });
  assert.deepEqual(parseAgentsSub(''), { lens: '', sessionId: '' });
  assert.deepEqual(parseAgentsSub('tree/%E0%A4%A'), { lens: 'tree', sessionId: '' });
  assert.equal(parseAgentsSub(parseHash(agentsSessionHash('a b/c')).sub).sessionId, 'a b/c');
});
