// The Agents view's reading guide lives in web/index.html as the
// #agents-guide template, so the shell carries the user-visible text the
// Sessions surface paints under its heading.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { agentsGuide } from './helpers/agents-guide.js';

const text = html => html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim();

test('index.html carries a guide that names the List, Header and Story of the Sessions surface', () => {
  const guide = agentsGuide();
  assert.match(guide, /<details class="guide">/);
  for (const part of ['List', 'Header', 'Story']) assert.match(guide, new RegExp(`<strong>${part}</strong>`));
  assert.match(text(guide), /Worth a look/);
});

test('the guide names none of the deleted lenses or the drawer', () => {
  assert.doesNotMatch(text(agentsGuide()), /\b(Feed|Tree|Timeline|Conversation|Insights|drawer)\b/i);
});
