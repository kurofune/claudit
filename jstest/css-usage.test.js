// Dead CSS in web/app.css: every class a selector names must appear as a
// class name somewhere the page renders from — inside a string literal of
// a web/*.js module, or in web/index.html. A class built from a template
// prefix (`tier-${t}`) counts as used for every class sharing that prefix.
// Theme files are not scanned.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { WEB } from './helpers/module-graph.js';

// selectorClasses lists the classes a stylesheet's selectors name,
// ignoring comments and declaration values.
export function selectorClasses(css) {
  const out = new Set();
  const code = css.replace(/\/\*[\s\S]*?\*\//g, '');
  for (const m of code.matchAll(/([^{}]+)\{/g)) {
    for (const c of m[1].matchAll(/\.([a-zA-Z][\w-]*)/g)) out.add(c[1]);
  }
  return out;
}

// jsStrings is the text of a module's string and template literals, its
// comments dropped first.
export function jsStrings(src) {
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:'"`\\])\/\/[^\n]*/g, '$1');
  return (code.match(/'(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"|`(?:[^`\\]|\\.)*`/g) || []).join('\n');
}

// unusedClasses lists the selector classes no page source names. sources
// is page text already reduced to what can render (jsStrings, HTML).
export function unusedClasses(css, sources) {
  const text = sources.join('\n');
  const words = new Set(text.match(/[A-Za-z][\w-]*/g));
  const prefixes = [...text.matchAll(/([A-Za-z][\w-]*[-_])\$\{/g)].map(m => m[1]);
  return [...selectorClasses(css)].filter(c => !words.has(c) && !prefixes.some(p => c.startsWith(p))).sort();
}

const pageSources = () => [
  ...readdirSync(WEB).filter(f => f.endsWith('.js')).map(f => jsStrings(readFileSync(join(WEB, f), 'utf8'))),
  readFileSync(join(WEB, 'index.html'), 'utf8'),
];

test('every class selector in web/app.css names a class web/*.js or web/index.html renders', () => {
  assert.deepEqual(unusedClasses(readFileSync(join(WEB, 'app.css'), 'utf8'), pageSources()), []);
});

test('the scan flags a selector class no source names, and skips comments and values', () => {
  const css = `/* .in-comment */
  .used, .gone > .also-gone { width: 1.5em; background: url(a.png); }
  .tr, .delta { color: blue; }
  @media print { .tier-high { color: red; } }`;
  const js = "const a = '<div class=\"used\">'; // .also-gone in a comment\nconst b = `tier-${t}`;\nconst tr = delta + 1;";
  assert.deepEqual(unusedClasses(css, [jsStrings(js)]), ['also-gone', 'delta', 'gone', 'tr']);
});
