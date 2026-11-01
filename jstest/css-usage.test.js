// Dead CSS in web/app.css: every class a selector names must be rendered
// as a class by web/*.js or web/index.html. Only class construction
// counts: a class="…" attribute (its static words and the string literals
// inside its ${…} interpolations), classList.add/toggle/replace,
// className = and setAttribute('class', …). A word in ordinary text does
// not. A class built from a template prefix (`tier-${t}`) counts as used
// for every class sharing that prefix. Theme files are not scanned.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { WEB } from './helpers/module-graph.js';

// CLASS_PARAMS are the functions whose parameter is a class name they
// interpolate into a class attribute: a string literal passed to one is a
// rendered class. Each entry is checked against its module below.
const CLASS_PARAMS = [
  { module: 'skeleton.js', fn: 'skel', param: 'variant' },
  { module: 'skeleton.js', fn: 'skelLine', param: 'cls' },
];

const IDENT = /^[A-Za-z_$][\w$]*$/;

// selectorClasses lists the classes a stylesheet's selectors name,
// ignoring comments and declaration values.
function selectorClasses(css) {
  const out = new Set();
  const code = css.replace(/\/\*[\s\S]*?\*\//g, '');
  for (const m of code.matchAll(/([^{}]+)\{/g)) {
    for (const c of m[1].matchAll(/\.([a-zA-Z][\w-]*)/g)) out.add(c[1]);
  }
  return out;
}

const stripComments = src =>
  src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:'"`\\])\/\/[^\n]*/g, '$1');

// Scan state: names and prefixes are rendered classes; carriers are the
// identifiers a module interpolates whole into a class.
const newScan = () => ({ names: new Set(), prefixes: new Set(), carriers: new Set() });

// addWords records the class words of one run of class text. A last word
// that runs straight into ${…} and ends in - or _ is a prefix.
function addWords(scan, text, beforeInterp) {
  const words = text.split(/\s+/);
  words.forEach((w, k) => {
    if (!/^[A-Za-z][\w-]*$/.test(w)) return;
    if (beforeInterp && k === words.length - 1 && /[-_]$/.test(w)) scan.prefixes.add(w);
    else scan.names.add(w);
  });
}

// scanLiteral reads the string or template literal opening at src[i],
// recording its text as class words, and returns the index past it.
function scanLiteral(src, i, scan) {
  const q = src[i];
  let j = i + 1, buf = '';
  while (j < src.length) {
    const c = src[j];
    if (c === '\\') { buf += src[j + 1] || ''; j += 2; continue; }
    if (c === q) break;
    if (q === '`' && c === '$' && src[j + 1] === '{') {
      addWords(scan, buf, true);
      buf = '';
      j = scanExpr(src, j + 2, scan, '}');
      continue;
    }
    buf += c;
    j++;
  }
  addWords(scan, buf, false);
  return j + 1;
}

// scanExpr reads a class-valued expression from i to its closing bracket
// (or, with end ';', to the statement's end), recording every literal in
// it, and a bare identifier as a carrier. Returns the index past the end.
function scanExpr(src, i, scan, end) {
  let depth = 0, j = i;
  for (; j < src.length; j++) {
    const c = src[j];
    if (c === "'" || c === '"' || c === '`') { j = scanLiteral(src, j, scan) - 1; continue; }
    if (c === '(' || c === '[' || c === '{') depth++;
    else if (c === ')' || c === ']' || c === '}') { if (depth === 0) break; depth--; }
    else if (depth === 0 && end === ';' && (c === ';' || c === '\n' || c === ',')) break;
  }
  const expr = src.slice(i, j).trim();
  if (IDENT.test(expr)) scan.carriers.add(expr);
  return j + 1;
}

// scanClassAttr reads a class attribute's value from i to its closing quote.
function scanClassAttr(src, i, q, scan) {
  let j = i, buf = '';
  while (j < src.length && src[j] !== q) {
    if (src[j] === '$' && src[j + 1] === '{') {
      addWords(scan, buf, true);
      buf = '';
      j = scanExpr(src, j + 2, scan, '}');
      continue;
    }
    buf += src[j];
    j++;
  }
  addWords(scan, buf, false);
}

// renderedClasses scans one module (or HTML page) for the classes it
// renders, then the values its class carriers are assigned.
function renderedClasses(src, params = CLASS_PARAMS) {
  const code = stripComments(src);
  const scan = newScan();
  for (const m of code.matchAll(/\bclass=(["'])/g)) scanClassAttr(code, m.index + m[0].length, m[1], scan);
  for (const m of code.matchAll(/\.classList\.(?:add|toggle|replace)\(|\.setAttribute\(\s*['"]class['"]\s*,/g)) {
    scanExpr(code, m.index + m[0].length, scan, ')');
  }
  for (const m of code.matchAll(/\.className\s*\+?=(?!=)/g)) scanExpr(code, m.index + m[0].length, scan, ';');
  for (const v of [...scan.carriers]) {
    for (const m of code.matchAll(new RegExp(`(?<![\\w$.])${v.replace(/\$/g, '\\$')}\\s*\\+?=(?![=>])`, 'g'))) {
      scanExpr(code, m.index + m[0].length, scan, ';');
    }
  }
  for (const { fn } of params) {
    for (const m of code.matchAll(new RegExp(`(?<![\\w$.])${fn}\\(`, 'g'))) scanExpr(code, m.index + m[0].length, scan, ')');
  }
  return scan;
}

// unusedClasses lists the selector classes no source renders. sources are
// the raw text of each web/*.js module and of web/index.html.
function unusedClasses(css, sources, params = CLASS_PARAMS) {
  const names = new Set(), prefixes = new Set();
  for (const src of sources) {
    const s = renderedClasses(src, params);
    s.names.forEach(n => names.add(n));
    s.prefixes.forEach(p => prefixes.add(p));
  }
  return [...selectorClasses(css)].filter(c => !names.has(c) && ![...prefixes].some(p => c.startsWith(p))).sort();
}

const read = f => readFileSync(join(WEB, f), 'utf8');
const pageSources = () => [...readdirSync(WEB).filter(f => f.endsWith('.js')).map(read), read('index.html')];

test('every class selector in web/app.css names a class web/*.js or web/index.html renders', () => {
  assert.deepEqual(unusedClasses(read('app.css'), pageSources()), []);
});

test('every CLASS_PARAMS function is defined in its module and puts its parameter into a class', () => {
  for (const { module, fn, param } of CLASS_PARAMS) {
    const src = read(module);
    const def = new RegExp(`(?:function\\s+${fn}\\s*\\(|const\\s+${fn}\\s*=\\s*\\()\\s*${param}\\b`);
    assert.match(src, def, `${module} defines ${fn}(${param})`);
    assert.ok(renderedClasses(src, []).carriers.has(param), `${module} interpolates ${param} into a class`);
  }
});

test('a selector name that appears only in ordinary text is unused', () => {
  assert.deepEqual(unusedClasses('.unused {color:red}', ['const msg = "unused";']), ['unused']);
  assert.deepEqual(unusedClasses('.gone {}', ['el.title = "gone"; el.innerHTML = `<p title="gone">gone</p>`;']), ['gone']);
  assert.deepEqual(unusedClasses('.probe {}', ["el.classList.contains('probe'); el.classList.remove('probe');"]), ['probe']);
});

test('the scan reads every supported form of class construction and skips comments and values', () => {
  const css = `/* .in-comment */
  .attr, .gone > .also-gone { width: 1.5em; background: url(a.png); }
  .lit, .tier-high, .toggled, .assigned, .carried, .param, .single { color: blue; }
  .is-on { color: red; }`;
  const js = [
    "const a = `<div class=\"attr ${on ? 'is-on' : ''}\">`; // .also-gone in a comment",
    'const b = `<i class="tier-${t}"></i>`;',
    "el.classList.toggle('toggled', on);",
    "el.className = 'assigned';",
    "const cls = ok ? 'carried' : 'lit';\nconst c = `<b class=\"${cls}\"></b>`;",
    "const box = (k) => `<s class=\"${k}\"></s>`;\nbox('param');",
    "const d = \"<u class='single'></u>\";",
  ].join('\n');
  assert.deepEqual(unusedClasses(css, [js], [{ fn: 'box' }]), ['also-gone', 'gone']);
  assert.deepEqual(unusedClasses('.param {}', [js], []), ['param'], 'a class parameter counts only through CLASS_PARAMS');
});
