import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseHash, redirectHash, spendHash, REDIRECTS, agentsSessionHash, parseAgentsSub } from '../web/router.js';

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
  for (const h of ['', '#overview', '#spend', '#spend/project/tokens', '#tools', '#agents/timeline', '#costly']) {
    assert.equal(redirectHash(h), null, h);
  }
});
test('the redirect table names exactly the three retired views', () => {
  assert.deepEqual(REDIRECTS.map(r => r.from).sort(), ['cache', 'cost', 'tokens']);
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
